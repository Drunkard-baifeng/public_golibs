package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"sync"
	"time"

	gws "github.com/gorilla/websocket"
)

// MessageType WebSocket 消息类型。
type MessageType int

const (
	TextMessage   MessageType = MessageType(gws.TextMessage)
	BinaryMessage MessageType = MessageType(gws.BinaryMessage)
	CloseMessage  MessageType = MessageType(gws.CloseMessage)
	PingMessage   MessageType = MessageType(gws.PingMessage)
	PongMessage   MessageType = MessageType(gws.PongMessage)
)

// Message 接收到的一条 WebSocket 消息。
type Message struct {
	Type MessageType
	Data []byte
}

// Text 将消息内容按 UTF-8 文本返回。
func (m Message) Text() string {
	return string(m.Data)
}

// IsText 判断消息是否为文本帧。
func (m Message) IsText() bool {
	return m.Type == TextMessage
}

// IsBinary 判断消息是否为二进制帧。
func (m Message) IsBinary() bool {
	return m.Type == BinaryMessage
}

// Client 是一个轻量 WebSocket 客户端封装。
type Client struct {
	cfg Config

	mu     sync.RWMutex
	conn   *gws.Conn
	sendMu sync.Mutex

	bgMu   sync.Mutex
	stopCh chan struct{}
	bgWG   sync.WaitGroup
}

// NewClient 创建 WebSocket 客户端。
func NewClient(url string, opts ...Option) (*Client, error) {
	cfg := DefaultConfig(url)
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return NewClientWithConfig(cfg)
}

// NewClientWithConfig 使用完整配置创建 WebSocket 客户端。
func NewClientWithConfig(cfg Config) (*Client, error) {
	cfg.normalize()
	cfg.Headers = cloneHeader(cfg.Headers)
	cfg.Subprotocols = append([]string(nil), cfg.Subprotocols...)
	if cfg.Proxy != nil {
		proxyCopy := *cfg.Proxy
		cfg.Proxy = &proxyCopy
	}

	if cfg.URL == "" {
		return nil, fmt.Errorf("websocket url is empty")
	}
	if cfg.AppHeartbeat.Enabled && cfg.AppHeartbeat.Interval <= 0 {
		return nil, fmt.Errorf("application heartbeat interval must be > 0")
	}
	if cfg.Heartbeat.Enabled && cfg.Heartbeat.Interval <= 0 {
		return nil, fmt.Errorf("heartbeat interval must be > 0")
	}

	client := &Client{cfg: cfg}
	if cfg.AutoConnect {
		if err := client.Connect(); err != nil {
			return nil, err
		}
	}
	return client, nil
}

// Dial 创建并连接 WebSocket 客户端。
func Dial(url string, opts ...Option) (*Client, error) {
	client, err := NewClient(url, opts...)
	if err != nil {
		return nil, err
	}
	if err = client.Connect(); err != nil {
		return nil, err
	}
	return client, nil
}

// DialContext 使用 context 创建并连接 WebSocket 客户端。
func DialContext(ctx context.Context, url string, opts ...Option) (*Client, error) {
	client, err := NewClient(url, opts...)
	if err != nil {
		return nil, err
	}
	if err = client.ConnectContext(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// Config 返回当前客户端配置副本。
func (c *Client) Config() Config {
	cfg := c.cfg
	cfg.Headers = cloneHeader(c.cfg.Headers)
	cfg.Subprotocols = append([]string(nil), c.cfg.Subprotocols...)
	if c.cfg.Proxy != nil {
		proxyCopy := *c.cfg.Proxy
		cfg.Proxy = &proxyCopy
	}
	return cfg
}

// Connected 判断当前是否持有连接对象。
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil
}

// RawConn 返回底层 gorilla/websocket 连接。
func (c *Client) RawConn() (*gws.Conn, error) {
	return c.requireConn()
}

// Connect 建立连接；已连接时直接返回。
func (c *Client) Connect() error {
	return c.ConnectContext(context.Background())
}

// ConnectContext 使用 context 建立连接；已连接时直接返回。
func (c *Client) ConnectContext(ctx context.Context) error {
	c.mu.RLock()
	if c.conn != nil {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	dialer, err := c.buildDialer()
	if err != nil {
		return err
	}

	if ctx == nil {
		ctx = context.Background()
	}
	headers := cloneHeader(c.cfg.Headers)
	if c.cfg.Origin != "" {
		headers.Set("Origin", c.cfg.Origin)
	}
	if c.cfg.UserAgent != "" {
		headers.Set("User-Agent", c.cfg.UserAgent)
	}

	if c.cfg.OpenTimeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.cfg.OpenTimeout)
			defer cancel()
		}
	}

	conn, resp, err := dialer.DialContext(ctx, c.cfg.URL, headers)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("websocket connect failed: %w, http status: %s", err, resp.Status)
		}
		return fmt.Errorf("websocket connect failed: %w", err)
	}
	if c.cfg.MaxMessageSize > 0 {
		conn.SetReadLimit(c.cfg.MaxMessageSize)
	}

	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	c.conn = conn
	c.mu.Unlock()

	c.startBackground()
	return nil
}

