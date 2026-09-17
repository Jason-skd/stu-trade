package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Jason-skd/stu-trade/internal/auth"
	"github.com/Jason-skd/stu-trade/internal/db"
	"github.com/Jason-skd/stu-trade/internal/httpapi"
	"github.com/Jason-skd/stu-trade/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

const startupTimeout = 5 * time.Second

// Serve 装配依赖并运行 HTTP 服务，直到上下文取消或服务失败。
func Serve(ctx context.Context, cfg Config, logger *slog.Logger) error {
	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()

	// === 准备数据库 ===
	if cfg.RunMigrations {
		if err := migrations.Up(startupCtx, cfg.DatabaseURL, logger); err != nil {
			return err
		}
	}
	pool, err := pgxpool.New(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(startupCtx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	migrationChecker, err := migrations.NewChecker(pool, logger)
	if err != nil {
		return err
	}
	defer migrationChecker.Close()

	// === 装配协议入口 ===
	queries := db.New(pool)
	readiness := httpapi.NewReadiness(queries, migrationChecker, logger)
	authServer := httpapi.NewAuthServer(auth.NewService(auth.NewPostgresStore(pool), time.Now, nil), logger, cfg.Profile == ProfileProduction)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(readiness, authServer),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// === 运行服务 ===
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr, "profile", cfg.Profile, "version", cfg.Version, "commit", cfg.Commit)
		serverErr <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve http: %w", err)
		}
		return nil
	}

	// === 停止服务 ===
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	return nil
}

// Migrate 将数据库迁移到内嵌 migration 的最新版本。
func Migrate(ctx context.Context, cfg Config, logger *slog.Logger) error {
	return migrations.Up(ctx, cfg.DatabaseURL, logger)
}
