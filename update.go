package main

import (
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/emersion/go-imap"
)

// Update — основной диспетчер событий Bubble Tea.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applySize()
		m.buildFolderItems()
		return m, nil

	case spinner.TickMsg:
		var c tea.Cmd
		m.spin, c = m.spin.Update(msg)
		if m.busy > 0 {
			return m, c
		}
		return m, nil

	case tickRefreshMsg:
		var cmds []tea.Cmd
		if m.view == viewMain && m.overlay == ovNone && m.curFolder != "" && !m.loadingList && m.busy == 0 {
			cmds = append(cmds, m.fetchList())
		}
		for i, acct := range m.accounts {
			if acct.State == "error" {
				m.begin()
				cmds = append(cmds, cmdConnect(i, acct))
			}
		}
		if m.cfg.RefreshSec > 0 {
			cmds = append(cmds, tickRefreshCmd(time.Duration(m.cfg.RefreshSec)*time.Second))
		}
		cmds = append(cmds, m.spinnerIfBusy())
		return m, tea.Batch(cmds...)

	case connDoneMsg:
		return m, m.handleConnDone(msg)

	case foldersDoneMsg:
		m.busy = max(0, m.busy-1)
		if msg.err != nil {
			m.setError(msg.err)
		}
		m.buildFolderItems()
		return m, m.spinnerIfBusy()

	case listDoneMsg:
		return m, m.handleListDone(msg)

	case bodyDebounceMsg:
		if msg.req != m.bodyReq {
			return m, nil
		}
		acct := m.acct()
		if acct == nil || m.bodyUID == 0 {
			return m, nil
		}
		m.begin()
		return m, tea.Batch(
			cmdFetchBody(m.acctIdx, acct, m.curFolder, m.bodyUID, msg.req),
			m.spin.Tick,
		)

	case bodyDoneMsg:
		return m, m.handleBodyDone(msg)

	case opDoneMsg:
		return m, m.handleOpDone(msg)

	case searchDoneMsg:
		if msg.acct < len(m.accounts) {
			return m, m.handleSearchDone(msg)
		}
		return m, nil

	case sendDoneMsg:
		return m.handleSendDone(msg)

	case draftDoneMsg:
		return m.handleDraftDone(msg)

	case testAcctDoneMsg:
		m.busy = max(0, m.busy-1)
		m.acctsUI.testBusy = false
		if msg.idx == m.acctsUI.cursor {
			m.acctsUI.testResult = fmt.Sprintf("IMAP: %s\nSMTP: %s", msg.imapRes, msg.smtpRes)
		}
		return m, m.spinnerIfBusy()

	case attachSavedMsg:
		m.busy = max(0, m.busy-1)
		if msg.err != nil {
			m.setError(msg.err)
		} else if len(msg.files) > 0 {
			m.setStatus("Вложения сохранены: %s", strings.Join(msg.files, ", "))
		}
		return m, m.spinnerIfBusy()

	case folderCreatedMsg:
		m.busy = max(0, m.busy-1)
		if msg.err != nil {
			m.setError(msg.err)
		} else {
			m.setStatus("Папка создана")
		}
		return m, m.spinnerIfBusy()
	}

	// Пачки символов (быстрый набор / вставка) bubbletea доставляет одним
	// KeyMsg с несколькими рунами. В главном экране и настройках каждая руна —
	// отдельная команда, поэтому обрабатываем их по очереди. В письме (compose)
	// пачка должна целиком попасть в текстовое поле.
	if km, ok := msg.(tea.KeyMsg); ok && km.Type == tea.KeyRunes && len(km.Runes) > 1 && m.view != viewCompose {
		var cmds []tea.Cmd
		mm := m
		for _, r := range km.Runes {
			sub := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: km.Alt}
			m2, c := mm.Update(sub)
			if next, ok2 := m2.(model); ok2 {
				mm = next
			}
			if c != nil {
				cmds = append(cmds, c)
			}
		}
		return mm, tea.Batch(cmds...)
	}

	m.buildFolderItems()

	switch m.view {
	case viewMain:
		return m.updateMain(msg)
	case viewCompose:
		return m.updateCompose(msg)
	case viewAccounts:
		return m.updateAccounts(msg)
	}
	return m, nil
}

