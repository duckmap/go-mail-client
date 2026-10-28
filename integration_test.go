package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	imapbackend "github.com/emersion/go-imap/backend/memory"
	imapserver "github.com/emersion/go-imap/server"
	"github.com/emersion/go-sasl"
	smtpserver "github.com/emersion/go-smtp"
	netmail "net/mail"
)

// ---------- тестовый IMAP-сервер (in-memory) ----------

func startTestIMAP(t *testing.T) (addr string, user *imapbackend.User, closeFn func()) {
	t.Helper()
	be := imapbackend.New()
	srv := imapserver.New(be)
	srv.AllowInsecureAuth = true
	srv.ErrorLog = log.New(io.Discard, "", 0)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)

	bu, err := be.Login(nil, "username", "password")
	if err != nil {
		t.Fatal(err)
	}
	user = bu.(*imapbackend.User)
	return ln.Addr().String(), user, func() { srv.Close(); ln.Close() }
}

func addMailbox(t *testing.T, user *imapbackend.User, name string) {
	t.Helper()
	if err := user.CreateMailbox(name); err != nil {
		t.Fatal(err)
	}
}

func addMessage(t *testing.T, user *imapbackend.User, mbox string, flags []string, raw []byte) {
	t.Helper()
	mb, err := user.GetMailbox(mbox)
	if err != nil {
		t.Fatal(err)
	}
	mm := mb.(*imapbackend.Mailbox)
	if err := mm.CreateMessage(flags, time.Now(), bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
}

func accountForIMAP(addr string) AccountConfig {
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	cfg := AccountConfig{
		Name: "Тест", Email: "username@example.com",
		IMAPHost: host, IMAPPort: p, IMAPTLS: TLSNone,
		IMAPUser: "username", IMAPPass: "password",
		SMTPHost: "localhost", SMTPPort: 2525, SMTPTLS: TLSNone,
	}
	cfg.normalize()
	return cfg
}

// ---------- тестовый SMTP-сервер ----------

type receivedMail struct {
	From string
	To   []string
	Data []byte
}

type testSMTPBackend struct {
	mu       sync.Mutex
	received []receivedMail
	user     string
	pass     string
}

type testSMTPSession struct {
	be   *testSMTPBackend
	from string
	to   []string
}

func (be *testSMTPBackend) NewSession(c *smtpserver.Conn) (smtpserver.Session, error) {
	return &testSMTPSession{be: be}, nil
}

func (s *testSMTPSession) AuthMechanisms() []string { return []string{"PLAIN"} }

func (s *testSMTPSession) Auth(mech string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(identity, username, password string) error {
		if username == s.be.user && password == s.be.pass {
			return nil
		}
		return errors.New("bad credentials")
	}), nil
}

func (s *testSMTPSession) Mail(from string, opts *smtpserver.MailOptions) error {
	s.from = from
	s.to = nil
	return nil
}

func (s *testSMTPSession) Rcpt(to string, opts *smtpserver.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *testSMTPSession) Data(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.be.mu.Lock()
	defer s.be.mu.Unlock()
	s.be.received = append(s.be.received, receivedMail{From: s.from, To: append([]string{}, s.to...), Data: b})
	return nil
}

func (s *testSMTPSession) Reset()        {}
func (s *testSMTPSession) Logout() error { return nil }

func (be *testSMTPBackend) all() []receivedMail {
	be.mu.Lock()
	defer be.mu.Unlock()
	return append([]receivedMail{}, be.received...)
}

func startTestSMTP(t *testing.T, user, pass string) (addr string, be *testSMTPBackend, closeFn func()) {
	t.Helper()
	be = &testSMTPBackend{user: user, pass: pass}
	srv := smtpserver.NewServer(be)
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	srv.ErrorLog = log.New(io.Discard, "", 0)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	return ln.Addr().String(), be, func() { srv.Close(); ln.Close() }
}

// ---------- тесты ----------

