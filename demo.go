package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	netmail "net/mail"
	"strconv"
	"time"

	imapbackend "github.com/emersion/go-imap/backend/memory"
	imapserver "github.com/emersion/go-imap/server"
	"github.com/emersion/go-sasl"
	smtpserver "github.com/emersion/go-smtp"
)

// DemoMsg — письмо для демо-ящика.
type DemoMsg struct {
	Mbox  string
	Flags []string
	Raw   []byte
	Date  time.Time
}

// DemoSeedMessages — содержимое демонстрационного ящика.
func DemoSeedMessages(now time.Time) []DemoMsg {
	post := func(mbox string, flags []string, when time.Time, from, fromName, subj, body string, atts []Attachment) DemoMsg {
		raw, err := BuildMessage(BuildMessageOptions{
			From:        &netmail.Address{Name: fromName, Address: from},
			To:          []*netmail.Address{{Address: "ivan@company.ru"}},
			Subject:     subj,
			Body:        body,
			Attachments: atts,
			Date:        when,
		})
		if err != nil {
			panic(err)
		}
		return DemoMsg{mbox, flags, raw, when}
	}
	return []DemoMsg{
		post("INBOX", []string{"\\Seen"}, now.Add(-9*24*time.Hour),
			"booking@hotel-sochi.ru", "Отель «Морской бриз»", "Подтверждение брони №8841",
			"Ваша бронь подтверждена.\nЗаезд: 10.10.2026, выезд: 17.10.2026.\nСтандартный двухместный номер, 7 ночей.\n", nil),
		post("INBOX", []string{"\\Seen"}, now.Add(-2*24*time.Hour),
			"noreply@github.com", "GitHub", "[gomail] New pull request #42",
			"emersion opened a pull request in your repository.\n\n+342 -12, 8 files changed.\n", nil),
		post("INBOX", []string{"\\Seen", "\\Answered"}, now.Add(-26*time.Hour),
			"sergey@mail.ru", "Сергей Волков", "Re: Встреча в пятницу",
			"Да, 15:00 меня устраивает. Забронирую переговорку на 4 этаже.\n\n> Встретимся в пятницу в 15:00?\n", nil),
		post("INBOX", []string{"\\Flagged"}, now.Add(-3*time.Hour),
			"anna@company.ru", "Анна Смирнова", "Отчёт за Q3 — финальная версия",
			"Иван, добрый день!\n\nВо вложении финальная версия отчёта за третий квартал.\n"+
				"Прошу посмотреть раздел 2 — там расхождения с прошлым драфтом.\n\n--\nС уважением, Анна\nОтдел аналитики",
			[]Attachment{
				{Filename: "отчёт_Q3.xlsx", ContentType: "application/vnd.ms-excel", Data: demoXLSX()},
				{Filename: "сводка.pdf", ContentType: "application/pdf", Data: demoPDF()},
			}),
		{Mbox: "INBOX", Date: now.Add(-35 * time.Minute), Raw: DemoHTMLLetter(
			"Уведомление: начислен налог",
			"<html><body><h2>Уважаемый Иван Петрович!</h2><p>Вам начислен <b>транспортный налог</b> за 2025 год.</p>"+
				"<p>Сумма: 4 270 ₽. Срок уплаты — до 1 декабря.</p>"+
				"<p><a href=\"https://gosuslugi.ru/taxes\">Оплатить на Госуслугах</a></p></body></html>",
			"Уважаемый Иван Петрович! Вам начислен транспортный налог за 2025 год.",
			now.Add(-35*time.Minute))},
		post("Sent", []string{"\\Seen"}, now.Add(-27*time.Hour),
			"sergey@mail.ru", "Иван Петров", "Re: Встреча в пятницу",
			"Встретимся в пятницу в 15:00?\n", nil),
		post("Drafts", []string{"\\Draft", "\\Seen"}, now.Add(-5*time.Hour),
			"boss@company.ru", "Иван Петров", "Заявление на отпуск",
			"Уважаемый Пётр Иванович!\n\nПрошу предоставить мне очередной оплачиваемый отпуск\nс 10 по 24 октября 2026 года.\n", nil),
		post("Work/Projects", []string{"\\Seen"}, now.Add(-30*time.Hour),
			"pm@company.ru", "Дмитрий Орлов", "Статус проекта «Горизонт»",
			"Коллеги, напоминаю про еженедельный статус до четверга.\n", nil),
	}
}