// applySize пересчитывает размеры компонентов после изменения окна.
func (m *model) applySize() {
	_, _, wv, h := m.layout()
	m.vp.Width = max(10, wv-2)
	m.vp.Height = max(3, h-1)
	if m.msgText != "" && m.curMsg != nil {
		m.msgText = renderMessage(m.curMsg, max(20, wv-2))
		m.vp.SetContent(m.msgText)
	}
	m.compose.setSize(m.width, m.height)
	m.acctsUI.setSize(m.width, m.height)
	m.searchIn.Width = clamp(m.width-16, 20, 60)
	m.newFold.Width = clamp(m.width-20, 20, 50)
}

// ---------- главный экран ----------

func (m model) updateMain(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.keyMain(msg)
	case tea.MouseMsg:
		return m.mouseMain(msg)
	}
	if m.focus == paneViewer {
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		return m, c
	}
	return m, nil
}

func (m model) keyMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// ---- оверлеи ----
	if m.overlay != ovNone {
		return m.keyOverlay(msg)
	}

	// ---- глобальные ----
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?", "f1":
		m.overlay = ovHelp
		return m, nil
	case "a":
		m.acctsUI.enter(m.cfg.Accounts)
		m.view = viewAccounts
		return m, nil
	case "c":
		return m.openCompose("new")
	case "ctrl+r", "f5":
		return m, m.refreshAll()
	case "/":
		m.overlay = ovSearch
		m.searchIn.SetValue("")
		m.searchIn.Focus()
		return m, textinput.Blink
	case "tab":
		m.focus = pane((int(m.focus) + 1) % 3)
		return m, nil
	case "shift+tab":
		m.focus = pane((int(m.focus) + 2) % 3)
		return m, nil
	case "right":
		if m.focus != paneViewer {
			m.focus++
		}
		return m, nil
	case "left":
		if m.focus == paneFolders {
			return m.keyFolders(msg) // «left» сворачивает счёт
		}
		m.focus--
		return m, nil
	case "S":
		return m.saveAttachments()
	case "esc":
		if m.searchMode {
			m.searchMode = false
			m.searchQuery = ""
			return m, m.fetchList()
		}
		return m, nil
	}

	// ---- операции с письмом (работают из любой панели) ----
	switch key {
	case "r":
		return m.openReply(false)
	case "R":
		return m.openReply(true)
	case "f":
		return m.openForward()
	case "d":
		return m.deleteCurrent(false)
	case "D":
		return m.deleteCurrent(true)
	case "m":
		return m.openMovePicker()
	case "y":
		return m.openFolderPicker(true)
	case "J":
		return m.markSpam()
	case "u":
		return m.toggleUnread()
	case "*":
		return m.toggleStar()
	case "A":
		return m.markAllRead()
	}

	// ---- навигация по панелям ----
	switch m.focus {
	case paneFolders:
		return m.keyFolders(msg)
	case paneList:
		return m.keyList(msg)
	case paneViewer:
		if key == "enter" || key == "esc" {
			m.focus = paneList
			return m, nil
		}
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		return m, c
	}
	return m, nil
}

func (m model) keyFolders(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.folderItems)
	switch msg.String() {
	case "up", "k":
		m.folderCur = clamp(m.folderCur-1, 0, max(0, n-1))
	case "down", "j":
		m.folderCur = clamp(m.folderCur+1, 0, max(0, n-1))
	case "g", "home":
		m.folderCur = 0
	case "G", "end":
		m.folderCur = max(0, n-1)
	case "N":
		if m.acct() == nil {
			m.setStatus("Сначала настройте учётную запись (a)")
			return m, nil
		}
		m.overlay = ovNewFolder
		m.newFold.SetValue("")
		m.newFold.Focus()
		return m, textinput.Blink
	case "enter", " ", "right", "l":
		if n == 0 {
			return m, nil
		}
		item := m.folderItems[m.folderCur]
		if item.isAccount {
			acct := m.accounts[item.acctIdx]
			m.expanded[item.acctIdx] = !m.expanded[item.acctIdx]
			if m.expanded[item.acctIdx] && len(acct.Folders) == 0 && acct.State == "online" {
				m.begin()
				return m, cmdRefreshFolders(item.acctIdx, acct)
			}
			m.buildFolderItems()
			return m, nil
		}
		cmd := m.selectFolder(item.acctIdx, item.folder, true)
		m.buildFolderItems()
		return m, cmd
	case "left", "h":
		if n > 0 && m.folderItems[m.folderCur].isAccount {
			m.expanded[m.folderItems[m.folderCur].acctIdx] = false
			m.buildFolderItems()
		}
	}
	return m, nil
}

