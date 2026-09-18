package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const readinessTimeout = 2 * time.Second

type databasePinger interface {
	Ping(context.Context) (int32, error)
}

type migrationChecker interface {
	HasPending(context.Context) (bool, error)
}

// Readiness 检查数据库和 schema 是否可供当前进程使用。
type Readiness struct {
	database   databasePinger
	migrations migrationChecker
	logger     *slog.Logger
	timeout    time.Duration
}

// NewReadiness 创建 schema 就绪检查器。
func NewReadiness(database databasePinger, migrations migrationChecker, logger *slog.Logger) *Readiness {
	return &Readiness{database: database, migrations: migrations, logger: logger, timeout: readinessTimeout}
}

// Check 返回当前数据库就绪状态。
func (r *Readiness) Check(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), r.timeout)
	defer cancel()

	if _, err := r.database.Ping(ctx); err != nil {
		r.logger.Error("readiness database check failed", "error", err)
		c.JSON(http.StatusServiceUnavailable, readinessResponse{Status: "not_ready", Reason: "database_unavailable"})
		return
	}
	pending, err := r.migrations.HasPending(ctx)
	if err != nil {
		r.logger.Error("readiness migration check failed", "error", err)
		c.JSON(http.StatusServiceUnavailable, readinessResponse{Status: "not_ready", Reason: "schema_unavailable"})
		return
	}
	if pending {
		c.JSON(http.StatusServiceUnavailable, readinessResponse{Status: "not_ready", Reason: "schema_outdated"})
		return
	}
	c.JSON(http.StatusOK, readinessResponse{Status: "ready"})
}

type readinessResponse struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Server 组合由各协议边界实现的 strict HTTP 操作。
type Server struct {
	*AuthServer
	*ListingServer
}

// NewRouter 创建健康检查和后端 API 路由。
func NewRouter(readiness *Readiness, authServer *AuthServer, listingServer *ListingServer) http.Handler {
	router := gin.New()
	router.Use(gin.Recovery())
	_ = router.SetTrustedProxies(nil)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/ready", readiness.Check)
	if authServer != nil || listingServer != nil {
		gin.EnableJsonDecoderDisallowUnknownFields()
		strict := NewStrictHandlerWithOptions(&Server{AuthServer: authServer, ListingServer: listingServer}, nil, StrictGinServerOptions{
			RequestErrorHandlerFunc: func(c *gin.Context, _ error) {
				p := problem(http.StatusBadRequest, ValidationFailed, "Validation failed", "request body is invalid")
				c.Header("Content-Type", "application/problem+json")
				c.JSON(http.StatusBadRequest, p)
			},
		})
		RegisterHandlersWithOptions(router, strict, GinServerOptions{BaseURL: "/api/v1"})
	}
	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	})
	return router
}
