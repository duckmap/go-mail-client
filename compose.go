package main

import (
	"fmt"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// поля формы письма
const (
	cfAcct = iota
	cfTo
	cfCc
	cfSubject
	cfAttach
	cfBody
	cfCount
)

const maxAttachSize = 20 << 20 // 20 МБ на вложение

// composeModel — экран написания письма.
type composeModel struct {
	active  bool
	mode    string // new, reply, replyAll, forward
	acctIdx int

	to      textinput.Model
	cc      textinput.Model
	subject textinput.Model
	attach  textinput.Model
	body    textarea.Model

	preAtts  []Attachment // вложения пересылаемого письма
	focusIdx int

	orig         *FullMessage
	origUID      uint32
	origFolder   string
	origAcct     int
	sending      bool
	errText      string
	confirmLeave bool

	width, height int
}

func newCompose() composeModel {
	to := textinput.New()
	to.Prompt = ""
	to.Placeholder = "адреса через запятую"
	to.CharLimit = 1000
	cc := textinput.New()
	cc.Prompt = ""
	cc.Placeholder = "не обязательно"
	cc.CharLimit = 1000
	subject := textinput.New()
	subject.Prompt = ""
	subject.Placeholder = "тема письма"
	subject.CharLimit = 300
	attach := textinput.New()
	attach.Prompt = ""
	attach.Placeholder = "пути через запятую, напр. ~/docs/отчёт.pdf"
	attach.CharLimit = 2000

	body := textarea.New()
	body.Placeholder = "Текст письма…"
	body.CharLimit = 1 << 20
	body.ShowLineNumbers = false
	body.Prompt = ""
	body.Focus()

	return composeModel{
		to: to, cc: cc, subject: subject, attach: attach, body: body,
		mode: "new", focusIdx: cfTo,
	}
}

func (cm *composeModel) setSize(w, h int) {
	cm.width, cm.height = w, h
	fieldW := max(10, w-16) // ширина содержимого однострочных полей
	cm.to.Width = fieldW
	cm.cc.Width = fieldW
	cm.subject.Width = fieldW
	cm.attach.Width = fieldW
	cm.body.SetWidth(max(20, w-6))
	cm.body.SetHeight(max(3, h-13))
}

// focusField переключает фокус ввода на текущее поле.
func (cm *composeModel) focusField() tea.Cmd {
	cm.to.Blur()
	cm.cc.Blur()
	cm.subject.Blur()
	cm.attach.Blur()
	cm.body.Blur()
	switch cm.focusIdx {
	case cfTo:
		cm.to.Focus()
		return textinput.Blink
	case cfCc:
		cm.cc.Focus()
		return textinput.Blink
	case cfSubject:
		cm.subject.Focus()
		return textinput.Blink
	case cfAttach:
		cm.attach.Focus()
		return textinput.Blink
	case cfBody:
		cm.body.Focus()
		return textarea.Blink
	}
	return nil
}

func (cm *composeModel) focusedInput() *textinput.Model {
	switch cm.focusIdx {
	case cfTo:
		return &cm.to
	case cfCc:
		return &cm.cc
	case cfSubject:
		return &cm.subject
	case cfAttach:
		return &cm.attach
	}
	return nil
}

func (cm *composeModel) dirty() bool {
	return strings.TrimSpace(cm.to.Value()) != "" ||
		strings.TrimSpace(cm.cc.Value()) != "" ||
		strings.TrimSpace(cm.subject.Value()) != "" ||
		strings.TrimSpace(cm.attach.Value()) != "" ||
		strings.TrimSpace(cm.body.Value()) != "" ||
		len(cm.preAtts) > 0
}

// ---------- обработка ввода ----------

func (m model) updateCompose(msg tea.Msg) (tea.Model, tea.Cmd) {
	cm := &m.compose

	if cm.confirmLeave {
		if km, ok := msg.(tea.KeyMsg); ok {
			switch strings.ToLower(km.String()) {
			case "y", "д":
				m.view = viewMain
				cm.active = false
				cm.confirmLeave = false
				m.setStatus("Написание письма отменено")
				return m, nil
			default:
				cm.confirmLeave = false
				return m, cm.focusField()
			}
		}
		return m, nil
	}

	if cm.sending {
		// во время отправки игнорируем ввод
		return m, nil
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		// прочий ввод — в активное поле
		if cm.focusIdx == cfBody {
			var c tea.Cmd
			cm.body, c = cm.body.Update(msg)
			return m, c
		}
		if in := cm.focusedInput(); in != nil {
			var c tea.Cmd
			*in, c = in.Update(msg)
			return m, c
		}
		return m, nil
	}

	switch km.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+s":
		return m.doSend()
	case "ctrl+d":
		return m.doSaveDraft()
	case "esc":
		if cm.dirty() {
			cm.confirmLeave = true
			return m, nil
		}
		m.view = viewMain
		cm.active = false
		return m, nil
	case "tab", "down":
		if cm.focusIdx == cfBody && km.String() == "down" {
			var c tea.Cmd
			cm.body, c = cm.body.Update(msg)
			return m, c
		}
		cm.focusIdx = (cm.focusIdx + 1) % cfCount
		return m, cm.focusField()
	case "shift+tab":
		cm.focusIdx = (cm.focusIdx + cfCount - 1) % cfCount
		return m, cm.focusField()
	case "up":
		if cm.focusIdx == cfBody {
			var c tea.Cmd
			cm.body, c = cm.body.Update(msg)
			return m, c
		}
		if cm.focusIdx > 0 {
			cm.focusIdx--
			return m, cm.focusField()
		}
	case "left":
		if cm.focusIdx == cfAcct {
			if len(m.accounts) > 0 {
				cm.acctIdx = (cm.acctIdx + len(m.accounts) - 1) % len(m.accounts)
			}
			return m, nil
		}
	case "right":
		if cm.focusIdx == cfAcct {
			if len(m.accounts) > 0 {
				cm.acctIdx = (cm.acctIdx + 1) % len(m.accounts)
			}
			return m, nil
		}
	case "enter":
		// enter в однострочных полях — переход к тексту
		if cm.focusIdx != cfBody {
			cm.focusIdx = cfBody
			return m, cm.focusField()
		}
	}

	if cm.focusIdx == cfBody {
		var c tea.Cmd
		cm.body, c = cm.body.Update(msg)
		return m, c
	}
	if in := cm.focusedInput(); in != nil {
		var c tea.Cmd
		*in, c = in.Update(msg)
		return m, c
	}
	return m, nil
}

