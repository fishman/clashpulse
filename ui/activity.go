package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/localize"
)

// activityEntry is one log row ready to show.
type activityEntry struct {
	when, level, text string
}

// activityView lists diagnostics newest first, one line each: time, level,
// then the sentence core.DiagnosticText builds.
type activityView struct {
	all      []core.DiagnosticSnapshot
	proxies  []core.ProxySnapshot
	rows     []activityEntry
	problems bool
	list     *widget.List
	empty    *widget.Label
	filter   *widget.Check
	content  fyne.CanvasObject
	dialog   *dialog.CustomDialog
	width    float32
	// whenWidth and levelWidth are the fixed columns, so every sentence
	// starts at the same x.
	whenWidth, levelWidth float32
	now                   func() time.Time
}

func newActivityView() *activityView {
	view := &activityView{now: time.Now}
	view.whenWidth = monospaceLabel("00-00 00:00").MinSize().Width
	for _, level := range []string{"error", "warning", "info", "ok"} {
		view.levelWidth = max(view.levelWidth, levelLabel(level).MinSize().Width)
	}
	view.list = widget.NewList(
		func() int { return len(view.rows) },
		func() fyne.CanvasObject { return view.newRow() },
		func(id widget.ListItemID, item fyne.CanvasObject) {
			item.(*activityRow).set(view.rows[id])
		},
	)
	view.empty = widget.NewLabel("")
	view.empty.Alignment = fyne.TextAlignCenter
	view.empty.Importance = widget.LowImportance
	view.filter = widget.NewCheck(localize.T("Problems only"), func(on bool) {
		view.problems = on
		view.rebuild()
	})
	view.content = container.New(activityLayout{view: view}, view.list, view.empty)
	view.rebuild()
	return view
}

type activityLayout struct{ view *activityView }

func (l activityLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (l activityLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Resize(size)
	}
	l.view.resizeRows(size.Width, false)
}

// activityRow is a fixed time column, a fixed level column, and the sentence,
// which wraps within its own column.
type activityRow struct {
	widget.BaseWidget
	when, level, text *widget.Label
	body              fyne.CanvasObject
}

func (v *activityView) newRow() *activityRow {
	r := &activityRow{when: monospaceLabel(""), level: levelLabel("info"), text: widget.NewLabel("")}
	r.when.Alignment = fyne.TextAlignTrailing
	r.text.Wrapping = fyne.TextWrapWord
	columns := container.NewHBox(
		container.New(fixedWidth(v.whenWidth), r.when),
		container.New(fixedWidth(v.levelWidth), r.level),
	)
	r.body = container.NewBorder(nil, nil, container.NewVBox(columns), nil, r.text)
	r.ExtendBaseWidget(r)
	return r
}

func (r *activityRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.body)
}

func (r *activityRow) set(entry activityEntry) {
	r.when.SetText(entry.when)
	r.level.Importance = levelImportance(entry.level)
	r.level.SetText(localize.Code("diagnostic.severity", entry.level))
	r.text.SetText(entry.text)
}

// fixedWidth lays its children out at one width, whatever their text.
type fixedWidth float32

func (w fixedWidth) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var height float32
	for _, object := range objects {
		height = max(height, object.MinSize().Height)
	}
	return fyne.NewSize(float32(w), height)
}

func (w fixedWidth) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Resize(size)
	}
}

func monospaceLabel(text string) *widget.Label {
	label := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	label.Importance = widget.LowImportance
	return label
}

func levelLabel(level string) *widget.Label {
	label := widget.NewLabelWithStyle(localize.Code("diagnostic.severity", level), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	label.Importance = levelImportance(level)
	return label
}

func levelImportance(level string) widget.Importance {
	switch level {
	case "error":
		return widget.DangerImportance
	case "warning":
		return widget.WarningImportance
	case "ok":
		return widget.SuccessImportance
	}
	return widget.LowImportance
}

// activityLevel is the level a row shows: a recovery reads as OK rather than
// as plain information.
func activityLevel(entry core.DiagnosticSnapshot) string {
	switch {
	case entry.Severity == "error" || entry.Severity == "warning":
		return entry.Severity
	case entry.Message == "recovered":
		return "ok"
	}
	return "info"
}

// activityTime shows the clock for today and the date for anything older.
func activityTime(at int64, now time.Time) string {
	when := time.Unix(at, 0).In(now.Location())
	if y, m, d := when.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return when.Format("15:04:05")
	}
	return when.Format("01-02 15:04")
}

