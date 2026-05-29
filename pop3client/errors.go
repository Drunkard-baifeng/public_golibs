package pop3client

import "errors"

// ProtocolError 表示 POP3 服务端返回了 -ERR 或非法响应。
type ProtocolError string

func (e ProtocolError) Error() string {
	return string(e)
}

var (
	// ErrClosed 表示客户端连接已关闭。
	ErrClosed = errors.New("pop3 client is closed")
	// ErrUnsupportedEmailType 表示邮箱类型不是当前封装支持的 126/163。
	ErrUnsupportedEmailType = errors.New("邮箱类型错误")
)
