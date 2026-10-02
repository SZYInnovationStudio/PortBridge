package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Role 运行角色：中转端 / 原站端
type Role string

const (
	RoleServer Role = "server" // 中转端（公网）
	RoleClient Role = "client" // 原站端（内网）
)

// Config 全局配置
type Config struct {
	Role     Role   `yaml:"role"`
	NodeName string `yaml:"node_name"`

	// 管理面（Web UI + REST + 控制通道），默认 :23255
	AdminAddr string `yaml:"admin_addr"`
	AdminPort int    `yaml:"admin_port"`
	JWTSecret string `yaml:"jwt_secret"`

	// 数据面（裸 TCP），默认 :23256
	DataAddr string `yaml:"data_addr"`
	DataPort int    `yaml:"data_port"`

	// 原站端专用：中转端地址与预共享密钥
	ServerAddr     string `yaml:"server_addr"`      // 例如 203.0.113.10:23255
	ServerDataAddr string `yaml:"server_data_addr"` // 例如 203.0.113.10:23256，可留空自动推导
	Token          string `yaml:"token"`

	DBDriver string `yaml:"db_driver"` // sqlite | postgres
	DSN      string `yaml:"dsn"`

	TLSEnabled  bool     `yaml:"tls_enabled"`
	TLSCert     string   `yaml:"tls_cert"`
	TLSKey      string   `yaml:"tls_key"`
	IPWhitelist []string `yaml:"ip_whitelist"` // 允许连接数据面的来源 CIDR

	HeartbeatSec int    `yaml:"heartbeat_sec"`
	ProxyPortMin int    `yaml:"proxy_port_min"`
	ProxyPortMax int    `yaml:"proxy_port_max"`
	WebDistPath  string `yaml:"web_dist_path"`
	LogLevel     string `yaml:"log_level"`
}

func (c *Config) applyDefaults() {
	if c.Role == "" {
		c.Role = RoleServer
	}
	if c.NodeName == "" {
		host, _ := os.Hostname()
		c.NodeName = host
	}
	if c.AdminPort == 0 {
		c.AdminPort = 23255
	}
	if c.AdminAddr == "" {
		c.AdminAddr = fmt.Sprintf("0.0.0.0:%d", c.AdminPort)
	}
	if c.DataPort == 0 {
		c.DataPort = 23256
	}
	if c.DataAddr == "" {
		c.DataAddr = fmt.Sprintf("0.0.0.0:%d", c.DataPort)
	}
	if c.DBDriver == "" {
		c.DBDriver = "sqlite"
	}
	if c.DSN == "" {
		c.DSN = "data/portbridge.db"
	}
	if c.HeartbeatSec <= 0 {
		c.HeartbeatSec = 10
	}
	if c.ProxyPortMin == 0 {
		c.ProxyPortMin = 6000
	}
	if c.ProxyPortMax == 0 {
		c.ProxyPortMax = 7000
	}
	if c.WebDistPath == "" {
		c.WebDistPath = "web/dist"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}

// applyEnv 用环境变量覆盖关键配置，便于容器化部署
func (c *Config) applyEnv() {
	if v := os.Getenv("PORTBRIDGE_ROLE"); v != "" {
		c.Role = Role(v)
	}
	if v := os.Getenv("PORTBRIDGE_NODE_NAME"); v != "" {
		c.NodeName = v
	}
	if v := os.Getenv("PORTBRIDGE_SERVER_ADDR"); v != "" {
		c.ServerAddr = v
	}
	if v := os.Getenv("PORTBRIDGE_SERVER_DATA_ADDR"); v != "" {
		c.ServerDataAddr = v
	}
	if v := os.Getenv("PORTBRIDGE_TOKEN"); v != "" {
		c.Token = v
	}
	if v := os.Getenv("PORTBRIDGE_JWT_SECRET"); v != "" {
		c.JWTSecret = v
	}
	if v := os.Getenv("PORTBRIDGE_ADMIN_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.AdminPort = p
			c.AdminAddr = fmt.Sprintf("0.0.0.0:%d", p)
		}
	}
	if v := os.Getenv("PORTBRIDGE_DSN"); v != "" {
		c.DSN = v
	}
	if v := os.Getenv("PORTBRIDGE_DB_DRIVER"); v != "" {
		c.DBDriver = v
	}
}

// Load 从 yaml 文件加载配置；文件不存在时使用默认值
func Load(path string) (*Config, error) {
	cfg := &Config{}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("解析配置文件失败: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
	}
	cfg.applyDefaults()
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// MustLoad 加载失败直接 panic
func MustLoad(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		panic(err)
	}
	return cfg
}

// weakJWTSecret 历史版本内置的占位弱密钥，出现即视为未配置
const weakJWTSecret = "portbridge-change-me"

func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 极端情况下的兜底，仍然避免使用固定值
		return fmt.Sprintf("pb-%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}

// Validate 校验配置合法性
func (c *Config) Validate() error {
	if c.Role != RoleServer && c.Role != RoleClient {
		return fmt.Errorf("非法角色 %q，仅支持 server / client", c.Role)
	}
	if c.JWTSecret == "" || c.JWTSecret == weakJWTSecret {
		// 使用内置占位弱密钥属于严重安全隐患，自动替换为随机值
		c.JWTSecret = randomSecret()
		log.Println("[config] 警告: 未配置 jwt_secret 或使用了默认弱密钥，已生成临时随机密钥；重启后登录态会失效，建议在配置文件中固定 jwt_secret")
	}
	if c.Role == RoleClient {
		if c.ServerAddr == "" {
			return fmt.Errorf("原站端必须配置 server_addr")
		}
		if c.Token == "" {
			return fmt.Errorf("原站端必须配置 token")
		}
	}
	if c.DBDriver != "sqlite" && c.DBDriver != "postgres" {
		return fmt.Errorf("不支持的数据库类型 %q", c.DBDriver)
	}
	return nil
}

// AgentControlURL 控制通道地址（ws/wss）
func (c *Config) AgentControlURL() string {
	scheme := "ws"
	if c.TLSEnabled {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/api/v1/agent/control", scheme, c.normalizedServerAddr())
}

// ServerDataEndpoint 数据面地址，未显式配置时用 server_addr 的主机 + data_port 推导
func (c *Config) ServerDataEndpoint() string {
	if c.ServerDataAddr != "" {
		return c.ServerDataAddr
	}
	host, _, err := net.SplitHostPort(c.normalizedServerAddr())
	if err != nil {
		return fmt.Sprintf("%s:%d", c.ServerAddr, c.DataPort)
	}
	return fmt.Sprintf("%s:%d", host, c.DataPort)
}

func (c *Config) normalizedServerAddr() string {
	addr := strings.TrimSpace(c.ServerAddr)
	if addr == "" {
		return addr
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return fmt.Sprintf("%s:%d", addr, c.AdminPort)
}