// Close 关闭连接并停止后台心跳。
func (c *Client) Close() error {
	return c.CloseWithReason(gws.CloseNormalClosure, "")
}

// CloseWithReason 关闭连接并发送 close frame。
func (c *Client) CloseWithReason(code int, reason string) error {
	backgroundStopped := c.signalBackground()

	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn == nil {
		if backgroundStopped {
			c.waitBackground()
		}
		return nil
	}

	var closeErr error
	if c.cfg.CloseTimeout > 0 {
		deadline := time.Now().Add(c.cfg.CloseTimeout)
		if c.sendMu.TryLock() {
			closeErr = conn.WriteControl(gws.CloseMessage, gws.FormatCloseMessage(code, reason), deadline)
			c.sendMu.Unlock()
		}
	}
	connErr := conn.Close()
	if backgroundStopped {
		c.waitBackground()
	}
	return errors.Join(closeErr, connErr)
}

// Reconnect 重新连接。
func (c *Client) Reconnect() error {
	if err := c.Close(); err != nil {
		return err
	}
	return c.Connect()
}

// ReconnectContext 使用 context 重新连接。
func (c *Client) ReconnectContext(ctx context.Context) error {
	if err := c.Close(); err != nil {
		return err
	}
	return c.ConnectContext(ctx)
}

// Send 发送文本或二进制消息。JSON 请显式调用 SendJSON。
func (c *Client) Send(message any) error {
	switch v := message.(type) {
	case string:
		return c.SendText(v)
	case []byte:
		return c.SendBinary(v)
	default:
		return fmt.Errorf("unsupported message type %T; use SendText, SendBinary, or SendJSON", message)
	}
}

// SendText 发送文本帧。
func (c *Client) SendText(text string) error {
	return c.writeMessage(gws.TextMessage, []byte(text))
}

// SendBinary 发送二进制帧。
func (c *Client) SendBinary(data []byte) error {
	return c.writeMessage(gws.BinaryMessage, data)
}

// SendBytes 发送二进制帧，保留这个名字方便和 Python 版习惯对应。
func (c *Client) SendBytes(data []byte) error {
	return c.SendBinary(data)
}

// SendHex 将 hex 文本转换成二进制帧发送。
func (c *Client) SendHex(data any) error {
	bytesData, err := BytesFromHex(data)
	if err != nil {
		return err
	}
	return c.SendBinary(bytesData)
}

// SendJSON 序列化 JSON 后发送文本帧。
func (c *Client) SendJSON(data any) error {
	payload, err := marshalJSON(data)
	if err != nil {
		return err
	}
	return c.SendText(string(payload))
}

// Recv 接收一条消息。传入 timeout 时只对本次接收生效。
func (c *Client) Recv(timeout ...time.Duration) (Message, error) {
	conn, err := c.requireConn()
	if err != nil {
		return Message{}, err
	}

	if len(timeout) > 0 {
		if timeout[0] > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(timeout[0]))
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		defer conn.SetReadDeadline(time.Time{})
	} else if c.cfg.ReadTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
		defer conn.SetReadDeadline(time.Time{})
	}

	messageType, data, err := conn.ReadMessage()
	if err != nil {
		return Message{}, err
	}
	return Message{Type: MessageType(messageType), Data: data}, nil
}

// RecvText 接收一条消息并按文本返回。
func (c *Client) RecvText(timeout ...time.Duration) (string, error) {
	msg, err := c.Recv(timeout...)
	if err != nil {
		return "", err
	}
	return msg.Text(), nil
}

