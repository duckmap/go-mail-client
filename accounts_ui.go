package main

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// типы полей формы
const (
	fText = iota
	fPass
	fPort
	fTLS
	fBool
)

type formField struct {
	label   string
	kind    int
	input   textinput.Model
	enumIdx int
	enums   []string
}

func (f *formField) value() string {
	switch f.kind {
	case fTLS, fBool:
		if f.enumIdx >= 0 && f.enumIdx < len(f.enums) {
			return f.enums[f.enumIdx]
		}
		return ""
	}
	return f.input.Value()
}

// accountsUI — экран управления учётными записями.
type accountsUI struct {
	items  []AccountConfig
	origin []AccountConfig // копия для сравнения «грязная/чистая»
	cursor int             // 0..len(items); len(items) — строка «+ Добавить»

	editing      bool
	fields       []formField
	fieldCur     int
	scroll       int
	confirmDel   bool
	confirmLeave bool

	testResult string
	testBusy   bool
	saveMsg    string

	width, height int
}

var accountFormDef = []struct {
	label string
	kind  int
}{
	{"Название ящика", fText},
	{"Имя отправителя", fText},
	{"E-mail", fText},
	{"IMAP-сервер", fText},
	{"IMAP-порт", fPort},
	{"IMAP-шифрование", fTLS},
	{"IMAP-логин", fText},
	{"IMAP-пароль", fPass},
	{"Не проверять сертификат IMAP", fBool},
	{"SMTP-сервер", fText},
	{"SMTP-порт", fPort},
	{"SMTP-шифрование", fTLS},
	{"SMTP-логин", fText},
	{"SMTP-пароль", fPass},
	{"Не проверять сертификат SMTP", fBool},
	{"Папка «Отправленные»", fText},
	{"Папка «Корзина»", fText},
	{"Папка «Черновики»", fText},
	{"Папка «Спам»", fText},
}

var tlsEnums = []string{"TLS", "STARTTLS", "нет"}
var boolEnums = []string{"нет", "да"}

func tlsToEnum(t TLSMode) int {
	switch t {
	case TLSImplicit:
		return 0
	case TLSStart:
		return 1
	default:
		return 2
	}
}

func enumToTLS(i int) TLSMode {
	switch i {
	case 0:
		return TLSImplicit
	case 1:
		return TLSStart
	default:
		return TLSNone
	}
}

// newAccountsUI создаёт UI с копией переданных учёток.
func newAccountsUI(accs []AccountConfig) accountsUI {
	ui := accountsUI{}
	ui.items = make([]AccountConfig, len(accs))
	copy(ui.items, accs)
	ui.origin = make([]AccountConfig, len(accs))
	copy(ui.origin, accs)
	ui.width, ui.height = 100, 30
	if len(ui.items) > 0 {
		ui.buildFields(0)
	} else {
		ui.fields = ui.emptyFields()
	}
	return ui
}

func (ui *accountsUI) setSize(w, h int) {
	ui.width, ui.height = w, h
	fw := ui.formInputWidth()
	for i := range ui.fields {
		ui.fields[i].input.Width = fw
	}
}

func (ui *accountsUI) formInputWidth() int {
	formW := max(30, ui.width-36)
	return max(10, formW-36) // метка 28 + отступы
}

// enter — вызывается при открытии экрана: синхронизация с конфигом.
func (ui *accountsUI) enter(accs []AccountConfig) {
	ui.items = make([]AccountConfig, len(accs))
	copy(ui.items, accs)
	ui.origin = make([]AccountConfig, len(accs))
	copy(ui.origin, accs)
	ui.cursor = 0
	ui.editing = false
	ui.confirmDel = false
	ui.confirmLeave = false
	ui.testResult = ""
	ui.saveMsg = ""
	ui.fieldCur = 0
	ui.scroll = 0
	if len(ui.items) > 0 {
		ui.buildFields(0)
	} else {
		ui.fields = ui.emptyFields()
	}
}

func (ui *accountsUI) emptyFields() []formField {
	var cfg AccountConfig
	cfg.normalize()
	return ui.makeFields(cfg)
}

