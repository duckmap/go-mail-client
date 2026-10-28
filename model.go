package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/emersion/go-imap"
)

// appView — какой экран сейчас показан.
type appView int

const (
	viewMain appView = iota
	viewCompose
	viewAccounts
)

// pane — активная панель основного экрана.
type pane int

const (
	paneFolders pane = iota
	paneList
	paneViewer
)

// overlay — всплывающий слой поверх основного экрана.
type overlay int

const (
	ovNone overlay = iota
	ovHelp
	ovPicker
	ovConfirm
	ovSearch
	ovNewFolder
)

const bodyDebounce = 250 * time.Millisecond

// folderItem — строка в левой панели (счёт или папка).
type folderItem struct {
	isAccount bool
	acctIdx   int
	folder    FolderInfo
	label     string
	depth     int
	current   bool // это текущая открытая папка
}

// ---------- сообщения (tea.Msg) ----------

type connDoneMsg struct {
	acct int
	err  error
}

type foldersDoneMsg struct {
	acct int
	err  error
}

type listDoneMsg struct {
	acct   int
	folder string
	req    int
	msgs   []MsgMeta
	total  uint32
	unseen uint32
	err    error
}

type bodyDebounceMsg struct{ req int }

type bodyDoneMsg struct {
	acct   int
	folder string
	uid    uint32
	req    int
	msg    *FullMessage
	err    error
}

type opDoneMsg struct {
	what    string // "flags", "move", "delete", "spam", "answered", "markall"
	err     error
	refresh bool
	uid     uint32
}

type searchDoneMsg struct {
	acct   int
	folder string
	req    int
	query  string
	uids   []uint32
	err    error
}

type sendDoneMsg struct {
	acctIdx    int
	sendErr    error
	archiveErr error
	replyUID   uint32
	folder     string
}

type draftDoneMsg struct{ err error }

type testAcctDoneMsg struct {
	idx     int
	imapRes string
	smtpRes string
}

type attachSavedMsg struct {
	files []string
	err   error
}

type folderCreatedMsg struct {
	acct int
	err  error
}

type tickRefreshMsg struct{}

// ---------- корневая модель ----------

type confirmState struct {
	msg string
	yes tea.Cmd
}

type pickerState struct {
	title  string
	items  []string
	values []string // соответствующие значения (имена папок)
	cursor int
	action func(value string) tea.Cmd
}

type model struct {
	cfg     *Config
	cfgPath string

	accounts []*Account
	acctIdx  int // -1, если нет учётных записей

	view    appView
	focus   pane
	overlay overlay

	width, height int

	// левая панель
	expanded    map[int]bool
	folderItems []folderItem
	folderCur   int

	// центральная панель (список писем)
	curFolder   string
	msgs        []MsgMeta
	msgCursor   int
	listTotal   uint32
	listUnseen  uint32
	loadingList bool
	searchMode  bool
	searchQuery string

	// правая панель (просмотр)
	vp          viewport.Model
	curMsg      *FullMessage
	msgText     string
	loadingBody bool
	bodyCache   map[uint32]*FullMessage

	// асинхронные запросы
	reqSeq  int
	listReq int
	bodyReq int
	bodyUID uint32
	busy    int
	spin    spinner.Model

	statusText string
	errText    string

	// оверлеи
	confirm  confirmState
	picker   pickerState
	searchIn textinput.Model
	newFold  textinput.Model

	compose composeModel
	acctsUI accountsUI
}

