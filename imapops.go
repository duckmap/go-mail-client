package main

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/emersion/go-imap"
)

// MsgMeta — краткие сведения о письме для списка.
type MsgMeta struct {
	UID      uint32
	SeqNum   uint32
	Date     time.Time
	Subject  string
	From     string // "Имя <addr>" для отображения
	FromAddr string
	To       string
	Size     uint32
	Seen     bool
	Flagged  bool
	Answered bool
	Draft    bool
	Deleted  bool
}

// ListMessages выбирает папку и загружает последние limit писем
// (сортировка — от новых к старым). Возвращает также общее число писем
// и число непрочитанных.
func (a *Account) ListMessages(folder string, limit int) ([]MsgMeta, uint32, uint32, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return nil, 0, 0, err
	}
	status, err := c.Select(folder, false)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("не удалось открыть «%s»: %w", folder, err)
	}
	if status.Messages == 0 {
		return nil, 0, status.Unseen, nil
	}
	if limit <= 0 {
		limit = 300
	}
	seqset := new(imap.SeqSet)
	from := uint32(1)
	if status.Messages > uint32(limit) {
		from = status.Messages - uint32(limit) + 1
	}
	seqset.AddRange(from, status.Messages)

	items := []imap.FetchItem{
		imap.FetchUid,
		imap.FetchFlags,
		imap.FetchEnvelope,
		imap.FetchInternalDate,
		imap.FetchRFC822Size,
	}
	msgCh := make(chan *imap.Message, 64)
	errCh := make(chan error, 1)
	// seqset содержит порядковые номера (не UID) — используем Fetch.
	go func() { errCh <- c.Fetch(seqset, items, msgCh) }()

	var out []MsgMeta
	for msg := range msgCh {
		out = append(out, metaFromIMAP(msg))
	}
	if err := <-errCh; err != nil {
		return nil, 0, 0, err
	}
	unseen := status.Unseen
	if unseen == 0 {
		// сервер не сообщил UNSEEN в ответе SELECT — считаем по загруженным флагам
		for i := range out {
			if !out[i].Seen {
				unseen++
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SeqNum > out[j].SeqNum })
	return out, status.Messages, unseen, nil
}

// FetchMessagesByUID загружает метаданные конкретных писем по UID.
func (a *Account) FetchMessagesByUID(folder string, uids []uint32) ([]MsgMeta, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return nil, err
	}
	if _, err := c.Select(folder, false); err != nil {
		return nil, fmt.Errorf("не удалось открыть «%s»: %w", folder, err)
	}
	items := []imap.FetchItem{
		imap.FetchUid,
		imap.FetchFlags,
		imap.FetchEnvelope,
		imap.FetchInternalDate,
		imap.FetchRFC822Size,
	}
	var out []MsgMeta
	// UID-запросы батчами, чтобы не строить гигантские команды.
	const batch = 50
	for i := 0; i < len(uids); i += batch {
		end := i + batch
		if end > len(uids) {
			end = len(uids)
		}
		seqset := new(imap.SeqSet)
		for _, u := range uids[i:end] {
			seqset.AddNum(u)
		}
		msgCh := make(chan *imap.Message, batch)
		errCh := make(chan error, 1)
		go func() { errCh <- c.UidFetch(seqset, items, msgCh) }()
		for msg := range msgCh {
			out = append(out, metaFromIMAP(msg))
		}
		if err := <-errCh; err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SeqNum > out[j].SeqNum })
	return out, nil
}

// metaFromIMAP преобразует ответ сервера в MsgMeta.
func metaFromIMAP(msg *imap.Message) MsgMeta {
	m := MsgMeta{UID: msg.Uid, SeqNum: msg.SeqNum, Size: msg.Size, Date: msg.InternalDate}
	for _, f := range msg.Flags {
		switch f {
		case imap.SeenFlag:
			m.Seen = true
		case imap.FlaggedFlag:
			m.Flagged = true
		case imap.AnsweredFlag:
			m.Answered = true
		case imap.DraftFlag:
			m.Draft = true
		case imap.DeletedFlag:
			m.Deleted = true
		}
	}
	env := msg.Envelope
	if env != nil {
		m.Subject = env.Subject
		if m.Subject == "" {
			m.Subject = "(без темы)"
		}
		if !env.Date.IsZero() {
			m.Date = env.Date
		}
		if len(env.From) > 0 {
			m.From = formatIMAPAddress(env.From[0])
			m.FromAddr = env.From[0].Address()
		} else if len(env.Sender) > 0 {
			m.From = formatIMAPAddress(env.Sender[0])
		}
		if m.From == "" {
			m.From = "(отправитель неизвестен)"
		}
		if len(env.To) > 0 {
			parts := make([]string, 0, len(env.To))
			for _, ad := range env.To {
				parts = append(parts, formatIMAPAddress(ad))
			}
			m.To = joinLimited(parts, 3)
		}
	}
	if m.Date.IsZero() {
		m.Date = time.Now()
	}
	return m
}

