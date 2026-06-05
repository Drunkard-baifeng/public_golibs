package websocket

import "errors"

var (
	// ErrNotConnected 表示当前还没有可用的 WebSocket 连接。
	ErrNotConnected = errors.New("websocket is not connected; call Connect() first")
)
