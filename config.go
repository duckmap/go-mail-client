package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TLSMode — способ защиты соединения.
type TLSMode string

const (
	TLSImplicit TLSMode = "tls"      // скрытое TLS-соединение (IMAP 993, SMTP 465)
	TLSStart    TLSMode = "starttls" // STARTTLS после обычного подключения
	TLSNone     TLSMode = "none"     // без шифрования
)

func (t TLSMode) String() string {
	switch t {
	case TLSImplicit:
		return "TLS"
	case TLSStart:
		return "STARTTLS"
	default:
		return "нет"
	}
}

// ParseTLSMode разбирает пользовательский ввод режима TLS.
func ParseTLSMode(s string) TLSMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tls", "ssl", "implicit":
		return TLSImplicit
	case "starttls", "start":
		return TLSStart
	default:
		return TLSNone
	}
}

// AccountConfig — настройки одной учётной записи.
type AccountConfig struct {
	ID   string `json:"id"`
	Name string `json:"name"` // отображаемое имя ящика

	Email      string `json:"email"`
	SenderName string `json:"sender_name"` // имя в заголовке From исходящих

	IMAPHost     string  `json:"imap_host"`
	IMAPPort     int     `json:"imap_port"`
	IMAPTLS      TLSMode `json:"imap_tls"`
	IMAPUser     string  `json:"imap_user"`
	IMAPPass     string  `json:"imap_pass"`
	IMAPInsecure bool    `json:"imap_insecure"` // не проверять сертификат

	SMTPHost     string  `json:"smtp_host"`
	SMTPPort     int     `json:"smtp_port"`
	SMTPTLS      TLSMode `json:"smtp_tls"`
	SMTPUser     string  `json:"smtp_user"`
	SMTPPass     string  `json:"smtp_pass"`
	SMTPInsecure bool    `json:"smtp_insecure"`

	// Переопределения специальных папок (если сервер не сообщает их сам).
	SentFolder    string `json:"sent_folder,omitempty"`
	TrashFolder   string `json:"trash_folder,omitempty"`
	DraftsFolder  string `json:"drafts_folder,omitempty"`
	JunkFolder    string `json:"junk_folder,omitempty"`
	ArchiveFolder string `json:"archive_folder,omitempty"`
}

// Config — весь конфигурационный файл приложения.
type Config struct {
	Version       int             `json:"version"`
	Accounts      []AccountConfig `json:"accounts"`
	AttachmentDir string          `json:"attachment_dir"` // куда сохранять вложения
	RefreshSec    int             `json:"refresh_sec"`    // автообновление, 0 — выключено
	FetchLimit    int             `json:"fetch_limit"`    // сколько писем грузить в папке
}

// DefaultConfigPath — путь к файлу конфигурации по умолчанию.
func DefaultConfigPath() string {
	if p := os.Getenv("GOMAIL_CONFIG"); p != "" {
		return p
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "gomail", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "gomail", "config.json")
}

// DefaultAttachmentDir — каталог для вложений по умолчанию.
func DefaultAttachmentDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "attachments"
	}
	dl := filepath.Join(home, "Downloads")
	if st, err := os.Stat(dl); err == nil && st.IsDir() {
		return dl
	}
	return filepath.Join(home, "attachments")
}

// LoadConfig читает конфигурацию из файла. Отсутствие файла — не ошибка.
func LoadConfig(path string) (*Config, error) {
	cfg := &Config{
		Version:       1,
		AttachmentDir: DefaultAttachmentDir(),
		RefreshSec:    120,
		FetchLimit:    300,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("ошибка разбора %s: %w", path, err)
	}
	if cfg.AttachmentDir == "" {
		cfg.AttachmentDir = DefaultAttachmentDir()
	}
	if cfg.FetchLimit <= 0 {
		cfg.FetchLimit = 300
	}
	if cfg.RefreshSec < 0 {
		cfg.RefreshSec = 0
	}
	for i := range cfg.Accounts {
		if cfg.Accounts[i].ID == "" {
			cfg.Accounts[i].ID = newID()
		}
		cfg.Accounts[i].normalize()
	}
	return cfg, nil
}

// Save записывает конфигурацию на диск с правами 0600 (внутри пароли).
func (c *Config) Save(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// normalize подставляет значения по умолчанию.
func (a *AccountConfig) normalize() {
	if a.IMAPPort == 0 {
		switch a.IMAPTLS {
		case TLSImplicit:
			a.IMAPPort = 993
		default:
			a.IMAPPort = 143
		}
	}
	if a.SMTPPort == 0 {
		switch a.SMTPTLS {
		case TLSImplicit:
			a.SMTPPort = 465
		default:
			a.SMTPPort = 587
		}
	}
	if a.IMAPUser == "" {
		a.IMAPUser = a.Email
	}
	if a.SMTPUser == "" {
		a.SMTPUser = a.Email
	}
	if a.Name == "" {
		a.Name = a.Email
	}
	if a.IMAPTLS == "" {
		a.IMAPTLS = TLSImplicit
	}
	if a.SMTPTLS == "" {
		a.SMTPTLS = TLSStart
	}
}

// Validate проверяет заполненность обязательных полей.
func (a *AccountConfig) Validate() error {
	if strings.TrimSpace(a.Email) == "" {
		return errors.New("не указан e-mail")
	}
	if !strings.Contains(a.Email, "@") {
		return errors.New("некорректный e-mail")
	}
	if strings.TrimSpace(a.IMAPHost) == "" {
		return errors.New("не указан IMAP-сервер")
	}
	if strings.TrimSpace(a.SMTPHost) == "" {
		return errors.New("не указан SMTP-сервер")
	}
	if a.IMAPPort <= 0 || a.IMAPPort > 65535 {
		return errors.New("некорректный IMAP-порт")
	}
	if a.SMTPPort <= 0 || a.SMTPPort > 65535 {
		return errors.New("некорректный SMTP-порт")
	}
	return nil
}

// Title — короткое имя для отображения.
func (a *AccountConfig) Title() string {
	if a.Name != "" {
		return a.Name
	}
	return a.Email
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "id"
	}
	return hex.EncodeToString(b)
}