// newModel создаёт корневую модель приложения.
func newModel(cfg *Config, cfgPath string) model {
	sp := spinner.New()
	// кадры в духе агентных CLI: точка → звёздочка
	sp.Spinner = spinner.Spinner{
		Frames: []string{"·", "✢", "✳", "✶", "✻", "✽"},
		FPS:    time.Second / 8,
	}
	sp.Style = stAccent

	si := textinput.New()
	si.Prompt = ""
	si.Placeholder = "текст для поиска…"
	si.CharLimit = 120
	si.Width = 46

	nf := textinput.New()
	nf.Prompt = ""
	nf.Placeholder = "имя новой папки…"
	nf.CharLimit = 80
	nf.Width = 40

	vp := viewport.New(40, 10)

	m := model{
		cfg:       cfg,
		cfgPath:   cfgPath,
		acctIdx:   -1,
		view:      viewMain,
		focus:     paneFolders,
		expanded:  map[int]bool{},
		bodyCache: map[uint32]*FullMessage{},
		spin:      sp,
		searchIn:  si,
		newFold:   nf,
		vp:        vp,
	}
	m.accounts = make([]*Account, 0, len(cfg.Accounts))
	for _, ac := range cfg.Accounts {
		ac.normalize()
		m.accounts = append(m.accounts, NewAccount(ac))
	}
	m.acctsUI = newAccountsUI(cfg.Accounts)
	m.compose = newCompose()
	return m
}

// ---------- команды ----------

func (m *model) nextReq() int {
	m.reqSeq++
	return m.reqSeq
}

func (m *model) begin() {
	m.busy++
}

func cmdConnect(i int, acct *Account) tea.Cmd {
	return func() tea.Msg {
		return connDoneMsg{acct: i, err: acct.Connect()}
	}
}

func cmdRefreshFolders(i int, acct *Account) tea.Cmd {
	return func() tea.Msg {
		return foldersDoneMsg{acct: i, err: acct.RefreshFolders()}
	}
}

func cmdFetchList(acctIdx int, acct *Account, folder string, limit, req int) tea.Cmd {
	return func() tea.Msg {
		msgs, total, unseen, err := acct.ListMessages(folder, limit)
		return listDoneMsg{acct: acctIdx, folder: folder, req: req, msgs: msgs, total: total, unseen: unseen, err: err}
	}
}

func cmdFetchBody(acctIdx int, acct *Account, folder string, uid uint32, req int) tea.Cmd {
	return func() tea.Msg {
		fm, err := acct.FetchMessage(folder, uid)
		return bodyDoneMsg{acct: acctIdx, folder: folder, uid: uid, req: req, msg: fm, err: err}
	}
}

func cmdStoreFlags(acct *Account, folder string, uids []uint32, add, remove []string, what string, refresh bool, uid uint32) tea.Cmd {
	return func() tea.Msg {
		err := acct.StoreFlags(folder, uids, add, remove)
		return opDoneMsg{what: what, err: err, refresh: refresh, uid: uid}
	}
}

func cmdMarkAllRead(acct *Account, folder string) tea.Cmd {
	return func() tea.Msg {
		err := acct.MarkAllRead(folder)
		return opDoneMsg{what: "markall", err: err, refresh: true}
	}
}

func cmdMove(acct *Account, from, to string, uids []uint32, what string) tea.Cmd {
	return func() tea.Msg {
		err := acct.MoveMessages(from, to, uids)
		return opDoneMsg{what: what, err: err, refresh: true}
	}
}

func cmdCopy(acct *Account, from, to string, uids []uint32) tea.Cmd {
	return func() tea.Msg {
		err := acct.CopyMessages(from, to, uids)
		return opDoneMsg{what: "copy", err: err, refresh: false}
	}
}

func cmdDelete(acct *Account, folder string, uids []uint32) tea.Cmd {
	return func() tea.Msg {
		err := acct.DeleteMessages(folder, uids)
		return opDoneMsg{what: "delete", err: err, refresh: true}
	}
}

func cmdSearch(acctIdx int, acct *Account, folder, query string, req int) tea.Cmd {
	return func() tea.Msg {
		uids, err := acct.Search(folder, query)
		return searchDoneMsg{acct: acctIdx, folder: folder, req: req, query: query, uids: uids, err: err}
	}
}

func cmdFetchByUIDs(acctIdx int, acct *Account, folder string, uids []uint32, req int) tea.Cmd {
	return func() tea.Msg {
		msgs, err := acct.FetchMessagesByUID(folder, uids)
		var total uint32
		if err == nil {
			total = uint32(len(msgs))
		}
		return listDoneMsg{acct: acctIdx, folder: folder, req: req, msgs: msgs, total: total, err: err}
	}
}