// DemoHTMLLetter — multipart/alternative письмо с HTML-версией.
func DemoHTMLLetter(subject, html, plain string, date time.Time) []byte {
	boundary := "DEMOMIME123"
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: =?utf-8?B?%s?= <noreply@gosuslugi.ru>\r\n", base64.StdEncoding.EncodeToString([]byte("Госуслуги")))
	fmt.Fprintf(&b, "To: ivan@company.ru\r\nSubject: =?utf-8?B?%s?=\r\n", base64.StdEncoding.EncodeToString([]byte(subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", date.Format(time.RFC1123Z))
	b.WriteString("Message-ID: <demo-gos-1@gosuslugi.ru>\r\nMIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, plain)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n", boundary, html)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func demoXLSX() []byte {
	return []byte("demo-xlsx: электронная таблица (демо-вложение)")
}
func demoPDF() []byte { return []byte("demo-pdf: %PDF-1.4 (демо-вложение)") }

// ---------- встроенные демо-серверы ----------

// demoSMTPBackend принимает письма и кладёт их во «Входящие» демо-ящика.
type demoSMTPBackend struct {
	user *imapbackend.User
}

type demoSMTPSession struct {
	be   *demoSMTPBackend
	from string
	to   []string
}

func (be *demoSMTPBackend) NewSession(c *smtpserver.Conn) (smtpserver.Session, error) {
	return &demoSMTPSession{be: be}, nil
}

func (s *demoSMTPSession) AuthMechanisms() []string { return []string{"PLAIN"} }

func (s *demoSMTPSession) Auth(mech string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(identity, username, password string) error {
		if username == "demo" && password == "demo" {
			return nil
		}
		return errors.New("bad credentials")
	}), nil
}

func (s *demoSMTPSession) Mail(from string, opts *smtpserver.MailOptions) error {
	s.from = from
	s.to = nil
	return nil
}

func (s *demoSMTPSession) Rcpt(to string, opts *smtpserver.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *demoSMTPSession) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	// доставляем письмо во «Входящие» демо-ящика
	mb, err := s.be.user.GetMailbox("INBOX")
	if err != nil {
		return err
	}
	return mb.(*imapbackend.Mailbox).CreateMessage(nil, time.Now(), bytes.NewReader(data))
}

func (s *demoSMTPSession) Reset()        {}
func (s *demoSMTPSession) Logout() error { return nil }

// StartDemoServers поднимает локальные IMAP- и SMTP-серверы с демо-почтой.
// Возвращает конфигурацию учётной записи и функцию остановки.
func StartDemoServers() (AccountConfig, func(), error) {
	be := imapbackend.New()
	imapSrv := imapserver.New(be)
	imapSrv.AllowInsecureAuth = true
	imapSrv.ErrorLog = log.New(io.Discard, "", 0)
	imapLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return AccountConfig{}, nil, err
	}
	go imapSrv.Serve(imapLn)

	// демо-пользователь: demo/demo
	bu, err := be.Login(nil, "username", "password")
	if err != nil {
		return AccountConfig{}, nil, err
	}
	user := bu.(*imapbackend.User)
	for _, mb := range []string{"Sent", "Trash", "Drafts", "Junk", "Archive", "Work/Projects", "Work/Invoices"} {
		if err := user.CreateMailbox(mb); err != nil {
			return AccountConfig{}, nil, err
		}
	}
	// очистить стандартное seed-письмо
	if mb, err := user.GetMailbox("INBOX"); err == nil {
		mb.(*imapbackend.Mailbox).Messages = nil
	}
	now := time.Now()
	for _, x := range DemoSeedMessages(now) {
		mb, err := user.GetMailbox(x.Mbox)
		if err != nil {
			return AccountConfig{}, nil, err
		}
		if err := mb.(*imapbackend.Mailbox).CreateMessage(x.Flags, x.Date, bytes.NewReader(x.Raw)); err != nil {
			return AccountConfig{}, nil, err
		}
	}

	smtpBe := &demoSMTPBackend{user: user}
	smtpSrv := smtpserver.NewServer(smtpBe)
	smtpSrv.Domain = "localhost"
	smtpSrv.AllowInsecureAuth = true
	smtpSrv.ErrorLog = log.New(io.Discard, "", 0)
	smtpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return AccountConfig{}, nil, err
	}
	go smtpSrv.Serve(smtpLn)

	_, iPort, _ := net.SplitHostPort(imapLn.Addr().String())
	_, sPort, _ := net.SplitHostPort(smtpLn.Addr().String())
	ip, _ := strconv.Atoi(iPort)
	sp, _ := strconv.Atoi(sPort)

	cfg := AccountConfig{
		ID: "demo", Name: "Демо-ящик", Email: "ivan@company.ru", SenderName: "Иван Петров",
		IMAPHost: "127.0.0.1", IMAPPort: ip, IMAPTLS: TLSNone, IMAPUser: "username", IMAPPass: "password",
		SMTPHost: "127.0.0.1", SMTPPort: sp, SMTPTLS: TLSNone, SMTPUser: "demo", SMTPPass: "demo",
	}
	cfg.normalize()
	shutdown := func() {
		imapSrv.Close()
		imapLn.Close()
		smtpSrv.Close()
		smtpLn.Close()
	}
	return cfg, shutdown, nil
}
