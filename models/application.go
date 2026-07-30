package models

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
	"reflect"
	"time"
)

const (
	// 读取权限单独一组.

	AppScopeRead              = 0xFFFFFFFFFFFFFFFF // 读取你的账户数据
	AppScopeReadAccounts      = 0x1                // 查看账号信息
	AppScopeReadBlocks        = 0x3                // 查看你的屏蔽列表
	AppScopeReadBookmarks     = 0x7                // 查看你的书签
	AppScopeReadFavourites    = 0xF                // 查看喜欢的嘟文
	AppScopeReadFilters       = 0x1F               // 查看你的过滤器
	AppScopeReadFollows       = 0x3F               // 查看你的关注
	AppScopeReadLists         = 0x7F               // 查看你的列表
	AppScopeReadMutes         = 0xFF               // 查看你的隐藏列表
	AppScopeReadNotifications = 0x1FF              // 查看你的通知
	AppScopeReadSearch        = 0x3FF              // 以你的身份搜索
	AppScopeReadStatuses      = 0x7FF              // 查看所有嘟文

	// 写入权限单独一组.

	AppScopeWrite              = 0xFFFFFFFFFFFFFFFF // 写入你的账户数据
	AppScopeWriteAccounts      = 0x1                // 修改你的个人资料
	AppScopeWriteBlocks        = 0x3                // 屏蔽账号和域名
	AppScopeWriteBookmarks     = 0x7                // 为嘟文添加书签
	AppScopeWriteConversations = 0xF                // 静音并删除会话
	AppScopeWriteFavourites    = 0x1F               // 喜欢的嘟文
	AppScopeWriteFilters       = 0x3F               // 创建过滤器
	AppScopeWriteFollows       = 0x7F               // 关注其他人
	AppScopeWriteLists         = 0xFF               // 创建列表
	AppScopeWriteMedia         = 0x1FF              // 上传媒体文件
	AppScopeWriteMutes         = 0x3FF              // 隐藏用户和对话
	AppScopeWriteNotifications = 0x7FF              // 清除你的通知
	AppScopeWriteReports       = 0xFFF              // 举报他人
	AppScopeWriteStatuses      = 0x1FFF             // 发表嘟文

	// 管理权限单独一组.

	AppScopeAdmin                          = 0xFFFFFFFFFFFFFFFF // 管理服务器上的所有数据
	AppScopeAdminRead                      = 0xFFFFFFFF
	AppScopeAdminReadAccounts              = 0x1
	AppScopeAdminReadCanonicalEmailBlocks  = 0x3
	AppScopeAdminReadDomainAllows          = 0x7
	AppScopeAdminReadDomainBlocks          = 0xF
	AppScopeAdminReadEmailDomainBlocks     = 0x1F
	AppScopeAdminReadIpBlocks              = 0x3F
	AppScopeAdminReadReports               = 0x7F
	AppScopeAdminWrite                     = 0xFFFFFFFF00000000
	AppScopeAdminWriteAccounts             = 0x100000000
	AppScopeAdminWriteCanonicalEmailBlocks = 0x300000000
	AppScopeAdminWriteDomainAllows         = 0x700000000
	AppScopeAdminWriteDomainBlocks         = 0xF00000000
	AppScopeAdminWriteEmailDomainBlocks    = 0x1F00000000
	AppScopeAdminWriteIpBlocks             = 0x3F00000000
	AppScopeAdminWriteReports              = 0x7F00000000
	AppScopeAdminWriteRules                = 0xF000000000
	AppScopeAdminReadThumbnails            = 0x1F000000000
	AppScopeAdminWriteThumbnails           = 0x3F000000000

	// 其他权限单独一组.
	AppScopeFollow = 0x1
	AppScopePush   = 0x2
	AppScopeCrypto = 0x4

	// 权限类型.
	AppScopeTypeRead  = 0
	AppScopeTypeWrite = 1
	AppScopeTypeAdmin = 2
	AppScopeTypeOther = 3
)

type ApplicationScope struct {
	ReadScope  uint64 `json:"read_scope"`
	WriteScope uint64 `json:"write_scope"`
	AdminScope uint64 `json:"admin_scope"`
	OtherScope uint64 `json:"other_scope"`
}

func (s *ApplicationScope) HasScope(scopeType uint8, scope uint64) bool {
	var sc uint64

	switch scopeType {
	case AppScopeTypeRead:
		sc = s.ReadScope
	case AppScopeTypeWrite:
		sc = s.WriteScope
	case AppScopeTypeAdmin:
		sc = s.AdminScope
	case AppScopeTypeOther:
		sc = s.OtherScope
	default:
		return false
	}

	return (sc & scope) == scope
}

