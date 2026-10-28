package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------- хелперы ----------

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func keySpecial(t tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: t}
}

// step передаёт сообщение в модель.
func step(m model, msg tea.Msg) (model, tea.Cmd) {
	m2, cmd := m.Update(msg)
	mm, ok := m2.(model)
	if !ok {
		panic("Update вернул не model")
	}
	return mm, cmd
}

// drain выполняет команду и все порождённые ею сообщения (рекурсивно),
// пропуская «вечные» тики спиннера.
func drain(m model, cmd tea.Cmd, depth int) model {
	if cmd == nil || depth > 40 {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	if _, ok := msg.(spinner.TickMsg); ok {
		return m
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = drain(m, c, depth+1)
		}
		return m
	}
	var next tea.Cmd
	m, next = step(m, msg)
	return drain(m, next, depth+1)
}

func send(m model, msg tea.Msg) (model, tea.Cmd) {
	return step(m, msg)
}

func sized(m model) model {
	m2, _ := step(m, tea.WindowSizeMsg{Width: 140, Height: 42})
	return m2
}

// ---------- тесты ----------

func TestUIRenderEmpty(t *testing.T) {
	cfg := &Config{AttachmentDir: t.TempDir(), RefreshSec: 0, FetchLimit: 100}
	m := sized(newModel(cfg, filepath.Join(t.TempDir(), "config.json")))
	v := m.View()
	for _, want := range []string{"Ящики", "Нажмите «a»"} {
		if !strings.Contains(v, want) {
			t.Errorf("View не содержит %q:\n%s", want, v)
		}
	}
	if m.width != 140 || m.height != 42 {
		t.Errorf("размер не применён: %dx%d", m.width, m.height)
	}
}

// TestUILayoutFits проверяет, что ни один экран не вылезает за размер терминала.
func TestUILayoutFits(t *testing.T) {
	for _, sz := range [][2]int{{70, 16}, {100, 24}, {140, 42}, {200, 50}} {
		dir := t.TempDir()
		cfg := &Config{AttachmentDir: dir, RefreshSec: 0}
		m := newModel(cfg, filepath.Join(dir, "c.json"))
		m, _ = step(m, tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})

		for _, view := range []appView{viewMain, viewAccounts, viewCompose} {
			m.view = view
			if view == viewCompose {
				m.compose = newCompose()
				m.compose.setSize(sz[0], sz[1])
			}
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) != sz[1] {
				t.Errorf("%dx%d, экран %v: %d строк вместо %d", sz[0], sz[1], view, len(lines), sz[1])
			}
			for i, ln := range lines {
				if w := lipgloss.Width(ln); w > sz[0] {
					t.Errorf("%dx%d, экран %v: строка %d шириной %d", sz[0], sz[1], view, i, w)
				}
			}
		}
	}
}

func TestUIHelpOverlay(t *testing.T) {
	cfg := &Config{AttachmentDir: t.TempDir(), RefreshSec: 0}
	m := sized(newModel(cfg, filepath.Join(t.TempDir(), "c.json")))
	m, _ = send(m, keyRunes("?"))
	if m.overlay != ovHelp {
		t.Fatalf("оверлей справки не открылся")
	}
	v := m.View()
	if !strings.Contains(v, "gomail") || !strings.Contains(v, "Навигация") {
		t.Errorf("справка не отрисовалась:\n%s", v)
	}
	m, _ = send(m, keyRunes("x")) // любая клавиша закрывает
	if m.overlay != ovNone {
		t.Errorf("справка не закрылась")
	}
}