// ---------- отправка ----------

func (m model) buildOutgoing() (raw []byte, from string, recipients []string, err error) {
	cm := &m.compose
	if cm.acctIdx < 0 || cm.acctIdx >= len(m.accounts) {
		return nil, "", nil, fmt.Errorf("не выбрана учётная запись")
	}
	acct := m.accounts[cm.acctIdx]
	to, err := ParseAddressList(cm.to.Value())
	if err != nil {
		return nil, "", nil, fmt.Errorf("поле «Кому»: %w", err)
	}
	cc, err := ParseAddressList(cm.cc.Value())
	if err != nil {
		return nil, "", nil, fmt.Errorf("поле «Копия»: %w", err)
	}
	if len(to) == 0 && len(cc) == 0 {
		return nil, "", nil, fmt.Errorf("укажите хотя бы одного получателя")
	}
	atts := append([]Attachment{}, cm.preAtts...)
	fileAtts, err := loadAttachments(cm.attach.Value())
	if err != nil {
		return nil, "", nil, err
	}
	atts = append(atts, fileAtts...)

	fromAddr := &mail.Address{Address: acct.Cfg.Email}
	if acct.Cfg.SenderName != "" {
		fromAddr.Name = acct.Cfg.SenderName
	} else if acct.Cfg.Name != "" && !strings.Contains(acct.Cfg.Name, "@") {
		fromAddr.Name = acct.Cfg.Name
	}

	opts := BuildMessageOptions{
		From:        fromAddr,
		To:          to,
		Cc:          cc,
		Subject:     cm.subject.Value(),
		Body:        cm.body.Value(),
		Attachments: atts,
	}
	if cm.mode == "reply" || cm.mode == "replyAll" {
		if cm.orig != nil {
			opts.InReplyTo = cm.orig.MessageID
			opts.References = cm.orig.References
		}
	}
	raw, err = BuildMessage(opts)
	if err != nil {
		return nil, "", nil, err
	}
	return raw, acct.Cfg.Email, AllRecipients(to, cc), nil
}

func (m model) doSend() (tea.Model, tea.Cmd) {
	cm := &m.compose
	raw, from, recipients, err := m.buildOutgoing()
	if err != nil {
		cm.errText = err.Error()
		return m, nil
	}
	cm.errText = ""
	cm.sending = true
	acct := m.accounts[cm.acctIdx]
	m.begin()
	replyUID := uint32(0)
	if cm.mode == "reply" || cm.mode == "replyAll" {
		replyUID = cm.origUID
	}
	return m, tea.Batch(
		cmdSend(cm.acctIdx, acct, raw, from, recipients, replyUID, cm.origFolder),
		m.spin.Tick,
	)
}

func (m model) doSaveDraft() (tea.Model, tea.Cmd) {
	cm := &m.compose
	raw, _, _, err := m.buildOutgoing()
	if err != nil {
		// для черновика получатель не обязателен — ослабляем проверку
		if strings.Contains(err.Error(), "получател") {
			raw, err = m.buildDraftLenient()
		}
		if err != nil {
			cm.errText = err.Error()
			return m, nil
		}
	}
	cm.errText = ""
	cm.sending = true
	acct := m.accounts[cm.acctIdx]
	m.begin()
	return m, tea.Batch(cmdSaveDraft(acct, raw), m.spin.Tick)
}

