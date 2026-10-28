package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	imapbackend "github.com/emersion/go-imap/backend/memory"
	"github.com/muesli/termenv"
)

type memMailbox = imapbackend.Mailbox

// TestProgramSmoke запускает полноценную tea.Program «вслепую»
// (ввод из файла, вывод в буфер) и проверяет, что цикл работает.
func TestProgramSmoke(t *testing.T) {
	cfg := &Config{AttachmentDir: t.TempDir(), RefreshSec: 0}
	m := newModel(cfg, filepath.Join(t.TempDir(), "config.json"))

	// сценарий: справка → закрыть → выход
	in := bytes.NewReader([]byte("?qq"))
	var out bytes.Buffer
	p := tea.NewProgram(m,
		tea.WithInput(in),
		tea.WithOutput(&out),
		tea.WithoutSignals(),
	)
	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal("программа не завершилась за 10 секунд")
	}
	if out.Len() == 0 {
		t.Errorf("рендер ничего не вывел")
	}
}

// TestRenderDemoScreens рендерит все экраны приложения на демо-данных
// и сохраняет ANSI-снимки в demo/ (для конвертации в HTML-превью).
func TestRenderDemoScreens(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	imapAddr, user, closeIMAP := startTestIMAP(t)
	defer closeIMAP()
	for _, mb := range []string{"Sent", "Trash", "Drafts", "Junk", "Archive", "Work/Projects", "Work/Invoices"} {
		addMailbox(t, user, mb)
	}

	// очистить стандартное seed-письмо backend'а и залить демо-почту
	if mb, err := user.GetMailbox("INBOX"); err == nil {
		mb.(*memMailbox).Messages = nil
	}
	now := time.Now()
	for _, x := range DemoSeedMessages(now) {
		mb, err := user.GetMailbox(x.Mbox)
		if err != nil {
			t.Fatal(err)
		}
		if err := mb.(*memMailbox).CreateMessage(x.Flags, x.Date, bytes.NewReader(x.Raw)); err != nil {
			t.Fatal(err)
		}
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	ih, ip, _ := net.SplitHostPort(imapAddr)
	ipn, _ := strconv.Atoi(ip)
	cfg := &Config{
		AttachmentDir: dir, RefreshSec: 0, FetchLimit: 100,
		Accounts: []AccountConfig{
			{ID: "1", Name: "Рабочий", Email: "ivan@company.ru", SenderName: "Иван Петров",
				IMAPHost: ih, IMAPPort: ipn, IMAPTLS: TLSNone, IMAPUser: "username", IMAPPass: "password",
				SMTPHost: "smtp.company.ru", SMTPPort: 465, SMTPTLS: TLSImplicit},
			{ID: "2", Name: "Личный", Email: "ivan@example.com",
				IMAPHost: "imap.mail.ru", IMAPPort: 993, IMAPTLS: TLSImplicit, IMAPPass: "••••••",
				SMTPHost: "smtp.mail.ru", SMTPPort: 465, SMTPTLS: TLSImplicit},
		},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	m := newModel(cfg, cfgPath)
	m, _ = step(m, tea.WindowSizeMsg{Width: 140, Height: 42})

	// подключаем только первый (тестовый) аккаунт
	if err := m.accounts[0].Connect(); err != nil {
		t.Fatal(err)
	}
	m.accounts[1].State = "error"
	m.accounts[1].LastErr = fmt.Errorf("демо: офлайн")
	var cmd tea.Cmd
	m, cmd = send(m, connDoneMsg{acct: 0})
	m = drain(m, cmd, 0)

	os.MkdirAll("demo", 0o755)
	save := func(name, view string) {
		if err := os.WriteFile(filepath.Join("demo", name+".ansi"), []byte(view), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	save("01_main", m.View())

	// письмо с вложениями (2-е в списке)
	m, cmd = send(m, keySpecial(tea.KeyDown))
	m = drain(m, cmd, 0)
	save("02_main_attachment", m.View())

	// перемещение
	m, _ = send(m, keyRunes("m"))
	save("03_picker_move", m.View())
	m, _ = send(m, keySpecial(tea.KeyEsc))

	// поиск
	m, _ = send(m, keyRunes("/"))
	for _, r := range "отчёт" {
		m, _ = send(m, keyRunes(string(r)))
	}
	save("04_search", m.View())
	m, _ = send(m, keySpecial(tea.KeyEsc))

	// ответ
	m, cmd = send(m, keyRunes("r"))
	save("05_compose_reply", m.View())
	m, _ = send(m, keySpecial(tea.KeyEsc))
	m, _ = send(m, keyRunes("y")) // подтвердить отмену письма

	// справка
	m, _ = send(m, keyRunes("?"))
	save("06_help", m.View())
	m, _ = send(m, keyRunes("q")) // закрыть справку

	// учётные записи: список
	m, _ = send(m, keyRunes("a"))
	save("07_accounts", m.View())
	// форма редактирования
	m, _ = send(m, keySpecial(tea.KeyEnter))
	save("08_accounts_form", m.View())
	// новая запись
	m, _ = send(m, keySpecial(tea.KeyEsc))
	m, _ = send(m, keyRunes("n"))
	save("09_accounts_new", m.View())

	t.Log("демо-экраны сохранены в demo/")
}

// TestDemoServers проверяет встроенные демо-серверы, включая доставку
// отправленного письма обратно во «Входящие».
func TestDemoServers(t *testing.T) {
	cfg, shutdown, err := StartDemoServers()
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()

	acc := NewAccount(cfg)
	if err := acc.Connect(); err != nil {
		t.Fatal(err)
	}
	defer acc.Close()

	_, total, _, err := acc.ListMessages("INBOX", 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Errorf("в демо-INBOX %d писем, want 5", total)
	}

	// отправка через демо-SMTP → письмо доставляется во входящие
	raw, err := BuildMessage(BuildMessageOptions{
		From:    mustAddr(t, "Иван <ivan@company.ru>"),
		To:      mustParseAddrs(t, "ivan@company.ru"),
		Subject: "Самопроверка демо",
		Body:    "тест доставки\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := SendMail(cfg, raw, cfg.Email, []string{"ivan@company.ru"}); err != nil {
		t.Fatal(err)
	}
	msgs, total2, _, err := acc.ListMessages("INBOX", 50)
	if err != nil {
		t.Fatal(err)
	}
	if total2 != 6 {
		t.Errorf("после отправки %d писем, want 6", total2)
	}
	found := false
	for _, mm := range msgs {
		if mm.Subject == "Самопроверка демо" {
			found = true
		}
	}
	if !found {
		t.Errorf("отправленное письмо не доставлено во входящие")
	}
}

// TestDemoModeProgram — полный цикл tea.Program на демо-серверах.
func TestDemoModeProgram(t *testing.T) {
	acct, shutdown, err := StartDemoServers()
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()

	cfg := &Config{
		Accounts:      []AccountConfig{acct},
		AttachmentDir: t.TempDir(),
		RefreshSec:    0,
		FetchLimit:    50,
	}
	m := newModel(cfg, filepath.Join(t.TempDir(), "demo.json"))
	in := bytes.NewReader([]byte("?qq"))
	p := tea.NewProgram(m,
		tea.WithInput(in),
		tea.WithOutput(&bytes.Buffer{}),
		tea.WithoutSignals(),
	)
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		p.Kill()
		t.Fatal("демо-программа не завершилась")
	}
}