func TestBuildAndParseMessage(t *testing.T) {
	raw, err := BuildMessage(BuildMessageOptions{
		From:    mustAddr(t, "Иван Тестов <ivan@example.com>"),
		To:      mustParseAddrs(t, "Пётр <petr@example.com>, olga@example.com"),
		Cc:      mustParseAddrs(t, "cc@example.com"),
		Subject: "Проверка связи — тест №1",
		Body:    "Привет!\n\nЭто тестовое письмо.\n",
		Attachments: []Attachment{{
			Filename:    "документ.txt",
			ContentType: "text/plain",
			Data:        []byte("содержимое вложения"),
		}},
		InReplyTo: "<orig-123@example.com>",
	})
	if err != nil {
		t.Fatal(err)
	}
	fm, err := parseRawMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Subject != "Проверка связи — тест №1" {
		t.Errorf("subject = %q", fm.Subject)
	}
	if len(fm.To) != 2 || fm.To[0].Address != "petr@example.com" {
		t.Errorf("to = %v", fm.To)
	}
	if len(fm.Cc) != 1 {
		t.Errorf("cc = %v", fm.Cc)
	}
	if !strings.Contains(fm.Body, "Это тестовое письмо") {
		t.Errorf("body = %q", fm.Body)
	}
	if len(fm.Attachments) != 1 || fm.Attachments[0].Filename != "документ.txt" {
		t.Errorf("attachments = %+v", fm.Attachments)
	} else if string(fm.Attachments[0].Data) != "содержимое вложения" {
		t.Errorf("attachment data = %q", fm.Attachments[0].Data)
	}
	if len(fm.InReplyTo) != 1 || fm.InReplyTo[0] != "orig-123@example.com" {
		t.Errorf("in-reply-to = %v", fm.InReplyTo)
	}
}