func TestUIAccountsFlow(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := &Config{AttachmentDir: dir, RefreshSec: 0}
	m := sized(newModel(cfg, cfgPath))

	// открыть экран учёток
	m, _ = send(m, keyRunes("a"))
	if m.view != viewAccounts {
		t.Fatalf("view = %v, want viewAccounts", m.view)
	}
	// новая запись
	m, _ = send(m, keyRunes("n"))
	if len(m.acctsUI.items) != 1 || !m.acctsUI.editing {
		t.Fatalf("не создалась новая запись: %+v", m.acctsUI)
	}
	// попытка сохранить пустую — ошибка валидации
	m, _ = send(m, keySpecial(tea.KeyCtrlS))
	if m.acctsUI.saveMsg == "" {
		t.Errorf("ожидалась ошибка валидации")
	}
	if m.view != viewAccounts {
		t.Errorf("экран не должен закрыться при ошибке")
	}
	// заполняем форму
	type_ := func(s string) {
		for _, r := range s {
			m, _ = send(m, keyRunes(string(r)))
		}
	}
	tab := func() { m, _ = send(m, keySpecial(tea.KeyTab)) }

	m, _ = send(m, keySpecial(tea.KeyCtrlU)) // очистить «Новый ящик»
	type_("Мой ящик")                        // 0 Название
	tab()
	type_("Иван") // 1 Имя отправителя
	tab()
	type_("me@example.com") // 2 E-mail
	tab()
	type_("imap.example.com") // 3 IMAP host
	tab()                     // 4 порт (993 уже стоит)
	tab()                     // 5 TLS enum
	tab()                     // 6 логин (подставится при сохранении)
	tab()                     // 7 пароль
	type_("secretpass")
	tab() // 8 bool
	tab() // 9 SMTP host — пока пустой
	// сохраняем: SMTP-сервер пустой → ошибка
	m, _ = send(m, keySpecial(tea.KeyCtrlS))
	if m.acctsUI.saveMsg == "" {
		t.Errorf("ожидалась ошибка про SMTP")
	}
	if m.acctsUI.fieldCur != 9 {
		for m.acctsUI.fieldCur != 9 {
			tab()
		}
	}
	type_("smtp.example.com")
	// доходим до конца и сохраняем
	for m.acctsUI.fieldCur != 18 {
		tab()
	}
	m, _ = send(m, keySpecial(tea.KeyCtrlS))
	if m.view != viewMain {
		t.Fatalf("после сохранения view = %v, saveMsg = %q", m.view, m.acctsUI.saveMsg)
	}
	// конфиг записан на диск
	loaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Accounts) != 1 {
		t.Fatalf("в файле %d учёток", len(loaded.Accounts))
	}
	a := loaded.Accounts[0]
	if a.Email != "me@example.com" || a.IMAPHost != "imap.example.com" ||
		a.IMAPPass != "secretpass" || a.SMTPHost != "smtp.example.com" ||
		a.Name != "Мой ящик" || a.SenderName != "Иван" || a.IMAPPort != 993 {
		t.Errorf("поля сохранились неверно: %+v", a)
	}
	// рантайм-аккаунт пересоздан
	if len(m.accounts) != 1 {
		t.Errorf("accounts = %d", len(m.accounts))
	}
	// выход из экрана по esc
	m, _ = send(m, keyRunes("a"))
	m, _ = send(m, keySpecial(tea.KeyEsc))
	if m.view != viewMain {
		t.Errorf("esc не закрыл экран учёток")
	}
}

