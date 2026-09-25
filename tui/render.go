package tui

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/fishman/notmutt/lib/tui/chrome"
	"github.com/fishman/notmutt/lib/tui/table"
	"github.com/fishman/notmutt/lib/tui/theme"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/mattn/go-runewidth"
)

var styles = theme.Resolve(theme.Palette{Base: map[string]string{
	"fg": "#ffffff", "bg": "#000000", "muted": "#c0c0c0", "accent": "#00ffff", "error": "#ff0000", "modal": "#00008b",
}}, "dark", map[string]theme.Style{
	"normal":        {Fg: "fg", Bg: "bg"},
	"muted":         {Fg: "muted"},
	"accent":        {Fg: "accent", Attrs: []string{"bold"}},
	"selected":      {Fg: "bg", Bg: "accent", Attrs: []string{"bold"}},
	"error":         {Fg: "error"},
	"modal":         {Bg: "modal"},
	"tabbar":        {Fg: "muted"},
	"tabbar.active": {Fg: "accent", Attrs: []string{"bold"}},
	"status":        {Fg: "muted"},
	"progress":      {Fg: "accent", Attrs: []string{"bold"}},
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
	headerLayout  = lipgloss.NewStyle().Align(lipgloss.Center)
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
	setLine(cache.current, 0, headerLayout.Width(width).Render("ClashPulse - "+viewTitle(model.Tab)), roleAccent)
	labels := make([]string, len(tabs))
	active := 0
	for i, tab := range tabs {
		labels[i] = itoa(i+1) + " " + viewTitle(tab)
		if tab == model.Tab {
			active = i
		}
	}
	setRuns(cache.current, 1, chrome.Tabs(labels, active, width, "tabbar", "tabbar.active"))
	layout := table.Layout{Cols: []table.Col{{Floor: 12, Cap: 24}}, Sep: "  "}
	sizes := layout.Sizes(width, true)
	setLine(cache.current, 2, layout.Line([]string{"Name", "Details"}, sizes), roleMuted)
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
		setLine(cache.current, 3+i, layout.Line([]string{row.Title, row.Detail}, sizes), role)
	}
	if len(rows) == 0 && contentHeight > 0 {
		setLine(cache.current, 3, " No items are currently reported by the service.", roleMuted)
	}
	if height >= 4 {
		progress := []chrome.Segment{{Runs: []chrome.Run{{Text: model.Progress(), Style: "progress"}}, Priority: 10}}
		var pending []chrome.Segment
		if model.Pending > 0 {
			pending = append(pending, chrome.Segment{Runs: []chrome.Run{{Text: "sending " + itoa(model.Pending), Style: "status"}}, Priority: 5})
		}
		setRuns(cache.current, height-3, chrome.Status(width, "status", progress, pending))
		if model.Notice != "" {
			style := "status"
			if strings.HasPrefix(model.Notice, "Command failed:") || strings.HasPrefix(model.Notice, "Managed source command failed") {
				style = "error"
			}
			setRuns(cache.current, height-2, chrome.Status(width, "status", []chrome.Segment{{Runs: []chrome.Run{{Text: model.Notice, Style: style}}, Priority: 10}}, nil))
		}
		setRuns(cache.current, height-1, chrome.Status(width, "status", []chrome.Segment{{Runs: []chrome.Run{{Text: strings.Join(model.Help(), "   "), Style: "muted"}}, Priority: 10}}, nil))
	}
	if model.Modal != nil {
		message := modalPrompt(model.Modal.Kind)
		if model.Modal.Kind == ModalMonitorSetting || model.Modal.Kind == ModalMonitorInterval || model.Modal.Kind == ModalAlertThreshold {
			message = monitorPrompt(model.Modal.monitor)
		} else if model.Modal.Kind == ModalDeleteSubscription || model.Modal.Kind == ModalDeleteDNS {
			message = "Remove " + model.Modal.Input + "?"
		} else if model.Modal.Kind == ModalSubscription {
			message = subscriptionModalPrompt(model.Modal)
		} else if model.Modal.Kind == ModalResource || model.Modal.Kind == ModalFilter {
			message = managedModalPrompt(model.Modal)
		} else if model.Modal.Kind == ModalDNSResolver || model.Modal.Kind == ModalDNSRoute {
			message = dnsModalPrompt(model.Modal)
		}
		boxWidth := len([]rune(message)) + 6
		if boxWidth > width {
			boxWidth = width
		}
		if boxWidth > 0 {
			x, y := (width-boxWidth)/2, height/2-1
			if y < 0 {
				y = 0
			}
			prefix := strings.Repeat(" ", x)
			setLine(cache.current, y, prefix+message, roleModal)
			if model.Modal.Kind == ModalDeleteSubscription || model.Modal.Kind == ModalDeleteDNS {
				setLine(cache.current, y+1, prefix+"Enter remove | Esc cancel", roleModal)
			} else {
				setLine(cache.current, y+1, prefix+"Input: "+modalDisplayInput(model.Modal)+"_", roleModal)
				action := "Enter apply"
				wizard := model.Modal.Kind == ModalSubscription || model.Modal.Kind == ModalResource || model.Modal.Kind == ModalFilter || model.Modal.Kind == ModalDNSResolver || model.Modal.Kind == ModalDNSRoute
				if wizard {
					more := model.Modal.Kind == ModalSubscription && model.Modal.subscription.step+1 < subscriptionFieldCount || (model.Modal.Kind == ModalResource || model.Modal.Kind == ModalFilter) && model.Modal.managed.step+1 < managedFieldCount(model.Modal.Kind) || (model.Modal.Kind == ModalDNSResolver || model.Modal.Kind == ModalDNSRoute) && model.Modal.dns.step < 2
					if more {
						action = "Enter next"
					}
					action += " | Shift+Tab back"
				}
				setLine(cache.current, y+2, prefix+action+" | Esc cancel", roleModal)
			}
		}
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

func modalDisplayInput(modal *Modal) string {
	if modal == nil {
		return ""
	}
	if modal.Kind == ModalSubscription && modal.subscription.step == subscriptionFieldURL ||
		modal.Kind == ModalResource && modal.managed.step == resourceFieldURL ||
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
