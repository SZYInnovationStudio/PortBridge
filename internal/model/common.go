package model

// 角色
const (
	RoleServer = "server"
	RoleClient = "client"
)

// 节点状态
const (
	NodeStatusOnline  = "online"
	NodeStatusOffline = "offline"
)

// 代理类型
const (
	ProxyTypeTCP = "tcp"
	ProxyTypeUDP = "udp"
)

// 转发方向
const (
	// DirectionReverse 反向：中转端监听公网端口 -> 原站端本地目标（主场景）
	DirectionReverse = "reverse"
	// DirectionForward 正向：原站端监听本地端口 -> 中转端侧目标
	DirectionForward = "forward"
)

// 规则创建来源
const (
	OriginServer = "server"
	OriginClient = "client"
)