func TestUIFullLoopWithServers(t *testing.T) {
	imapAddr, user, closeIMAP := startTestIMAP(t)
	defer closeIMAP()
	addMailbox(t, user, "Sent")
	addMailbox(t, user, "Trash")
	addMailbox(t, user, "Drafts")
	seed := []byte("From: contact@example.org\r\nTo: username@example.com\r\n" +
		"Subject: Семинар по садоводству\r\nDate: Wed, 11 May 2016 14:31:59 +0000\r\n" +
		"Message-ID: <seed-1@localhost>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Приходите на семинар!\r\n")
	addMessage(t, user, "INBOX", []string{"\\Seen"}, seed)

	smtpAddr, smtpBe, closeSMTP := startTestSMTP(t, "username", "password")
	defer closeSMTP()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := &Config{AttachmentDir: dir, RefreshSec: 0, FetchLimit: 50}
	cfg.Accounts = []AccountConfig{mergeAddrs(t, imapAddr, smtpAddr)}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	m := sized(newModel(cfg, cfgPath))

	// подключение (синхронно для детерминизма)
	if err := m.accounts[0].Connect(); err != nil {
		t.Fatal(err)
	}
	var cmd tea.Cmd
	m, cmd = send(m, connDoneMsg{acct: 0})
	m = drain(m, cmd, 0)

	// автоматически выбрана папка INBOX и загружено письмо
	if m.curFolder != "INBOX" {
		t.Errorf("curFolder = %q", m.curFolder)
	}
	if len(m.msgs) != 2 { // seed-сообщение backend'а + наше
		t.Fatalf("msgs = %d", len(m.msgs))
	}
	if !strings.Contains(m.msgs[0].Subject, "Семинар") {
		t.Errorf("subject[0] = %q", m.msgs[0].Subject)
	}
	if m.curMsg == nil || !strings.Contains(m.curMsg.Body, "семинар") {
		t.Errorf("тело не загружено: %+v", m.curMsg)
	}
	v := m.View()
	if !strings.Contains(v, "Семинар") {
		t.Errorf("View не содержит тему письма")
	}

	// ответ на письмо
	m, cmd = send(m, keyRunes("r"))
	if m.view != viewCompose {
		t.Fatalf("view = %v после «r»", m.view)
	}
	if got := m.compose.to.Value(); !strings.Contains(got, "contact@example.org") {
		t.Errorf("to = %q", got)
	}
	if got := m.compose.subject.Value(); !strings.HasPrefix(got, "Re:") {
		t.Errorf("subject = %q", got)
	}
	if got := m.compose.body.Value(); !strings.Contains(got, "> Приходите") {
		t.Errorf("цитата не подставлена: %q", got)
	}
	// дописываем текст и отправляем
	for m.compose.focusIdx != cfBody {
		m, _ = send(m, keySpecial(tea.KeyTab))
	}
	m, _ = send(m, keyRunes("Буду!"))
	m, cmd = send(m, keySpecial(tea.KeyCtrlS))
	m = drain(m, cmd, 0)

	if m.view != viewMain {
		t.Fatalf("после отправки view = %v, err = %q", m.view, m.compose.errText)
	}
	got := smtpBe.all()
	if len(got) != 1 {
		t.Fatalf("SMTP получил %d писем", len(got))
	}
	fm, err := parseRawMessage(got[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Subject != "Re: Семинар по садоводству" {
		t.Errorf("отправлена тема %q", fm.Subject)
	}
	if !strings.Contains(fm.Text, "Буду!") {
		t.Errorf("тело = %q", fm.Text)
	}
	if len(fm.InReplyTo) != 1 || fm.InReplyTo[0] != "seed-1@localhost" {
		t.Errorf("In-Reply-To = %v", fm.InReplyTo)
	}
	// письмо легло в Sent через IMAP APPEND
	acc := m.accounts[0]
	sentMsgs, _, _, err := acc.ListMessages("Sent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sentMsgs) != 1 {
		t.Errorf("в Sent %d писем", len(sentMsgs))
	}
	// флаг Answered проставлен
	if !m.msgs[0].Answered {
		t.Errorf("Answered не установлен в списке")
	}

	// удаляем письмо из INBOX → уезжает в Trash
	if !strings.Contains(m.msgs[0].Subject, "Семинар") {
		t.Fatalf("курсор не на «Семинаре»: %q", m.msgs[0].Subject)
	}
	m, cmd = send(m, keyRunes("d"))
	m = drain(m, cmd, 0)
	if len(m.msgs) != 1 {
		t.Errorf("в INBOX должно остаться 1 письмо, осталось %d", len(m.msgs))
	}
	trashMsgs, _, _, err := acc.ListMessages("Trash", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(trashMsgs) != 1 {
		t.Errorf("в Trash %d писем", len(trashMsgs))
	}
}

// mergeAddrs собирает AccountConfig из адресов тест-серверов.
func mergeAddrs(t *testing.T, imapAddr, smtpAddr string) AccountConfig {
	t.Helper()
	ih, ip, err := splitHostPortStrict(imapAddr)
	if err != nil {
		t.Fatal(err)
	}
	sh, sp, err := splitHostPortStrict(smtpAddr)
	if err != nil {
		t.Fatal(err)
	}
	cfg := AccountConfig{
		ID: "test", Name: "Тестовый", Email: "username@example.com",
		IMAPHost: ih, IMAPPort: ip, IMAPTLS: TLSNone, IMAPUser: "username", IMAPPass: "password",
		SMTPHost: sh, SMTPPort: sp, SMTPTLS: TLSNone, SMTPUser: "username", SMTPPass: "password",
	}
	cfg.normalize()
	return cfg
}

func TestUISearchFlow(t *testing.T) {
	imapAddr, user, closeIMAP := startTestIMAP(t)
	defer closeIMAP()
	addMailbox(t, user, "Trash")
	addMessage(t, user, "INBOX", nil, []byte("From: a@b.c\r\nTo: username@example.com\r\nSubject: УникальныйЗаголовок\r\nDate: Wed, 11 May 2016 14:31:59 +0000\r\nMessage-ID: <s2@x>\r\nContent-Type: text/plain\r\n\r\nтекст\r\n"))

	dir := t.TempDir()
	cfg := &Config{AttachmentDir: dir, RefreshSec: 0}
	cfg.Accounts = []AccountConfig{func() AccountConfig {
		h, p, _ := splitHostPortStrict(imapAddr)
		c := AccountConfig{Name: "T", Email: "username@example.com", IMAPHost: h, IMAPPort: p, IMAPTLS: TLSNone, IMAPUser: "username", IMAPPass: "password", SMTPHost: "127.0.0.1", SMTPPort: 1, SMTPTLS: TLSNone}
		c.normalize()
		return c
	}()}
	m := sized(newModel(cfg, filepath.Join(dir, "c.json")))
	if err := m.accounts[0].Connect(); err != nil {
		t.Fatal(err)
	}
	var cmd tea.Cmd
	m, cmd = send(m, connDoneMsg{acct: 0})
	m = drain(m, cmd, 0)
	if len(m.msgs) != 2 { // seed + добавленное
		t.Fatalf("msgs = %d", len(m.msgs))
	}
	// поиск
	m, _ = send(m, keyRunes("/"))
	if m.overlay != ovSearch {
		t.Fatalf("оверлей поиска не открылся")
	}
	for _, r := range "УникальныйЗаголовок" {
		m, _ = send(m, keyRunes(string(r)))
	}
	m, cmd = send(m, keySpecial(tea.KeyEnter))
	m = drain(m, cmd, 0)
	if !m.searchMode {
		t.Fatalf("режим поиска не включён")
	}
	if len(m.msgs) != 1 || !strings.Contains(m.msgs[0].Subject, "Уникальный") {
		t.Errorf("результаты поиска: %+v", m.msgs)
	}
	// сброс поиска по esc
	m, cmd = send(m, keySpecial(tea.KeyEsc))
	m = drain(m, cmd, 0)
	if m.searchMode {
		t.Errorf("поиск не сброшен")
	}
	if len(m.msgs) != 2 {
		t.Errorf("после сброса msgs = %d", len(m.msgs))
	}
}

func TestUIFlagToggles(t *testing.T) {
	imapAddr, user, closeIMAP := startTestIMAP(t)
	defer closeIMAP()
	addMailbox(t, user, "Trash")
	addMailbox(t, user, "Junk")
	addMessage(t, user, "INBOX", nil, []byte("From: a@b.c\r\nTo: u@example.com\r\nSubject: Флаги\r\nDate: Wed, 11 May 2016 14:31:59 +0000\r\nMessage-ID: <f1@x>\r\nContent-Type: text/plain\r\n\r\nтекст\r\n"))
	dir := t.TempDir()
	cfg := &Config{AttachmentDir: dir, RefreshSec: 0}
	h, p, _ := splitHostPortStrict(imapAddr)
	c := AccountConfig{Name: "T", Email: "username@example.com", IMAPHost: h, IMAPPort: p, IMAPTLS: TLSNone, IMAPUser: "username", IMAPPass: "password", SMTPHost: "127.0.0.1", SMTPPort: 1, SMTPTLS: TLSNone}
	c.normalize()
	cfg.Accounts = []AccountConfig{c}
	m := sized(newModel(cfg, filepath.Join(dir, "c.json")))
	if err := m.accounts[0].Connect(); err != nil {
		t.Fatal(err)
	}
	var cmd tea.Cmd
	m, cmd = send(m, connDoneMsg{acct: 0})
	m = drain(m, cmd, 0)
	if len(m.msgs) != 2 {
		t.Fatalf("msgs = %d", len(m.msgs))
	}
	// «Флаги» — второе письмо (seed первое по порядку? порядок от новых к старым)
	idx := -1
	for i := range m.msgs {
		if m.msgs[i].Subject == "Флаги" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("письмо «Флаги» не найдено")
	}
	for i := 0; i < idx; i++ {
		m, cmd = send(m, keySpecial(tea.KeyDown))
		m = drain(m, cmd, 0)
	}
	if m.msgs[m.msgCursor].Subject != "Флаги" {
		t.Fatalf("курсор не на том письме: %q", m.msgs[m.msgCursor].Subject)
	}
	before := m.msgs[m.msgCursor].Seen
	m, cmd = send(m, keyRunes("u")) // toggle unread
	m = drain(m, cmd, 0)
	if m.msgs[m.msgCursor].Seen == before {
		t.Errorf("Seen не переключился")
	}
	m, cmd = send(m, keyRunes("*")) // star
	m = drain(m, cmd, 0)
	if !m.msgs[m.msgCursor].Flagged {
		t.Errorf("Flagged не установлен")
	}
	// проверка на сервере
	uid := m.msgs[m.msgCursor].UID
	serverMsgs, err := m.accounts[0].FetchMessagesByUID("INBOX", []uint32{uid})
	if err != nil {
		t.Fatal(err)
	}
	if !serverMsgs[0].Flagged {
		t.Errorf("флаг не сохранён на сервере")
	}
	// «J» — в спам
	m, cmd = send(m, keyRunes("J"))
	m = drain(m, cmd, 0)
	junk, _, _, err := m.accounts[0].ListMessages("Junk", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(junk) != 1 {
		t.Errorf("в Junk %d писем", len(junk))
	}
	// «A» — прочитать все (с подтверждением)
	m, cmd = send(m, keyRunes("A"))
	if m.overlay != ovConfirm {
		t.Fatalf("нет подтверждения")
	}
	m, cmd = send(m, keyRunes("y"))
	m = drain(m, cmd, 0)
	all, _, _, err := m.accounts[0].ListMessages("INBOX", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, mm := range all {
		if !mm.Seen {
			t.Errorf("письмо %d не помечено прочитанным", mm.UID)
		}
	}
}

func TestUISaveAttachments(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{AttachmentDir: dir, RefreshSec: 0}
	m := sized(newModel(cfg, filepath.Join(dir, "c.json")))
	m.curMsg = &FullMessage{Attachments: []Attachment{
		{Filename: "отчёт/../evil.txt", Data: []byte("data1")},
		{Filename: "отчёт/../evil.txt", Data: []byte("data2")},
	}}
	var cmd tea.Cmd
	m, cmd = send(m, keyRunes("S"))
	m = drain(m, cmd, 0)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("в каталоге %d файлов: %v", len(entries), names)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "..") || strings.Contains(e.Name(), "/") {
			t.Errorf("небезопасное имя: %q", e.Name())
		}
	}
}

// splitHostPortStrict — net.SplitHostPort + atoi.
func splitHostPortStrict(addr string) (string, int, error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(p)
	return h, n, err
}
