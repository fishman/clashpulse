package tui

import (
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/fishman/notmutt/lib/tui/chrome"
	"github.com/fishman/notmutt/lib/tui/modal"
	"github.com/fishman/notmutt/lib/tui/theme"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/mattn/go-runewidth"
)

var styles = theme.Resolve(theme.Palette{Base: map[string]string{
	"base": "#1e1e2e", "text": "#cdd6f4", "surface": "#313244", "muted": "#7f849c",
	"blue": "#89b4fa", "green": "#a6e3a1", "yellow": "#f9e2af", "red": "#f38ba8",
}}, "dark", map[string]theme.Style{
	"normal":        {Fg: "text", Bg: "base"},
	"muted":         {Fg: "muted"},
	"accent":        {Fg: "blue", Attrs: []string{"bold"}},
	"selected":      {Fg: "base", Bg: "blue", Attrs: []string{"bold"}},
	"error":         {Fg: "red"},
	"modal":         {Bg: "surface"},
	"tabbar":        {Fg: "text", Bg: "surface"},
	"tabbar.active": {Fg: "base", Bg: "blue", Attrs: []string{"bold"}},
	"status":        {Fg: "text", Bg: "surface"},
	"connection":    {Fg: "base", Bg: "green", Attrs: []string{"bold"}},
	"profile":       {Fg: "base", Bg: "blue"},
	"progress":      {Fg: "base", Bg: "yellow"},
})

var compiledStyles = func() map[string]tcell.Style {
	resolved := make(map[string]tcell.Style, len(styles))
	for name, definition := range styles {
		resolved[name] = compileStyle(definition)
	}
	return resolved
}()

var (
	baseStyle     = styleForName("normal")
	mutedStyle    = styleForName("muted")
	accentStyle   = styleForName("accent")
	selectedStyle = styleForName("selected")
	errorStyle    = styleForName("error")
	modalStyle    = styleForName("modal")
	statusLayout  = lipgloss.NewStyle().Align(lipgloss.Left)
)

type renderLine struct {
	text string
	role uint8
	runs []chrome.Run
}

const (
	roleBase uint8 = iota
	roleMuted
	roleAccent
	roleSelected
	roleError
	roleModal
)

type renderCache struct {
	width, height     int
	tab               Tab
	previous, current []renderLine
	blank             string
}

