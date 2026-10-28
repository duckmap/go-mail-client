package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// Специальные роли папок.
const (
	SpInbox   = "inbox"
	SpSent    = "sent"
	SpDrafts  = "drafts"
	SpTrash   = "trash"
	SpJunk    = "junk"
	SpArchive = "archive"
)

// FolderInfo — сведения о папке (mailbox) на сервере.
type FolderInfo struct {
	Name      string
	Delimiter string
	Attrs     []string
	Special   string // роль: SpInbox, SpSent, ... (пусто для обычных)
	NoSelect  bool   // папка-контейнер, выбирать нельзя
}

// Account — учётная запись вместе с активным IMAP-подключением.
type Account struct {
	Cfg AccountConfig

	mu      sync.Mutex
	conn    *client.Client
	Folders []FolderInfo
	Special map[string]string // роль -> имя папки
	State   string            // "", "connecting", "online", "error"
	LastErr error
}

// NewAccount создаёт объект учётной записи (без подключения).
func NewAccount(cfg AccountConfig) *Account {
	return &Account{Cfg: cfg, Special: map[string]string{}}
}

// tlsConfig формирует TLS-конфигурацию для хоста.
func (a *Account) tlsConfig(host string, insecure bool) *tls.Config {
	return &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: insecure,
	}
}

// dialIMAP устанавливает TCP/TLS-соединение согласно настройкам.
func (a *Account) dialIMAP() (*client.Client, error) {
	addr := net.JoinHostPort(a.Cfg.IMAPHost, strconv.Itoa(a.Cfg.IMAPPort))
	tlsCfg := a.tlsConfig(a.Cfg.IMAPHost, a.Cfg.IMAPInsecure)

	var (
		c   *client.Client
		err error
	)
	switch a.Cfg.IMAPTLS {
	case TLSImplicit:
		c, err = client.DialTLS(addr, tlsCfg)
	case TLSStart:
		c, err = client.Dial(addr)
		if err == nil {
			if ok, _ := c.SupportStartTLS(); ok {
				if err = c.StartTLS(tlsCfg); err != nil {
					c.Terminate()
					return nil, fmt.Errorf("STARTTLS: %w", err)
				}
			}
		}
	default:
		c, err = client.Dial(addr)
	}
	if err != nil {
		return nil, err
	}
	c.Timeout = 90 * time.Second
	return c, nil
}

// Connect подключается к IMAP, логинится и загружает список папок.
// Повторный вызов при активном соединении ничего не делает.
func (a *Account) Connect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connectLocked()
}

// connectLocked — Connect под уже взятой блокировкой.
func (a *Account) connectLocked() error {
	a.State = "connecting"
	if a.conn != nil {
		a.State = "online"
		a.LastErr = nil
		return nil
	}
	c, err := a.dialIMAP()
	if err != nil {
		a.State = "error"
		a.LastErr = err
		return fmt.Errorf("IMAP %s: %w", a.Cfg.IMAPHost, err)
	}
	user := a.Cfg.IMAPUser
	if user == "" {
		user = a.Cfg.Email
	}
	if err := c.Login(user, a.Cfg.IMAPPass); err != nil {
		c.Terminate()
		a.State = "error"
		a.LastErr = err
		return fmt.Errorf("IMAP: ошибка входа: %w", err)
	}
	a.conn = c
	a.State = "online"
	a.LastErr = nil
	if err := a.loadFoldersLocked(); err != nil {
		// не фатально: подключение есть, папки можно обновить позже
		a.LastErr = err
	}
	return nil
}

// Close корректно закрывает соединение.
func (a *Account) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeLocked()
}

func (a *Account) closeLocked() {
	if a.conn != nil {
		_ = a.conn.Logout()
		a.conn.Terminate()
		a.conn = nil
	}
	a.State = ""
}

// ensureConnLocked возвращает живое соединение, переподключаясь при
// необходимости. Вызывать держа a.mu.
func (a *Account) ensureConnLocked() (*client.Client, error) {
	if a.conn == nil {
		if err := a.connectLocked(); err != nil {
			return nil, err
		}
	}
	if a.conn == nil {
		return nil, fmt.Errorf("нет соединения с IMAP")
	}
	return a.conn, nil
}

// loadFoldersLocked получает список папок и определяет специальные.
func (a *Account) loadFoldersLocked() error {
	c := a.conn
	if c == nil {
		return fmt.Errorf("нет соединения")
	}
	ch := make(chan *imap.MailboxInfo, 64)
	var folders []FolderInfo
	errCh := make(chan error, 1)
	go func() { errCh <- c.List("", "*", ch) }()
	for info := range ch {
		fi := FolderInfo{
			Name:      info.Name,
			Delimiter: info.Delimiter,
			Attrs:     info.Attributes,
		}
		for _, at := range info.Attributes {
			switch strings.ToLower(at) {
			case `\noselect`:
				fi.NoSelect = true
			case `\sent`:
				fi.Special = SpSent
			case `\drafts`:
				fi.Special = SpDrafts
			case `\trash`:
				fi.Special = SpTrash
			case `\junk`:
				fi.Special = SpJunk
			case `\archive`, `\all`:
				fi.Special = SpArchive
			}
		}
		if strings.EqualFold(info.Name, "INBOX") {
			fi.Special = SpInbox
		}
		folders = append(folders, fi)
	}
	if err := <-errCh; err != nil {
		return err
	}
	a.Folders = folders
	a.detectSpecial() // сначала определяем роли (влияют на порядок и подписи)
	sortFolders(a.Folders)
	return nil
}

