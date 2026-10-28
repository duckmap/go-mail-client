package main

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	gomessage "github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // регистрация кодировок (koi8-r, windows-1251, ...)
	gomail "github.com/emersion/go-message/mail"
)

// Attachment — вложение письма.
type Attachment struct {
	Filename    string
	ContentType string
	Size        int64
	Data        []byte
}

// FullMessage — полностью разобранное письмо.
type FullMessage struct {
	UID        uint32
	From       []*mail.Address
	To         []*mail.Address
	Cc         []*mail.Address
	Subject    string
	Date       time.Time
	MessageID  string
	ReplyTo    []*mail.Address
	References []string
	InReplyTo  []string

	Text string // text/plain части
	HTML string // text/html части
	Body string // итоговый текст для показа

	Attachments []Attachment
	Flags       []string
	Raw         []byte
	ParseNote   string // предупреждения разбора
}

// ограничение на размер одной части (25 МБ).
const maxPartSize = 25 << 20

// parseRawMessage разбирает RFC822-письмо.
func parseRawMessage(raw []byte) (*FullMessage, error) {
	fm := &FullMessage{Raw: raw}
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		// Сообщение не является корректным MIME — пробуем разобрать хотя бы заголовки.
		m, herr := mail.ReadMessage(bytes.NewReader(raw))
		if herr != nil {
			return nil, fmt.Errorf("не удалось разобрать письмо: %w", err)
		}
		fm.Subject = decodeHeader(m.Header.Get("Subject"))
		fm.Date, _ = m.Header.Date()
		fm.MessageID = strings.TrimSpace(m.Header.Get("Message-Id"))
		fm.From, _ = m.Header.AddressList("From")
		fm.To, _ = m.Header.AddressList("To")
		fm.Cc, _ = m.Header.AddressList("Cc")
		body, _ := io.ReadAll(io.LimitReader(m.Body, maxPartSize))
		fm.Text = string(body)
		fm.prepareBody()
		return fm, nil
	}
	defer mr.Close()

	hdr := mr.Header
	fm.From, _ = hdr.AddressList("From")
	fm.To, _ = hdr.AddressList("To")
	fm.Cc, _ = hdr.AddressList("Cc")
	fm.ReplyTo, _ = hdr.AddressList("Reply-To")
	subj, _ := hdr.Subject()
	fm.Subject = subj
	fm.Date, _ = hdr.Date()
	fm.MessageID, _ = hdr.MessageID()
	fm.InReplyTo, _ = hdr.MsgIDList("In-Reply-To")
	fm.References, _ = hdr.MsgIDList("References")

	var textParts, htmlParts []string
	var nAtt int
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if isBenign(err) {
				fm.ParseNote = "часть письма не разобрана: " + err.Error()
				continue
			}
			fm.ParseNote = "ошибка разбора части: " + err.Error()
			break
		}
		switch h := p.Header.(type) {
		case *gomail.InlineHeader:
			ct, _, _ := h.ContentType()
			data, rerr := io.ReadAll(io.LimitReader(p.Body, maxPartSize))
			if rerr != nil && !isBenign(rerr) {
				fm.ParseNote = "ошибка чтения текстовой части: " + rerr.Error()
			}
			switch {
			case strings.EqualFold(ct, "text/html"):
				htmlParts = append(htmlParts, string(data))
			case strings.HasPrefix(strings.ToLower(ct), "text/"):
				textParts = append(textParts, string(data))
			default:
				// inline-часть неизвестного типа — как вложение
				name, _ := inlineFilename(h)
				if name == "" {
					name = fmt.Sprintf("part-%d.bin", nAtt+1)
				}
				fm.Attachments = append(fm.Attachments, Attachment{
					Filename:    name,
					ContentType: ct,
					Size:        int64(len(data)),
					Data:        data,
				})
				nAtt++
			}
		case *gomail.AttachmentHeader:
			name, _ := h.Filename()
			if name == "" {
				name = fmt.Sprintf("attachment-%d", nAtt+1)
			}
			ct, _, _ := h.ContentType()
			data, rerr := io.ReadAll(io.LimitReader(p.Body, maxPartSize))
			if rerr != nil && !isBenign(rerr) {
				fm.ParseNote = "ошибка чтения вложения: " + rerr.Error()
			}
			fm.Attachments = append(fm.Attachments, Attachment{
				Filename:    name,
				ContentType: ct,
				Size:        int64(len(data)),
				Data:        data,
			})
			nAtt++
		}
	}
	fm.Text = strings.Join(textParts, "\n")
	fm.HTML = strings.Join(htmlParts, "\n")
	fm.prepareBody()
	return fm, nil
}