func (ui *accountsUI) makeFields(cfg AccountConfig) []formField {
	fw := ui.formInputWidth()
	fields := make([]formField, len(accountFormDef))
	for i, def := range accountFormDef {
		f := formField{label: def.label, kind: def.kind}
		ti := textinput.New()
		ti.Width = fw
		ti.CharLimit = 300
		ti.Prompt = ""
		if def.kind == fPass {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		if def.kind == fPort {
			ti.CharLimit = 5
			ti.Validate = func(s string) error {
				if s == "" {
					return nil
				}
				_, err := strconv.Atoi(s)
				return err
			}
		}
		f.input = ti
		switch def.kind {
		case fTLS:
			f.enums = tlsEnums
		case fBool:
			f.enums = boolEnums
		}
		f.input.SetValue(ui.getVal(&cfg, i))
		f.enumIdx = ui.getEnum(&cfg, i)
		fields[i] = f
	}
	return fields
}

func (ui *accountsUI) buildFields(idx int) {
	if idx < 0 || idx >= len(ui.items) {
		ui.fields = ui.emptyFields()
		return
	}
	cfg := ui.items[idx]
	cfg.normalize()
	ui.fields = ui.makeFields(cfg)
	if ui.fieldCur >= len(ui.fields) {
		ui.fieldCur = 0
	}
	ui.blurAll()
	if ui.editing {
		ui.focusField()
	}
}

// getVal/getEnum/setVal — мост между AccountConfig и полями формы.
func (ui *accountsUI) getVal(cfg *AccountConfig, i int) string {
	switch i {
	case 0:
		return cfg.Name
	case 1:
		return cfg.SenderName
	case 2:
		return cfg.Email
	case 3:
		return cfg.IMAPHost
	case 4:
		return itoaNZ(cfg.IMAPPort)
	case 6:
		return cfg.IMAPUser
	case 7:
		return cfg.IMAPPass
	case 9:
		return cfg.SMTPHost
	case 10:
		return itoaNZ(cfg.SMTPPort)
	case 12:
		return cfg.SMTPUser
	case 13:
		return cfg.SMTPPass
	case 15:
		return cfg.SentFolder
	case 16:
		return cfg.TrashFolder
	case 17:
		return cfg.DraftsFolder
	case 18:
		return cfg.JunkFolder
	}
	return ""
}

func itoaNZ(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func (ui *accountsUI) getEnum(cfg *AccountConfig, i int) int {
	switch i {
	case 5:
		return tlsToEnum(cfg.IMAPTLS)
	case 8:
		if cfg.IMAPInsecure {
			return 1
		}
		return 0
	case 11:
		return tlsToEnum(cfg.SMTPTLS)
	case 14:
		if cfg.SMTPInsecure {
			return 1
		}
		return 0
	}
	return 0
}

func (ui *accountsUI) setVal(cfg *AccountConfig, i int, v string) {
	v = strings.TrimSpace(v)
	switch i {
	case 0:
		cfg.Name = v
	case 1:
		cfg.SenderName = v
	case 2:
		cfg.Email = v
	case 3:
		cfg.IMAPHost = v
	case 4:
		cfg.IMAPPort, _ = strconv.Atoi(v)
	case 5:
		cfg.IMAPTLS = enumToTLS(ui.fields[i].enumIdx)
	case 6:
		cfg.IMAPUser = v
	case 7:
		cfg.IMAPPass = ui.fields[i].input.Value() // пароль не тримим
	case 8:
		cfg.IMAPInsecure = ui.fields[i].enumIdx == 1
	case 9:
		cfg.SMTPHost = v
	case 10:
		cfg.SMTPPort, _ = strconv.Atoi(v)
	case 11:
		cfg.SMTPTLS = enumToTLS(ui.fields[i].enumIdx)
	case 12:
		cfg.SMTPUser = v
	case 13:
		cfg.SMTPPass = ui.fields[i].input.Value()
	case 14:
		cfg.SMTPInsecure = ui.fields[i].enumIdx == 1
	case 15:
		cfg.SentFolder = v
	case 16:
		cfg.TrashFolder = v
	case 17:
		cfg.DraftsFolder = v
	case 18:
		cfg.JunkFolder = v
	}
}

// syncAll записывает значения полей формы в items[cursor].
func (ui *accountsUI) syncAll() {
	if ui.cursor >= len(ui.items) || len(ui.fields) == 0 {
		return
	}
	cfg := &ui.items[ui.cursor]
	for i := range ui.fields {
		ui.setVal(cfg, i, ui.fields[i].value())
	}
}

func (ui *accountsUI) dirty() bool {
	ui.syncAll()
	return !reflect.DeepEqual(ui.items, ui.origin)
}

func (ui *accountsUI) blurAll() {
	for i := range ui.fields {
		ui.fields[i].input.Blur()
	}
}

func (ui *accountsUI) focusField() tea.Cmd {
	ui.blurAll()
	if ui.fieldCur < len(ui.fields) && ui.fields[ui.fieldCur].kind != fTLS && ui.fields[ui.fieldCur].kind != fBool {
		return ui.fields[ui.fieldCur].input.Focus()
	}
	return nil
}

func (ui *accountsUI) moveField(delta int) tea.Cmd {
	n := len(ui.fields)
	if n == 0 {
		return nil
	}
	ui.fieldCur = (ui.fieldCur + delta + n) % n
	return ui.focusField()
}

// ---------- обработка клавиш ----------

func (m model) updateAccounts(msg tea.Msg) (tea.Model, tea.Cmd) {
	ui := &m.acctsUI
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		if ui.editing && ui.fieldCur < len(ui.fields) {
			f := &ui.fields[ui.fieldCur]
			if f.kind != fTLS && f.kind != fBool {
				var c tea.Cmd
				f.input, c = f.input.Update(msg)
				return m, c
			}
		}
		return m, nil
	}

	key := km.String()

	// подтверждение удаления
	if ui.confirmDel {
		ui.confirmDel = false
		switch strings.ToLower(key) {
		case "y", "д":
			if ui.cursor < len(ui.items) {
				ui.items = append(ui.items[:ui.cursor], ui.items[ui.cursor+1:]...)
				ui.saveMsg = ""
				ui.testResult = ""
			}
			ui.cursor = clamp(ui.cursor, 0, len(ui.items))
			ui.buildFields(min(ui.cursor, max(0, len(ui.items)-1)))
		}
		return m, nil
	}

	// подтверждение выхода без сохранения
	if ui.confirmLeave {
		ui.confirmLeave = false
		switch strings.ToLower(key) {
		case "y", "д":
			m.view = viewMain
			m.setStatus("Изменения учётных записей не сохранены")
			return m, nil
		}
		// иначе продолжаем обработку этой же клавиши
	}

	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+s":
		return m.saveAccounts()
	case "esc":
		if ui.editing {
			ui.editing = false
			ui.blurAll()
			ui.syncAll()
			return m, nil
		}
		if ui.dirty() {
			ui.confirmLeave = true
			return m, nil
		}
		m.view = viewMain
		return m, nil
	}

	if ui.editing {
		return m.keyAccountsForm(km)
	}

	// навигация по списку
	switch key {
	case "up", "k":
		ui.syncAll()
		ui.cursor = clamp(ui.cursor-1, 0, len(ui.items))
		if ui.cursor < len(ui.items) {
			ui.buildFields(ui.cursor)
		}
		ui.testResult = ""
	case "down", "j":
		ui.syncAll()
		ui.cursor = clamp(ui.cursor+1, 0, len(ui.items))
		if ui.cursor < len(ui.items) {
			ui.buildFields(ui.cursor)
		}
		ui.testResult = ""
	case "enter":
		if ui.cursor == len(ui.items) {
			return m.addAccount()
		}
		ui.editing = true
		ui.fieldCur = 0
		ui.scroll = 0
		return m, ui.focusField()
	case "n":
		return m.addAccount()
	case "ctrl+d", "D":
		if ui.cursor < len(ui.items) {
			ui.confirmDel = true
		}
	case "t":
		return m.testSelectedAccount()
	}
	return m, nil
}

