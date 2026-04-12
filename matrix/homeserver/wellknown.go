package homeserver

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// WellKnownService Well-Known 服务
type WellKnownService struct {
	hs *HomeServer
}

// NewWellKnownService 创建 WellKnown 服务
func NewWellKnownService(hs *HomeServer) *WellKnownService {
	return &WellKnownService{hs: hs}
}

// ServerInfo 服务器发现信息
type ServerInfo struct {
	ServerName string `json:"m.server"`
}

// ClientInfo 客户端发现信息
type ClientInfo struct {
	HomeServer     HomeServerInfo      `json:"m.homeserver"`
	IdentityServer *IdentityServerInfo `json:"m.identity_server,omitempty"`
}

// HomeServerInfo 主服务器信息
type HomeServerInfo struct {
	BaseURL string `json:"base_url"`
}

// IdentityServerInfo 身份服务器信息
type IdentityServerInfo struct {
	BaseURL string `json:"base_url"`
}

// GetServerInfo 获取服务器发现信息
func (ws *WellKnownService) GetServerInfo() *ServerInfo {
	server := ws.hs.Config.ServerName
	if ws.hs.Config.FederationPort != 443 {
		server = fmt.Sprintf("%s:%d", server, ws.hs.Config.FederationPort)
	}
	return &ServerInfo{
		ServerName: server,
	}
}

// GetClientInfo 获取客户端发现信息
func (ws *WellKnownService) GetClientInfo() *ClientInfo {
	return &ClientInfo{
		HomeServer: HomeServerInfo{
			BaseURL: ws.hs.Config.BaseURL,
		},
		IdentityServer: &IdentityServerInfo{
			BaseURL: ws.hs.Config.BaseURL,
		},
	}
}

// RegisterWellKnownRoutes 注册 WellKnown 路由
func RegisterWellKnownRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 服务器发现（用于联邦）
	r.GET("/.well-known/matrix/server", handleWellKnownServer)

	// 客户端发现
	r.GET("/.well-known/matrix/client", handleWellKnownClient)
}

// handleWellKnownServer 处理服务器发现请求
func handleWellKnownServer(c *gin.Context) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Server not configured",
		})
		return
	}

	info := hs.WellKnown.GetServerInfo()
	c.JSON(http.StatusOK, info)
}

// handleWellKnownClient 处理客户端发现请求
func handleWellKnownClient(c *gin.Context) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Server not configured",
		})
		return
	}

	info := hs.WellKnown.GetClientInfo()
	c.JSON(http.StatusOK, info)
}
