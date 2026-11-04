package mailer

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fiftyapi/internal/configenv"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type Message struct{ Email, Name, URL string }
type Sender interface {
	SendInvitation(context.Context, Message) error
}
type SMTP struct{ Host, Port, From, Username, Password, TLSMode string }

func FromEnv() (*SMTP, error) {
	c := &SMTP{Host: strings.TrimSpace(configenv.Value("FIFTYAPI_SMTP_HOST")), Port: configenv.Value("FIFTYAPI_SMTP_PORT"), From: configenv.Value("FIFTYAPI_SMTP_FROM"), Username: configenv.Value("FIFTYAPI_SMTP_USERNAME"), Password: configenv.Value("FIFTYAPI_SMTP_PASSWORD"), TLSMode: configenv.Value("FIFTYAPI_SMTP_TLS")}
	if c.Host == "" {
		if c.From != "" || c.Username != "" || c.Password != "" {
			return nil, errors.New("邮件配置不完整：需要 FIFTYAPI_SMTP_HOST")
		}
		return nil, nil
	}
	if c.Port == "" {
		c.Port = "587"
	}
	if c.TLSMode == "" {
		c.TLSMode = "starttls"
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *SMTP) Validate() error {
	if c.Host == "" || strings.ContainsAny(c.Host, "\r\n /\\") {
		return errors.New("SMTP 主机格式无效")
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("SMTP 端口无效")
	}
	if _, err = mail.ParseAddress(c.From); err != nil || strings.ContainsAny(c.From, "\r\n") {
		return errors.New("SMTP 发件地址无效")
	}
	if c.Username == "" && c.Password != "" {
		return errors.New("SMTP 密码需要配套用户名")
	}
	switch c.TLSMode {
	case "starttls", "tls":
	case "none":
		if c.Host != "localhost" && c.Host != "127.0.0.1" && c.Host != "::1" {
			return errors.New("SMTP 明文模式仅允许本机测试")
		}
	default:
		return errors.New("SMTP TLS 模式应为 starttls 或 tls")
	}
	return nil
}
func (c *SMTP) SendInvitation(ctx context.Context, m Message) error {
	body := "你好，" + m.Name + "：\n\n管理员已邀请你使用 FiftyAPI。请在 24 小时内打开以下链接，设置至少 8 字节的登录密码：\n\n" + m.URL + "\n\n登录邮箱：" + m.Email + "\n\n链接只能使用一次，重新发送邀请后旧链接失效。如果你未预期收到此邀请，请忽略或联系管理员。\n"
	body += "\nHello " + m.Name + ",\n\nYou have been invited to FiftyAPI. Open the link below within 24 hours to set a sign-in password of 8–72 bytes:\n\n" + m.URL + "\n\nSign-in email: " + m.Email + "\n\nThis link is single-use. Resending the invitation invalidates it. If unexpected, ignore this message or contact your administrator.\n"
	return c.send(ctx, m.Email, "设置你的 FiftyAPI 登录密码 / Set your FiftyAPI password", body)
}
func (c *SMTP) SendTest(ctx context.Context, email string) error {
	return c.send(ctx, email, "FiftyAPI 邮件配置测试 / Email test", "这是一封 FiftyAPI 邮件配置测试邮件。SMTP 服务器已接受发送请求，请确认收件情况。\n\nThis is a FiftyAPI email configuration test. The SMTP server accepted the request. Please confirm delivery.\n")
}
func (c *SMTP) send(ctx context.Context, email, subject, body string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return errors.New("收件地址无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(c.Host, c.Port))
	if err != nil {
		return errors.New("无法连接 SMTP 服务器")
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	secure := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	if c.TLSMode == "tls" {
		tlsConn := tls.Client(conn, secure)
		if tlsConn.HandshakeContext(ctx) != nil {
			return errors.New("SMTP TLS 握手失败")
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return errors.New("SMTP 会话建立失败")
	}
	defer client.Close()
	if c.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP 服务器未提供 STARTTLS")
		}
		if client.StartTLS(secure) != nil {
			return errors.New("SMTP STARTTLS 失败")
		}
	}
	if c.Username != "" {
		if client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)) != nil {
			return errors.New("SMTP 认证失败，请核对邮件配置")
		}
	}
	from, _ := mail.ParseAddress(c.From)
	if client.Mail(from.Address) != nil || client.Rcpt(email) != nil {
		return errors.New("SMTP 拒绝发件地址或收件地址")
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	var lines strings.Builder
	for len(encoded) > 76 {
		lines.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	lines.WriteString(encoded + "\r\n")
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n%s", from.String(), address.String(), mime.QEncoding.Encode("UTF-8", subject), time.Now().Format(time.RFC1123Z), lines.String())
	writer, err := client.Data()
	if err != nil {
		return errors.New("SMTP 未接受邮件内容")
	}
	_, writeErr := writer.Write([]byte(message))
	closeErr := writer.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("SMTP 未确认接收邮件，请重试")
	}
	// A successful DATA response means accepted by the SMTP server. QUIT failure
	// cannot safely turn it into a failed send and cause duplicate delivery.
	client.Quit()
	return nil
}