// buildDraftLenient собирает черновик даже без получателя.
func (m model) buildDraftLenient() ([]byte, error) {
	cm := &m.compose
	acct := m.accounts[cm.acctIdx]
	to, _ := ParseAddressList(cm.to.Value())
	cc, _ := ParseAddressList(cm.cc.Value())
	atts := append([]Attachment{}, cm.preAtts...)
	fileAtts, err := loadAttachments(cm.attach.Value())
	if err != nil {
		return nil, err
	}
	atts = append(atts, fileAtts...)
	fromAddr := &mail.Address{Address: acct.Cfg.Email, Name: acct.Cfg.SenderName}
	return BuildMessage(BuildMessageOptions{
		From: fromAddr, To: to, Cc: cc,
		Subject:     cm.subject.Value(),
		Body:        cm.body.Value(),
		Attachments: atts,
	})
}

// loadAttachments читает файлы по списку путей через запятую.
func loadAttachments(s string) ([]Attachment, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ";", ","))
	if s == "" {
		return nil, nil
	}
	var out []Attachment
	seen := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if strings.HasPrefix(p, "~/") || p == "~" {
			home, err := os.UserHomeDir()
			if err == nil {
				p = filepath.Join(home, strings.TrimPrefix(p, "~"))
			}
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("вложение %s: %w", p, err)
		}
		if st.IsDir() {
			return nil, fmt.Errorf("вложение %s: это каталог", p)
		}
		if st.Size() > maxAttachSize {
			return nil, fmt.Errorf("вложение %s: больше %s", p, humanSize(maxAttachSize))
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("вложение %s: %w", p, err)
		}
		ct := "application/octet-stream"
		if t := mime.TypeByExtension(filepath.Ext(p)); t != "" {
			ct = t
		}
		out = append(out, Attachment{
			Filename:    filepath.Base(p),
			ContentType: ct,
			Size:        int64(len(data)),
			Data:        data,
		})
	}
	return out, nil
}

// ---------- отрисовка ----------

const composeLabelW = 8

func composeRow(label, val string, focused bool) string {
	mark := "  "
	if focused {
		mark = stAccent.Render("❯") + " "
	}
	return " " + mark + stFaint.Render(padRight(label, composeLabelW)) + val
}

func (m model) viewCompose() string {
	cm := &m.compose
	w, h := m.width, m.height

	// сколько строк займут поля и служебные элементы
	extra := 0
	if len(cm.preAtts) > 0 {
		extra++
	}
	if cm.errText != "" || cm.confirmLeave {
		extra++
	}
	bodyH := max(1, h-13-extra)
	cm.body.SetWidth(max(20, w-6))
	cm.body.SetHeight(bodyH)

	rows := make([]string, 0, cfCount+4)

	// От: выбор ящика стрелками
	acctVal := stFaint.Render("—")
	if cm.acctIdx >= 0 && cm.acctIdx < len(m.accounts) {
		a := m.accounts[cm.acctIdx].Cfg
		if cm.focusIdx == cfAcct {
			acctVal = stAcc.Render("◀ ") + stBold.Render(a.Title()) +
				stFaint.Render(" · "+a.Email+" ") + stAcc.Render("▶")
		} else {
			acctVal = stFaint.Render("◀ ") + stText.Render(a.Title()) +
				stFaint.Render(" · "+a.Email+" ") + stFaint.Render("▶")
		}
	}
	rows = append(rows, composeRow("От", acctVal, cm.focusIdx == cfAcct))
	rows = append(rows, composeRow("Кому", cm.to.View(), cm.focusIdx == cfTo))
	rows = append(rows, composeRow("Копия", cm.cc.View(), cm.focusIdx == cfCc))
	rows = append(rows, composeRow("Тема", cm.subject.View(), cm.focusIdx == cfSubject))
	rows = append(rows, composeRow("Файлы", cm.attach.View(), cm.focusIdx == cfAttach))
	if len(cm.preAtts) > 0 {
		var names []string
		for _, a := range cm.preAtts {
			names = append(names, fmt.Sprintf("%s (%s)", a.Filename, humanSize(a.Size)))
		}
		rows = append(rows, " "+stFaint.Render(padRight("", composeLabelW))+
			stWarn.Render("⎿ "+truncate(strings.Join(names, ", "), max(8, w-composeLabelW-8))))
	}

	rows = append(rows, stRule.Render(strings.Repeat("─", max(10, w-4))))

	bodySt := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cRule).
		Padding(0, 1).
		Width(max(20, w-4))
	if cm.focusIdx == cfBody {
		bodySt = bodySt.BorderForeground(cAccent)
	}
	rows = append(rows, bodySt.Render(cm.body.View()))

	if cm.errText != "" {
		rows = append(rows, " "+stError.Render(truncate(cm.errText, w-2)))
	} else if cm.confirmLeave {
		rows = append(rows, " "+stError.Render("Отменить письмо? y — отменить, любая другая клавиша — продолжить"))
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderTopBar(),
		stRule.Render(strings.Repeat("─", w)),
		strings.Join(rows, "\n"),
		m.renderFooter(),
	)
}
