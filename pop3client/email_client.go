package pop3client

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"time"
)

// ServerConfig 邮箱 POP3 服务端配置。
type ServerConfig struct {
	Host string
	Port int
}

// EmailConfig 邮箱客户端配置。
type EmailConfig struct {
	Username      string
	Password      string
	ProxyType     ProxyType
	ProxyHost     string
	ProxyPort     int
	ProxyUsername string
	ProxyPassword string
	Timeout       time.Duration
}

// EmailClient 支持 126 和 163 邮箱的 POP3 邮件客户端。
type EmailClient struct {
	popServer *POP3Client

	emailType         string
	username          string
	password          string
	initialEmailCount int
	initialCountSet   bool

	proxyType     ProxyType
	proxyHost     string
	proxyPort     int
	proxyUsername string
	proxyPassword string
	timeout       time.Duration
}

var serverConfigs = map[string]ServerConfig{
	"163": {Host: "pop.163.com", Port: POP3SSLPort},
	"126": {Host: "pop.126.com", Port: POP3SSLPort},
}

// NewEmailClient 创建邮箱客户端。
func NewEmailClient() *EmailClient {
	return &EmailClient{timeout: defaultTimeout}
}

// NewEmailClientWithConfig 使用配置创建邮箱客户端。
func NewEmailClientWithConfig(config EmailConfig) (*EmailClient, error) {
	client := NewEmailClient()
	if err := client.SetEmailWithConfig(config); err != nil {
		return nil, err
	}
	return client, nil
}

// SetEmail 设置邮箱账号和密码。
func (c *EmailClient) SetEmail(username, password string) error {
	return c.SetEmailWithConfig(EmailConfig{
		Username: username,
		Password: password,
		Timeout:  c.timeout,
	})
}

// SetEmailWithConfig 设置邮箱账号、密码和代理配置。
func (c *EmailClient) SetEmailWithConfig(config EmailConfig) error {
	emailType, err := detectEmailType(config.Username)
	if err != nil {
		log.Printf("不支持的邮箱类型: %s，仅支持: 126, 163", config.Username)
		return err
	}

	c.emailType = emailType
	c.username = config.Username
	c.password = config.Password
	c.initialEmailCount = 0
	c.initialCountSet = false
	c.proxyType = config.ProxyType
	c.proxyHost = config.ProxyHost
	c.proxyPort = config.ProxyPort
	c.proxyUsername = config.ProxyUsername
	c.proxyPassword = config.ProxyPassword
	if config.Timeout > 0 {
		c.timeout = config.Timeout
	}
	if c.timeout == 0 {
		c.timeout = defaultTimeout
	}
	return nil
}

// Connect 连接并登录邮件服务器。
func (c *EmailClient) Connect() error {
	if c.popServer != nil {
		_, _ = c.popServer.Quit()
		c.popServer = nil
	}

	config, ok := serverConfigs[c.emailType]
	if !ok {
		return ErrUnsupportedEmailType
	}

	popServer, err := NewPOP3ClientWithConfig(POP3Config{
		Host:    config.Host,
		Port:    config.Port,
		Timeout: c.timeout,
		Proxy: ProxyConfig{
			Type:     c.proxyType,
			Host:     c.proxyHost,
			Port:     c.proxyPort,
			Username: c.proxyUsername,
			Password: c.proxyPassword,
		},
	})
	if err != nil {
		return cleanEmailError(err)
	}
	c.popServer = popServer

	if _, err := c.popServer.User(c.username); err != nil {
		c.closePopServer()
		return cleanEmailError(err)
	}
	if _, err := c.popServer.Pass(c.password); err != nil {
		c.closePopServer()
		return cleanEmailError(err)
	}

	if !c.initialCountSet {
		count, _, err := c.GetEmailStats()
		if err != nil {
			c.closePopServer()
			return err
		}
		c.initialEmailCount = count
		c.initialCountSet = true
	}
	return nil
}

// Reconnect 重新连接邮件服务器，用于获取最新状态。
func (c *EmailClient) Reconnect() error {
	c.Close()
	return c.Connect()
}

// GetEmailStats 获取邮件统计信息。
func (c *EmailClient) GetEmailStats() (int, int, error) {
	if c.popServer == nil {
		return 0, 0, ErrClosed
	}
	return c.popServer.Stat()
}

// GetEmailList 获取邮件列表。
func (c *EmailClient) GetEmailList() ([]string, error) {
	if c.popServer == nil {
		return nil, ErrClosed
	}
	_, mails, _, err := c.popServer.List()
	return mails, err
}

// GetEmailContent 获取指定索引的邮件内容，索引从 1 开始。
func (c *EmailClient) GetEmailContent(emailIndex int) (*EmailInfo, error) {
	if c.popServer == nil {
		return nil, ErrClosed
	}

	_, lines, _, err := c.popServer.Retr(emailIndex)
	if err != nil {
		return nil, err
	}
	raw := bytes.Join(lines, []byte("\r\n"))
	return ParseEmail(raw)
}

// GetNewEmail 获取最新的一封新邮件，支持按主题和发件人过滤。
//
// fromFilter 和 subjectFilter 均为空时，只要存在新邮件就返回最新邮件。
// 两者任一非空时，最新邮件的发件人或主题命中对应过滤词才返回。
func (c *EmailClient) GetNewEmail(fromFilter, subjectFilter string) (*EmailInfo, bool, error) {
	if err := c.Reconnect(); err != nil {
		log.Printf("重新连接邮件服务器失败: %v", err)
		return nil, false, err
	}

	newCount, _, err := c.GetEmailStats()
	if err != nil {
		return nil, false, err
	}
	log.Printf("邮箱: %s, 旧邮件数量: %d, 新邮件数量: %d", c.username, c.initialEmailCount, newCount)

	if newCount <= c.initialEmailCount {
		return nil, false, nil
	}

	latestEmail, err := c.GetEmailContent(newCount)
	if err != nil {
		return nil, false, err
	}
	if emailMatches(latestEmail, fromFilter, subjectFilter) {
		return latestEmail, true, nil
	}
	return nil, false, nil
}

// InitialEmailCount 返回第一次连接时记录的邮件数量。
func (c *EmailClient) InitialEmailCount() int {
	return c.initialEmailCount
}

// Close 关闭连接。
func (c *EmailClient) Close() {
	c.closePopServer()
}

func (c *EmailClient) closePopServer() {
	if c.popServer == nil {
		return
	}
	_, _ = c.popServer.Quit()
	c.popServer = nil
}

func detectEmailType(username string) (string, error) {
	lower := strings.ToLower(username)
	switch {
	case strings.Contains(lower, "@126.com"):
		return "126", nil
	case strings.Contains(lower, "@163.com"):
		return "163", nil
	default:
		return "", ErrUnsupportedEmailType
	}
}

func cleanEmailError(err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()
	msg = strings.TrimPrefix(msg, "-ERR ")
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return err
	}
	return fmt.Errorf("%s", msg)
}

func emailMatches(info *EmailInfo, fromFilter, subjectFilter string) bool {
	if info == nil {
		return false
	}

	fromFilter = strings.ToLower(strings.TrimSpace(fromFilter))
	subjectFilter = strings.ToLower(strings.TrimSpace(subjectFilter))
	if fromFilter == "" && subjectFilter == "" {
		return true
	}

	from := strings.ToLower(info.From)
	subject := strings.ToLower(info.Subject)
	if fromFilter != "" && strings.Contains(from, fromFilter) {
		return true
	}
	if subjectFilter != "" && strings.Contains(subject, subjectFilter) {
		return true
	}
	return false
}
