package pop3client

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/tls"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// POP3Port 标准 POP3 端口。
	POP3Port = 110
	// POP3SSLPort POP3 SSL 端口。
	POP3SSLPort = 995

	defaultTimeout = 30 * time.Second
	maxLineLength  = 1024 * 10 * 10
)

var apopTimestampRe = regexp.MustCompile(`\+OK.[^<]*(<.*>)`)

// POP3Config POP3 客户端配置。
type POP3Config struct {
	Host               string
	Port               int
	Timeout            time.Duration
	Proxy              ProxyConfig
	TLSConfig          *tls.Config
	InsecureSkipVerify bool
}

// POP3Client 是一个支持 SSL 和代理的 POP3 客户端。
type POP3Client struct {
	host               string
	port               int
	timeout            time.Duration
	proxy              ProxyConfig
	tlsConfig          *tls.Config
	insecureSkipVerify bool

	conn           net.Conn
	reader         *bufio.Reader
	welcome        string
	debugLevel     int
	tlsEstablished bool
	implicitTLS    bool
}

// NewPOP3Client 创建并连接 POP3 服务端。
func NewPOP3Client(host string, port int, config ...POP3Config) (*POP3Client, error) {
	cfg := POP3Config{
		Host:    host,
		Port:    port,
		Timeout: defaultTimeout,
	}
	if len(config) > 0 {
		userCfg := config[0]
		userCfg.Host = firstNonEmpty(userCfg.Host, host)
		if userCfg.Port == 0 {
			userCfg.Port = port
		}
		if userCfg.Timeout == 0 {
			userCfg.Timeout = defaultTimeout
		}
		cfg = userCfg
	}
	return NewPOP3ClientWithConfig(cfg)
}

// NewPOP3ClientWithConfig 使用配置创建并连接 POP3 服务端。
func NewPOP3ClientWithConfig(cfg POP3Config) (*POP3Client, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("pop3 host is empty")
	}
	if cfg.Port == 0 {
		cfg.Port = POP3Port
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	c := &POP3Client{
		host:               cfg.Host,
		port:               cfg.Port,
		timeout:            cfg.Timeout,
		proxy:              cfg.Proxy,
		tlsConfig:          cfg.TLSConfig,
		insecureSkipVerify: cfg.InsecureSkipVerify,
		implicitTLS:        cfg.Port == POP3SSLPort,
	}

	if err := c.connect(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *POP3Client) connect() error {
	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	conn, err := dialWithProxy("tcp", addr, c.timeout, c.proxy)
	if err != nil {
		return err
	}
	c.conn = conn

	if c.implicitTLS {
		if err := c.wrapTLS(); err != nil {
			_ = c.Close()
			return err
		}
	}

	c.reader = bufio.NewReader(c.conn)
	welcome, err := c.getResp()
	if err != nil {
		_ = c.Close()
		return err
	}
	c.welcome = welcome
	return nil
}

// GetWelcome 返回服务端欢迎语。
func (c *POP3Client) GetWelcome() string {
	return c.welcome
}

// SetDebugLevel 设置调试级别，level > 0 时打印命令和响应。
func (c *POP3Client) SetDebugLevel(level int) {
	c.debugLevel = level
}

// User 发送 USER 命令。
func (c *POP3Client) User(user string) (string, error) {
	return c.shortCmd("USER %s", user)
}

// Pass 发送 PASS 命令。
func (c *POP3Client) Pass(password string) (string, error) {
	return c.shortCmd("PASS %s", password)
}

// Stat 获取邮箱状态，返回邮件数量和总大小。
func (c *POP3Client) Stat() (int, int, error) {
	resp, err := c.shortCmd("STAT")
	if err != nil {
		return 0, 0, err
	}

	parts := strings.Fields(resp)
	if len(parts) < 3 {
		return 0, 0, ProtocolError(resp)
	}

	count, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	size, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, err
	}
	return count, size, nil
}

// List 获取邮件列表。传入 which 时返回指定邮件的 LIST 响应。
func (c *POP3Client) List(which ...int) (string, []string, int, error) {
	if len(which) > 0 {
		resp, err := c.shortCmd("LIST %d", which[0])
		return resp, nil, 0, err
	}

	resp, lines, octets, err := c.longCmd("LIST")
	if err != nil {
		return "", nil, 0, err
	}
	return resp, bytesLinesToStrings(lines), octets, nil
}

// Retr 获取指定邮件完整内容。
func (c *POP3Client) Retr(which int) (string, [][]byte, int, error) {
	return c.longCmd("RETR %d", which)
}

// Dele 删除指定邮件。
func (c *POP3Client) Dele(which int) (string, error) {
	return c.shortCmd("DELE %d", which)
}

// Noop 检查连接是否存活。
func (c *POP3Client) Noop() (string, error) {
	return c.shortCmd("NOOP")
}

// Rset 取消当前会话内标记删除的邮件。
func (c *POP3Client) Rset() (string, error) {
	return c.shortCmd("RSET")
}

// Quit 退出并关闭连接。
func (c *POP3Client) Quit() (string, error) {
	resp, err := c.shortCmd("QUIT")
	closeErr := c.Close()
	if err != nil {
		return resp, err
	}
	return resp, closeErr
}

// Close 直接关闭连接。
func (c *POP3Client) Close() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.reader = nil
	return err
}

// Rpop 发送 RPOP 命令。
func (c *POP3Client) Rpop(user string) (string, error) {
	return c.shortCmd("RPOP %s", user)
}