// formatIMAPAddress — "Имя <адрес>" из IMAP-адреса.
func formatIMAPAddress(a *imap.Address) string {
	addr := a.PersonalName
	email := a.Address()
	switch {
	case addr != "" && email != "":
		return fmt.Sprintf("%s <%s>", addr, email)
	case email != "":
		return email
	case addr != "":
		return addr
	}
	return ""
}

func joinLimited(parts []string, n int) string {
	if len(parts) <= n {
		return joinParts(parts)
	}
	return joinParts(parts[:n]) + fmt.Sprintf(" и ещё %d", len(parts)-n)
}

func joinParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// FetchMessage загружает полное письмо по UID (с пометкой прочитанным
// мы не занимаемся — используется BODY.PEEK[]).
func (a *Account) FetchMessage(folder string, uid uint32) (*FullMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return nil, err
	}
	if _, err := c.Select(folder, false); err != nil {
		return nil, fmt.Errorf("не удалось открыть «%s»: %w", folder, err)
	}
	seqset := new(imap.SeqSet)
	seqset.AddNum(uid)
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{
		section.FetchItem(),
		imap.FetchEnvelope,
		imap.FetchFlags,
		imap.FetchInternalDate,
	}
	msgCh := make(chan *imap.Message, 2)
	errCh := make(chan error, 1)
	go func() { errCh <- c.UidFetch(seqset, items, msgCh) }()

	var (
		raw      []byte
		envelope *imap.Envelope
		flags    []string
	)
	for msg := range msgCh {
		if r := msg.GetBody(&imap.BodySectionName{}); r != nil {
			raw, err = io.ReadAll(io.LimitReader(r, maxRawSize))
			if err != nil {
				return nil, fmt.Errorf("чтение тела письма: %w", err)
			}
		}
		envelope = msg.Envelope
		flags = msg.Flags
	}
	if err := <-errCh; err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("письмо UID %d не найдено в «%s»", uid, folder)
	}
	fm, perr := parseRawMessage(raw)
	if perr != nil && fm == nil {
		return nil, perr
	}
	if fm == nil {
		fm = &FullMessage{Raw: raw}
	}
	fm.UID = uid
	fm.Flags = flags
	// Envelope надёжнее заголовков (сервер уже всё раскодировал).
	if envelope != nil {
		if fm.Subject == "" {
			fm.Subject = envelope.Subject
		}
		if fm.Date.IsZero() {
			fm.Date = envelope.Date
		}
		if len(fm.From) == 0 {
			for _, ad := range envelope.From {
				fm.From = append(fm.From, imapAddrToNetMail(ad))
			}
		}
		if len(fm.To) == 0 {
			for _, ad := range envelope.To {
				fm.To = append(fm.To, imapAddrToNetMail(ad))
			}
		}
		if len(fm.Cc) == 0 {
			for _, ad := range envelope.Cc {
				fm.Cc = append(fm.Cc, imapAddrToNetMail(ad))
			}
		}
		if fm.MessageID == "" {
			fm.MessageID = envelope.MessageId
		}
	}
	fm.prepareBody()
	return fm, perr
}

// максимальный размер письма, который мы готовы прочитать (30 МБ).
const maxRawSize = 30 << 20

