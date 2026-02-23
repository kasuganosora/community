package homeserver

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Matrix spec: localpart must only contain [a-z0-9._=/-]+
var validLocalpartRe = regexp.MustCompile(`^[a-z0-9._=\-/+]+$`)

// AuthService 认证服务
type AuthService struct {
	HS *HomeServer
}

// NewAuthService 创建认证服务
func NewAuthService(hs *HomeServer) *AuthService {
	return &AuthService{HS: hs}
}

// ===== 登录流程 =====

// LoginRequest 登录请求
type LoginRequest struct {
	Type                   string                 `json:"type"`
	Identifier             *LoginIdentifier       `json:"identifier"`
	User                   string                 `json:"user"`                   // 旧格式用户名
	Password               string                 `json:"password"`
	Token                  string                 `json:"token"`                  // Token 登录
	DeviceID               *string                `json:"device_id"`
	InitialDeviceDisplayName *string              `json:"initial_device_display_name"`
	RefreshToken           bool                   `json:"refresh_token"`
}

// LoginIdentifier 登录标识
type LoginIdentifier struct {
	Type string `json:"type"` // m.id.user, m.id.thirdparty, m.id.phone
	User string `json:"user,omitempty"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	UserID      string  `json:"user_id"`
	AccessToken string  `json:"access_token"`
	DeviceID    string  `json:"device_id"`
	ExpiresIn   *int64  `json:"expires_in,omitempty"`
	RefreshToken *string `json:"refresh_token,omitempty"`
	WellKnown   *WellKnown `json:"well_known,omitempty"`
}

// WellKnown 服务器发现信息
type WellKnown struct {
	Homeserver struct {
		BaseURL string `json:"base_url"`
	} `json:"m.homeserver"`
}

// LoginFlowsResponse 登录流程响应
type LoginFlowsResponse struct {
	Flows []LoginFlow `json:"flows"`
}

// LoginFlow 登录流程
type LoginFlow struct {
	Type string `json:"type"`
}

// Login 处理登录请求
func (a *AuthService) Login(req *LoginRequest) (*LoginResponse, *MatrixError) {
	// 支持 m.login.password 和 m.login.token
	switch req.Type {
	case "m.login.password":
		return a.loginWithPassword(req)
	case "m.login.token":
		return a.loginWithToken(req)
	default:
		return nil, ErrUnrecognized
	}
}

// loginWithPassword 密码登录
func (a *AuthService) loginWithPassword(req *LoginRequest) (*LoginResponse, *MatrixError) {
	// 获取用户名
	username := ""
	if req.Identifier != nil && req.Identifier.Type == "m.id.user" {
		username = req.Identifier.User
	} else if req.User != "" {
		username = req.User
	}
	
	if username == "" || req.Password == "" {
		return nil, ErrInvalidParam
	}
	
	// 标准化用户名
	username = strings.ToLower(strings.TrimSpace(username))
	
	// 解析用户ID
	var localpart, domain string
	if strings.HasPrefix(username, "@") {
		parts := strings.SplitN(username[1:], ":", 2)
		if len(parts) == 2 {
			localpart = parts[0]
			domain = parts[1]
		} else {
			localpart = parts[0]
		}
	} else {
		localpart = username
	}
	
	// 检查域名（如果是远程用户，需要走不同的流程）
	if domain != "" && domain != a.HS.Config.ServerName {
		return nil, &MatrixError{
			ErrCode: "M_USER_DEACTIVATED",
			Error:   "Cannot login as remote user",
		}
	}
	
	// 查找本地用户
	var account models.Account
	var user models.User
	err := db.GetDB().Where("username = ?", localpart).First(&account).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrForbidden
		}
		return nil, ErrInternal
	}
	
	// 获取用户密钥数据
	err = db.GetDB().Where("id = ?", account.UserId).First(&user).Error
	if err != nil {
		return nil, ErrInternal
	}
	
	var secretData models.UserSecretData
	err = db.GetDB().Where("user_id = ?", user.Id).First(&secretData).Error
	if err != nil {
		return nil, ErrInternal
	}
	
	// 验证密码
	err = models.CompareHashAndPassword(secretData.Password, req.Password, secretData.Salt)
	if err != nil {
		return nil, ErrForbidden
	}
	
	// 检查用户状态
	if user.IsBan || user.Status != models.UserStatusNormal {
		return nil, &MatrixError{
			ErrCode: "M_USER_DEACTIVATED",
			Error:   "User is deactivated or banned",
		}
	}
	
	// 生成访问令牌
	return a.createSession(account, req.DeviceID, req.InitialDeviceDisplayName, req.RefreshToken)
}

// loginWithToken Token 登录（支持社区系统 Token）
func (a *AuthService) loginWithToken(req *LoginRequest) (*LoginResponse, *MatrixError) {
	// Token 登录允许使用社区的现有 Token
	if req.Token == "" {
		return nil, ErrInvalidParam
	}
	
	// 尝试解析社区 Token
	tokenData, err := models.GetAuthTokenData(req.Token, a.HS.Config.EncryptedKey)
	if err != nil {
		return nil, ErrUnknownToken
	}
	
	// 检查 Token 是否过期
	if tokenData.ExpiredAt > 0 && tokenData.ExpiredAt < time.Now().Unix() {
		return nil, ErrUnknownToken
	}
	
	// 验证 Token 是否存在于数据库
	_, err = models.GetTokenByTokenStr(req.Token)
	if err != nil {
		return nil, ErrUnknownToken
	}
	
	// 获取用户信息
	var user models.User
	err = db.GetDB().Where("id = ?", tokenData.UserId).First(&user).Error
	if err != nil {
		return nil, ErrInternal
	}
	
	// 获取账户信息
	var account models.Account
	err = db.GetDB().Where("user_id = ?", tokenData.UserId).First(&account).Error
	if err != nil {
		return nil, ErrInternal
	}
	
	// 检查用户状态
	if user.IsBan || user.Status != models.UserStatusNormal {
		return nil, &MatrixError{
			ErrCode: "M_USER_DEACTIVATED",
			Error:   "User is deactivated or banned",
		}
	}
	
	// 创建 Matrix 会话
	return a.createSession(account, req.DeviceID, req.InitialDeviceDisplayName, req.RefreshToken)
}

// createSession 创建会话
func (a *AuthService) createSession(account models.Account, deviceID *string, displayName *string, withRefresh bool) (*LoginResponse, *MatrixError) {
	matrixUserID := fmt.Sprintf("@%s:%s", account.Username, a.HS.Config.ServerName)
	
	// 生成设备 ID
	if deviceID == nil || *deviceID == "" {
		id := generateDeviceID()
		deviceID = &id
	}
	
	// 生成访问令牌
	accessToken := generateAccessToken()
	
	// 检查设备是否已存在，如果存在则更新
	var refreshToken *string
	var existingSession MatrixUserSession
	err := db.GetDB().Where("user_id = ? AND device_id = ?", matrixUserID, *deviceID).First(&existingSession).Error
	if err == nil {
		// 更新现有会话
		updates := map[string]interface{}{
			"access_token":   accessToken,
			"display_name":   displayName,
			"last_active_ts": time.Now().UnixMilli(),
		}
		if withRefresh {
			rt := generateRefreshToken()
			refreshToken = &rt
			updates["refresh_token"] = rt
		}
		db.GetDB().Model(&existingSession).Updates(updates)
	} else {
		// 创建新会话
		session := &MatrixUserSession{
			UserID:         matrixUserID,
			AccessToken:    accessToken,
			DeviceID:       *deviceID,
			DisplayName:    displayName,
			LastActiveTS:   utils.GetPtr(time.Now().UnixMilli()),
			LocalAccountID: &account.Id,
		}

		if withRefresh {
			rt := generateRefreshToken()
			refreshToken = &rt
			session.RefreshToken = refreshToken
		}

		if err := db.GetDB().Create(session).Error; err != nil {
			return nil, ErrInternal
		}
	}

	// 构建响应
	resp := &LoginResponse{
		UserID:       matrixUserID,
		AccessToken:  accessToken,
		DeviceID:     *deviceID,
		RefreshToken: refreshToken,
		WellKnown:    &WellKnown{},
	}
	resp.WellKnown.Homeserver.BaseURL = fmt.Sprintf("https://%s", a.HS.Config.ServerName)

	return resp, nil
}

// ===== 注册流程 =====

// RegisterRequest 注册请求
type RegisterRequest struct {
	Username               string                 `json:"username"`
	Password               string                 `json:"password"`
	DeviceID               *string                `json:"device_id"`
	InitialDeviceDisplayName *string              `json:"initial_device_display_name"`
	InhibitLogin           bool                   `json:"inhibit_login"`
	RefreshToken           bool                   `json:"refresh_token"`
	Auth                   *UIAuthRequest         `json:"auth"`
}

// UIAuthRequest UIAA 认证请求
type UIAuthRequest struct {
	Type     string                 `json:"type"`
	Session  string                 `json:"session"`
	Params   map[string]interface{} `json:"-"`
}

// RegisterResponse 注册响应
type RegisterResponse struct {
	UserID      string  `json:"user_id"`
	AccessToken string  `json:"access_token,omitempty"`
	DeviceID    string  `json:"device_id,omitempty"`
	RefreshToken *string `json:"refresh_token,omitempty"`
}

// RegisterRegistrationsAvailable 是否允许注册
type RegisterRegistrationsAvailable struct {
	RegistrationAvailable bool `json:"registration_available"`
}

// UIAuthResponse is returned when User-Interactive Auth is needed
type UIAuthResponse struct {
	Session string                   `json:"session"`
	Flows   []map[string]interface{} `json:"flows"`
}

// NeedsUIAuth checks if the register request needs User-Interactive Auth
func (a *AuthService) NeedsUIAuth(req *RegisterRequest) *UIAuthResponse {
	if req.Auth != nil && req.Auth.Type == "m.login.dummy" {
		return nil
	}
	return &UIAuthResponse{
		Session: generateAccessToken()[:16],
		Flows: []map[string]interface{}{
			{"stages": []string{"m.login.dummy"}},
		},
	}
}

// Register 处理注册请求
func (a *AuthService) Register(req *RegisterRequest, isAdmin bool) (*RegisterResponse, *MatrixError) {

	if req.Username == "" {
		return nil, ErrInvalidUsername
	}

	req.Username = strings.ToLower(strings.TrimSpace(req.Username))

	// Validate localpart per Matrix spec: [a-z0-9._=/-]+
	if !validLocalpartRe.MatchString(req.Username) {
		return nil, ErrInvalidUsername
	}

	// 检查用户名是否已存在
	var count int64
	db.GetDB().Model(&models.Account{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		return nil, ErrUserInUse
	}

	// 创建本地用户
	return a.createLocalUser(req.Username, req.Password, req.DeviceID, req.InitialDeviceDisplayName, req.InhibitLogin, isAdmin, req.RefreshToken)
}

// createLocalUser 创建本地用户（User + Account + UserSecretData + MatrixSession）
func (a *AuthService) createLocalUser(username, password string, deviceID *string, displayName *string, inhibitLogin bool, isAdmin bool, withRefresh bool) (*RegisterResponse, *MatrixError) {
	if password == "" {
		password = "complement_default_password"
	}

	salt := utils.GetRandomString(64)
	hashedPassword, err := models.EncryptPassword(password, salt)
	if err != nil {
		return nil, ErrInternal
	}

	user := &models.User{
		UUID:           uuid.New().String(),
		UserName:       username,
		EmailHash:      fmt.Sprintf("matrix_%s_%d", username, time.Now().UnixNano()),
		Status:         models.UserStatusNormal,
		IsMailVerified: true,
		IsAdmin:        isAdmin,
	}

	account := &models.Account{
		Username:    username,
		DisplayName: username,
	}

	secret := &models.UserSecretData{
		Salt:     salt,
		Password: hashedPassword,
		Email:    fmt.Sprintf("%s@%s", username, a.HS.Config.ServerName),
	}

	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		secret.UserId = user.Id
		if err := tx.Create(secret).Error; err != nil {
			return err
		}
		account.UserId = user.Id
		if err := tx.Create(account).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Error("Failed to create local user %s: %v", username, err)
		return nil, ErrInternal
	}

	userID := fmt.Sprintf("@%s:%s", username, a.HS.Config.ServerName)

	if inhibitLogin {
		return &RegisterResponse{
			UserID: userID,
		}, nil
	}

	// 创建登录会话
	loginResp, loginErr := a.createSession(*account, deviceID, displayName, withRefresh)
	if loginErr != nil {
		return nil, loginErr
	}

	return &RegisterResponse{
		UserID:      loginResp.UserID,
		AccessToken: loginResp.AccessToken,
		DeviceID:    loginResp.DeviceID,
		RefreshToken: loginResp.RefreshToken,
	}, nil
}

// ===== Token 刷新 =====

// RefreshRequest 刷新请求
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshResponse 刷新响应
type RefreshResponse struct {
	AccessToken  string  `json:"access_token"`
	ExpiresIn    *int64  `json:"expires_in,omitempty"`
	RefreshToken *string `json:"refresh_token,omitempty"`
}

// Refresh 刷新访问令牌
func (a *AuthService) Refresh(req *RefreshRequest) (*RefreshResponse, *MatrixError) {
	var session MatrixUserSession
	err := db.GetDB().Where("refresh_token = ?", req.RefreshToken).First(&session).Error
	if err != nil {
		return nil, ErrUnknownToken
	}
	
	// 生成新令牌
	accessToken := generateAccessToken()
	refreshToken := generateRefreshToken()
	
	// 更新会话
	db.GetDB().Model(&session).Updates(map[string]interface{}{
		"access_token":   accessToken,
		"refresh_token":  refreshToken,
		"last_active_ts": time.Now().UnixMilli(),
	})
	
	return &RefreshResponse{
		AccessToken:  accessToken,
		RefreshToken: &refreshToken,
	}, nil
}

// ===== 登出 =====

// Logout 登出
func (a *AuthService) Logout(accessToken string) *MatrixError {
	result := db.GetDB().Where("access_token = ?", accessToken).Delete(&MatrixUserSession{})
	if result.RowsAffected == 0 {
		return nil // 已登出或不存在
	}
	return nil
}

// LogoutAll 登出所有设备
func (a *AuthService) LogoutAll(accessToken string) *MatrixError {
	// 获取用户ID
	var session MatrixUserSession
	err := db.GetDB().Where("access_token = ?", accessToken).First(&session).Error
	if err != nil {
		return ErrUnknownToken
	}
	
	// 删除所有会话
	db.GetDB().Where("user_id = ?", session.UserID).Delete(&MatrixUserSession{})
	return nil
}

// ===== 用户查询 =====

// GetUserIDByToken 通过 Token 获取用户ID
func (a *AuthService) GetUserIDByToken(accessToken string) (string, *MatrixError) {
	var session MatrixUserSession
	err := db.GetDB().Where("access_token = ?", accessToken).First(&session).Error
	if err != nil {
		return "", ErrUnknownToken
	}
	
	// 更新最后活跃时间
	db.GetDB().Model(&session).Update("last_active_ts", time.Now().UnixMilli())
	
	return session.UserID, nil
}

// GetDeviceByToken 通过 Token 获取设备信息
func (a *AuthService) GetDeviceByToken(accessToken string) (*MatrixUserSession, *MatrixError) {
	var session MatrixUserSession
	err := db.GetDB().Where("access_token = ?", accessToken).First(&session).Error
	if err != nil {
		return nil, ErrUnknownToken
	}
	return &session, nil
}

// ===== 工具函数 =====

// generateAccessToken 生成访问令牌
func generateAccessToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Error("Failed to generate random bytes for access token: %v", err)
		// 使用备用方案（不安全但总比 panic 好）
		return "syt_" + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return "syt_" + base64.RawURLEncoding.EncodeToString(b)
}

// generateRefreshToken 生成刷新令牌
func generateRefreshToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Error("Failed to generate random bytes for refresh token: %v", err)
		return "rtr_" + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return "rtr_" + base64.RawURLEncoding.EncodeToString(b)
}

// generateDeviceID 生成设备ID
func generateDeviceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		log.Error("Failed to generate random bytes for device ID: %v", err)
		return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashPassword 哈希密码（兼容社区密码格式）
func HashPassword(password, salt string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(salt+password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(hash), nil
}

// ===== 登录流程响应 =====

// GetLoginFlows 获取支持的登录流程
func (a *AuthService) GetLoginFlows() *LoginFlowsResponse {
	return &LoginFlowsResponse{
		Flows: []LoginFlow{
			{Type: "m.login.password"},
			{Type: "m.login.token"},
		},
	}
}

// ===== 用户存在检查 =====

// UserExists 检查用户是否存在
func (a *AuthService) UserExists(username string) bool {
	var count int64
	localpart := username
	if strings.HasPrefix(username, "@") {
		parts := strings.SplitN(username[1:], ":", 2)
		if len(parts) == 2 && parts[1] != a.HS.Config.ServerName {
			// 远程用户
			return false
		}
		if len(parts) >= 1 {
			localpart = parts[0]
		}
	}
	
	db.GetDB().Model(&models.Account{}).Where("username = ?", localpart).Count(&count)
	return count > 0
}

// GetLocalAccountID 获取本地账户ID
// VerifyUserPassword 验证用户密码（用于 UIA）
func (a *AuthService) VerifyUserPassword(matrixUserID, password string) bool {
	localpart, _, ok := ParseMatrixUserID(matrixUserID)
	if !ok {
		return false
	}
	var account models.Account
	if db.GetDB().Where("username = ?", localpart).First(&account).Error != nil {
		return false
	}
	var user models.User
	if db.GetDB().First(&user, account.UserId).Error != nil {
		return false
	}
	var secretData models.UserSecretData
	if db.GetDB().Where("user_id = ?", user.Id).First(&secretData).Error != nil {
		return false
	}
	return models.CompareHashAndPassword(secretData.Password, password, secretData.Salt) == nil
}

func (a *AuthService) GetLocalAccountID(matrixUserID string) (uint64, error) {
	localpart, _, ok := ParseMatrixUserID(matrixUserID)
	if !ok {
		return 0, fmt.Errorf("invalid matrix user id")
	}
	
	var account models.Account
	err := db.GetDB().Where("username = ?", localpart).First(&account).Error
	if err != nil {
		return 0, err
	}
	
	return account.Id, nil
}

// ===== 服务器发现 =====

// ServerVersion 服务器版本
type ServerVersion struct {
	Server struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"server"`
}