func (m model) keyAccountsForm(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	ui := &m.acctsUI
	key := km.String()
	switch key {
	case "tab", "down":
		return m, ui.moveField(1)
	case "shift+tab", "up":
		return m, ui.moveField(-1)
	case "enter":
		return m, ui.moveField(1)
	case "left":
		if f := &ui.fields[ui.fieldCur]; f.kind == fTLS || f.kind == fBool {
			f.enumIdx = (f.enumIdx + len(f.enums) - 1) % len(f.enums)
			return m, nil
		}
	case "right":
		if f := &ui.fields[ui.fieldCur]; f.kind == fTLS || f.kind == fBool {
			f.enumIdx = (f.enumIdx + 1) % len(f.enums)
			return m, nil
		}
	}
	f := &ui.fields[ui.fieldCur]
	if f.kind == fTLS || f.kind == fBool {
		return m, nil // enum-поля не принимают текст
	}
	var c tea.Cmd
	f.input, c = f.input.Update(km)
	return m, c
}

func (m model) addAccount() (tea.Model, tea.Cmd) {
	ui := &m.acctsUI
	ui.syncAll()
	def := AccountConfig{
		ID:       newID(),
		Name:     "Новый ящик",
		IMAPTLS:  TLSImplicit,
		IMAPPort: 993,
		SMTPTLS:  TLSStart,
		SMTPPort: 587,
	}
	ui.items = append(ui.items, def)
	ui.cursor = len(ui.items) - 1
	ui.editing = true
	ui.fieldCur = 0
	ui.scroll = 0
	ui.testResult = ""
	ui.buildFields(ui.cursor)
	return m, ui.focusField()
}

