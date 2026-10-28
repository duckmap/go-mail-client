package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// SendMail отправляет готовое RFC822-письмо через SMTP-сервер аккаунта.
func SendMail(cfg AccountConfig, raw []byte, envelopeFrom string, to []string) error {
	if len(to) == 0 {
		return fmt.Errorf("пустой список получателей")
	}
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))
	tlsCfg := &tls.Config{
		ServerName:         cfg.SMTPHost,
		InsecureSkipVerify: cfg.SMTPInsecure,
	}

	var (
		c   *smtp.Client
		err error
	)
	switch cfg.SMTPTLS {
	case TLSImplicit:
		c, err = smtp.DialTLS(addr, tlsCfg)
	case TLSStart:
		c, err = smtp.DialStartTLS(addr, tlsCfg)
		if err != nil {
			// если сервер не поддерживает STARTTLS — пробуем без шифрования
			c, err = smtp.Dial(addr)
		}
	default:
		c, err = smtp.Dial(addr)
	}
	if err != nil {
		return fmt.Errorf("SMTP %s: %w", addr, err)
	}
	c.CommandTimeout = 60 * time.Second
	c.SubmissionTimeout = 120 * time.Second
	defer c.Close()

	if cfg.SMTPUser != "" && cfg.SMTPPass != "" {
		if !c.SupportsAuth("PLAIN") {
			return fmt.Errorf("SMTP-сервер не поддерживает аутентификацию PLAIN")
		}
		if err := c.Auth(sasl.NewPlainClient("", cfg.SMTPUser, cfg.SMTPPass)); err != nil {
			return fmt.Errorf("SMTP: ошибка аутентификации: %w", err)
		}
	}

	if err := c.SendMail(envelopeFrom, to, bytes.NewReader(raw)); err != nil {
		return fmt.Errorf("SMTP: отправка: %w", err)
	}
	if err := c.Quit(); err != nil {
		c.Close()
	}
	return nil
}

// TestSMTPConfig проверяет подключение и (при наличии) аутентификацию SMTP.
func TestSMTPConfig(cfg AccountConfig) error {
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))
	tlsCfg := &tls.Config{
		ServerName:         cfg.SMTPHost,
		InsecureSkipVerify: cfg.SMTPInsecure,
	}
	var (
		c   *smtp.Client
		err error
	)
	switch cfg.SMTPTLS {
	case TLSImplicit:
		c, err = smtp.DialTLS(addr, tlsCfg)
	case TLSStart:
		c, err = smtp.DialStartTLS(addr, tlsCfg)
		if err != nil {
			c, err = smtp.Dial(addr)
		}
	default:
		c, err = smtp.Dial(addr)
	}
	if err != nil {
		return fmt.Errorf("подключение к %s: %w", addr, err)
	}
	c.CommandTimeout = 30 * time.Second
	defer c.Close()
	if cfg.SMTPUser != "" && cfg.SMTPPass != "" {
		if err := c.Auth(sasl.NewPlainClient("", cfg.SMTPUser, cfg.SMTPPass)); err != nil {
			return fmt.Errorf("аутентификация: %w", err)
		}
	}
	if err := c.Noop(); err != nil {
		return err
	}
	return c.Quit()
}