// StoreFlags добавляет/снимает флаги у писем (silent).
func (a *Account) StoreFlags(folder string, uids []uint32, add, remove []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return err
	}
	if _, err := c.Select(folder, false); err != nil {
		return err
	}
	seqset := new(imap.SeqSet)
	for _, u := range uids {
		seqset.AddNum(u)
	}
	if len(add) > 0 {
		vals := make([]interface{}, 0, len(add))
		for _, f := range add {
			vals = append(vals, f)
		}
		item := imap.FormatFlagsOp(imap.AddFlags, true)
		if err := c.UidStore(seqset, item, vals, nil); err != nil {
			return err
		}
	}
	if len(remove) > 0 {
		vals := make([]interface{}, 0, len(remove))
		for _, f := range remove {
			vals = append(vals, f)
		}
		item := imap.FormatFlagsOp(imap.RemoveFlags, true)
		if err := c.UidStore(seqset, item, vals, nil); err != nil {
			return err
		}
	}
	return nil
}

// MarkAllRead помечает все письма в папке прочитанными.
func (a *Account) MarkAllRead(folder string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return err
	}
	status, err := c.Select(folder, false)
	if err != nil {
		return err
	}
	if status.Messages == 0 {
		return nil
	}
	seqset := new(imap.SeqSet)
	seqset.AddRange(1, status.Messages)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	return c.Store(seqset, item, []interface{}{imap.SeenFlag}, nil)
}

// DeleteMessages удаляет письма безвозвратно (\Deleted + EXPUNGE).
func (a *Account) DeleteMessages(folder string, uids []uint32) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return err
	}
	if _, err := c.Select(folder, false); err != nil {
		return err
	}
	seqset := new(imap.SeqSet)
	for _, u := range uids {
		seqset.AddNum(u)
	}
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := c.UidStore(seqset, item, []interface{}{imap.DeletedFlag}, nil); err != nil {
		return err
	}
	return c.Expunge(nil)
}

// MoveMessages перемещает письма в другую папку.
// Использует MOVE (RFC 6851), а при его отсутствии/отказе — COPY + DELETE + EXPUNGE.
func (a *Account) MoveMessages(folder, dest string, uids []uint32) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return err
	}
	if _, err := c.Select(folder, false); err != nil {
		return err
	}
	seqset := new(imap.SeqSet)
	for _, u := range uids {
		seqset.AddNum(u)
	}
	if ok, _ := c.Support("MOVE"); ok {
		if err := c.UidMove(seqset, dest); err == nil {
			return nil
		}
		// сервер мог анонсировать MOVE, но отказать — падаем на COPY+EXPUNGE
	}
	if err := c.UidCopy(seqset, dest); err != nil {
		return fmt.Errorf("копирование в «%s»: %w", dest, err)
	}
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := c.UidStore(seqset, item, []interface{}{imap.DeletedFlag}, nil); err != nil {
		return err
	}
	return c.Expunge(nil)
}

// CopyMessages копирует письма в другую папку (оригиналы остаются).
func (a *Account) CopyMessages(folder, dest string, uids []uint32) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return err
	}
	if _, err := c.Select(folder, false); err != nil {
		return err
	}
	seqset := new(imap.SeqSet)
	for _, u := range uids {
		seqset.AddNum(u)
	}
	if err := c.UidCopy(seqset, dest); err != nil {
		return fmt.Errorf("копирование в «%s»: %w", dest, err)
	}
	return nil
}

// Search ищет письма в папке по тексту (заголовки + тело).
func (a *Account) Search(folder, text string) ([]uint32, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return nil, err
	}
	if _, err := c.Select(folder, true); err != nil {
		return nil, err
	}
	criteria := imap.NewSearchCriteria()
	criteria.Text = []string{text}
	return c.UidSearch(criteria)
}

// AppendMessage добавляет готовое RFC822-письмо в папку (например, в
// «Отправленные» или «Черновики»).
func (a *Account) AppendMessage(folder string, raw []byte, flags []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return err
	}
	if flags == nil {
		flags = []string{imap.SeenFlag}
	}
	return c.Append(folder, flags, time.Now(), bytes.NewReader(raw))
}

// FolderExists проверяет наличие папки в кэше; при отсутствии пробует создать.
func (a *Account) EnsureFolder(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return err
	}
	for _, f := range a.Folders {
		if f.Name == name {
			return nil
		}
	}
	if err := c.Create(name); err != nil {
		return err
	}
	return a.loadFoldersLocked()
}

// Noop — пинг сервера (обновление состояния).
func (a *Account) Noop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.ensureConnLocked()
	if err != nil {
		return err
	}
	return c.Noop()
}
