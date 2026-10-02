// Command portbridge 是 PortBridge 的统一可执行入口：
// 同一份程序通过配置中的 role 选择运行「中转端(server)」或「原站端(client)」。
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"portbridge/internal/client"
	"portbridge/internal/config"
	"portbridge/internal/server"
	"portbridge/internal/store"
	"portbridge/internal/version"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.StringVar(configPath, "c", "config.yaml", "配置文件路径（简写）")
	showVersion := flag.Bool("version", false, "打印版本后退出")
	flag.Parse()

	if *showVersion {
		log.Printf("PortBridge %s (build %s)", version.Version, version.BuildTime)
		return
	}

	cfg := config.MustLoad(*configPath)
	db := store.Open(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cfg.Role {
	case config.RoleClient:
		client.New(cfg, db).Run(ctx)
	default:
		server.New(cfg, db).Run(ctx)
	}
	os.Exit(0)
}