func render(screen tcell.Screen, model Model, cache *renderCache) {
	width, height := screen.Size()
	if width <= 0 || height <= 0 {
		return
	}
	invalidate := cache.width != width || cache.height != height || cache.tab != model.Tab
	if invalidate {
		cache.width, cache.height, cache.tab = width, height, model.Tab
		cache.previous, cache.current = make([]renderLine, height), make([]renderLine, height)
		cache.blank = strings.Repeat(" ", width)
		screen.SetStyle(baseStyle)
		screen.Clear()
	}
	for i := range cache.current {
		cache.current[i] = renderLine{}
	}
	labels := make([]string, len(tabs))
	active := 0
	for i, tab := range tabs {
		labels[i] = itoa(i+1) + " " + viewTitle(tab)
		if tab == model.Tab {
			active = i
		}
	}
	setRuns(cache.current, 0, chrome.Tabs(labels, active, width, "tabbar", "tabbar.active"))
	columns := tableColumns(model.Tab)
	indexes, layout, sizes := fitTable(columns, width)
	headings := make([]string, len(indexes))
	for i, index := range indexes {
		headings[i] = columns[index].heading
	}
	setLine(cache.current, 1, layout.Line(headings, sizes), roleMuted)
	rows := model.Rows()
	contentHeight := height - 6
	if contentHeight < 0 {
		contentHeight = 0
	}
	for i, row := range visibleRows(rows, contentHeight) {
		role := roleBase
		if row.Selected {
			role = roleSelected
		}
		values := row.Cells
		if values == nil {
			values = []string{row.Title, row.Detail}
		}
		var cells [7]string
		for j, index := range indexes {
			if index < len(values) {
				cells[j] = values[index]
			}
		}
		setLine(cache.current, 2+i, layout.Line(cells[:len(indexes)], sizes), role)
	}
	if len(rows) == 0 && contentHeight > 0 {
		setLine(cache.current, 2, " No items are currently reported by the service.", roleMuted)
	}
	if height >= 6 && model.Tab != TabOverview {
		for _, row := range rows {
			if row.Selected {
				setLine(cache.current, height-4, row.Detail, roleMuted)
				break
			}
		}
	}
	if model.LogOpen {
		drawLog(cache.current, width, height, model)
	}
	if height >= 6 {
		profile := "profile not reported"
		for _, subscription := range model.snapshot.Snapshot.Subscriptions {
			if subscription.Active {
				profile = "profile " + subscription.Name
				if subscription.Name == "" {
					profile = "profile " + subscription.ID
				}
				break
			}
		}
		left := []chrome.Segment{
			{Runs: []chrome.Run{{Text: "IPC connected", Style: "connection"}}, Priority: 10},
			{Runs: []chrome.Run{{Text: profile, Style: "profile"}}, Priority: 9},
			{Runs: []chrome.Run{{Text: model.Progress(), Style: "progress"}}, Priority: 2},
		}
		if width >= 64 {
			for i := len(model.snapshot.Snapshot.Diagnostics) - 1; i >= 0; i-- {
				diagnostic := model.snapshot.Snapshot.Diagnostics[i]
				if diagnostic.Severity == "error" && diagnostic.Message != "" {
					left = append(left, chrome.Segment{Runs: []chrome.Run{{Text: "latest error: " + diagnostic.Message, Style: "error"}}, Priority: 1})
					break
				}
			}
		}
		var pending []chrome.Segment
		if model.Pending > 0 {
			pending = append(pending, chrome.Segment{Runs: []chrome.Run{{Text: "sending " + itoa(model.Pending), Style: "status"}}, Priority: 3})
		}
		setRuns(cache.current, height-1, chrome.Status(width, "status", left, pending))
		if model.Notice != "" {
			style := "status"
			if strings.HasPrefix(model.Notice, "Command failed:") || strings.HasPrefix(model.Notice, "Managed source command failed") {
				style = "error"
			}
			setRuns(cache.current, height-3, chrome.Status(width, "status", []chrome.Segment{{Runs: []chrome.Run{{Text: model.Notice, Style: style}}, Priority: 10}}, nil))
		}
		setRuns(cache.current, height-2, chrome.Status(width, "status", []chrome.Segment{{Runs: []chrome.Run{{Text: strings.Join(model.Help(), "   "), Style: "muted"}}, Priority: 10}}, nil))
	}
	if model.Modal != nil {
		drawModal(cache.current, width, height, model.Modal)
	}
	changed := invalidate
	for y, line := range cache.current {
		previous := cache.previous[y]
		if !invalidate && line.text == previous.text && line.role == previous.role && slices.Equal(line.runs, previous.runs) {
			continue
		}
		screen.PutStrStyled(0, y, cache.blank, baseStyle)
		if len(line.runs) > 0 {
			x := 0
			for _, run := range line.runs {
				putText(screen, x, y, width-x, run.Text, styleForName(run.Style))
				x += runewidth.StringWidth(run.Text)
			}
		} else if line.text != "" {
			putText(screen, 0, y, width, line.text, styleForRole(line.role))
		}
		changed = true
	}
	cache.previous, cache.current = cache.current, cache.previous
	if changed {
		screen.Show()
	}
}

func drawLog(lines []renderLine, width, height int, model Model) {
	diagnostics := model.snapshot.Snapshot.Diagnostics
	setLine(lines, 1, "Session log ("+itoa(len(diagnostics))+")", roleAccent)
	rows := height - 5
	if rows <= 0 {
		return
	}
	perEntry := 1
	if width < 64 {
		perEntry = 2
	}
	visible := rows / perEntry
	start := len(diagnostics) - visible - model.LogOffset
	if start < 0 {
		start = 0
	}
	end := min(start+visible, len(diagnostics))
	if start == end {
		setLine(lines, 2, "No session diagnostics.", roleMuted)
		return
	}
	y := 2
	for _, diagnostic := range diagnostics[start:end] {
		role := roleBase
		if diagnostic.Severity == "error" {
			role = roleError
		}
		if perEntry == 1 {
			setLine(lines, y, timeLabel(diagnostic.At)+" "+diagnostic.Severity+" "+diagnostic.Kind+" "+diagnostic.SourceID+" "+diagnostic.Message, role)
			y++
			continue
		}
		setLine(lines, y, time.Unix(diagnostic.At, 0).UTC().Format("15:04:05Z")+" "+diagnostic.Severity, role)
		setLine(lines, y+1, diagnostic.Kind+" "+diagnostic.SourceID+" "+diagnostic.Message, role)
		y += 2
	}
}

