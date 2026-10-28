package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// View — точка отрисовки всего приложения.
func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Инициализация…"
	}
	// строки левой панели всегда пересобираются по актуальному состоянию
	m.buildFolderItems()
	if m.width < 70 || m.height < 16 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			stError.Render("⏺ Терминал слишком мал.\n  нужно минимум 70×16, лучше 120×32."))
	}
	var base string
	switch m.view {
	case viewCompose:
		base = m.viewCompose()
	case viewAccounts:
		base = m.viewAccounts()
	default:
		base = m.viewMain()
	}
	switch m.overlay {
	case ovHelp:
		return m.renderHelpOverlay(base)
	case ovPicker:
		return m.renderPickerOverlay(base)
	case ovConfirm:
		return m.renderConfirmOverlay(base)
	case ovSearch:
		return m.renderSearchOverlay(base)
	case ovNewFolder:
		return m.renderNewFolderOverlay(base)
	}
	return base
}

// ---------- шапка и подвал ----------

// renderTopBar — строка контекста в стиле агентных CLI:
// «✳ gomail › адрес» слева, состояние справа.
func (m model) renderTopBar() string {
	left := " " + stAccent.Render("✳") + " " + stBold.Render("gomail") + stRule.Render("  ›  ")
	switch m.view {
	case viewCompose:
		left += stText.Render(composeModeLabel(m.compose.mode, m.compose.sending))
	case viewAccounts:
		left += stText.Render("ящики")
	default:
		if a := m.acct(); a != nil {
			left += stText.Render(a.Cfg.Email)
		} else {
			left += stFaint.Render("нет подключённых ящиков")
		}
	}
	if right := m.topRight(); right != "" {
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
		if gap > 0 {
			left += spaces(gap) + right
		}
	}
	return fitLine(left, m.width)
}

func (m model) topRight() string {
	acct := m.acct()
	switch {
	case m.busy > 0:
		return m.spin.View() + " " + stFaint.Render("синхронизация")
	case m.view == viewMain && m.errText != "":
		return stError.Render("✗ ошибка")
	case m.view == viewMain && acct != nil && acct.State == "online":
		return stOK.Render("⏺ в сети")
	case m.view == viewMain && acct != nil && acct.State == "error":
		return stError.Render("✗ нет связи")
	case m.view == viewMain && acct != nil:
		return stFaint.Render("⏺ подключение")
	case m.view == viewAccounts:
		return stFaint.Render("ctrl+s — сохранить")
	case m.view == viewCompose:
		if m.compose.acctIdx >= 0 && m.compose.acctIdx < len(m.accounts) {
			return stFaint.Render(m.accounts[m.compose.acctIdx].Cfg.Email)
		}
	}
	return ""
}

func (m model) renderFooter() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		stRule.Render(strings.Repeat("─", m.width)),
		m.renderHintLine(),
		m.renderStatusLine(),
	)
}

// renderHintLine — строка подсказок с «приглашением» ❯, как в агентном REPL.
func (m model) renderHintLine() string {
	avail := max(10, m.width-4)
	line := " " + stAccent.Render("❯") + " " + stFaint.Render(truncate(m.hints(), avail))
	return fitLine(line, m.width)
}

// renderStatusLine — контекст слева, состояние операции справа.
func (m model) renderStatusLine() string {
	left := " " + stFaint.Render(truncate(m.contextLine(), m.width/2))
	right := m.statusRight()
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		left = " " + stFaint.Render(truncate(m.contextLine(), max(4, m.width-lipgloss.Width(right)-2)))
		gap = max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right)-1)
	}
	return fitLine(left+spaces(gap)+right, m.width)
}

func (m model) contextLine() string {
	switch m.view {
	case viewCompose:
		mode := composeModeLabel(m.compose.mode, false)
		from := ""
		if m.compose.acctIdx >= 0 && m.compose.acctIdx < len(m.accounts) {
			from = " · " + m.accounts[m.compose.acctIdx].Cfg.Email
		}
		return mode + from
	case viewAccounts:
		return "файл: " + m.cfgPath
	}
	acct := m.acct()
	if acct == nil {
		return "gomail"
	}
	folder := specialLabel(m.specialOfCurrent(), m.curFolder)
	if folder == "" {
		return acct.Cfg.Email
	}
	parts := []string{acct.Cfg.Email, folder}
	if m.searchMode {
		parts = append(parts, fmt.Sprintf("поиск «%s»", m.searchQuery))
	} else if m.listTotal > 0 {
		parts = append(parts, fmt.Sprintf("%d писем", m.listTotal))
		if m.listUnseen > 0 {
			parts[len(parts)-1] += fmt.Sprintf(" · %d новых", m.listUnseen)
		}
	}
	return strings.Join(parts, " · ")
}

