// Command uz801d is the MSM8916 (Qualcomm 8916) OpenStick web management daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"uz801d/internal/auth"
	"uz801d/internal/buildinfo"
	"uz801d/internal/config"
	"uz801d/internal/server"
	"uz801d/internal/state"
	"uz801d/internal/svc"
)

func main() {
	var (
		configPath = flag.String("config", defaultConfigPath(), "配置文件路径")
		statePath  = flag.String("state", defaultStatePath(), "状态文件路径")
		listen     = flag.String("listen", "", "监听地址，例如 :80")
		initOnly   = flag.Bool("init", false, "初始化配置与随机密码后退出")
		password   = flag.String("password", "", "配合 -init 设置管理密码")
		resetPass  = flag.Bool("reset-password", false, "生成新的随机管理密码并退出")
		showVer    = flag.Bool("version", false, "显示版本信息")
		debug      = flag.Bool("debug", false, "输出调试日志")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("%s %s (%s, %s)\n", buildinfo.Name, buildinfo.Version, buildinfo.Commit, buildinfo.BuildTime)
		return
	}

	logger := log.New(os.Stderr, "uz801d: ", log.LstdFlags)
	if *debug {
		logger.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	cfg, created, err := loadOrCreate(*configPath, logger)
	if err != nil {
		logger.Fatalf("加载配置失败: %v", err)
	}
	if created {
		logger.Printf("已创建默认配置 %s", *configPath)
	}
	if migrated, err := migrateLegacyListen(*configPath, cfg); err != nil {
		logger.Printf("迁移监听端口失败: %v", err)
	} else if migrated {
		logger.Printf("监听端口已从 8080 迁移为 80")
	}
	if *listen != "" {
		cfg.Listen = *listen
		cfg.Normalize()
		if err := config.Save(*configPath, cfg); err != nil {
			logger.Fatalf("保存监听地址失败: %v", err)
		}
	}

	if *resetPass {
		pass, err := auth.RandomPassword(12)
		if err != nil {
			logger.Fatalf("生成密码失败: %v", err)
		}
		hash, err := auth.HashPassword(pass)
		if err != nil {
			logger.Fatalf("生成密码失败: %v", err)
		}
		cfg.PasswordHash = hash
		if err := config.Save(*configPath, cfg); err != nil {
			logger.Fatalf("保存密码失败: %v", err)
		}
		path := filepath.Join(filepath.Dir(*configPath), "initial-password")
		_ = os.WriteFile(path, []byte(fmt.Sprintf("username: %s\npassword: %s\n", cfg.AdminUser, pass)), 0o600)
		fmt.Printf("管理员用户名: %s\n管理员密码: %s\n已写入 %s\n", cfg.AdminUser, pass, path)
		return
	}

	if cfg.PasswordHash == "" {
		pass := *password
		if pass == "" {
			var err error
			pass, err = auth.RandomPassword(12)
			if err != nil {
				logger.Fatalf("生成密码失败: %v", err)
			}
		}
		if len(pass) < 8 {
			logger.Fatalf("密码至少需要 8 个字符")
		}
		hash, err := auth.HashPassword(pass)
		if err != nil {
			logger.Fatalf("生成密码失败: %v", err)
		}
		cfg.PasswordHash = hash
		if err := config.Save(*configPath, cfg); err != nil {
			logger.Fatalf("保存密码失败: %v", err)
		}
		path := filepath.Join(filepath.Dir(*configPath), "initial-password")
		_ = os.WriteFile(path, []byte(fmt.Sprintf("username: %s\npassword: %s\n", cfg.AdminUser, pass)), 0o600)
		logger.Printf("已生成初始管理员账号: %s", cfg.AdminUser)
		logger.Printf("初始密码: %s", pass)
		logger.Printf("初始密码同时写入 %s，登录后请立即修改", path)
	}

	if *initOnly {
		fmt.Printf("配置已初始化: %s\n管理员用户名: %s\n", *configPath, cfg.AdminUser)
		return
	}

	store, err := state.Open(*statePath)
	if err != nil {
		logger.Fatalf("打开状态文件失败: %v", err)
	}
	m, err := svc.New(cfg, svc.Options{
		ConfigPath: *configPath,
		StatePath:  *statePath,
		Logger:     logger,
		Store:      store,
	})
	if err != nil {
		logger.Fatalf("初始化服务失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	if len(cfg.LEDs) > 0 {
		go m.ApplyLEDs()
	}
	// When the bridge was left enabled, svc.Start resumes it (and falls back to
	// the normal AP on failure); starting the normal AP here would fight it.
	if cfg.Wifi.ApplyOnStart && !cfg.Wifi.Bridge.Enabled {
		go func() {
			if err := m.ApplyWifi(ctx); err != nil {
				logger.Printf("启动时应用 WiFi 配置失败: %v", err)
			}
		}()
	}

	srv := server.New(m)
	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				srv.Sessions().Cleanup()
				srv.Limiter().Cleanup()
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		logger.Printf("%s %s 已启动，监听 %s", buildinfo.Name, buildinfo.Version, cfg.Listen)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		logger.Printf("收到信号 %v，正在退出", sig)
	case err := <-errCh:
		logger.Printf("HTTP 服务异常: %v", err)
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	m.Stop()
	logger.Printf("已退出")
}

func defaultConfigPath() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.TempDir()
		}
		return filepath.Join(base, "uz801d", "config.json")
	}
	return config.DefaultConfigPath
}

func defaultStatePath() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.TempDir()
		}
		return filepath.Join(base, "uz801d", "state.json")
	}
	return config.DefaultStatePath
}

func loadOrCreate(path string, logger *log.Logger) (*config.Config, bool, error) {
	cfg, err := config.Load(path)
	if err == nil {
		return cfg, false, nil
	}
	if !os.IsNotExist(err) {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	cfg = config.Default()
	if runtime.GOOS == "windows" {
		cfg.Listen = "127.0.0.1:80"
	}
	if err := config.Save(path, cfg); err != nil {
		return nil, false, err
	}
	logger.Printf("配置文件不存在，已创建默认配置")
	return cfg, true, nil
}

// migrateLegacyListen moves the old default port 8080 to 80 so upgrading
// devices follow the new default without editing config.json by hand. An
// explicit -listen flag is applied afterwards and still wins.
func migrateLegacyListen(path string, cfg *config.Config) (bool, error) {
	old := cfg.Listen
	switch old {
	case ":8080", "0.0.0.0:8080":
		cfg.Listen = ":80"
	case "127.0.0.1:8080":
		cfg.Listen = "127.0.0.1:80"
	default:
		return false, nil
	}
	if err := config.Save(path, cfg); err != nil {
		cfg.Listen = old
		return false, err
	}
	return true, nil
}
