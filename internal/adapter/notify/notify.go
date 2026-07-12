// Package notify 摘要推送发送器：email（net/smtp）与 webhook（POST JSON）。
package notify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/usecase"
)

// SMTPConfig email 通道配置（来自 env）
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

func (c SMTPConfig) configured() bool { return c.Host != "" && c.From != "" }

// Sender 实现 usecase.DigestSender
type Sender struct {
	smtp SMTPConfig
	hc   *http.Client
}

func NewSender(smtp SMTPConfig) *Sender {
	return &Sender{smtp: smtp, hc: &http.Client{Timeout: 30 * time.Second}}
}

func (s *Sender) Available(channel string) bool {
	switch channel {
	case domain.DigestChannelEmail:
		return s.smtp.configured()
	case domain.DigestChannelWebhook:
		return true
	}
	return false
}

func (s *Sender) Send(ctx context.Context, channel, target string, c usecase.DigestContent) error {
	switch channel {
	case domain.DigestChannelEmail:
		return s.sendEmail(target, c)
	case domain.DigestChannelWebhook:
		return s.sendWebhook(ctx, target, c)
	}
	return fmt.Errorf("未知通道: %s", channel)
}

// sendEmail net/smtp + PlainAuth；smtp.SendMail 会在服务器支持时自动 STARTTLS
func (s *Sender) sendEmail(to string, c usecase.DigestContent) error {
	if !s.smtp.configured() {
		return fmt.Errorf("SMTP 未配置")
	}
	addr := net.JoinHostPort(s.smtp.Host, strconv.Itoa(s.smtp.Port))
	var auth smtp.Auth
	if s.smtp.User != "" {
		auth = smtp.PlainAuth("", s.smtp.User, s.smtp.Pass, s.smtp.Host)
	}

	const boundary = "family-finances-digest-boundary"
	var msg strings.Builder
	msg.WriteString("From: " + s.smtp.From + "\r\n")
	msg.WriteString("To: " + to + "\r\n")
	msg.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", c.Subject) + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: multipart/alternative; boundary=" + boundary + "\r\n\r\n")
	writePart := func(contentType, body string) {
		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: " + contentType + "; charset=UTF-8\r\n")
		msg.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		msg.WriteString(base64.StdEncoding.EncodeToString([]byte(body)) + "\r\n")
	}
	writePart("text/plain", c.Text)
	writePart("text/html", c.HTML)
	msg.WriteString("--" + boundary + "--\r\n")

	return smtp.SendMail(addr, auth, s.smtp.From, []string{to}, []byte(msg.String()))
}

// sendWebhook POST JSON {subject, text, html}
func (s *Sender) sendWebhook(ctx context.Context, url string, c usecase.DigestContent) error {
	payload, _ := json.Marshal(map[string]string{
		"subject": c.Subject,
		"text":    c.Text,
		"html":    c.HTML,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook http %d", resp.StatusCode)
	}
	return nil
}