func (m model) statusRight() string {
	switch {
	case m.errText != "":
		return stError.Render("✗ " + truncate(m.errText, max(12, m.width*45/100)))
	case m.statusText != "":
		return stStatus.Render("⏺ " + truncate(m.statusText, max(12, m.width*45/100)))
	case m.busy > 0:
		return stFaint.Render(m.spin.View() + " работает…")
	default:
		return stFaint.Render("⏺ готово")
	}
}

func composeModeLabel(mode string, sending bool) string {
	var s string
	switch mode {
	case "new":
		s = "новое письмо"
	case "reply":
		s = "ответ"
	case "replyAll":
		s = "ответ всем"
	case "forward":
		s = "пересылка"
	default:
		s = "письмо"
	}
	if sending {
		s += " · отправка…"
	}
	return s
}

// ---------- главный экран ----------

func (m model) viewMain() string {
	wf, wl, wv, h := m.layout()
	folders := m.folderLines(wf, h)
	list := m.listLines(wl, h)
	viewer := m.viewerLines(wv, h)

	rows := make([]string, h)
	for i := 0; i < h; i++ {
		rows[i] = lineAt(folders, i, wf) + stRule.Render("│") +
			lineAt(list, i, wl) + stRule.Render("│") + lineAt(viewer, i, wv)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderTopBar(),
		stRule.Render(strings.Repeat("─", m.width)),
		strings.Join(rows, "\n"),
		m.renderFooter(),
	)
}

// paneHeader — заголовок колонки: маркер фокуса, название и счётчик справа.
func (m model) paneHeader(title string, width int, focused bool, right string) string {
	mark := " "
	ts := stColTitle
	if focused {
		mark = stAccent.Render("▌")
		ts = stColTitleFocus
	}
	line := " " + mark + " " + ts.Render(truncate(title, max(1, width-10)))
	if right != "" {
		gap := width - lipgloss.Width(line) - lipgloss.Width(right) - 1
		if gap > 0 {
			line += spaces(gap) + stFaint.Render(right)
		}
	}
	return fitLine(line, width)
}