// RecvBinary 接收一条消息并返回 bytes。
func (c *Client) RecvBinary(timeout ...time.Duration) ([]byte, error) {
	msg, err := c.Recv(timeout...)
	if err != nil {
		return nil, err
	}
	return msg.Data, nil
}

// RecvBytes 接收一条消息并返回 bytes。
func (c *Client) RecvBytes(timeout ...time.Duration) ([]byte, error) {
	return c.RecvBinary(timeout...)
}

// RecvJSON 接收 JSON 消息并反序列化到 out。
func (c *Client) RecvJSON(out any, timeout ...time.Duration) error {
	msg, err := c.Recv(timeout...)
	if err != nil {
		return err
	}
	return json.Unmarshal(msg.Data, out)
}

// RecvHex 接收消息并返回 hex 文本。
func (c *Client) RecvHex(timeout ...time.Duration) (string, error) {
	return c.RecvHexWithOptions("", false, timeout...)
}

// RecvHexWithOptions 接收消息并返回可配置格式的 hex 文本。
func (c *Client) RecvHexWithOptions(separator string, uppercase bool, timeout ...time.Duration) (string, error) {
	msg, err := c.Recv(timeout...)
	if err != nil {
		return "", err
	}
	return BytesToHex(msg.Data, separator, uppercase)
}

// Request 发送一条消息并等待一条回复。
func (c *Client) Request(message any, timeout ...time.Duration) (Message, error) {
	if err := c.Send(message); err != nil {
		return Message{}, err
	}
	return c.Recv(timeout...)
}

// RequestText 发送文本并按文本接收回复。
func (c *Client) RequestText(text string, timeout ...time.Duration) (string, error) {
	if err := c.SendText(text); err != nil {
		return "", err
	}
	return c.RecvText(timeout...)
}

// RequestJSON 发送 JSON 并把回复解析到 out。
func (c *Client) RequestJSON(data any, out any, timeout ...time.Duration) error {
	if err := c.SendJSON(data); err != nil {
		return err
	}
	return c.RecvJSON(out, timeout...)
}

// RequestHex 发送 hex 二进制帧并返回 hex 回复。
func (c *Client) RequestHex(data any, timeout ...time.Duration) (string, error) {
	return c.RequestHexWithOptions(data, "", false, timeout...)
}

// RequestHexWithOptions 发送 hex 二进制帧并返回可配置格式的 hex 回复。
func (c *Client) RequestHexWithOptions(data any, separator string, uppercase bool, timeout ...time.Duration) (string, error) {
	if err := c.SendHex(data); err != nil {
		return "", err
	}
	return c.RecvHexWithOptions(separator, uppercase, timeout...)
}

// Ping 发送 WebSocket ping frame。
func (c *Client) Ping(data []byte, timeout ...time.Duration) error {
	return c.writeControl(gws.PingMessage, data, timeout...)
}

// Pong 发送 WebSocket pong frame。
func (c *Client) Pong(data []byte, timeout ...time.Duration) error {
	return c.writeControl(gws.PongMessage, data, timeout...)
}

// Listen 持续读取消息，直到连接错误、context 结束或 handler 返回错误。
func (c *Client) Listen(ctx context.Context, handler func(Message) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-done:
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := c.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if handler != nil {
			if err = handler(msg); err != nil {
				return err
			}
		}
	}
}

func (c *Client) buildDialer() (*gws.Dialer, error) {
	dialer := &gws.Dialer{
		HandshakeTimeout:  c.cfg.OpenTimeout,
		ReadBufferSize:    c.cfg.ReadBufferSize,
		WriteBufferSize:   c.cfg.WriteBufferSize,
		Subprotocols:      c.cfg.Subprotocols,
		EnableCompression: c.cfg.EnableCompression,
		TLSClientConfig:   c.cfg.TLSConfig,
	}

	if c.cfg.UseEnvProxy {
		dialer.Proxy = http.ProxyFromEnvironment
	}

	if c.cfg.Proxy != nil {
		httpProxy, netDialContext, err := proxyFromConfig(*c.cfg.Proxy, c.cfg.OpenTimeout)
		if err != nil {
			return nil, err
		}
		if httpProxy != nil {
			dialer.Proxy = httpProxy
		}
		if netDialContext != nil {
			dialer.NetDialContext = netDialContext
		}
	}

	if dialer.NetDialContext == nil {
		netDialer := &net.Dialer{Timeout: c.cfg.OpenTimeout}
		dialer.NetDialContext = netDialer.DialContext
	}
	return dialer, nil
}