func (m model) keyList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		return m, m.moveListCursor(-1)
	case "down", "j":
		return m, m.moveListCursor(1)
	case "pgup":
		return m, m.moveListCursor(-10)
	case "pgdown":
		return m, m.moveListCursor(10)
	case "g", "home":
		return m, m.setListCursor(0)
	case "G", "end":
		return m, m.setListCursor(len(m.msgs) - 1)
	case "enter", "right", "l":
		if len(m.msgs) > 0 {
			m.focus = paneViewer
		}
	}
	return m, nil
}

// ---------- оверлеи ----------

func (m model) keyOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.overlay {
	case ovHelp:
		m.overlay = ovNone
		return m, nil

	case ovConfirm:
		switch strings.ToLower(key) {
		case "y", "д":
			m.overlay = ovNone
			cmd := m.confirm.yes
			m.confirm.yes = nil
			m.confirm.msg = ""
			if cmd == nil {
				return m, nil
			}
			m.begin()
			return m, tea.Batch(cmd, m.spin.Tick)
		case "n", "н", "esc", "q":
			m.overlay = ovNone
			m.confirm.msg = ""
			m.confirm.yes = nil
		}
		return m, nil

	case ovPicker:
		switch key {
		case "esc", "q":
			m.overlay = ovNone
			return m, nil
		case "up", "k":
			m.picker.cursor = clamp(m.picker.cursor-1, 0, max(0, len(m.picker.items)-1))
		case "down", "j":
			m.picker.cursor = clamp(m.picker.cursor+1, 0, max(0, len(m.picker.items)-1))
		case "enter":
			m.overlay = ovNone
			if m.picker.action != nil && m.picker.cursor < len(m.picker.values) {
				cmd := m.picker.action(m.picker.values[m.picker.cursor])
				m.picker.action = nil
				if cmd != nil {
					m.begin()
					return m, tea.Batch(cmd, m.spin.Tick)
				}
			}
		}
		return m, nil

	case ovSearch:
		switch key {
		case "esc":
			m.overlay = ovNone
			m.searchIn.Blur()
			return m, nil
		case "enter":
			q := strings.TrimSpace(m.searchIn.Value())
			m.overlay = ovNone
			m.searchIn.Blur()
			if q == "" {
				return m, nil
			}
			acct := m.acct()
			if acct == nil || m.curFolder == "" {
				m.setStatus("Не выбрана папка для поиска")
				return m, nil
			}
			m.begin()
			m.listReq = m.nextReq()
			return m, tea.Batch(
				cmdSearch(m.acctIdx, acct, m.curFolder, q, m.listReq),
				m.spin.Tick,
			)
		}
		var c tea.Cmd
		m.searchIn, c = m.searchIn.Update(msg)
		return m, c

	case ovNewFolder:
		switch key {
		case "esc":
			m.overlay = ovNone
			m.newFold.Blur()
			return m, nil
		case "enter":
			name := strings.TrimSpace(m.newFold.Value())
			m.overlay = ovNone
			m.newFold.Blur()
			if name == "" {
				return m, nil
			}
			acct := m.acct()
			if acct == nil {
				return m, nil
			}
			m.begin()
			return m, tea.Batch(cmdCreateFolder(m.acctIdx, acct, name), m.spin.Tick)
		}
		var c tea.Cmd
		m.newFold, c = m.newFold.Update(msg)
		return m, c
	}
	return m, nil
}

// ---------- операции с письмами ----------

