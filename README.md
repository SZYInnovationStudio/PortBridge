# PortBridge

PortBridge 是一套**仅限 Linux 运行**的类 FRP 四层端口转发 / 端口映射系统。
它把一个部署在**公网**的「中转端」和一个部署在**内网**的「原站端」配对起来，
让外部用户通过访问中转端的公网 `IP:端口`，即可透明访问到原站端内网的任意 `IP:端口`。

- 只做 **TCP / UDP 四层透明转发**，不解析任何应用层协议
- 两端使用**同一套程序**，通过配置选择角色，安装后即可用**同一套 Web 管理平台**
- 原站端**主动**连接中转端，天然适配 NAT / 防火墙内网环境
- 单二进制部署，也可 Docker / docker-compose 部署

---

## 目录

- [架构与角色](#架构与角色)
- [核心特性](#核心特性)
- [端口说明](#端口说明)
- [快速开始（裸机）](#快速开始裸机)
- [Web 管理平台配对流程](#web-管理平台配对流程)
- [Docker 部署](#docker-部署)
- [systemd 部署](#systemd-部署)
- [配置文件](#配置文件)
- [安全说明](#安全说明)
- [本地开发](#本地开发)
- [目录结构](#目录结构)

---

## 架构与角色

```
                        公网                         内网 / NAT 后
   ┌──────────┐      ┌───────────────────────┐      ┌──────────────────────┐
   │  用户/访客 │─────▶│  中转端 (server)       │◀─────│  原站端 (client)      │
   └──────────┘      │  北京阿里云 · 公网 IP   │ 主动 │  宁波电信 / 内网       │
                     │                       │ 连接 │                      │
   访问 公网IP:8080  │  监听 :8080 ──────────┼──────┼──▶ 转发到 127.0.0.1:80 │
                     │  管理面 :23255         │      │  管理面 :23255        │
                     │  数据面 :23256         │      │                      │
                     └───────────────────────┘      └──────────────────────┘
```

**两种角色：**

| 角色 | 标识 | 位置 | 职责 |
| --- | --- | --- | --- |
| 中转端 | `server` | 公网服务器（如北京阿里云，有公网 IP） | 监听公网端口、接受原站端注册、承载转发流量 |
| 原站端 | `client` | 内网机器（如宁波电信 / 内网） | 主动连接中转端、本地监听并转发到真实服务 |

**控制面 / 数据面分离：**

- **控制面**：`WebSocket + JSON`，走管理端口 `23255`，路径 `/api/v1/agent/control`，
  用于节点注册、心跳、规则同步、下发建连指令。
- **数据面**：裸 `TCP`，走数据端口 `23256`，用于承载真正的转发流量。

**一次 TCP 转发的完整链路（on-demand work connection）：**

1. 用户连接中转端公网 `IP:端口`，中转端生成 `sessionID`；
2. 中转端通过控制通道下发 `new_work_conn` 指令；
3. 原站端据此**主动** dial 中转端的数据端口 `23256`；
4. 原站端发送首行 JSON 握手，携带 `HMAC-SHA256` 签名；
5. 中转端校验签名、放行（无有效握手直接静默 Close，抵御端口扫描）；
6. 两端建立 TCP 双向桥接；原站端再连接本地真实目标，完成透明转发。

**UDP 处理方式：** 以「用户源地址」为 key 维护会话映射表，
数据帧采用 2 字节大端长度前缀分帧，在 TCP 数据通道上承载 UDP 会话
（即 *UDP over TCP*）。

---

## 核心特性

- **转发能力**：TCP / UDP 四层透明转发，双向（`reverse` / `forward`）
- **配对**：中转端生成密钥 → 原站端填地址 / 密钥 / 节点名 / 本地目标 → 中转端接受节点并配置公网监听端口
- **双端管理**：两端 Web UI 均可创建 / 编辑 / 启停 / 删除规则，可选角色与方向
- **实时状态**：节点在线状态、心跳、延迟、版本、自动重连、连接数
- **流量统计**：按规则的入 / 出流量统计，30s 定时落库，支持月流量限额自动停用
- **规则同步**：`proxy_report` → 服务端合并 → `proxy_sync` 下发，客户端收敛（LWW）
- **运维能力**：端口占用检测、实时日志、配置导入导出、系统设置
- **安全**：token / 密钥认证、TLS、IP 白名单、限速、流量限额、审计日志、防端口扫描

---

## 端口说明

| 端口 | 协议 | 用途 | 是否必须 |
| --- | --- | --- | --- |
| `23255` | TCP | 管理面：Web UI + REST API + 控制通道（WebSocket） | 两端均默认开放 |
| `23256` | TCP | 数据面：裸 TCP 数据通道 | 中转端必须对原站端开放 |
| 业务端口（如 `8080`） | TCP / UDP | 实际对外发布的转发端口 | 按规则配置 |

> 管理端口默认 **23255**，两端一致，可配置。

---

## 快速开始（裸机）

### 1. 编译

需要 Go 1.22+ 与 Node 18+：

```bash
# 构建前端静态资源
cd web && npm install && npm run build && cd ..

# 构建后端（含前端资源，单二进制）
go build -trimpath -ldflags "-s -w" -o bin/portbridge ./cmd/portbridge
```

或使用脚本一次构建多架构产物：

```bash
./deploy/build.sh 0.1.0   # 输出 bin/portbridge-linux-amd64 与 bin/portbridge-linux-arm64
```

### 2. 部署中转端（公网服务器）

```bash
cp config.example.yaml config.yaml
vi config.yaml        # 修改 jwt_secret、ip_whitelist 等
./bin/portbridge -c config.yaml
```

启动后访问 `http://<公网IP>:23255`，首次登录使用默认账号：

- 用户名：`admin`
- 密码：`admin123`

> ⚠️ 首次登录后请立即修改管理员密码，并修改 `jwt_secret`。

### 3. 部署原站端（内网机器）

```bash
cp config.client.example.yaml config.yaml
vi config.yaml        # 填写 server_addr / server_data_addr / token / node_name
./bin/portbridge -c config.yaml
```

访问原站端本地管理界面 `http://127.0.0.1:23255` 即可管理本端规则。

---

## Web 管理平台配对流程

1. **在中转端创建节点**
   - 打开中转端 Web → 「节点管理」→ 新增节点，填写节点名（如 `ningbo-origin`）；
   - 创建后系统会弹出**一次性密钥（token）**，请立即复制保存（只显示一次）。

2. **在原站端填写连接信息**
   - 打开原站端 Web → 「系统设置」，或直接编辑原站端 `config.yaml`：
     - `server_addr`：中转端 `IP:23255`
     - `token`：上一步的一次性密钥
     - `node_name`：与中转端创建的节点名一致
   - 保存后原站端会自动连接，节点状态变为**在线**。

3. **在中转端或原站端创建转发规则**
   - 选择所属节点、协议（TCP / UDP）、方向；
   - 填写「监听地址 : 监听端口」与「目标 IP : 目标端口」；
   - 可通过「端口占用检测」提前发现冲突；
   - 保存并启用。

4. **验证**
   - 外部访问 `中转端公网IP:监听端口`，流量将被透明转发到原站端的 `目标IP:目标端口`。

> 规则可在两端任一 Web 中创建；两端会对规则做双向同步与收敛。

---

## Docker 部署

```bash
# 仅启动中转端
docker compose up -d server

# 启动原站端（先把 SERVER_ADDR / TOKEN 改为真实值）
docker compose --profile client up -d client
```

数据与配置通过目录挂载持久化：

- `./deploy/data/<role>` → `/app/data`（SQLite 数据库等）
- `./deploy/config/<role>` → `/app/config`（配置文件，只读）

也可直接构建镜像：

```bash
docker build -t portbridge:latest .
docker run -d --name portbridge-server \
  -p 23255:23255 -p 23256:23256 -p 8080:8080 \
  -e PORTBRIDGE_ROLE=server \
  -e PORTBRIDGE_NODE_NAME=beijing-relay \
  -e PORTBRIDGE_JWT_SECRET=change-me \
  -v $PWD/deploy/data/server:/app/data \
  portbridge:latest
```

### 发布镜像到 Docker Hub

仓库内置 GitHub Actions 工作流 [`.github/workflows/docker-publish.yml`](.github/workflows/docker-publish.yml)，
用于自动构建并推送 **linux/amd64 + linux/arm64** 多架构镜像。

使用前需在仓库 `Settings → Secrets and variables → Actions` 配置：

| Secret | 说明 |
| --- | --- |
| `DOCKERHUB_USERNAME` | Docker Hub 用户名 |
| `DOCKERHUB_TOKEN` | Docker Hub Access Token（不是登录密码） |

发布方式：

- **打标签自动发布**：`git tag v0.1.0 && git push origin v0.1.0`
- **手动触发**：Actions 页面选择该工作流 → Run workflow，可指定版本号

发布后的镜像名为 `<DOCKERHUB_USERNAME>/portbridge`，可直接拉取运行：

```bash
docker run -d --name portbridge-server \
  -p 23255:23255 -p 23256:23256 -p 8080:8080 \
  -e PORTBRIDGE_ROLE=server \
  -e PORTBRIDGE_JWT_SECRET=change-me \
  -v $PWD/data:/app/data \
  <DOCKERHUB_USERNAME>/portbridge:latest
```

本机若无 Docker，也可在任意有 Docker 的机器上手动构建推送：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t <DOCKERHUB_USERNAME>/portbridge:latest --push .
```

---

## systemd 部署

适用于裸机 / 单二进制安装到 `/opt/portbridge`：

```bash
# 1. 创建运行用户与目录
sudo useradd -r -s /sbin/nologin portbridge
sudo mkdir -p /opt/portbridge/{data,config}

# 2. 拷贝程序与静态资源
sudo cp bin/portbridge-linux-amd64 /opt/portbridge/portbridge
sudo cp -r web/dist /opt/portbridge/web/
sudo cp config.example.yaml /opt/portbridge/config.yaml   # 原站端改用 config.client.example.yaml
sudo chown -R portbridge:portbridge /opt/portbridge
sudo chmod +x /opt/portbridge/portbridge

# 3. 安装 service（中转端用 portbridge-server.service，原站端用 portbridge-client.service）
sudo cp deploy/systemd/portbridge-server.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now portbridge-server
sudo systemctl status portbridge-server
```

日志查看：`journalctl -u portbridge-server -f`

---

## 配置文件

完整字段与注释见：

- 中转端模板：[`config.example.yaml`](config.example.yaml)
- 原站端模板：[`config.client.example.yaml`](config.client.example.yaml)

所有配置项均支持用环境变量覆盖，命名规则 `PORTBRIDGE_<大写字段名>`，常用项：

| 环境变量 | 对应配置 | 说明 |
| --- | --- | --- |
| `PORTBRIDGE_ROLE` | `role` | `server` / `client` |
| `PORTBRIDGE_NODE_NAME` | `node_name` | 本端节点名 |
| `PORTBRIDGE_ADMIN_PORT` | `admin_port` | 管理端口（默认 23255） |
| `PORTBRIDGE_SERVER_ADDR` | `server_addr` | 原站端专用：中转端管理面地址 |
| `PORTBRIDGE_SERVER_DATA_ADDR` | `server_data_addr` | 原站端专用：中转端数据面地址 |
| `PORTBRIDGE_TOKEN` | `token` | 原站端专用：节点密钥 |
| `PORTBRIDGE_JWT_SECRET` | `jwt_secret` | JWT 签名密钥 |
| `PORTBRIDGE_DSN` | `dsn` | 数据库连接串 |
| `PORTBRIDGE_DB_DRIVER` | `db_driver` | `sqlite`（默认）/ `postgres` |

---

## 安全说明

- **认证**：原站端凭 `token` 登录，服务端以 SHA-256 比对 `Node.TokenHash`；
  登录成功后下发**随机会话密钥**，数据连接握手使用
  `HMAC-SHA256(sessionKey, "runID|proxyName|sessionID|ts")` 签名，时间戳校验 ±60s。
- **管理面鉴权**：`JWT + bcrypt`，WS 通过 query `?token=` 鉴权与 Header 二选一。
- **传输加密**：支持 TLS（`tls_enabled`），启用后控制通道自动使用 `wss`。
- **IP 白名单**：`ip_whitelist` 限制可连接数据面的来源 CIDR。
- **限速与限额**：按规则限速（KB/s）与月流量上限，超限自动停用。
- **防端口扫描**：数据端口在收到无有效握手的数据时直接静默关闭。
- **审计**：关键操作写入审计日志。

> 生产环境请务必：修改默认管理员密码、设置强随机 `jwt_secret`、启用 TLS、配置最小化的 IP 白名单。

---

## 本地开发

**后端**（默认监听 `23255`）：

```bash
go run ./cmd/portbridge -c config.yaml
```

**前端**（Vite Dev Server，自动代理 `/api` 与 WebSocket 到 `127.0.0.1:23255`）：

```bash
cd web
npm install
npm run dev          # 开发
npm run type-check   # 类型检查
npm run build        # 产物输出到 web/dist
```

---

## 目录结构

```
.
├── cmd/portbridge/          # 统一程序入口（按 role 分派）
├── internal/
│   ├── config/              # 配置加载与校验（yaml + env）
│   ├── model/               # GORM 数据模型（node / proxy / user / misc）
│   ├── store/               # 数据库初始化与 AutoMigrate
│   ├── auth/                # JWT、密码哈希、鉴权中间件
│   ├── protocol/            # 控制通道消息与编解码
│   ├── proxy/               # TCP/UDP 转发核心、限速、统计、本地监听
│   ├── server/              # 中转端：AgentHub / ProxyManager / 数据监听 / REST+WS
│   ├── client/              # 原站端：控制连接 / work conn / 本地转发 / REST
│   ├── apiutil/ loghub/ eventhub/ util/ version/
├── web/                     # Vue3 + TS + Vite + Element Plus 前端
├── deploy/
│   ├── build.sh             # 多架构交叉编译脚本
│   └── systemd/             # portbridge-server.service / portbridge-client.service
├── Dockerfile
├── docker-compose.yml
├── config.example.yaml          # 中转端配置模板
└── config.client.example.yaml   # 原站端配置模板
```

---

## 技术栈

| 层 | 技术 |
| --- | --- |
| 语言 / 运行时 | Go 1.22+（仅 Linux） |
| Web 框架 | Gin |
| ORM / 存储 | GORM + SQLite（默认） / PostgreSQL（可选） |
| 认证 | JWT + bcrypt |
| 控制通道 | WebSocket + JSON |
| 数据通道 | TCP（`net.Listen` + 双向 `io.Copy`）/ UDP（`net.ListenUDP` + 会话映射表） |
| 前端 | Vue 3 + TypeScript + Vite + Element Plus + Pinia + vue-router |
| 部署 | Docker / docker-compose / systemd 单二进制 |