func mustAddr(t *testing.T, s string) *netmail.Address {
	t.Helper()
	a, err := netmail.ParseAddress(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustParseAddrs(t *testing.T, s string) []*netmail.Address {
	t.Helper()
	l, err := ParseAddressList(s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestIMAPEndToEnd(t *testing.T) {
	addr, user, closeFn := startTestIMAP(t)
	defer closeFn()
	addMailbox(t, user, "Sent")
	addMailbox(t, user, "Trash")
	addMailbox(t, user, "Drafts")

	raw := testMessageRaw(t)
	addMessage(t, user, "INBOX", []string{"\\Seen"}, raw)

	cfg := accountForIMAP(addr)
	acc := NewAccount(cfg)
	if err := acc.Connect(); err != nil {
		t.Fatal(err)
	}
	defer acc.Close()

	// Папки
	names := map[string]bool{}
	for _, f := range acc.Folders {
		names[f.Name] = true
	}
	for _, want := range []string{"INBOX", "Sent", "Trash", "Drafts"} {
		if !names[want] {
			t.Errorf("папка %s не найдена, есть: %v", want, names)
		}
	}
	if acc.Special[SpInbox] != "INBOX" {
		t.Errorf("special inbox = %q", acc.Special[SpInbox])
	}
	if acc.Special[SpSent] != "Sent" {
		t.Errorf("special sent = %q", acc.Special[SpSent])
	}

	// Список писем
	msgs, total, _, err := acc.ListMessages("INBOX", 100)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 { // одно предзаполненное + одно добавленное
		t.Errorf("total = %d, want 2", total)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	var found *MsgMeta
	for i := range msgs {
		if strings.Contains(msgs[i].Subject, "тест") {
			found = &msgs[i]
		}
	}
	if found == nil {
		t.Fatalf("добавленное письмо не найдено в списке: %+v", msgs)
	}
	if !found.Seen {
		t.Errorf("флаг Seen не подхватился")
	}
	if found.UID == 0 {
		t.Errorf("UID не получен")
	}

	// Полное письмо
	fm, err := acc.FetchMessage("INBOX", found.UID)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Subject != "Интеграционный тест" {
		t.Errorf("subject = %q", fm.Subject)
	}
	if !strings.Contains(fm.Body, "Тело интеграционного теста") {
		t.Errorf("body = %q", fm.Body)
	}
	if len(fm.Attachments) != 1 || string(fm.Attachments[0].Data) != "вложение!" {
		t.Errorf("attachments = %+v", fm.Attachments)
	}

	// Флаги: снять Seen
	if err := acc.StoreFlags("INBOX", []uint32{found.UID}, nil, []string{"\\Seen"}); err != nil {
		t.Fatal(err)
	}
	msgs2, _, _, err := acc.ListMessages("INBOX", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs2 {
		if m.UID == found.UID && m.Seen {
			t.Errorf("письмо всё ещё Seen после снятия флага")
		}
	}

	// Поиск
	uids, err := acc.Search("INBOX", "Интеграционный")
	if err != nil {
		t.Fatal(err)
	}
	if len(uids) != 1 || uids[0] != found.UID {
		t.Errorf("search uids = %v, want [%d]", uids, found.UID)
	}

	// Append в Sent
	if err := acc.AppendMessage("Sent", raw, []string{"\\Seen"}); err != nil {
		t.Fatal(err)
	}
	sentMsgs, _, _, err := acc.ListMessages("Sent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sentMsgs) != 1 {
		t.Errorf("в Sent %d писем, want 1", len(sentMsgs))
	}

	// Перемещение в Trash (через COPY+EXPUNGE, т.к. MOVE нет)
	if err := acc.MoveMessages("INBOX", "Trash", []uint32{found.UID}); err != nil {
		t.Fatal(err)
	}
	trashMsgs, _, _, err := acc.ListMessages("Trash", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(trashMsgs) != 1 || trashMsgs[0].UID == 0 {
		t.Fatalf("в Trash %d писем, want 1", len(trashMsgs))
	}
	inboxMsgs, totalInbox, _, err := acc.ListMessages("INBOX", 10)
	if err != nil {
		t.Fatal(err)
	}
	if totalInbox != 1 || len(inboxMsgs) != 1 {
		t.Errorf("в INBOX осталось %d писем, want 1", len(inboxMsgs))
	}

	// Полное удаление из Trash
	if err := acc.DeleteMessages("Trash", []uint32{trashMsgs[0].UID}); err != nil {
		t.Fatal(err)
	}
	_, totalTrash, _, err := acc.ListMessages("Trash", 10)
	if err != nil {
		t.Fatal(err)
	}
	if totalTrash != 0 {
		t.Errorf("в Trash %d писем после удаления, want 0", totalTrash)
	}

	// TestIMAPConfig
	n, err := TestIMAPConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n < 4 {
		t.Errorf("TestIMAPConfig вернул %d папок", n)
	}
}

func testMessageRaw(t *testing.T) []byte {
	t.Helper()
	raw, err := BuildMessage(BuildMessageOptions{
		From:    mustAddr(t, "Тестовый <test@example.com>"),
		To:      mustParseAddrs(t, "username@example.com"),
		Subject: "Интеграционный тест",
		Body:    "Тело интеграционного теста.\n",
		Attachments: []Attachment{{
			Filename: "file.bin",
			Data:     []byte("вложение!"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSMTPEndToEnd(t *testing.T) {
	addr, be, closeFn := startTestSMTP(t, "smtpuser", "smtppass")
	defer closeFn()
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)

	cfg := AccountConfig{
		Email:    "me@example.com",
		SMTPHost: host, SMTPPort: p, SMTPTLS: TLSNone,
		SMTPUser: "smtpuser", SMTPPass: "smtppass",
	}
	cfg.normalize()

	raw, err := BuildMessage(BuildMessageOptions{
		From:    mustAddr(t, "Я <me@example.com>"),
		To:      mustParseAddrs(t, "you@example.com, third@example.com"),
		Cc:      mustParseAddrs(t, "cc@example.com"),
		Subject: "SMTP тест",
		Body:    "проверка отправки\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	recipients := []string{"you@example.com", "third@example.com", "cc@example.com"}
	if err := SendMail(cfg, raw, "me@example.com", recipients); err != nil {
		t.Fatal(err)
	}
	got := be.all()
	if len(got) != 1 {
		t.Fatalf("получено %d писем, want 1", len(got))
	}
	if got[0].From != "me@example.com" {
		t.Errorf("envelope from = %q", got[0].From)
	}
	if len(got[0].To) != 3 {
		t.Errorf("envelope to = %v", got[0].To)
	}
	fm, err := parseRawMessage(got[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Subject != "SMTP тест" {
		t.Errorf("subject = %q", fm.Subject)
	}

	// Неправильный пароль
	bad := cfg
	bad.SMTPPass = "wrong"
	if err := SendMail(bad, raw, "me@example.com", recipients); err == nil {
		t.Errorf("ожидалась ошибка аутентификации")
	}

	// TestSMTPConfig
	if err := TestSMTPConfig(cfg); err != nil {
		t.Errorf("TestSMTPConfig: %v", err)
	}
	bad2 := cfg
	bad2.SMTPPass = "nope"
	if err := TestSMTPConfig(bad2); err == nil {
		t.Errorf("TestSMTPConfig должен был упасть с неверным паролем")
	}
}

func TestHTMLToText(t *testing.T) {
	in := `<html><head><style>body{color:red}</style><script>var x=1;</script></head>
<body><h1>Заголовок</h1><p>Первый параграф со <b>жирным</b> и <a href="https://example.com/page">ссылкой</a>.</p>
<ul><li>пункт 1</li><li>пункт 2</li></ul>
<p>Второй&nbsp;параграф<br>с переносом</p>
<pre>  преформат
   текст</pre>
</body></html>`
	out := HTMLToText(in)
	for _, want := range []string{"Заголовок", "Первый параграф", "жирным", "[https://example.com/page]", "пункт 1", "Второй параграф", "с переносом", "преформат"} {
		if !strings.Contains(out, want) {
			t.Errorf("в тексте нет %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"color:red", "var x=1"} {
		if strings.Contains(out, bad) {
			t.Errorf("в текст попал %q", bad)
		}
	}
}

func TestParseCharsetKOI8R(t *testing.T) {
	raw := "From: =?koi8-r?B?6dfBziDwxdTSz9c=?= <ivan@example.com>\r\n" +
		"To: petr@example.com\r\n" +
		"Subject: =?koi8-r?B?8NLJ18XU?=\r\n" +
		"Date: Mon, 2 Jan 2023 15:04:05 +0000\r\n" +
		"Content-Type: text/plain; charset=koi8-r\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" +
		"\xf0\xd2\xc9\xd7\xc5\xd4!\n"
	fm, err := parseRawMessage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Subject != "Привет" {
		t.Errorf("subject = %q, want Привет", fm.Subject)
	}
	if !strings.Contains(fm.Body, "Привет!") {
		t.Errorf("body = %q", fm.Body)
	}
	if len(fm.From) != 1 || fm.From[0].Name != "Иван Петров" {
		t.Errorf("from = %+v", fm.From)
	}
}

func TestParseHTMLAlternative(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: html test\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=BB\r\n\r\n" +
		"--BB\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain version\r\n" +
		"--BB\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>html version</p>\r\n--BB--\r\n")
	fm, err := parseRawMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fm.Body, "plain version") {
		t.Errorf("должна выбираться text/plain часть, body = %q", fm.Body)
	}
}

func TestReplyAndForwardHelpers(t *testing.T) {
	fm := &FullMessage{
		From:    []*netmail.Address{{Name: "Пётр", Address: "petr@example.com"}},
		To:      []*netmail.Address{{Address: "me@example.com"}},
		Subject: "Вопрос",
		Date:    time.Date(2026, 9, 25, 10, 30, 0, 0, time.UTC),
		Body:    "Строка 1\nСтрока 2\n",
	}
	q := fm.QuoteBody()
	if !strings.Contains(q, "> Строка 1") || !strings.Contains(q, "писал(а):") {
		t.Errorf("quote = %q", q)
	}
	if ReplySubject("Вопрос") != "Re: Вопрос" {
		t.Errorf("ReplySubject")
	}
	if ReplySubject("Re: Вопрос") != "Re: Вопрос" {
		t.Errorf("ReplySubject повторно")
	}
	if ForwardSubject("Вопрос") != "Fwd: Вопрос" {
		t.Errorf("ForwardSubject")
	}
	fwd := fm.ForwardPreamble()
	if !strings.Contains(fwd, "Пересылаемое сообщение") || !strings.Contains(fwd, "Строка 1") {
		t.Errorf("forward = %q", fwd)
	}
}

func TestParseAddressListLenient(t *testing.T) {
	l, err := ParseAddressList("a@example.com, Имя <b@example.com>; c@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != 3 {
		t.Errorf("len = %d, want 3 (%v)", len(l), l)
	}
	if _, err := ParseAddressList("not-an-address"); err == nil {
		t.Errorf("ожидалась ошибка")
	}
	l2, err := ParseAddressList("")
	if err != nil || len(l2) != 0 {
		t.Errorf("пустая строка: %v %v", l2, err)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.json"
	cfg := &Config{AttachmentDir: dir, RefreshSec: 60, FetchLimit: 50}
	cfg.Accounts = []AccountConfig{{
		ID: "x", Name: "Рабочий", Email: "w@example.com",
		IMAPHost: "imap.example.com", IMAPPort: 993, IMAPTLS: TLSImplicit,
		IMAPPass: "secret", SMTPHost: "smtp.example.com", SMTPPort: 465, SMTPTLS: TLSImplicit,
	}}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Accounts) != 1 || loaded.Accounts[0].Email != "w@example.com" || loaded.Accounts[0].IMAPPass != "secret" {
		t.Errorf("конфигурация не сохранилась: %+v", loaded)
	}
	// отсутствующий файл — пустая конфигурация без ошибки
	empty, err := LoadConfig(dir + "/nonexistent.json")
	if err != nil || len(empty.Accounts) != 0 {
		t.Errorf("empty: %v %v", empty, err)
	}
}

func TestSpecialFolderDetection(t *testing.T) {
	acc := NewAccount(AccountConfig{Email: "x@example.com"})
	acc.Folders = []FolderInfo{
		{Name: "INBOX", Special: SpInbox},
		{Name: "Отправленные"},
		{Name: "Корзина"},
		{Name: "Черновики"},
		{Name: "Спам"},
		{Name: "Архив"},
	}
	acc.detectSpecial()
	want := map[string]string{
		SpSent: "Отправленные", SpTrash: "Корзина", SpDrafts: "Черновики",
		SpJunk: "Спам", SpArchive: "Архив",
	}
	for role, name := range want {
		if got := acc.Special[role]; got != name {
			t.Errorf("special[%s] = %q, want %q", role, got, name)
		}
	}
}