func (m model) saveAttachments() (tea.Model, tea.Cmd) {
	if m.curMsg == nil || len(m.curMsg.Attachments) == 0 {
		m.setStatus("У этого письма нет вложений")
		return m, nil
	}
	dir := m.cfg.AttachmentDir
	if dir == "" {
		dir = "."
	}
	m.begin()
	return m, tea.Batch(cmdSaveAttachments(dir, m.curMsg.Attachments), m.spin.Tick)
}

func (m model) deleteCurrent(force bool) (tea.Model, tea.Cmd) {
	uids := m.curUIDs()
	acct := m.acct()
	if acct == nil || uids == nil {
		m.setStatus("Письмо не выбрано")
		return m, nil
	}
	trash := acct.Special[SpTrash]
	subject := ""
	if len(m.msgs) > 0 {
		subject = m.msgs[m.msgCursor].Subject
	}
	if !force && trash != "" && m.curFolder != trash {
		m.begin()
		folder := m.curFolder
		return m, tea.Batch(cmdMove(acct, folder, trash, uids, "move"), m.spin.Tick)
	}
	// безвозвратное удаление — с подтверждением
	m.overlay = ovConfirm
	m.confirm.msg = fmt.Sprintf("Удалить БЕЗВОЗВРАТНО письмо\n«%s»?", truncate(subject, 40))
	folder := m.curFolder
	m.confirm.yes = cmdDelete(acct, folder, uids)
	return m, nil
}

func (m model) markSpam() (tea.Model, tea.Cmd) {
	uids := m.curUIDs()
	acct := m.acct()
	if acct == nil || uids == nil {
		m.setStatus("Письмо не выбрано")
		return m, nil
	}
	junk := acct.Special[SpJunk]
	if junk == "" {
		m.setStatus("Папка «Спам» не найдена — используйте «m» для перемещения")
		return m, nil
	}
	if m.curFolder == junk {
		inbox := acct.Special[SpInbox]
		if inbox == "" {
			m.setStatus("Нет папки «Входящие»")
			return m, nil
		}
		m.begin()
		folder := m.curFolder
		return m, tea.Batch(cmdMove(acct, folder, inbox, uids, "move"), m.spin.Tick)
	}
	m.begin()
	folder := m.curFolder
	return m, tea.Batch(cmdMove(acct, folder, junk, uids, "spam"), m.spin.Tick)
}

func (m model) toggleUnread() (tea.Model, tea.Cmd) {
	uids := m.curUIDs()
	acct := m.acct()
	if acct == nil || uids == nil {
		m.setStatus("Письмо не выбрано")
		return m, nil
	}
	mm := &m.msgs[m.msgCursor]
	folder := m.curFolder
	if mm.Seen {
		mm.Seen = false
		m.listUnseen++
		m.begin()
		return m, tea.Batch(
			cmdStoreFlags(acct, folder, uids, nil, []string{imap.SeenFlag}, "unread", false, mm.UID),
			m.spin.Tick,
		)
	}
	mm.Seen = true
	if m.listUnseen > 0 {
		m.listUnseen--
	}
	m.begin()
	return m, tea.Batch(
		cmdStoreFlags(acct, folder, uids, []string{imap.SeenFlag}, nil, "read", false, mm.UID),
		m.spin.Tick,
	)
}

func (m model) toggleStar() (tea.Model, tea.Cmd) {
	uids := m.curUIDs()
	acct := m.acct()
	if acct == nil || uids == nil {
		m.setStatus("Письмо не выбрано")
		return m, nil
	}
	mm := &m.msgs[m.msgCursor]
	folder := m.curFolder
	mm.Flagged = !mm.Flagged
	m.begin()
	if mm.Flagged {
		return m, tea.Batch(
			cmdStoreFlags(acct, folder, uids, []string{imap.FlaggedFlag}, nil, "star", false, mm.UID),
			m.spin.Tick,
		)
	}
	return m, tea.Batch(
		cmdStoreFlags(acct, folder, uids, nil, []string{imap.FlaggedFlag}, "star", false, mm.UID),
		m.spin.Tick,
	)
}

func (m model) markAllRead() (tea.Model, tea.Cmd) {
	acct := m.acct()
	if acct == nil || m.curFolder == "" {
		m.setStatus("Папка не выбрана")
		return m, nil
	}
	folder := m.curFolder
	m.overlay = ovConfirm
	m.confirm.msg = fmt.Sprintf("Пометить ВСЕ письма в «%s»\nпрочитанными?", folder)
	m.confirm.yes = cmdMarkAllRead(acct, folder)
	return m, nil
}

