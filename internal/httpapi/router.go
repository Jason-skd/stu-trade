package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

type schemaVersionReader interface {
	GetSchemaVersion(context.Context) (int64, error)
}

// Readiness 检查数据库和 schema 是否可供当前进程使用。
type Readiness struct {
	versions schemaVersionReader
	required int64
}

// NewReadiness 创建 schema 就绪检查器。
func NewReadiness(versions schemaVersionReader, required int64) *Readiness {
	return &Readiness{versions: versions, required: required}
}

// Check 返回当前数据库就绪状态。
func (r *Readiness) Check(c *gin.Context) {
	version, err := r.versions.GetSchemaVersion(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, readinessResponse{Status: "not_ready", Reason: "database_unavailable"})
		return
	}
	if version < r.required {
		c.JSON(http.StatusServiceUnavailable, readinessResponse{Status: "not_ready", Reason: "schema_outdated", SchemaVersion: version})
		return
	}
	c.JSON(http.StatusOK, readinessResponse{Status: "ready", SchemaVersion: version})
}

type readinessResponse struct {
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	SchemaVersion int64  `json:"schemaVersion,omitempty"`
}

// NewRouter 创建健康检查和后端 API 路由。
func NewRouter(readiness *Readiness) http.Handler {
	router := gin.New()
	router.Use(gin.Recovery())
	_ = router.SetTrustedProxies(nil)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/ready", readiness.Check)
	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	})
	return router
}