func cmdSend(acctIdx int, acct *Account, raw []byte, from string, to []string, replyUID uint32, replyFolder string) tea.Cmd {
	cfg := acct.Cfg
	return func() tea.Msg {
		sendErr := SendMail(cfg, raw, from, to)
		var archiveErr error
		if sendErr == nil {
			if sent := acct.SpecialFolder(SpSent); sent != "" {
				archiveErr = acct.AppendMessage(sent, raw, []string{imap.SeenFlag})
			}
		}
		return sendDoneMsg{acctIdx: acctIdx, sendErr: sendErr, archiveErr: archiveErr, replyUID: replyUID, folder: replyFolder}
	}
}

func cmdSaveDraft(acct *Account, raw []byte) tea.Cmd {
	return func() tea.Msg {
		folder := acct.SpecialFolder(SpDrafts)
		if folder == "" {
			folder = "Drafts"
			if err := acct.EnsureFolder(folder); err != nil {
				return draftDoneMsg{err: fmt.Errorf("нет папки черновиков: %w", err)}
			}
		}
		err := acct.AppendMessage(folder, raw, []string{imap.DraftFlag, imap.SeenFlag})
		return draftDoneMsg{err: err}
	}
}

func cmdTestAccount(idx int, cfg AccountConfig) tea.Cmd {
	return func() tea.Msg {
		imapRes := "OK"
		if n, err := TestIMAPConfig(cfg); err != nil {
			imapRes = err.Error()
		} else {
			imapRes = fmt.Sprintf("OK (%d папок)", n)
		}
		smtpRes := "OK"
		if err := TestSMTPConfig(cfg); err != nil {
			smtpRes = err.Error()
		}
		return testAcctDoneMsg{idx: idx, imapRes: imapRes, smtpRes: smtpRes}
	}
}

func cmdSaveAttachments(dir string, atts []Attachment) tea.Cmd {
	return func() tea.Msg {
		files, err := SaveAttachments(dir, atts)
		return attachSavedMsg{files: files, err: err}
	}
}

func cmdCreateFolder(i int, acct *Account, name string) tea.Cmd {
	return func() tea.Msg {
		return folderCreatedMsg{acct: i, err: acct.EnsureFolder(name)}
	}
}

func tickRefreshCmd(d time.Duration) tea.Cmd {
	if d <= 0 {
		return nil
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return tickRefreshMsg{} })
}

// ---------- Init ----------

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for i, acct := range m.accounts {
		m.busy++
		cmds = append(cmds, cmdConnect(i, acct))
	}
	if m.cfg.RefreshSec > 0 {
		cmds = append(cmds, tickRefreshCmd(time.Duration(m.cfg.RefreshSec)*time.Second))
	}
	if m.busy > 0 {
		cmds = append(cmds, m.spin.Tick)
	}
	return tea.Batch(cmds...)
}

// ---------- вспомогательные методы модели ----------

func (m *model) setStatus(format string, args ...interface{}) {
	m.statusText = fmt.Sprintf(format, args...)
	m.errText = ""
}

func (m *model) setError(err error) {
	if err == nil {
		return
	}
	m.errText = err.Error()
	m.statusText = ""
}

func (m *model) acct() *Account {
	if m.acctIdx < 0 || m.acctIdx >= len(m.accounts) {
		return nil
	}
	return m.accounts[m.acctIdx]
}

func (m *model) curUIDs() []uint32 {
	if len(m.msgs) == 0 || m.msgCursor < 0 || m.msgCursor >= len(m.msgs) {
		return nil
	}
	return []uint32{m.msgs[m.msgCursor].UID}
}

// buildFolderItems строит плоский список строк левой панели.
func (m *model) buildFolderItems() {
	items := make([]folderItem, 0, 32)
	for i, acct := range m.accounts {
		state := ""
		switch acct.State {
		case "online":
			state = "●"
		case "connecting", "":
			state = "…"
		case "error":
			state = "✗"
		}
		items = append(items, folderItem{
			isAccount: true,
			acctIdx:   i,
			label:     fmt.Sprintf("%s %s", state, acct.Cfg.Title()),
		})
		if !m.expanded[i] {
			continue
		}
		for _, f := range acct.Folders {
			depth := 0
			label := f.Name
			if f.Delimiter != "" {
				parts := splitFolder(f.Name, f.Delimiter)
				depth = len(parts) - 1
				if depth > 3 {
					label = "…/" + parts[len(parts)-1]
					depth = 3
				} else {
					label = parts[len(parts)-1]
				}
			}
			label = specialLabel(f.Special, label)
			cur := i == m.acctIdx && f.Name == m.curFolder
			items = append(items, folderItem{
				acctIdx: i,
				folder:  f,
				label:   label,
				depth:   depth,
				current: cur,
			})
		}
	}
	m.folderItems = items
	if m.folderCur >= len(items) {
		m.folderCur = max(0, len(items)-1)
	}
}