// GetServerVersion 获取服务器版本
func (a *AuthService) GetServerVersion() *ServerVersion {
	v := &ServerVersion{}
	v.Server.Name = "Community Matrix Homeserver"
	v.Server.Version = "1.0.0"
	return v
}

// ServerWellKnown 服务器发现
type ServerWellKnown struct {
	Homeserver struct {
		BaseURL string `json:"base_url"`
	} `json:"m.homeserver"`
	IdentityServer *struct {
		BaseURL string `json:"base_url"`
	} `json:"m.identity_server,omitempty"`
}

// GetWellKnown 获取服务器发现信息
func (a *AuthService) GetWellKnown() *ServerWellKnown {
	wk := &ServerWellKnown{}
	wk.Homeserver.BaseURL = fmt.Sprintf("https://%s", a.HS.Config.ServerName)
	return wk
}

// ===== 兼容旧版 =====

// ParseMatrixUserID 解析 Matrix 用户 ID
func ParseMatrixUserID(userID string) (localpart, domain string, ok bool) {
	if !strings.HasPrefix(userID, "@") {
		return "", "", false
	}
	parts := strings.SplitN(userID[1:], ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// FormatMatrixUserID 格式化 Matrix 用户 ID
func FormatMatrixUserID(localpart, domain string) string {
	return fmt.Sprintf("@%s:%s", localpart, domain)
}

// ===== 错误定义 =====

var (
	ErrForbidden     = &MatrixError{ErrCode: "M_FORBIDDEN", Error: "Forbidden"}
	ErrUnknownToken  = &MatrixError{ErrCode: "M_UNKNOWN_TOKEN", Error: "Unknown token"}
	ErrUnrecognized  = &MatrixError{ErrCode: "M_UNRECOGNIZED", Error: "Unrecognized request"}
	ErrInvalidParam  = &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Invalid parameter"}
	ErrInvalidUsername = &MatrixError{ErrCode: "M_INVALID_USERNAME", Error: "Invalid username"}
	ErrUserInUse     = &MatrixError{ErrCode: "M_USER_IN_USE", Error: "Username already in use"}
	ErrNotFound      = &MatrixError{ErrCode: "M_NOT_FOUND", Error: "Not found"}
	ErrInternal      = &MatrixError{ErrCode: "M_INTERNAL", Error: "Internal server error"}
	ErrUnauthorized  = &MatrixError{ErrCode: "M_UNAUTHORIZED", Error: "Unauthorized"}
)

// MatrixError Matrix 错误
type MatrixError struct {
	ErrCode string `json:"errcode"`
	Error   string `json:"error"`
}

// ToJSON 转换为 JSON
func (e *MatrixError) ToJSON() []byte {
	b, _ := json.Marshal(e)
	return b
}