func (m model) openMovePicker() (tea.Model, tea.Cmd) {
	return m.openFolderPicker(false)
}

// openFolderPicker — выбор папки для перемещения (или копирования).
func (m model) openFolderPicker(copyMode bool) (tea.Model, tea.Cmd) {
	acct := m.acct()
	if acct == nil || len(m.curUIDs()) == 0 {
		m.setStatus("Письмо не выбрано")
		return m, nil
	}
	var items, values []string
	for _, f := range acct.Folders {
		if f.NoSelect || f.Name == m.curFolder {
			continue
		}
		label := f.Name
		if f.Delimiter != "" {
			parts := splitFolder(f.Name, f.Delimiter)
			label = strings.Repeat("  ", len(parts)-1) + specialLabel(f.Special, parts[len(parts)-1])
		} else {
			label = specialLabel(f.Special, label)
		}
		items = append(items, label)
		values = append(values, f.Name)
	}
	if len(items) == 0 {
		m.setStatus("Нет доступных папок")
		return m, nil
	}
	folder := m.curFolder
	uids := m.curUIDs()
	title := "Переместить письмо в папку:"
	if copyMode {
		title = "Скопировать письмо в папку:"
	}
	m.overlay = ovPicker
	m.picker = pickerState{
		title:  title,
		items:  items,
		values: values,
		cursor: 0,
		action: func(dest string) tea.Cmd {
			if copyMode {
				return cmdCopy(acct, folder, dest, uids)
			}
			return cmdMove(acct, folder, dest, uids, "move")
		},
	}
	return m, nil
}

// ---------- отправка / черновики ----------

func (m model) handleSendDone(msg sendDoneMsg) (tea.Model, tea.Cmd) {
	m.busy = max(0, m.busy-1)
	if msg.sendErr != nil {
		m.compose.sending = false
		m.compose.errText = "Ошибка отправки: " + msg.sendErr.Error()
		return m, m.spinnerIfBusy()
	}
	m.view = viewMain
	m.compose.active = false
	m.compose.sending = false
	if msg.archiveErr != nil {
		m.setStatus("Письмо отправлено (не удалось сохранить в «Отправленные»: %v)", msg.archiveErr)
	} else {
		m.setStatus("Письмо отправлено")
	}
	var cmds []tea.Cmd
	if msg.replyUID > 0 && msg.acctIdx >= 0 && msg.acctIdx < len(m.accounts) {
		acct := m.accounts[msg.acctIdx]
		m.begin()
		cmds = append(cmds, cmdStoreFlags(acct, msg.folder, []uint32{msg.replyUID},
			[]string{imap.AnsweredFlag}, nil, "answered", false, msg.replyUID))
	}
	if m.curFolder != "" {
		cmds = append(cmds, m.fetchList())
	}
	cmds = append(cmds, m.spinnerIfBusy())
	return m, tea.Batch(cmds...)
}

func (m model) handleDraftDone(msg draftDoneMsg) (tea.Model, tea.Cmd) {
	m.busy = max(0, m.busy-1)
	if msg.err != nil {
		m.compose.sending = false
		m.compose.errText = "Не удалось сохранить черновик: " + msg.err.Error()
		return m, m.spinnerIfBusy()
	}
	m.view = viewMain
	m.compose.active = false
	m.compose.sending = false
	m.setStatus("Черновик сохранён")
	var cmds []tea.Cmd
	if m.curFolder != "" {
		cmds = append(cmds, m.fetchList())
	}
	cmds = append(cmds, m.spinnerIfBusy())
	return m, tea.Batch(cmds...)
}

// ---------- мышь ----------