func (m model) testSelectedAccount() (tea.Model, tea.Cmd) {
	ui := &m.acctsUI
	if ui.cursor >= len(ui.items) {
		ui.saveMsg = "Выберите учётную запись"
		return m, nil
	}
	ui.syncAll()
	cfg := ui.items[ui.cursor]
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		ui.testResult = "Ошибка настроек: " + err.Error()
		return m, nil
	}
	ui.items[ui.cursor] = cfg
	ui.testBusy = true
	ui.testResult = "Проверка соединения…"
	m.begin()
	return m, tea.Batch(cmdTestAccount(ui.cursor, cfg), m.spin.Tick)
}

// saveAccounts валидирует, пишет конфиг и пересоздаёт подключения.
func (m model) saveAccounts() (tea.Model, tea.Cmd) {
	ui := &m.acctsUI
	ui.syncAll()
	for i := range ui.items {
		ui.items[i].normalize()
		if err := ui.items[i].Validate(); err != nil {
			ui.saveMsg = fmt.Sprintf("«%s»: %v", ui.items[i].Title(), err)
			ui.cursor = i
			ui.editing = true
			ui.buildFields(i)
			return m, nil
		}
	}
	for i := range ui.items {
		if ui.items[i].ID == "" {
			ui.items[i].ID = newID()
		}
	}
	m.cfg.Accounts = make([]AccountConfig, len(ui.items))
	copy(m.cfg.Accounts, ui.items)
	if err := m.cfg.Save(m.cfgPath); err != nil {
		ui.saveMsg = "Не удалось сохранить файл: " + err.Error()
		return m, nil
	}
	// пересоздаём рантайм-аккаунты
	for _, a := range m.accounts {
		a.Close()
	}
	m.accounts = make([]*Account, 0, len(m.cfg.Accounts))
	for _, ac := range m.cfg.Accounts {
		m.accounts = append(m.accounts, NewAccount(ac))
	}
	m.acctIdx = -1
	m.expanded = map[int]bool{}
	m.curFolder = ""
	m.msgs = nil
	m.msgCursor = 0
	m.curMsg = nil
	m.msgText = ""
	m.vp.SetContent("")
	m.bodyCache = map[uint32]*FullMessage{}
	m.searchMode = false
	m.searchQuery = ""
	var cmds []tea.Cmd
	for i, acct := range m.accounts {
		m.begin()
		cmds = append(cmds, cmdConnect(i, acct))
	}
	cmds = append(cmds, m.spinnerIfBusy())
	m.view = viewMain
	m.acctsUI = newAccountsUI(m.cfg.Accounts)
	m.setStatus("Сохранено: %s (%d учётных записей)", m.cfgPath, len(m.cfg.Accounts))
	m.buildFolderItems()
	return m, tea.Batch(cmds...)
}

// ---------- отрисовка ----------