func splitFolder(name, delim string) []string {
	var out []string
	for _, seg := range strings.Split(name, delim) {
		if seg != "" {
			out = append(out, seg)
		}
	}
	if len(out) == 0 {
		out = []string{name}
	}
	return out
}

// specialLabel переводит названия специальных папок на русский.
func specialLabel(special, def string) string {
	switch special {
	case SpInbox:
		return "Входящие"
	case SpSent:
		return "Отправленные"
	case SpDrafts:
		return "Черновики"
	case SpTrash:
		return "Корзина"
	case SpJunk:
		return "Спам"
	case SpArchive:
		return "Архив"
	}
	return def
}

// fetchList запускает загрузку списка писем текущей папки.
func (m *model) fetchList() tea.Cmd {
	acct := m.acct()
	if acct == nil || m.curFolder == "" {
		return nil
	}
	m.begin()
	m.loadingList = true
	m.listReq = m.nextReq()
	return cmdFetchList(m.acctIdx, acct, m.curFolder, m.cfg.FetchLimit, m.listReq)
}

// selectFolder открывает папку.
func (m *model) selectFolder(acctIdx int, f FolderInfo, moveFocus bool) tea.Cmd {
	if f.NoSelect {
		m.setStatus("«%s» — служебная папка, в ней нельзя выбрать письма", f.Name)
		return nil
	}
	m.acctIdx = acctIdx
	m.curFolder = f.Name
	m.msgs = nil
	m.msgCursor = 0
	m.curMsg = nil
	m.msgText = ""
	m.bodyCache = map[uint32]*FullMessage{}
	m.searchMode = false
	m.searchQuery = ""
	m.listTotal = 0
	m.listUnseen = 0
	if moveFocus {
		m.focus = paneList
	}
	return m.fetchList()
}

// moveListCursor сдвигает курсор списка и заказывает тело письма.
func (m *model) moveListCursor(delta int) tea.Cmd {
	if len(m.msgs) == 0 {
		return nil
	}
	idx := clamp(m.msgCursor+delta, 0, len(m.msgs)-1)
	if idx == m.msgCursor {
		return nil
	}
	m.msgCursor = idx
	return m.requestBody(true)
}

func (m *model) setListCursor(idx int) tea.Cmd {
	idx = clamp(idx, 0, max(0, len(m.msgs)-1))
	if idx == m.msgCursor {
		return nil
	}
	m.msgCursor = idx
	return m.requestBody(true)
}

// requestBody заказывает загрузку тела текущего письма (с debounce).
func (m *model) requestBody(debounce bool) tea.Cmd {
	if len(m.msgs) == 0 {
		m.curMsg = nil
		m.msgText = ""
		m.vp.SetContent("")
		return nil
	}
	mm := m.msgs[m.msgCursor]
	if cached, ok := m.bodyCache[mm.UID]; ok {
		m.setBody(cached)
		return nil
	}
	m.bodyUID = mm.UID
	m.bodyReq = m.nextReq()
	m.loadingBody = true
	m.curMsg = nil
	m.msgText = ""
	m.vp.SetContent("")
	req := m.bodyReq
	if debounce {
		return tea.Tick(bodyDebounce, func(time.Time) tea.Msg { return bodyDebounceMsg{req} })
	}
	acct := m.acct()
	folder := m.curFolder
	m.begin()
	return cmdFetchBody(m.acctIdx, acct, folder, mm.UID, req)
}

