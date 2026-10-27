package utils

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// LoadSMTPConfig reads SMTP credentials from environment variables.
func LoadSMTPConfig() (SMTPConfig, error) {
	cfg := SMTPConfig{
		Host:     os.Getenv("SMTP_HOST"),
		Port:     os.Getenv("SMTP_PORT"),
		Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     os.Getenv("SMTP_FROM"),
	}

	if cfg.Host == "" || cfg.Port == "" ||
		cfg.Username == "" || cfg.Password == "" ||
		cfg.From == "" {
		return SMTPConfig{}, fmt.Errorf(
			"SMTP configuration is incomplete; check SMTP_HOST, SMTP_PORT, SMTP_USERNAME, SMTP_PASSWORD and SMTP_FROM",
		)
	}

	if _, err := strconv.Atoi(cfg.Port); err != nil {
		return SMTPConfig{}, fmt.Errorf("invalid SMTP_PORT")
	}

	if _, err := mail.ParseAddress(cfg.Username); err != nil {
		return SMTPConfig{}, fmt.Errorf("invalid SMTP_USERNAME")
	}

	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return SMTPConfig{}, fmt.Errorf("invalid SMTP_FROM")
	}

	return cfg, nil
}

// SendPDFEmail sends a PDF report as a direct email attachment.
// pdfBytes must contain the complete PDF file.
func SendPDFEmail(
	to string,
	subject string,
	body string,
	filename string,
	pdfBytes []byte,
) error {
	cfg, err := LoadSMTPConfig()
	if err != nil {
		return err
	}

	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("invalid recipient email address")
	}

	if len(pdfBytes) == 0 {
		return fmt.Errorf("PDF attachment is empty")
	}

	if len(pdfBytes) > 20*1024*1024 {
		return fmt.Errorf("PDF exceeds the 20 MB attachment limit")
	}

	// Prevent newline/header injection and path separators in filenames.
	filename = strings.ReplaceAll(filename, "\r", "")
	filename = strings.ReplaceAll(filename, "\n", "")
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	if filename == "" {
		filename = "georesist-report.pdf"
	}

	fromAddress, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return fmt.Errorf("invalid sender email address")
	}

	// Gmail requires STARTTLS on port 587.
	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("create SMTP client: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); !ok {
		return fmt.Errorf("SMTP server does not advertise STARTTLS")
	}

	tlsConfig := &tls.Config{
		ServerName: cfg.Host,
		MinVersion: tls.VersionTLS12,
	}

	if err := client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("start SMTP TLS: %w", err)
	}

	auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP authentication failed: %w", err)
	}

	if err := client.Mail(fromAddress.Address); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}

	if err := client.Rcpt(recipient.Address); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}

	// Build a multipart MIME message.
	var message bytes.Buffer
	boundary := "GeoResistBoundary7MA4YWxkTrZu0gW"

	message.WriteString("From: " + fromAddress.String() + "\r\n")
	message.WriteString("To: " + recipient.String() + "\r\n")
	message.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\n")
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n")
	message.WriteString("\r\n")

	// Plain-text email body.
	message.WriteString("--" + boundary + "\r\n")
	message.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	message.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")

	bodyEncoder := base64.NewEncoder(base64.StdEncoding, &message)
	_, err = bodyEncoder.Write([]byte(body))
	if err != nil {
		return fmt.Errorf("encode email body: %w", err)
	}
	if err := bodyEncoder.Close(); err != nil {
		return fmt.Errorf("finish email body encoding: %w", err)
	}
	message.WriteString("\r\n")

	// PDF attachment.
	message.WriteString("--" + boundary + "\r\n")
	message.WriteString("Content-Type: application/pdf; name=\"" + filename + "\"\r\n")
	message.WriteString("Content-Transfer-Encoding: base64\r\n")
	message.WriteString("Content-Disposition: attachment; filename=\"" + filename + "\"\r\n\r\n")

	attachmentEncoder := base64.NewEncoder(
		base64.StdEncoding,
		&lineBreakWriter{w: &message, width: 76},
	)
	if _, err := attachmentEncoder.Write(pdfBytes); err != nil {
		return fmt.Errorf("encode PDF attachment: %w", err)
	}
	if err := attachmentEncoder.Close(); err != nil {
		return fmt.Errorf("finish PDF attachment encoding: %w", err)
	}

	message.WriteString("\r\n--" + boundary + "--\r\n")

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("start SMTP message: %w", err)
	}

	if _, err := writer.Write(message.Bytes()); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("send SMTP message: %w", err)
	}

	if err := client.Quit(); err != nil {
		return fmt.Errorf("close SMTP session: %w", err)
	}

	return nil
}

// lineBreakWriter wraps base64 output at 76 characters per line.
type lineBreakWriter struct {
	w     *bytes.Buffer
	width int
	col   int
}

func (w *lineBreakWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if w.col == w.width {
			if _, err := w.w.WriteString("\r\n"); err != nil {
				return 0, err
			}
			w.col = 0
		}

		if err := w.w.WriteByte(b); err != nil {
			return 0, err
		}
		w.col++
	}

	return len(p), nil
}