// detectSpecial заполняет карту специальных папок: сначала из атрибутов
// SPECIAL-USE, затем из переопределений пользователя, затем по именам.
func (a *Account) detectSpecial() {
	sp := map[string]string{}
	for _, f := range a.Folders {
		if f.Special != "" && !f.NoSelect {
			if _, ok := sp[f.Special]; !ok {
				sp[f.Special] = f.Name
			}
		}
	}
	overrides := map[string]string{
		SpSent:    a.Cfg.SentFolder,
		SpTrash:   a.Cfg.TrashFolder,
		SpDrafts:  a.Cfg.DraftsFolder,
		SpJunk:    a.Cfg.JunkFolder,
		SpArchive: a.Cfg.ArchiveFolder,
	}
	for role, name := range overrides {
		if name != "" {
			sp[role] = name
		}
	}
	// Эвристика по именам, если сервер не сообщил SPECIAL-USE.
	nameGuess := map[string][]string{
		SpSent:    {"sent", "sent messages", "sent items", "отправленные"},
		SpDrafts:  {"drafts", "черновики"},
		SpTrash:   {"trash", "корзина", "deleted", "deleted messages", "deleted items", "удаленные"},
		SpJunk:    {"junk", "junk e-mail", "spam", "спам", "нежелательная почта"},
		SpArchive: {"archive", "архив", "all mail", "все письма"},
	}
	for _, f := range a.Folders {
		if f.NoSelect || f.Special == SpInbox {
			continue
		}
		last := f.Name
		if f.Delimiter != "" {
			if idx := strings.LastIndex(f.Name, f.Delimiter); idx >= 0 {
				last = f.Name[idx+len(f.Delimiter):]
			}
		}
		for role, variants := range nameGuess {
			if _, ok := sp[role]; ok {
				continue
			}
			for _, v := range variants {
				if strings.EqualFold(last, v) || strings.EqualFold(f.Name, v) {
					sp[role] = f.Name
				}
			}
		}
	}
	if _, ok := sp[SpInbox]; !ok {
		for _, f := range a.Folders {
			if strings.EqualFold(f.Name, "INBOX") && !f.NoSelect {
				sp[SpInbox] = f.Name
				break
			}
		}
	}
	// Помечаем роли в списке папок (для иконок в UI).
	for i := range a.Folders {
		for role, name := range sp {
			if name == a.Folders[i].Name {
				if a.Folders[i].Special == "" {
					a.Folders[i].Special = role
				}
			}
		}
	}
	a.Special = sp
}

// SpecialFolder возвращает имя специальной папки (или "" если её нет).
func (a *Account) SpecialFolder(role string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Special[role]
}

// RefreshFolders перечитывает список папок с сервера.
func (a *Account) RefreshFolders() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.ensureConnLocked(); err != nil {
		return err
	}
	return a.loadFoldersLocked()
}

// sortFolders сортирует: INBOX и специальные первыми, затем по имени (иерархия).
func sortFolders(f []FolderInfo) {
	order := map[string]int{
		SpInbox: 0, SpSent: 1, SpDrafts: 2, SpArchive: 3, SpJunk: 4, SpTrash: 5,
	}
	sort.SliceStable(f, func(i, j int) bool {
		oi, oki := order[f[i].Special]
		oj, okj := order[f[j].Special]
		if oki && okj {
			if oi != oj {
				return oi < oj
			}
		} else if oki != okj {
			return oki
		}
		return strings.ToLower(f[i].Name) < strings.ToLower(f[j].Name)
	})
}

// TestIMAPConfig проверяет подключение и вход с указанными настройками.
// Возвращает число найденных папок.
func TestIMAPConfig(cfg AccountConfig) (int, error) {
	addr := net.JoinHostPort(cfg.IMAPHost, strconv.Itoa(cfg.IMAPPort))
	tlsCfg := &tls.Config{ServerName: cfg.IMAPHost, InsecureSkipVerify: cfg.IMAPInsecure}
	var (
		c   *client.Client
		err error
	)
	switch cfg.IMAPTLS {
	case TLSImplicit:
		c, err = client.DialTLS(addr, tlsCfg)
	case TLSStart:
		c, err = client.Dial(addr)
		if err == nil {
			if ok, _ := c.SupportStartTLS(); ok {
				err = c.StartTLS(tlsCfg)
			}
		}
	default:
		c, err = client.Dial(addr)
	}
	if err != nil {
		return 0, fmt.Errorf("подключение к %s: %w", addr, err)
	}
	defer c.Terminate()
	c.Timeout = 30 * time.Second
	user := cfg.IMAPUser
	if user == "" {
		user = cfg.Email
	}
	if err := c.Login(user, cfg.IMAPPass); err != nil {
		return 0, fmt.Errorf("вход: %w", err)
	}
	ch := make(chan *imap.MailboxInfo, 64)
	n := 0
	errCh := make(chan error, 1)
	go func() { errCh <- c.List("", "*", ch) }()
	for range ch {
		n++
	}
	if err := <-errCh; err != nil {
		return n, err
	}
	return n, c.Logout()
}