func (s *ApplicationScope) AddScope(scopeType uint8, scope uint64) {
	switch scopeType {
	case AppScopeTypeRead:
		s.ReadScope |= scope
	case AppScopeTypeWrite:
		s.WriteScope |= scope
	case AppScopeTypeAdmin:
		s.AdminScope |= scope
	case AppScopeTypeOther:
		s.OtherScope |= scope
	}
}

func (s *ApplicationScope) RemoveScope(scopeType uint8, scope uint64) {
	switch scopeType {
	case AppScopeTypeRead:
		s.ReadScope &= ^scope
	case AppScopeTypeWrite:
		s.WriteScope &= ^scope
	case AppScopeTypeAdmin:
		s.AdminScope &= ^scope
	case AppScopeTypeOther:
		s.OtherScope &= ^scope
	}
}

// MarshalJSON JSON序列化
// 把3个uint64 转换成byte数组,然后按照顺序拼接起来再转换成hex字符串.
func (s *ApplicationScope) MarshalJSON() ([]byte, error) {
	rValue := reflect.ValueOf(s).Elem()
	vBytes := make([]byte, 0, rValue.NumField()*8)

	for _, field := range rValue.Fields() {
		v := field.Uint()
		// uint64转换成byte数组
		bytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(bytes, v)
		vBytes = append(vBytes, bytes...)
	}

	// 拼接成hex字符串，用双引号包裹使其成为合法 JSON 字符串
	ret := `"` + hex.EncodeToString(vBytes) + `"`

	return []byte(ret), nil
}

// UnmarshalJSON JSON反序列化
// 把hex字符串转换成byte数组,然后按照8个byte分割成3个uint64.
func (s *ApplicationScope) UnmarshalJSON(data []byte) (err error) {
	// 去除可能的 JSON 字符串引号
	str := string(data)
	if len(str) >= 2 && str[0] == '"' && str[len(str)-1] == '"' {
		str = str[1 : len(str)-1]
	}
	vBytes, err := hex.DecodeString(str)
	if err != nil {
		return err
	}

	rValue := reflect.ValueOf(s).Elem()

	expectedLen := rValue.NumField() * 8
	if len(vBytes) < expectedLen {
		return errors.Errorf("invalid scope data length: expected %d bytes, got %d", expectedLen, len(vBytes))
	}

	for i := range rValue.NumField() {
		// 每次取8个byte
		bytes := vBytes[i*8 : (i+1)*8]
		// byte数组转换成uint64
		v := binary.LittleEndian.Uint64(bytes)
		rValue.Field(i).SetUint(v)
	}

	return
}

func (s *ApplicationScope) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue any) (err error) {
	var vBytes []byte

	switch dbValue := dbValue.(type) {
	case []byte:
		vBytes = dbValue
	case string:
		vBytes = []byte(dbValue)
	default:
		return errors.Errorf("invalid type %#v", dbValue)
	}

	return s.UnmarshalJSON(vBytes)
}

func (s *ApplicationScope) Value(ctx context.Context, field *schema.Field, dst reflect.Value, fieldValue any) (ret any, err error) {
	return s.MarshalJSON()
}

type Application struct {
	Id          uint64           `gorm:"primary_key;autoIncrement"          json:"id"`
	Uuid        *uuid.UUID       `gorm:"type:char(36);uniqueIndex;not null" json:"uuid"`
	UserId      int64            `gorm:"index"                              json:"user_id"`
	Name        string           `gorm:"type:varchar(32);not null"          json:"name"`
	Website     string           `gorm:"type:varchar(255);not null"         json:"website"`
	RedirectUri string           `gorm:"type:varchar(255);not null"         json:"redirect_uri"`
	Scopes      ApplicationScope `gorm:"type:varchar(255);not null"         json:"scopes"`
	IsEnabled   bool             `gorm:"not null;default:true"              json:"is_enabled"`
	CreatedAt   *time.Time       `gorm:"autoCreateTime"                     json:"created_at"`
	UpdatedAt   *time.Time       `gorm:"autoUpdateTime"                     json:"updated_at"`
	DeletedAt   gorm.DeletedAt   `gorm:"index"                              json:"deleted_at"`
}

func (a *Application) BeforeCreate(tx *gorm.DB) (err error) {
	if a.Uuid == nil {
		uuidObj := uuid.New()
		a.Uuid = &uuidObj
	}

	return
}

func (a *Application) TableName() string {
	return "applications"
}

type ApplicationUserAuth struct {
	ID uint64 `gorm:"primary_key;autoIncrement" json:"id"`
	// 应用ID
	ApplicationId uint64 `gorm:"index" json:"application_id"`
	// 用户ID
	UserId    int64      `gorm:"index"          json:"user_id"`
	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt *time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (a *ApplicationUserAuth) TableName() string {
	return "application_user_auths"
}