func (m model) mouseMain(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	wf, wl, _, h := m.layout()
	x, y := msg.X, msg.Y
	if m.width == 0 {
		return m, nil
	}
	// 0 — шапка, 1 — разделитель, 2 — заголовки колонок
	row := y - 3
	if row < -1 || row >= h-1 {
		return m, nil
	}
	visible := max(1, h-1)
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			switch {
			case x < wf:
				off := scrollOffset(m.folderCur, len(m.folderItems), visible)
				idx := off + row
				if idx >= 0 && idx < len(m.folderItems) {
					m.folderCur = idx
					item := m.folderItems[idx]
					if item.isAccount {
						m.expanded[item.acctIdx] = !m.expanded[item.acctIdx]
						m.buildFolderItems()
					} else {
						cmd := m.selectFolder(item.acctIdx, item.folder, true)
						m.buildFolderItems()
						return m, cmd
					}
				}
			case x < wf+wl+1:
				off := scrollOffset(m.msgCursor, len(m.msgs), visible)
				idx := off + row
				if idx >= 0 && idx < len(m.msgs) {
					m.focus = paneList
					return m, m.setListCursor(idx)
				}
				m.focus = paneList
			default:
				m.focus = paneViewer
			}
		}
	case tea.MouseActionMotion:
		// прокрутка колесом
		switch msg.Button {
		case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
			delta := -3
			if msg.Button == tea.MouseButtonWheelDown {
				delta = 3
			}
			switch {
			case x > wf+wl:
				var c tea.Cmd
				m.vp, c = m.vp.Update(msg)
				return m, c
			case x > wf:
				return m, m.moveListCursor(delta)
			default:
				m.folderCur = clamp(m.folderCur+delta, 0, max(0, len(m.folderItems)-1))
			}
		}
	}
	return m, nil
}

// scrollOffset — детерминированная прокрутка списка под курсор.
func scrollOffset(cursor, count, visible int) int {
	if visible <= 0 || count <= visible {
		return 0
	}
	off := cursor - visible/2
	return clamp(off, 0, count-visible)
}

// ---------- открытие compose ----------

func (m model) openReply(all bool) (tea.Model, tea.Cmd) {
	if all {
		return m.openCompose("replyAll")
	}
	return m.openCompose("reply")
}

func (m model) openForward() (tea.Model, tea.Cmd) {
	return m.openCompose("forward")
}

func (m model) openCompose(mode string) (tea.Model, tea.Cmd) {
	if len(m.accounts) == 0 {
		m.setStatus("Сначала добавьте учётную запись — клавиша «a»")
		return m, nil
	}
	acctIdx := m.acctIdx
	if acctIdx < 0 || acctIdx >= len(m.accounts) {
		acctIdx = 0
	}
	cm := newCompose()
	cm.setSize(m.width, m.height)
	cm.acctIdx = acctIdx
	cm.mode = mode

	switch mode {
	case "reply", "replyAll", "forward":
		if m.curMsg == nil {
			m.setStatus("Письмо ещё не загружено — подождите или выберите другое")
			return m, nil
		}
		orig := m.curMsg
		cm.orig = orig
		cm.origUID = orig.UID
		cm.origFolder = m.curFolder
		cm.origAcct = m.acctIdx
		switch mode {
		case "reply", "replyAll":
			var to []*mail.Address
			if ra := orig.ReplyAddress(); ra != nil {
				to = append(to, ra)
			}
			if mode == "replyAll" {
				own := map[string]bool{}
				for _, a := range m.accounts {
					own[strings.ToLower(a.Cfg.Email)] = true
				}
				seen := map[string]bool{}
				for _, a := range to {
					seen[strings.ToLower(a.Address)] = true
				}
				for _, list := range [][]*mail.Address{orig.To, orig.Cc} {
					for _, a := range list {
						low := strings.ToLower(a.Address)
						if !own[low] && !seen[low] {
							seen[low] = true
							to = append(to, a)
						}
					}
				}
			}
			cm.to.SetValue(formatAddrList(to))
			cm.subject.SetValue(ReplySubject(orig.Subject))
			cm.body.SetValue("\n\n" + orig.QuoteBody())
		case "forward":
			cm.subject.SetValue(ForwardSubject(orig.Subject))
			cm.preAtts = orig.Attachments
			cm.body.SetValue(orig.ForwardPreamble())
		}
	}
	cm.active = true
	cm.focusIdx = cfTo
	cm.focusField()
	m.compose = cm
	m.view = viewCompose
	return m, textinput.Blink
}