func (v *activityView) resizeRows(width float32, force bool) {
	if width <= 0 || (!force && width == v.width) {
		return
	}
	v.width = width
	// The sentence column is what remains after the two fixed columns and the
	// padding the HBox and Border put between them; a very narrow dialog still
	// measures a few words per line rather than a negative width.
	textWidth := max(width-v.whenWidth-v.levelWidth-2*theme.Padding(), 4*theme.TextSize())
	measure := widget.NewLabel("")
	measure.Wrapping = fyne.TextWrapWord
	minHeight := monospaceLabel("0").MinSize().Height
	for id, row := range v.rows {
		measure.SetText(row.text)
		measure.Resize(fyne.NewSize(textWidth, 1<<15))
		v.list.SetItemHeight(widget.ListItemID(id), max(minHeight, measure.MinSize().Height))
	}
}

// rebuild orders the entries newest first and applies the problems filter.
func (v *activityView) rebuild() {
	now := v.now()
	v.rows = v.rows[:0]
	for i := len(v.all) - 1; i >= 0; i-- {
		entry := v.all[i]
		level := activityLevel(entry)
		if v.problems && level != "error" && level != "warning" {
			continue
		}
		v.rows = append(v.rows, activityEntry{when: activityTime(entry.At, now), level: level, text: core.DiagnosticText(v.proxies, entry)})
	}
	switch {
	case len(v.rows) > 0:
		v.empty.Hide()
	case v.problems:
		v.empty.SetText(localize.T("No problems recorded"))
		v.empty.Show()
	default:
		v.empty.SetText(localize.T("No activity yet"))
		v.empty.Show()
	}
	v.list.Refresh()
	v.resizeRows(v.list.Size().Width, true)
}

// update shows entries; proxies name the nodes stream probe entries refer to.
func (v *activityView) update(entries []core.DiagnosticSnapshot, proxies []core.ProxySnapshot) {
	v.all, v.proxies = append(v.all[:0], entries...), proxies
	v.rebuild()
}

// overrideRow shows one generated config change: the setting in a fixed
// monospace column, the kind of change, then why ClashPulse made it.
type overrideRow struct {
	key, change, text *widget.Label
	box               *fyne.Container
}

// overrideKeyWidth fits the longest key a change may name, so every reason
// starts at the same x.
func overrideKeyWidth() float32 {
	var width float32
	for _, key := range core.ConfigOverrideKeys {
		width = max(width, monospaceLabel(key).MinSize().Width)
	}
	return width
}

var overrideChanges = []string{"added", "replaced", "removed"}

func newOverrideRow(keyWidth float32) *overrideRow {
	r := &overrideRow{key: monospaceLabel(""), change: levelLabel("info"), text: widget.NewLabel("")}
	r.key.Importance = widget.MediumImportance
	r.text.Wrapping = fyne.TextWrapWord
	var changeWidth float32
	for _, change := range overrideChanges {
		changeWidth = max(changeWidth, widget.NewLabelWithStyle(localize.Code("override.change", change), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}).MinSize().Width)
	}
	columns := container.NewHBox(container.New(fixedWidth(keyWidth), r.key), container.New(fixedWidth(changeWidth), r.change))
	r.box = container.NewBorder(nil, widget.NewSeparator(), container.NewVBox(columns), nil, r.text)
	r.box.Hide()
	return r
}

func (r *overrideRow) set(item core.ConfigOverrideSnapshot) {
	r.key.SetText(item.Key)
	r.change.SetText(localize.Code("override.change", item.Change))
	r.text.SetText(item.Description())
	r.box.Show()
}

// openActivity shows the log panel: one tab for the diagnostics this service
// recorded, one for what it changed in the generated configuration.
func (d *desktopUI) openActivity() {
	if d.activity.dialog == nil {
		d.activityTabs = container.NewAppTabs(
			container.NewTabItem(localize.T("Log"), container.NewBorder(d.activity.filter, nil, nil, nil, d.activity.content)),
			container.NewTabItem(localize.T("Generated config changes"), d.overridesSection),
		)
		d.activity.dialog = dialog.NewCustom(localize.T("Activity"), localize.T("Close"), d.activityTabs, d.window)
	}
	showSized(d.activity.dialog, d.window, logDialogWidth, true)
}