// Apop 使用 APOP 认证。
func (c *POP3Client) Apop(user, password string) (string, error) {
	match := apopTimestampRe.FindStringSubmatch(c.welcome)
	if len(match) < 2 {
		return "", ProtocolError("-ERR APOP not supported by server")
	}

	sum := md5.Sum([]byte(match[1] + password))
	return c.shortCmd("APOP %s %x", user, sum)
}

// Top 获取指定邮件头和正文前 howMuch 行。
func (c *POP3Client) Top(which, howMuch int) (string, [][]byte, int, error) {
	return c.longCmd("TOP %d %d", which, howMuch)
}

// Uidl 获取邮件唯一 ID 列表。传入 which 时返回指定邮件的 UIDL 响应。
func (c *POP3Client) Uidl(which ...int) (string, []string, int, error) {
	if len(which) > 0 {
		resp, err := c.shortCmd("UIDL %d", which[0])
		return resp, nil, 0, err
	}

	resp, lines, octets, err := c.longCmd("UIDL")
	if err != nil {
		return "", nil, 0, err
	}
	return resp, bytesLinesToStrings(lines), octets, nil
}

// UTF8 尝试进入 UTF-8 模式。
func (c *POP3Client) UTF8() (string, error) {
	return c.shortCmd("UTF8")
}

// Capa 获取服务端能力列表。
func (c *POP3Client) Capa() (map[string][]string, error) {
	_, lines, _, err := c.longCmd("CAPA")
	if err != nil {
		return nil, ProtocolError("-ERR CAPA not supported by server")
	}

	caps := make(map[string][]string, len(lines))
	for _, line := range lines {
		parts := strings.Fields(string(line))
		if len(parts) == 0 {
			continue
		}
		caps[parts[0]] = parts[1:]
	}
	return caps, nil
}

// STLS 将当前明文连接升级为 TLS。
func (c *POP3Client) STLS(config ...*tls.Config) (string, error) {
	if c.tlsEstablished {
		return "", ProtocolError("-ERR TLS session already established")
	}
	if c.implicitTLS {
		c.tlsEstablished = true
		return "+OK TLS already established (SSL mode)", nil
	}
	if len(config) > 0 && config[0] != nil {
		c.tlsConfig = config[0]
	}

	resp, err := c.shortCmd("STLS")
	if err != nil {
		return "", err
	}
	if err := c.wrapTLS(); err != nil {
		return "", err
	}
	c.reader = bufio.NewReader(c.conn)
	return resp, nil
}

func (c *POP3Client) shortCmd(format string, args ...interface{}) (string, error) {
	if err := c.putCmd(fmt.Sprintf(format, args...)); err != nil {
		return "", err
	}
	return c.getResp()
}

func (c *POP3Client) longCmd(format string, args ...interface{}) (string, [][]byte, int, error) {
	if err := c.putCmd(fmt.Sprintf(format, args...)); err != nil {
		return "", nil, 0, err
	}
	return c.getLongResp()
}

func (c *POP3Client) putCmd(line string) error {
	if c.debugLevel > 0 {
		fmt.Printf("*cmd* %q\n", line)
	}
	return c.putLine([]byte(line))
}

func (c *POP3Client) putLine(line []byte) error {
	if c.conn == nil {
		return ErrClosed
	}
	c.setDeadline()
	_, err := c.conn.Write(append(append([]byte{}, line...), '\r', '\n'))
	return err
}

func (c *POP3Client) getResp() (string, error) {
	line, _, err := c.getLine()
	if err != nil {
		return "", err
	}
	if c.debugLevel > 1 {
		fmt.Printf("*resp* %q\n", line)
	}
	if !bytes.HasPrefix(line, []byte("+")) {
		return "", ProtocolError(string(line))
	}
	return string(line), nil
}

func (c *POP3Client) getLongResp() (string, [][]byte, int, error) {
	resp, err := c.getResp()
	if err != nil {
		return "", nil, 0, err
	}

	var lines [][]byte
	octets := 0
	for {
		line, n, err := c.getLine()
		if err != nil {
			return "", nil, 0, err
		}
		if bytes.Equal(line, []byte(".")) {
			break
		}
		if bytes.HasPrefix(line, []byte("..")) {
			line = line[1:]
			n--
		}
		octets += n
		lines = append(lines, append([]byte(nil), line...))
	}
	return resp, lines, octets, nil
}

func (c *POP3Client) getLine() ([]byte, int, error) {
	if c.reader == nil {
		return nil, 0, ErrClosed
	}
	c.setDeadline()
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, 0, err
	}
	if len(line) > maxLineLength {
		return nil, 0, ProtocolError("line too long")
	}
	if c.debugLevel > 1 {
		fmt.Printf("*get* %q\n", line)
	}

	octets := len(line)
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	line = bytes.TrimPrefix(line, []byte("\r"))
	return line, octets, nil
}

func (c *POP3Client) wrapTLS() error {
	if c.conn == nil {
		return ErrClosed
	}

	tlsConfig := &tls.Config{}
	if c.tlsConfig != nil {
		tlsConfig = c.tlsConfig.Clone()
	}
	if tlsConfig.ServerName == "" && net.ParseIP(c.host) == nil {
		tlsConfig.ServerName = c.host
	}
	if c.insecureSkipVerify || net.ParseIP(c.host) != nil {
		tlsConfig.InsecureSkipVerify = true
	}

	if c.timeout > 0 {
		_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
		defer c.conn.SetDeadline(time.Time{})
	}

	tlsConn := tls.Client(c.conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		return err
	}
	c.conn = tlsConn
	c.tlsEstablished = true
	return nil
}

func (c *POP3Client) setDeadline() {
	if c.conn == nil || c.timeout <= 0 {
		return
	}
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
}

func bytesLinesToStrings(lines [][]byte) []string {
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		result = append(result, string(line))
	}
	return result
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