func (m model) viewAccounts() string {
	ui := &m.acctsUI
	w, h := m.width, m.height

	listW := clamp(w*24/100, 24, 34)
	formW := w - listW - 1
	bodyH := max(3, h-5)

	// ---- левая колонка: список ----
	left := make([]string, 0, bodyH)
	left = append(left, m.paneHeader("Учётные записи", listW, !ui.editing, fmt.Sprintf("%d", len(ui.items))))
	visible := max(1, bodyH-1)

	for i := 0; i < len(ui.items) && len(left)-1 < visible; i++ {
		it := ui.items[i]
		dot, dotSt := "○", stFaint
		if i < len(m.accounts) {
			switch m.accounts[i].State {
			case "online":
				dot, dotSt = "●", stOK
			case "error":
				dot, dotSt = "✗", stError
			}
		}
		name := truncate(it.Title(), listW-6)
		email := truncate(it.Email, listW-6)
		if i == ui.cursor {
			st := stSel
			if ui.editing {
				st = stSelSoft
			}
			left = append(left,
				st.Width(listW).Render(truncatePlain(" ❯ "+name, listW)),
				st.Width(listW).Render(truncatePlain("   "+email, listW)))
			continue
		}
		left = append(left,
			fitLine(" "+dotSt.Render(dot)+" "+stText.Render(name), listW),
			fitLine("   "+stFaint.Render(email), listW))
	}

	if len(left)-1 < visible {
		add := "  + добавить ящик · n"
		if ui.cursor == len(ui.items) {
			left = append(left, stSel.Width(listW).Render(truncatePlain(" ❯ добавить ящик · n", listW)))
		} else {
			left = append(left, fitLine(" "+stFaint.Render(add), listW))
		}
	}
	for len(left) < bodyH {
		left = append(left, spaces(listW))
	}

	// ---- правая колонка: форма ----
	title := "Параметры ящика"
	if ui.cursor >= len(ui.items) {
		title = "Новая учётная запись"
	}
	right := make([]string, 0, bodyH)
	right = append(right, m.paneHeader(title, formW, ui.editing, ""))

	visible = max(1, bodyH-1)
	if ui.fieldCur < ui.scroll {
		ui.scroll = ui.fieldCur
	}
	if ui.fieldCur >= ui.scroll+visible {
		ui.scroll = ui.fieldCur - visible + 1
	}
	labelW := 28
	valW := max(8, formW-labelW-6)
	padVal := func(s string, focused bool) string {
		st := stFieldVal
		if focused {
			st = stFieldValFocused
		}
		r := st.Render(s)
		if vw := lipgloss.Width(r); vw < valW {
			r += spaces(valW - vw)
		}
		return r
	}
	for i := ui.scroll; i < len(ui.fields) && len(right)-1 < visible; i++ {
		f := &ui.fields[i]
		label := truncate(f.label, labelW)
		focused := i == ui.fieldCur && ui.editing
		mark := "  "
		if focused {
			mark = stAccent.Render("❯") + " "
		}
		var valStr string
		switch f.kind {
		case fTLS, fBool:
			valStr = padVal(" "+f.value(), focused)
			if focused {
				valStr += stFaint.Render(" ←/→")
			}
		case fPass:
			v := f.input.Value()
			shown := strings.Repeat("•", min(len([]rune(v)), 24))
			if focused {
				shown = f.input.View()
			}
			valStr = padVal(truncatePlain(shown, valW-2), focused)
		default:
			shown := f.value()
			if focused {
				shown = f.input.View()
			}
			valStr = padVal(truncatePlain(shown, valW-2), focused)
		}
		right = append(right, fitLine(" "+mark+stFaint.Render(padRight(label, labelW))+valStr, formW))
	}

	if ui.testResult != "" {
		style := stStatus
		if strings.Contains(ui.testResult, "Ошибка") || strings.Contains(ui.testResult, "ошибка") ||
			strings.Contains(ui.testResult, "failed") || strings.Contains(ui.testResult, ": ") && !strings.Contains(ui.testResult, "OK") {
			style = stError
		}
		if ui.testBusy {
			style = stFaint
		}
		for _, ln := range strings.Split(ui.testResult, "\n") {
			right = append(right, fitLine(" "+style.Render(truncate(ln, formW-2)), formW))
		}
	}
	if ui.saveMsg != "" {
		right = append(right, fitLine(" "+stError.Render(truncate(ui.saveMsg, formW-2)), formW))
	}
	if ui.confirmDel {
		right = append(right, fitLine(" "+stError.Render("Удалить учётную запись? (y — да, другая клавиша — отмена)"), formW))
	}
	if ui.confirmLeave {
		right = append(right, fitLine(" "+stError.Render("Выйти БЕЗ сохранения? (y — выйти, ctrl+s — сохранить)"), formW))
	}
	for len(right) < bodyH {
		right = append(right, spaces(formW))
	}

	rows := make([]string, bodyH)
	for i := 0; i < bodyH; i++ {
		rows[i] = lineAt(left, i, listW) + stRule.Render("│") + lineAt(right, i, formW)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderTopBar(),
		stRule.Render(strings.Repeat("─", w)),
		strings.Join(rows, "\n"),
		m.renderFooter(),
	)
}
