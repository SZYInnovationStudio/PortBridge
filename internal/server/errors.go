package server

import "errors"

var (
	// errNodeOffline 节点不在线
	errNodeOffline = errors.New("节点不在线")
	// errSendQueueFull 发送队列已满
	errSendQueueFull = errors.New("发送队列已满")
	// errProxyNotFound 规则不存在
	errProxyNotFound = errors.New("规则不存在")
	// errNoSession 无待配对的数据连接
	errNoSession = errors.New("无待配对的数据会话")
	// errPortInUse 端口被占用
	errPortInUse = errors.New("监听端口被占用")
)