// isBenign — ошибки кодировок, которые не мешают показать письмо.
func isBenign(err error) bool {
	return gomessage.IsUnknownEncoding(err) || gomessage.IsUnknownCharset(err)
}

func inlineFilename(h *gomail.InlineHeader) (string, error) {
	_, params, _ := h.ContentType()
	if n, ok := params["name"]; ok && n != "" {
		return n, nil
	}
	_, dparams, err := h.ContentDisposition()
	if err == nil {
		if n, ok := dparams["filename"]; ok && n != "" {
			return n, nil
		}
	}
	return "", nil
}

// prepareBody выбирает текст для показа: text/plain, иначе HTML→текст.
func (fm *FullMessage) prepareBody() {
	switch {
	case strings.TrimSpace(fm.Text) != "":
		fm.Body = normalizeNewlines(fm.Text)
	case strings.TrimSpace(fm.HTML) != "":
		fm.Body = HTMLToText(fm.HTML)
	default:
		fm.Body = "[Письмо не содержит текста]"
	}
	fm.Body = strings.TrimRight(fm.Body, "\n")
}

// normalizeNewlines приводит \r\n к \n.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// decodeHeader декодирует RFC2047-заголовок.
func decodeHeader(s string) string {
	dec := new(mime.WordDecoder)
	out, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

// ---------- сборка исходящего письма ----------

// BuildMessageOptions — параметры собираемого письма.
type BuildMessageOptions struct {
	From        *mail.Address
	To          []*mail.Address
	Cc          []*mail.Address
	Subject     string
	Body        string
	Date        time.Time // нулевая — использовать текущее время
	Attachments []Attachment
	InReplyTo   string   // Message-ID исходного письма
	References  []string // цепочка Message-ID
}

// BuildMessage собирает RFC822/MIME-письмо.
func BuildMessage(opts BuildMessageOptions) ([]byte, error) {
	if opts.From == nil {
		return nil, fmt.Errorf("не указан отправитель")
	}
	if len(opts.To) == 0 && len(opts.Cc) == 0 {
		return nil, fmt.Errorf("не указан ни один получатель")
	}
	var h gomail.Header
	h.SetAddressList("From", []*mail.Address{opts.From})
	if len(opts.To) > 0 {
		h.SetAddressList("To", opts.To)
	}
	if len(opts.Cc) > 0 {
		h.SetAddressList("Cc", opts.Cc)
	}
	h.SetSubject(opts.Subject)
	if opts.Date.IsZero() {
		h.SetDate(time.Now())
	} else {
		h.SetDate(opts.Date)
	}
	if err := h.GenerateMessageID(); err != nil {
		h.SetMessageID(fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), newID(), domainOf(opts.From.Address)))
	}
	if opts.InReplyTo != "" {
		h.SetMsgIDList("In-Reply-To", []string{stripMsgID(opts.InReplyTo)})
		refs := append(append([]string{}, opts.References...), opts.InReplyTo)
		h.SetMsgIDList("References", stripMsgIDs(dedupeStrings(refs)))
	} else if len(opts.References) > 0 {
		h.SetMsgIDList("References", stripMsgIDs(dedupeStrings(opts.References)))
	}
	h.Set("User-Agent", "gomail/1.0")

	var buf bytes.Buffer
	w, err := gomail.CreateWriter(&buf, h)
	if err != nil {
		return nil, err
	}
	var ih gomail.InlineHeader
	ih.Set("Content-Type", "text/plain; charset=utf-8")
	tw, err := w.CreateSingleInline(ih)
	if err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(normalizeNewlines(opts.Body))); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	for _, att := range opts.Attachments {
		var ah gomail.AttachmentHeader
		ah.SetFilename(att.Filename)
		if att.ContentType != "" {
			ah.Set("Content-Type", att.ContentType)
		}
		aw, err := w.CreateAttachment(ah)
		if err != nil {
			return nil, err
		}
		if _, err := aw.Write(att.Data); err != nil {
			return nil, err
		}
		if err := aw.Close(); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func domainOf(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		return email[i+1:]
	}
	return "localhost"
}

// stripMsgID убирает угловые скобки вокруг Message-ID:
// go-message добавляет их сам при записи заголовка.
func stripMsgID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	return s
}