// folderLines — левая колонка: ящики и папки без рамки.
func (m model) folderLines(w, h int) []string {
	focused := m.view == viewMain && m.focus == paneFolders && m.overlay == ovNone
	lines := []string{m.paneHeader("Ящики", w, focused, "")}
	visible := max(0, h-1)

	if len(m.accounts) == 0 {
		lines = append(lines,
			"",
			" "+stFaint.Render("Нет учётных записей."),
			"",
			" "+stFaint.Render("Нажмите «a», чтобы"),
			" "+stFaint.Render("добавить почтовый ящик."),
		)
	}

	off := scrollOffset(m.folderCur, len(m.folderItems), visible)
	for i := off; i < len(m.folderItems) && len(lines)-1 < visible; i++ {
		item := m.folderItems[i]
		var plain, styled string
		if item.isAccount {
			arrow := "▸"
			if m.expanded[item.acctIdx] {
				arrow = "▾"
			}
			acct := m.accounts[item.acctIdx]
			dot, dotSt := "…", stFaint
			switch acct.State {
			case "online":
				dot, dotSt = "●", stOK
			case "error":
				dot, dotSt = "✗", stError
			}
			name := truncate(acct.Cfg.Title(), w-6)
			plain = " " + arrow + " " + dot + " " + name
			styled = " " + stFaint.Render(arrow) + " " + dotSt.Render(dot) + " " + stBold.Render(name)
		} else {
			indent := strings.Repeat("  ", item.depth+1)
			marker := "  "
			label := item.label
			switch {
			case item.current:
				marker = "❯ "
				plain = " " + indent + marker + label
				styled = " " + stFaint.Render(indent) + stAccent.Render(marker) + stAccent.Render(label)
			case item.folder.NoSelect:
				plain = " " + indent + marker + label
				styled = " " + stFaint.Render(indent+marker+label)
			default:
				plain = " " + indent + marker + label
				styled = " " + stFaint.Render(indent+marker) + stText.Render(label)
			}
		}
		if i == m.folderCur {
			st := stSel
			if !focused {
				st = stSelSoft
			}
			styled = st.Width(w).Render(truncatePlain(plain, w))
		}
		lines = append(lines, fitLine(styled, w))
	}
	for len(lines) < h {
		lines = append(lines, spaces(w))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// truncatePlain обрезает строку с учётом ANSI-последовательностей.
func truncatePlain(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

// listLines — центральная колонка: список писем.
func (m model) listLines(w, h int) []string {
	focused := m.view == viewMain && m.focus == paneList && m.overlay == ovNone

	title := "Письма"
	right := ""
	if m.curFolder != "" {
		label := specialLabel(m.specialOfCurrent(), m.curFolder)
		if m.searchMode {
			title = "Поиск"
			right = fmt.Sprintf("%d", len(m.msgs))
		} else {
			title = label
			right = fmt.Sprintf("%d", m.listTotal)
			if m.listUnseen > 0 {
				right += fmt.Sprintf(" · %d нов.", m.listUnseen)
			}
		}
	}
	lines := []string{m.paneHeader(title, w, focused, right)}
	visible := max(0, h-1)

	switch {
	case m.curFolder == "":
		lines = append(lines, "", " "+stFaint.Render("Выберите папку слева"))
	case m.loadingList && len(m.msgs) == 0:
		lines = append(lines, "", " "+stFaint.Render(m.spin.View()+" загрузка писем…"))
	case len(m.msgs) == 0:
		if m.searchMode {
			lines = append(lines, "", " "+stFaint.Render("Ничего не найдено"))
		} else {
			lines = append(lines, "", " "+stFaint.Render("В этой папке нет писем"))
		}
	}

	dateW := 8
	if w < 44 {
		dateW = 0 // в узкой колонке дату не показываем
	}
	fromW := clamp(w*30/100, 10, 22)
	subjectW := w - 8 - fromW - dateW
	if subjectW < 6 {
		fromW = max(6, fromW-(6-subjectW))
		subjectW = 6
	}

	off := scrollOffset(m.msgCursor, len(m.msgs), visible)
	for i := off; i < len(m.msgs) && len(lines)-1 < visible; i++ {
		mm := m.msgs[i]
		from := truncate(displayFrom(mm.From), fromW)
		subj := truncate(mm.Subject, subjectW)
		date := ""
		if dateW > 0 {
			date = padLeft(formatDate(mm.Date), dateW)
		}

		if i == m.msgCursor {
			plain := " ❯" + flagChars(mm) + " " + padRight(from, fromW) + " " +
				padRight(subj, subjectW) + date
			st := stSel
			if !focused {
				st = stSelSoft
			}
			lines = append(lines, st.Width(w).Render(truncatePlain(plain, w)))
			continue
		}

		textSt := stDim
		if !mm.Seen {
			textSt = stBold
		}
		line := "  " + styledFlags(mm) + " " +
			textSt.Render(padRight(from, fromW)) + " " +
			textSt.Render(padRight(subj, subjectW)) + stFaint.Render(date)
		lines = append(lines, fitLine(line, w))
	}
	for len(lines) < h {
		lines = append(lines, spaces(w))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// flagChars — четыре фиксированные позиции: ● ★ ↩ ✎.
func flagChars(mm MsgMeta) string {
	var b strings.Builder
	if !mm.Seen {
		b.WriteString("●")
	} else {
		b.WriteString(" ")
	}
	if mm.Flagged {
		b.WriteString("★")
	} else {
		b.WriteString(" ")
	}
	if mm.Answered {
		b.WriteString("↩")
	} else {
		b.WriteString(" ")
	}
	if mm.Draft {
		b.WriteString("✎")
	} else {
		b.WriteString(" ")
	}
	return b.String()
}

func styledFlags(mm MsgMeta) string {
	var b strings.Builder
	if !mm.Seen {
		b.WriteString(stAccent.Render("●"))
	} else {
		b.WriteString(" ")
	}
	if mm.Flagged {
		b.WriteString(stStar.Render("★"))
	} else {
		b.WriteString(" ")
	}
	if mm.Answered {
		b.WriteString(stFaint.Render("↩"))
	} else {
		b.WriteString(" ")
	}
	if mm.Draft {
		b.WriteString(stFaint.Render("✎"))
	} else {
		b.WriteString(" ")
	}
	return b.String()
}

// viewerLines — правая колонка: письмо.
func (m model) viewerLines(w, h int) []string {
	focused := m.view == viewMain && m.focus == paneViewer && m.overlay == ovNone

	title := "Письмо"
	right := ""
	if m.curMsg != nil && len(m.curMsg.Attachments) > 0 {
		right = fmt.Sprintf("%d влож.", len(m.curMsg.Attachments))
	}
	lines := []string{m.paneHeader(title, w, focused, right)}

	switch {
	case m.loadingBody:
		lines = append(lines, "", " "+stFaint.Render(m.spin.View()+" загрузка письма…"))
	case m.curMsg == nil:
		switch {
		case m.curFolder == "":
			lines = append(lines, "", " "+stFaint.Render("Выберите папку и письмо"))
		case len(m.msgs) == 0:
			lines = append(lines, "", " "+stFaint.Render("Письмо не выбрано"))
		}
	default:
		m.vp.Width = max(10, w-2)
		m.vp.Height = max(1, h-1)
		lines = append(lines, strings.Split(m.vp.View(), "\n")...)
	}

	for len(lines) < h {
		lines = append(lines, spaces(w))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m model) specialOfCurrent() string {
	acct := m.acct()
	if acct == nil {
		return ""
	}
	for _, f := range acct.Folders {
		if f.Name == m.curFolder {
			return f.Special
		}
	}
	return ""
}

// ---------- подсказки ----------

func (m model) hints() string {
	switch m.view {
	case viewCompose:
		return "ctrl+s отправить · ctrl+d в черновики · tab/↑↓ поле · ←→ ящик · esc отмена"
	case viewAccounts:
		if m.acctsUI.editing {
			return "tab/↑↓ поле · ←→ значение · ctrl+s сохранить · t проверка · esc назад"
		}
		return "↑↓ выбор · ↵ правка · n новая · ctrl+d удалить · ctrl+s сохранить · t проверка"
	}
	switch m.overlay {
	case ovSearch:
		return "enter искать · esc отмена"
	case ovNewFolder:
		return "enter создать · esc отмена"
	case ovPicker:
		return "↑↓ выбор · enter переместить · esc отмена"
	case ovConfirm:
		return "y — да · n — нет"
	case ovHelp:
		return "любая клавиша — закрыть"
	}
	switch m.focus {
	case paneFolders:
		return "↑↓ выбор · ↵ открыть · N папка · tab панели · a ящики · ctrl+r обновить · ? помощь · q выход"
	case paneList:
		h := "↑↓ письмо · ↵ просмотр · c написать · r/R ответ · f переслать · d удалить · m переместить · / поиск · ? помощь"
		if m.searchMode {
			h += " · esc сброс поиска"
		}
		return h
	default:
		return "↑↓ прокрутка · r ответить · S вложения · esc/tab назад · ? помощь"
	}
}

// ---------- рендер письма ----------

// renderMessage формирует текст для правой панели в виде «транскрипта».
func renderMessage(fm *FullMessage, width int) string {
	if fm == nil {
		return ""
	}
	width = max(16, width)
	inner := width - 2

	var sb strings.Builder
	sb.WriteString(" " + stAccent.Render("⏺") + " " +
		stBold.Render(truncate(formatAddrList(fm.From), inner-2)) + "\n")

	rows := make([][2]string, 0, 4)
	if len(fm.To) > 0 {
		rows = append(rows, [2]string{"Кому", formatAddrList(fm.To)})
	}
	if len(fm.Cc) > 0 {
		rows = append(rows, [2]string{"Копия", formatAddrList(fm.Cc)})
	}
	if !fm.Date.IsZero() {
		rows = append(rows, [2]string{"Дата", fm.Date.Local().Format("02.01.2006 15:04")})
	}
	if strings.TrimSpace(fm.Subject) != "" {
		rows = append(rows, [2]string{"Тема", fm.Subject})
	}

	for i, r := range rows {
		branch := "│"
		if i == len(rows)-1 && len(fm.Attachments) == 0 {
			branch = "⎿"
		}
		sb.WriteString(" " + stFaint.Render(branch) + " " +
			stFaint.Render(padRight(r[0], 6)) +
			stText.Render(truncate(r[1], inner-9)) + "\n")
	}

	if len(fm.Attachments) > 0 {
		var names []string
		for _, a := range fm.Attachments {
			names = append(names, fmt.Sprintf("%s (%s)", a.Filename, humanSize(a.Size)))
		}
		sb.WriteString(" " + stFaint.Render("⎿") + " " +
			stWarn.Render(truncate(strings.Join(names, ", "), inner-3)) + "\n")
		sb.WriteString("   " + stFaint.Render("S — сохранить вложения") + "\n")
	}
	if fm.ParseNote != "" {
		sb.WriteString("   " + stFaint.Render("примечание: "+truncate(fm.ParseNote, inner-14)) + "\n")
	}

	sb.WriteString("\n " + stRule.Render(strings.Repeat("─", inner)) + "\n\n")

	body := fm.Body
	if strings.TrimSpace(body) == "" {
		body = "[пустое письмо]"
	}
	// длинные строки без пробелов ломаем принудительно
	body = hardBreakLongLines(body, inner*2)
	wrapped := lipgloss.NewStyle().Width(inner).Render(body)
	for _, ln := range strings.Split(wrapped, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), ">") {
			sb.WriteString(" " + stFaint.Render(ln) + "\n")
		} else {
			sb.WriteString(" " + stText.Render(ln) + "\n")
		}
	}
	return sb.String()
}

func hardBreakLongLines(s string, max int) string {
	if max <= 0 {
		return s
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		r := []rune(line)
		for len(r) > max {
			out = append(out, string(r[:max]))
			r = r[max:]
		}
		out = append(out, string(r))
	}
	return strings.Join(out, "\n")
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciБ", float64(n)/float64(div), "КМГТ"[exp])
}

var monthsShort = [...]string{"янв", "фев", "мар", "апр", "мая", "июн",
	"июл", "авг", "сен", "окт", "ноя", "дек"}

// formatDate — компактная метка времени в стиле CLI (сейчас / 12м / 15:04 / вчера / 12 сен).
func formatDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	now := time.Now()
	t = t.Local()
	d := now.Sub(t)
	yest := now.AddDate(0, 0, -1)
	switch {
	case d >= 0 && d < time.Minute:
		return "сейчас"
	case d >= 0 && d < time.Hour:
		return fmt.Sprintf("%dм", int(d.Minutes()))
	case t.Year() == now.Year() && t.YearDay() == now.YearDay():
		return t.Format("15:04")
	case t.Year() == yest.Year() && t.YearDay() == yest.YearDay():
		return "вчера"
	case t.Year() == now.Year():
		return fmt.Sprintf("%02d %s", t.Day(), monthsShort[t.Month()-1])
	default:
		return t.Format("02.01.06")
	}
}

// displayFrom — имя отправителя для узкой колонки.
func displayFrom(s string) string {
	if i := strings.LastIndex(s, "<"); i > 0 {
		name := strings.TrimSpace(s[:i])
		if name != "" {
			return name
		}
		return strings.Trim(s[i:], "<>")
	}
	return s
}

// ---------- оверлеи ----------

func placeOverlay(bg, ov string) string {
	w, h := lipgloss.Width(bg), lipgloss.Height(bg)
	ovW, ovH := lipgloss.Width(ov), lipgloss.Height(ov)
	x := max(0, (w-ovW)/2)
	y := max(0, (h-ovH)/2)

	// Фон приглушаем, а полосу под диалог вырезаем целиком —
	// так диалог не смешивается с текстом писем.
	plain := strings.Split(ansi.Strip(bg), "\n")
	ovLines := strings.Split(ov, "\n")

	out := make([]string, 0, len(plain))
	for i, ln := range plain {
		row := i - y
		if row < -1 || row > ovH {
			out = append(out, stFaint.Render(padRight(ln, w)))
			continue
		}
		mid := spaces(w)
		if row >= 0 && row < len(ovLines) {
			dlg := fitLine(ovLines[row], ovW)
			mid = spaces(x) + dlg + spaces(max(0, w-(x+ovW)))
		}
		out = append(out, mid)
	}
	return strings.Join(out, "\n")
}

func (m model) renderConfirmOverlay(bg string) string {
	w := max(lipgloss.Width(m.confirm.msg)+4, 40)
	body := stOverlay.Copy().Width(w).Render(
		stOverlayTitle.Render("⏺ Подтверждение") + "\n" + m.confirm.msg + "\n\n" +
			stFaint.Render("y/д — да    n/н или esc — нет"))
	return placeOverlay(bg, body)
}

func (m model) renderPickerOverlay(bg string) string {
	var lines []string
	lines = append(lines, stOverlayTitle.Render("⏺ "+m.picker.title))
	visible := min(len(m.picker.items), max(3, m.height/2))
	off := scrollOffset(m.picker.cursor, len(m.picker.items), visible)
	for i := off; i < len(m.picker.items) && i < off+visible; i++ {
		mark := "  "
		item := m.picker.items[i]
		if i == m.picker.cursor {
			mark = stAccent.Render("❯") + " "
			item = stSel.Render(truncate(item, 42))
		}
		lines = append(lines, mark+item)
	}
	lines = append(lines, "", stFaint.Render("enter — выбрать, esc — отмена"))
	body := stOverlay.Copy().Width(50).Render(strings.Join(lines, "\n"))
	return placeOverlay(bg, body)
}

func (m model) renderSearchOverlay(bg string) string {
	fname := specialLabel(m.specialOfCurrent(), m.curFolder)
	body := stOverlay.Copy().Render(
		stOverlayTitle.Render("⏺ Поиск в папке «"+fname+"»") + "\n" +
			" " + stAccent.Render("❯") + " " + m.searchIn.View() + "\n\n" +
			stFaint.Render("enter — найти, esc — отмена"))
	return placeOverlay(bg, body)
}

func (m model) renderNewFolderOverlay(bg string) string {
	body := stOverlay.Copy().Render(
		stOverlayTitle.Render("⏺ Новая папка") + "\n" +
			" " + stAccent.Render("❯") + " " + m.newFold.View() + "\n\n" +
			stFaint.Render("enter — создать, esc — отмена"))
	return placeOverlay(bg, body)
}

func (m model) renderHelpOverlay(bg string) string {
	body := stOverlay.Copy().MaxHeight(m.height - 4).MaxWidth(m.width - 8).Render(helpText())
	return placeOverlay(bg, body)
}

func helpText() string {
	sec := func(title string, rows [][2]string) string {
		var sb strings.Builder
		sb.WriteString(stAccent.Render(title) + "\n")
		for _, r := range rows {
			sb.WriteString("  " + stText.Render(padRight(r[0], 13)) + stFaint.Render(r[1]) + "\n")
		}
		return sb.String()
	}
	left := sec("Навигация", [][2]string{
		{"tab / ←→", "переключение панелей"},
		{"↑↓ / j k", "перемещение по списку"},
		{"g / G", "в начало / в конец"},
		{"enter", "открыть папку / письмо / просмотр"},
		{"PgUp PgDn", "страница в списке или письме"},
		{"ctrl+r / F5", "обновить папки и письма"},
		{"/", "поиск в текущей папке"},
		{"esc", "сбросить результаты поиска"},
		{"q", "выход"},
		{"?", "эта справка"},
	}) + "\n" + sec("Письмо", [][2]string{
		{"c", "написать письмо"},
		{"r / R", "ответить / ответить всем"},
		{"f", "переслать"},
		{"d", "удалить (в корзину)"},
		{"D", "удалить безвозвратно"},
		{"m", "переместить в папку…"},
		{"y", "скопировать в папку…"},
		{"J", "в спам / из спама"},
		{"u", "прочитано ↔ непрочитано"},
		{"*", "пометка ★ (важное)"},
		{"A", "прочитать все в папке"},
		{"S", "сохранить вложения"},
	})
	right := sec("Папки и ящики", [][2]string{
		{"N", "новая папка (в панели папок)"},
		{"a", "настройка учётных записей"},
		{"enter", "развернуть/свернуть ящик"},
	}) + "\n" + sec("Новое письмо", [][2]string{
		{"ctrl+s", "отправить"},
		{"ctrl+d", "сохранить черновик"},
		{"tab", "следующее поле"},
		{"←→", "выбор ящика-отправителя"},
		{"esc", "отменить (спросит подтверждение)"},
	}) + "\n" + sec("Учётные записи", [][2]string{
		{"n", "новая учётная запись"},
		{"enter", "править выделенную"},
		{"ctrl+d", "удалить выделенную"},
		{"t", "проверить соединение"},
		{"ctrl+s", "сохранить в файл и применить"},
	})
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
	title := " " + stAccent.Render("✳") + " " + stBold.Render("gomail") +
		stFaint.Render("  ·  консольный почтовый клиент")
	return title + "\n\n" + body + "\n\n" + stFaint.Render("Любая клавиша — закрыть справку")
}