// setBody показывает загруженное письмо в правой панели.
func (m *model) setBody(fm *FullMessage) {
	m.curMsg = fm
	m.msgText = renderMessage(fm, max(20, m.viewerWidth()))
	m.vp.SetContent(m.msgText)
	m.vp.GotoTop()
	m.loadingBody = false
}

// markCurrentRead локально помечает письмо прочитанным и отправляет флаг.
func (m *model) markCurrentRead() tea.Cmd {
	if len(m.msgs) == 0 {
		return nil
	}
	mm := &m.msgs[m.msgCursor]
	if mm.Seen {
		return nil
	}
	mm.Seen = true
	if m.listUnseen > 0 {
		m.listUnseen--
	}
	acct := m.acct()
	if acct == nil {
		return nil
	}
	m.begin()
	return cmdStoreFlags(acct, m.curFolder, []uint32{mm.UID}, []string{imap.SeenFlag}, nil, "seen", false, mm.UID)
}

// refreshAll перечитывает папки всех счетов и текущий список писем.
func (m *model) refreshAll() tea.Cmd {
	var cmds []tea.Cmd
	for i, acct := range m.accounts {
		m.begin()
		if acct.State == "online" {
			cmds = append(cmds, cmdRefreshFolders(i, acct))
		} else {
			cmds = append(cmds, cmdConnect(i, acct))
		}
	}
	if m.curFolder != "" && m.acct() != nil {
		cmds = append(cmds, m.fetchList())
	}
	m.setStatus("Обновление…")
	if m.busy > 0 {
		cmds = append(cmds, m.spin.Tick)
	}
	return tea.Batch(cmds...)
}

// autoSelectFirstFolder выбирает INBOX первого подключённого счёта.
func (m *model) autoSelectFirstFolder(i int) tea.Cmd {
	acct := m.accounts[i]
	inbox := acct.Special[SpInbox]
	if inbox == "" {
		for _, f := range acct.Folders {
			if !f.NoSelect {
				inbox = f.Name
				break
			}
		}
	}
	if inbox == "" {
		return nil
	}
	m.expanded[i] = true
	for idx, f := range acct.Folders {
		if f.Name == inbox {
			_ = idx
			return m.selectFolder(i, f, true)
		}
	}
	return m.selectFolder(i, FolderInfo{Name: inbox}, true)
}

// ---------- обработка сообщений ----------

func (m *model) handleConnDone(msg connDoneMsg) tea.Cmd {
	m.busy = max(0, m.busy-1)
	if msg.acct < 0 || msg.acct >= len(m.accounts) {
		return m.spinnerIfBusy()
	}
	acct := m.accounts[msg.acct]
	if msg.err != nil {
		acct.State = "error"
		acct.LastErr = msg.err
		if msg.acct == m.acctIdx || m.acctIdx == -1 {
			m.setError(fmt.Errorf("%s: %w", acct.Cfg.Title(), msg.err))
		}
		return m.spinnerIfBusy()
	}
	acct.State = "online"
	acct.LastErr = nil
	if m.acctIdx == -1 {
		m.setStatus("Подключено: %s", acct.Cfg.Title())
		cmd := m.autoSelectFirstFolder(msg.acct)
		return tea.Batch(cmd, m.spinnerIfBusy())
	}
	m.setStatus("Подключено: %s", acct.Cfg.Title())
	return m.spinnerIfBusy()
}

func (m *model) spinnerIfBusy() tea.Cmd {
	if m.busy > 0 {
		return m.spin.Tick
	}
	return nil
}

func (m *model) handleListDone(msg listDoneMsg) tea.Cmd {
	if msg.req != m.listReq {
		return nil // устаревший ответ
	}
	m.busy = max(0, m.busy-1)
	m.loadingList = false
	if msg.err != nil {
		m.setError(msg.err)
		return m.spinnerIfBusy()
	}
	if msg.acct != m.acctIdx || msg.folder != m.curFolder {
		return m.spinnerIfBusy()
	}
	m.msgs = msg.msgs
	if !m.searchMode {
		m.listTotal = msg.total
		m.listUnseen = msg.unseen
	}
	if m.msgCursor >= len(m.msgs) {
		m.msgCursor = max(0, len(m.msgs)-1)
	}
	if m.statusText == "Обновление…" {
		m.setStatus("Обновлено: %s", time.Now().Format("15:04:05"))
	}
	var cmd tea.Cmd
	if len(m.msgs) > 0 {
		cmd = m.requestBody(false)
	} else {
		m.curMsg = nil
		m.msgText = ""
		m.vp.SetContent("")
	}
	return tea.Batch(cmd, m.spinnerIfBusy())
}

