package media

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册媒体路由.
func RegisterRoutes(rg *gin.RouterGroup) {
	mediaGroup := rg.Group("/media")

	// 需要写权限
	mediaGroup.POST("", Upload)         // 上传媒体
	mediaGroup.POST("/v2", UploadAsync) // 异步上传（返回 202）

	// 需要读权限
	mediaGroup.GET("/:id", Get) // 获取媒体信息

	// 需要写权限
	mediaGroup.PUT("/:id", Update)    // 更新媒体信息
	mediaGroup.DELETE("/:id", Delete) // 删除媒体
}