func drawModal(lines []renderLine, width, height int, item *Modal) {
	if width < 3 {
		return
	}
	var body []renderLine
	if item.Form != nil {
		body = append(body, renderLine{text: strings.ReplaceAll(string(item.Kind), "_", " ") + " settings", role: roleAccent})
		for _, row := range item.Form.Rows(max(1, width-4), max(1, height-7)) {
			role := roleModal
			if row.Selected {
				role = roleSelected
			}
			body = append(body, renderLine{text: row.Text, role: role})
		}
	} else {
		message := modalPrompt(item.Kind)
		if item.Kind == ModalMonitorSetting || item.Kind == ModalMonitorInterval || item.Kind == ModalAlertThreshold {
			message = monitorPrompt(item.monitor)
		} else if item.Kind == ModalDeleteSubscription || item.Kind == ModalDeleteDNS {
			message = "Remove " + item.Input + "?"
		} else if item.Kind == ModalResource || item.Kind == ModalFilter {
			message = managedModalPrompt(item)
		} else if item.Kind == ModalDNSResolver || item.Kind == ModalDNSRoute {
			message = dnsModalPrompt(item)
		}
		body = append(body, renderLine{text: message, role: roleAccent})
		if item.Kind == ModalDeleteSubscription || item.Kind == ModalDeleteDNS {
			body = append(body, renderLine{text: "Enter remove  Esc cancel", role: roleModal})
		} else {
			input := modalDisplayInput(item)
			wrapped, _, _ := modal.Wrap(input, len(input), max(1, width-10), max(1, height-8))
			for i, line := range wrapped {
				prefix := "       "
				if i == 0 {
					prefix = "Input: "
				}
				body = append(body, renderLine{text: prefix + line, role: roleModal})
			}
			action := "Enter apply  Esc cancel"
			wizard := item.Kind == ModalResource || item.Kind == ModalFilter || item.Kind == ModalDNSResolver || item.Kind == ModalDNSRoute
			if wizard {
				more := (item.Kind == ModalResource || item.Kind == ModalFilter) && item.managed.step+1 < managedFieldCount(item.Kind) ||
					(item.Kind == ModalDNSResolver || item.Kind == ModalDNSRoute) && item.dns.step < 2
				if more {
					action = "Enter next  Shift+Tab back  Esc cancel"
				}
			}
			body = append(body, renderLine{text: action, role: roleMuted})
		}
	}
	box, ok := modal.Bottom(width, height, len(body), 3)
	if !ok {
		return
	}
	if len(body) > box.BodyRows {
		body = body[len(body)-box.BodyRows:]
	}
	setLine(lines, box.Y, "\u256d"+strings.Repeat("\u2500", width-2)+"\u256e", roleModal)
	for i, row := range body {
		text := runewidth.Truncate(row.text, width-2, "")
		text += strings.Repeat(" ", width-2-runewidth.StringWidth(text))
		setLine(lines, box.Y+i+1, "\u2502"+text+"\u2502", row.role)
	}
	setLine(lines, box.Y+box.Height-1, "\u2570"+strings.Repeat("\u2500", width-2)+"\u256f", roleModal)
}

func modalDisplayInput(modal *Modal) string {
	if modal == nil {
		return ""
	}
	if modal.Kind == ModalResource && modal.managed.step == resourceFieldURL ||
		modal.Kind == ModalDNSResolver && modal.dns.step == dnsSetEndpoints ||
		modal.Kind == ModalMonitorSetting && modal.monitor == monitorTestURL {
		if modal.Input != "" {
			return "[hidden]"
		}
	}
	return modal.Input
}

func setLine(lines []renderLine, y int, text string, role uint8) {
	if y >= 0 && y < len(lines) {
		lines[y] = renderLine{text: text, role: role}
	}
}

func setRuns(lines []renderLine, y int, runs []chrome.Run) {
	if y >= 0 && y < len(lines) {
		lines[y] = renderLine{runs: runs}
	}
}

func styleForRole(role uint8) tcell.Style {
	switch role {
	case roleMuted:
		return mutedStyle
	case roleAccent:
		return accentStyle
	case roleSelected:
		return selectedStyle
	case roleError:
		return errorStyle
	case roleModal:
		return modalStyle
	default:
		return baseStyle
	}
}

func visibleRows(rows []Row, height int) []Row {
	if height <= 0 {
		return nil
	}
	if len(rows) <= height {
		return rows
	}
	selected := 0
	for index, row := range rows {
		if row.Selected {
			selected = index
			break
		}
	}
	start := 0
	if selected >= height {
		start = selected - height + 1
	}
	if start > len(rows)-height {
		start = len(rows) - height
	}
	return rows[start : start+height]
}

func styleForName(name string) tcell.Style {
	if style, ok := compiledStyles[name]; ok {
		return style
	}
	return compiledStyles["normal"]
}

func compileStyle(definition theme.Style) tcell.Style {
	style := tcell.StyleDefault
	if definition.Fg != "" {
		style = style.Foreground(color.GetColor(definition.Fg))
	}
	if definition.Bg != "" {
		style = style.Background(color.GetColor(definition.Bg))
	}
	for _, attribute := range definition.Attrs {
		switch attribute {
		case "bold":
			style = style.Bold(true)
		case "italic":
			style = style.Italic(true)
		case "underline":
			style = style.Underline(true)
		case "reverse":
			style = style.Reverse(true)
		}
	}
	return style
}

func putText(screen tcell.Screen, x, y, width int, text string, style tcell.Style) {
	if y < 0 || width <= 0 {
		return
	}
	laidOut := statusLayout.MaxWidth(width).Render(text)
	screen.PutStrStyled(x, y, laidOut, style)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	index := len(buf)
	for value > 0 {
		index--
		buf[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[index:])
}
