package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/gdamore/tcell/v3"
)

var (
	baseStyle     = tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
	mutedStyle    = tcell.StyleDefault.Foreground(tcell.ColorSilver).Background(tcell.ColorBlack)
	accentStyle   = tcell.StyleDefault.Foreground(tcell.ColorAqua).Background(tcell.ColorBlack).Bold(true)
	selectedStyle = tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorAqua).Bold(true)
	errorStyle    = tcell.StyleDefault.Foreground(tcell.ColorRed).Background(tcell.ColorBlack)
	modalStyle    = tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorDarkBlue)
	headerLayout  = lipgloss.NewStyle().Align(lipgloss.Center)
	statusLayout  = lipgloss.NewStyle().Align(lipgloss.Left)
)

type renderLine struct {
	text string
	role uint8
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
	blank, separator  string
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
		cache.blank, cache.separator = strings.Repeat(" ", width), strings.Repeat("-", width)
		screen.SetStyle(baseStyle)
		screen.Clear()
	}
	for i := range cache.current {
		cache.current[i] = renderLine{}
	}
	setLine(cache.current, 0, headerLayout.Width(width).Render("ClashPulse - "+viewTitle(model.Tab)), roleAccent)
	setLine(cache.current, 1, tabLine(model.Tab), roleMuted)
	setLine(cache.current, 2, cache.separator, roleMuted)
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
		text := row.Title
		if row.Detail != "" {
			text += "  " + row.Detail
		}
		setLine(cache.current, 3+i, text, role)
	}
	if len(rows) == 0 && contentHeight > 0 {
		setLine(cache.current, 3, " No items are currently reported by the service.", roleMuted)
	}
	if height >= 4 {
		progress := model.Progress()
		if model.Pending > 0 {
			progress += " | sending " + itoa(model.Pending)
		}
		setLine(cache.current, height-3, progress, roleAccent)
		if model.Notice != "" {
			role := roleMuted
			if strings.HasPrefix(model.Notice, "Command failed:") || strings.HasPrefix(model.Notice, "Managed source command failed") {
				role = roleError
			}
			setLine(cache.current, height-2, model.Notice, role)
		}
		setLine(cache.current, height-1, strings.Join(model.Help(), "   "), roleMuted)
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
		if !invalidate && line == cache.previous[y] {
			continue
		}
		screen.PutStrStyled(0, y, cache.blank, baseStyle)
		if line.text != "" {
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

func tabLine(active Tab) string {
	labels := make([]string, 0, len(tabs))
	for index, tab := range tabs {
		label := viewTitle(tab)
		if tab == active {
			label = "[" + label + "]"
		}
		labels = append(labels, itoa(index+1)+" "+label)
	}
	return strings.Join(labels, "   ")
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