func stripMsgIDs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = stripMsgID(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---------- вспомогательное для ответов ----------

// imapAddrToNetMail преобразует IMAP-адрес в net/mail-адрес.
func imapAddrToNetMail(a *imap.Address) *mail.Address {
	return &mail.Address{Name: a.PersonalName, Address: a.Address()}
}

// ReplyAddress выбирает адрес для ответа (учитывая Reply-To).
func (fm *FullMessage) ReplyAddress() *mail.Address {
	if len(fm.ReplyTo) > 0 && fm.ReplyTo[0].Address != "" {
		return fm.ReplyTo[0]
	}
	if len(fm.From) > 0 {
		return fm.From[0]
	}
	return nil
}

// QuoteBody формирует цитату исходного письма для ответа.
func (fm *FullMessage) QuoteBody() string {
	who := "Корреспондент"
	if a := fm.ReplyAddress(); a != nil {
		who = AddrDisplay(a)
	}
	when := fm.Date.Local().Format("02.01.2006 15:04")
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s, %s писал(а):\n", when, who)
	body := fm.Body
	if body == "" {
		body = fm.Text
	}
	for _, line := range strings.Split(normalizeNewlines(body), "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			sb.WriteString(">\n")
		} else {
			sb.WriteString("> " + line + "\n")
		}
	}
	return sb.String()
}

// ForwardPreamble формирует шапку пересылаемого сообщения.
func (fm *FullMessage) ForwardPreamble() string {
	var sb strings.Builder
	sb.WriteString("\n-------- Пересылаемое сообщение --------\n")
	sb.WriteString("От: " + formatAddrList(fm.From) + "\n")
	sb.WriteString("Кому: " + formatAddrList(fm.To) + "\n")
	if len(fm.Cc) > 0 {
		sb.WriteString("Копия: " + formatAddrList(fm.Cc) + "\n")
	}
	if !fm.Date.IsZero() {
		sb.WriteString("Дата: " + fm.Date.Local().Format("02.01.2006 15:04") + "\n")
	}
	sb.WriteString("Тема: " + fm.Subject + "\n\n")
	sb.WriteString(normalizeNewlines(fm.Body))
	sb.WriteString("\n")
	return sb.String()
}

func formatAddrList(addrs []*mail.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, AddrDisplay(a))
	}
	return strings.Join(parts, ", ")
}

// AddrDisplay форматирует адрес для показа в терминале: имя остаётся в
// юникоде (net/mail.Address.String() перекодирует его в RFC2047 — нам это
// не нужно).
func AddrDisplay(a *mail.Address) string {
	if a == nil {
		return ""
	}
	name := strings.TrimSpace(a.Name)
	if name != "" {
		return name + " <" + a.Address + ">"
	}
	return a.Address
}

// ReplySubject / ForwardSubject — темы с префиксами.
func ReplySubject(subj string) string {
	s := strings.TrimSpace(subj)
	if s == "" {
		return "Re: (без темы)"
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "re:") || strings.HasPrefix(low, "от:") {
		return s
	}
	return "Re: " + s
}

func ForwardSubject(subj string) string {
	s := strings.TrimSpace(subj)
	if s == "" {
		return "Fwd: (без темы)"
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "fwd:") || strings.HasPrefix(low, "fw:") || strings.HasPrefix(low, "пер:") {
		return s
	}
	return "Fwd: " + s
}

// ParseAddressList разбирает строку со списком адресов ("a@b.c, Имя <d@e.f>").
func ParseAddressList(s string) ([]*mail.Address, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	s = strings.ReplaceAll(s, ";", ",")
	addrs, err := mail.ParseAddressList(s)
	if err != nil {
		// пробуем по одному: возможно, имена не в кавычках
		var out []*mail.Address
		for _, part := range strings.Split(s, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			a, err2 := mail.ParseAddress(part)
			if err2 != nil {
				// возможно, это голый адрес
				if strings.Contains(part, "@") && !strings.ContainsAny(part, "<> ") {
					out = append(out, &mail.Address{Address: part})
					continue
				}
				return nil, fmt.Errorf("некорректный адрес «%s»", part)
			}
			out = append(out, a)
		}
		if len(out) == 0 {
			return nil, err
		}
		return out, nil
	}
	return addrs, nil
}

// Recipients — все получатели (To+Cc) для SMTP-конверта.
func AllRecipients(to, cc []*mail.Address) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]*mail.Address{to, cc} {
		for _, a := range list {
			addr := strings.ToLower(strings.TrimSpace(a.Address))
			if addr != "" && !seen[addr] {
				seen[addr] = true
				out = append(out, a.Address)
			}
		}
	}
	return out
}
