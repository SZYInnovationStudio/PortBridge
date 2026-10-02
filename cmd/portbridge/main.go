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

	// SIGHUP 的默认行为是终止进程，会导致 `systemctl reload` 意外杀掉服务。
	// 本程序暂不支持运行时热重载配置，这里显式接收并忽略 SIGHUP，只给出提示。
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			log.Println("[main] 收到 SIGHUP：本程序不支持热重载配置，已忽略；如需使配置生效请重启服务")
		}
	}()

	switch cfg.Role {
	case config.RoleClient:
		client.New(cfg, db).Run(ctx)
	default:
		server.New(cfg, db).Run(ctx)
	}
	os.Exit(0)
}