func (c *Client) requireConn() (*gws.Conn, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.conn == nil {
		return nil, ErrNotConnected
	}
	return c.conn, nil
}

func (c *Client) writeMessage(messageType int, data []byte) error {
	conn, err := c.requireConn()
	if err != nil {
		return err
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if c.cfg.WriteTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout))
		defer conn.SetWriteDeadline(time.Time{})
	}
	return conn.WriteMessage(messageType, data)
}

func (c *Client) writeControl(messageType int, data []byte, timeout ...time.Duration) error {
	conn, err := c.requireConn()
	if err != nil {
		return err
	}

	deadline := time.Time{}
	if len(timeout) > 0 && timeout[0] > 0 {
		deadline = time.Now().Add(timeout[0])
	} else if len(timeout) == 0 && c.cfg.WriteTimeout > 0 {
		deadline = time.Now().Add(c.cfg.WriteTimeout)
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return conn.WriteControl(messageType, data, deadline)
}

func (c *Client) startBackground() {
	c.bgMu.Lock()
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	c.bgMu.Unlock()

	if c.cfg.Heartbeat.Enabled {
		c.bgWG.Add(1)
		go c.runProtocolHeartbeat(stopCh)
	}
	if c.cfg.AppHeartbeat.Enabled {
		c.bgWG.Add(1)
		go c.runAppHeartbeat(stopCh)
	}
}

func (c *Client) stopBackground() {
	if c.signalBackground() {
		c.waitBackground()
	}
}

func (c *Client) signalBackground() bool {
	c.bgMu.Lock()
	defer c.bgMu.Unlock()

	stopCh := c.stopCh
	if stopCh != nil {
		close(stopCh)
		c.stopCh = nil
	}
	return stopCh != nil
}

func (c *Client) waitBackground() {
	c.bgWG.Wait()
}

func (c *Client) runProtocolHeartbeat(stopCh <-chan struct{}) {
	defer c.bgWG.Done()

	interval := c.cfg.Heartbeat.Interval
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			if err := c.Ping(nil, c.cfg.Heartbeat.Timeout); err != nil {
				return
			}
		}
	}
}

func (c *Client) runAppHeartbeat(stopCh <-chan struct{}) {
	defer c.bgWG.Done()

	if c.cfg.AppHeartbeat.Immediate {
		if err := c.sendAppHeartbeatOnce(); err != nil {
			return
		}
	}

	ticker := time.NewTicker(c.cfg.AppHeartbeat.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			if err := c.sendAppHeartbeatOnce(); err != nil {
				return
			}
		}
	}
}

func (c *Client) sendAppHeartbeatOnce() error {
	heartbeat := c.cfg.AppHeartbeat
	switch heartbeat.Mode {
	case AppHeartbeatHex:
		return c.SendHex(heartbeat.Message)
	case AppHeartbeatJSON:
		return c.SendJSON(heartbeat.Message)
	case AppHeartbeatBinary:
		data, ok := heartbeat.Message.([]byte)
		if !ok {
			return c.SendBinary([]byte(fmt.Sprint(heartbeat.Message)))
		}
		return c.SendBinary(data)
	case AppHeartbeatText:
		return c.SendText(fmt.Sprint(heartbeat.Message))
	case AppHeartbeatAuto:
		if isJSONLike(heartbeat.Message) {
			return c.SendJSON(heartbeat.Message)
		}
		return c.Send(heartbeat.Message)
	default:
		return fmt.Errorf("unsupported application heartbeat mode: %s", heartbeat.Mode)
	}
}

func cloneHeader(header http.Header) http.Header {
	result := make(http.Header, len(header))
	for key, values := range header {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func marshalJSON(data any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(data); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func isJSONLike(value any) bool {
	if value == nil {
		return false
	}
	switch value.(type) {
	case string, []byte:
		return false
	}
	kind := reflect.TypeOf(value).Kind()
	return kind == reflect.Map || kind == reflect.Slice || kind == reflect.Array || kind == reflect.Struct
}