func (m *model) handleBodyDone(msg bodyDoneMsg) tea.Cmd {
	if msg.req != m.bodyReq {
		return nil // устаревший ответ
	}
	m.busy = max(0, m.busy-1)
	m.loadingBody = false
	if msg.err != nil {
		m.setError(msg.err)
		return m.spinnerIfBusy()
	}
	if msg.acct != m.acctIdx || msg.folder != m.curFolder {
		return m.spinnerIfBusy()
	}
	if len(m.msgs) == 0 || m.msgs[m.msgCursor].UID != msg.uid {
		return m.spinnerIfBusy()
	}
	m.bodyCache[msg.uid] = msg.msg
	m.setBody(msg.msg)
	return tea.Batch(m.markCurrentRead(), m.spinnerIfBusy())
}

func (m *model) handleOpDone(msg opDoneMsg) tea.Cmd {
	m.busy = max(0, m.busy-1)
	if msg.err != nil {
		m.setError(msg.err)
		return m.spinnerIfBusy()
	}
	switch msg.what {
	case "seen":
	case "unread":
		m.setStatus("Письмо помечено непрочитанным")
	case "read":
		m.setStatus("Письмо помечено прочитанным")
	case "star":
		m.setStatus("Пометка обновлена")
	case "answered":
		for i := range m.msgs {
			if m.msgs[i].UID == msg.uid {
				m.msgs[i].Answered = true
			}
		}
	case "markall":
		for i := range m.msgs {
			m.msgs[i].Seen = true
		}
		m.listUnseen = 0
		m.setStatus("Все письма помечены прочитанными")
	case "move":
		m.setStatus("Перемещено")
	case "copy":
		m.setStatus("Скопировано")
	case "spam":
		m.setStatus("Перемещено в спам")
	case "delete":
		m.setStatus("Удалено")
	}
	if msg.refresh {
		return tea.Batch(m.fetchList(), m.spinnerIfBusy())
	}
	return m.spinnerIfBusy()
}

func (m *model) handleSearchDone(msg searchDoneMsg) tea.Cmd {
	if msg.req != m.listReq {
		return nil
	}
	m.busy = max(0, m.busy-1)
	if msg.err != nil {
		m.setError(msg.err)
		return m.spinnerIfBusy()
	}
	m.searchMode = true
	m.searchQuery = msg.query
	if len(msg.uids) == 0 {
		m.msgs = nil
		m.listTotal = 0
		m.curMsg = nil
		m.vp.SetContent("")
		m.setStatus("Поиск «%s»: ничего не найдено", msg.query)
		return m.spinnerIfBusy()
	}
	m.setStatus("Поиск «%s»: найдено %d", msg.query, len(msg.uids))
	m.begin()
	m.listReq = m.nextReq()
	acct := m.accounts[msg.acct]
	return tea.Batch(
		cmdFetchByUIDs(msg.acct, acct, msg.folder, msg.uids, m.listReq),
		m.spinnerIfBusy(),
	)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ---------- служебные ----------

// layout возвращает ширины трёх колонок и высоту рабочей области
// (шапка + разделители + подвал занимают 5 строк).
func (m *model) layout() (wf, wl, wv, h int) {
	w := m.width
	h = m.height - 5
	if h < 3 {
		h = 3
	}
	wf = clamp(w*22/100, 20, 36)
	rest := w - wf - 2 // два вертикальных разделителя
	wl = clamp(rest*50/100, 28, 60)
	wv = rest - wl
	if wv < 22 { // совсем узкий терминал — придерживаем минимум просмотру
		wv = 22
		wl = rest - wv
	}
	return
}

func (m *model) viewerWidth() int {
	_, _, wv, _ := m.layout()
	return wv - 2
}
