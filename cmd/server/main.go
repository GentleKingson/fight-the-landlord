package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/observability"
	"github.com/palemoky/fight-the-landlord/internal/server"
)

// version 是服务端版本号，可通过编译时 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	devDefaults := flag.Bool("dev-defaults", false, "配置加载失败时使用开发默认值")
	healthcheck := flag.Bool("healthcheck", false, "检查本地服务健康状态后退出")
	healthcheckURL := flag.String("healthcheck-url", defaultHealthcheckURL(), "健康检查地址")
	flag.Parse()
	if *healthcheck {
		if err := checkHealth(*healthcheckURL); err != nil {
			log.Printf("健康检查失败: %v", err)
			os.Exit(1)
		}
		return
	}

	// 将注入的版本号传递给 server 包，供 /version 接口公布
	server.Version = version

	// 加载配置
	cfg, err := loadServerConfig(*configPath, *devDefaults)
	if err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}
	if _, err := observability.ConfigureDefaultLogger(cfg.Observability.LogFormat, os.Stderr); err != nil {
		log.Fatalf("配置日志失败: %v", err)
	}

	// 创建服务器
	srv, err := server.NewServer(cfg)
	if err != nil {
		log.Fatalf("创建服务器失败: %v", err)
	}

	// 监听关闭信号
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 启动服务器
	go func() {
		slog.Info("server starting", "event", "server_starting", "protocol_version", server.Version)
		if err := srv.Start(); err != nil {
			slog.Error("server start failed", "event", "server_start_failed", "error_code", "listen_failed", "error", err)
			os.Exit(1)
		}
	}()

	// 等待关闭信号
	<-ctx.Done()
	slog.Info("shutdown signal received", "event", "shutdown_started")
	srv.GracefulShutdown(cfg.Game.ShutdownTimeoutDuration())
}

func loadServerConfig(path string, devDefaults bool) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err == nil {
		return cfg, nil
	}
	if !devDefaults {
		return nil, fmt.Errorf("load config: %w", err)
	}
	log.Printf("配置加载失败，显式使用开发默认值: %v", err)
	return config.Default(), nil
}

func defaultHealthcheckURL() string {
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "1780"
	}
	return "http://127.0.0.1:" + port + "/health"
}

func checkHealth(endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request health endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", resp.Status)
	}
	return nil
}
