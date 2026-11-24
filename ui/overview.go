package ui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
	"github.com/fishman/clashpulse/monitor"
)

type nodeCard struct {
	Visible                                        bool
	Group, Node, Latency, Unit, Detail, Automation string
}

type streamCard struct {
	Visible                     bool
	Title, KBps, Label, Summary string
	Fraction                    float64
	Color                       fyne.ThemeColorName
}

type switchCard struct {
	Empty                         bool
	Title, Time, Reason, Evidence string
}

type usageCard struct {
	Visible                    bool
	Name, Used, Total, Expires string
	Fraction                   float64
	Color                      fyne.ThemeColorName
}

type healthCard struct {
	Counts, Issue string
	State         pillState
}

type overviewModel struct {
	Profile, Mode, Policy, Mihomo string
	Node                          nodeCard
	Stream                        streamCard
	Switch                        switchCard
	Usage                         usageCard
	Health                        healthCard
}

// buildOverview derives every Overview card from one snapshot; it touches
// no widget, so it is tested without a display.
func buildOverview(s core.Snapshot) overviewModel {
	// Mode and Policy are raw values; their selects label them.
	m := overviewModel{
		Profile: localize.T("None"),
		Mode:    s.ProxyMode,
		Policy:  s.Monitor.SwitchPolicy,
	}
	binary := binaryFieldValues(s.Binary)
	m.Mihomo = binary[1] + " \u00b7 " + binary[0]
	var active *core.SubscriptionSnapshot
	for i := range s.Subscriptions {
		if s.Subscriptions[i].Active {
			active = &s.Subscriptions[i]
			break
		}
	}
	switch {
	case s.ActiveSource == "local":
		m.Profile = localize.T("Local profile")
	case active != nil && active.Name != "":
		m.Profile = active.Name
	case active != nil:
		m.Profile = active.ID
	}

	var node *core.ProxySnapshot
	if len(s.Groups) > 0 {
		group := s.Groups[0]
		for _, candidate := range s.Groups {
			if candidate.AutomationEnabled {
				group = candidate
				break
			}
		}
		groupName := group.Label
		if groupName == "" {
			groupName = group.ID
		}
		m.Node = nodeCard{Visible: true, Group: groupName, Node: core.ProxyLabel(s.Proxies, group.Selected), Latency: "-"}
		switch {
		case group.ManualOverride:
			m.Node.Automation = localize.T("Manual override")
		case group.AutomationEnabled:
			m.Node.Automation = localize.T("Automatic switching on")
		default:
			m.Node.Automation = localize.T("Automatic switching off")
		}
		for i := range s.Proxies {
			if s.Proxies[i].GroupID == group.ID && s.Proxies[i].ID == group.Selected {
				node = &s.Proxies[i]
				break
			}
		}
		if node != nil {
			switch {
			case node.Outcome == "success" && node.LatencyMillis > 0:
				m.Node.Latency, m.Node.Unit = strconv.FormatInt(node.LatencyMillis, 10), localize.T("ms")
			case node.MihomoMillis > 0:
				m.Node.Latency, m.Node.Unit = strconv.FormatInt(node.MihomoMillis, 10), localize.T("ms (mihomo)")
			}
			m.Node.Detail = node.StabilityLabel()
		}
	}

	m.Stream.Title = localize.T("URL test")
	m.Stream.Summary = "-"
	if s.Monitor.IntervalSeconds > 0 {
		m.Stream.Summary = fmt.Sprintf(localize.T("URL test every %d s, timeout %d ms"), s.Monitor.IntervalSeconds, s.Monitor.TimeoutMillis)
	}
	if s.Monitor.SwitchPolicy == monitor.SwitchSteadyStream && node != nil && node.StreamSamples > 0 {
		fraction := float64(node.StreamCompleted) / float64(node.StreamSamples)
		color := theme.ColorNameWarning
		switch {
		case fraction >= 1:
			color = theme.ColorNameSuccess
		case fraction <= 0:
			color = theme.ColorNameError
		}
		m.Stream = streamCard{Visible: true, Title: localize.T("Steady stream"), KBps: strconv.FormatInt(node.StreamKBps, 10), Label: node.StreamLabel(), Fraction: fraction, Color: color, Summary: m.Stream.Summary}
	}

	m.Switch = switchCard{Empty: true, Title: localize.T("No switch yet")}
	if len(s.Switches) > 0 {
		event := s.Switches[len(s.Switches)-1]
		m.Switch = switchCard{
			Title:    core.ProxyLabel(s.Proxies, event.OldID) + " -> " + core.ProxyLabel(s.Proxies, event.NewID),
			Time:     detailTime(event.At),
			Reason:   localize.T(event.Reason),
			Evidence: switchEvidence(s.Proxies, event),
		}
	}

	if active != nil && active.Usage != nil && active.Usage.TotalBytes > 0 {
		used := active.Usage.UploadedBytes + active.Usage.DownloadedBytes
		fraction := float64(used) / float64(active.Usage.TotalBytes)
		color := theme.ColorNamePrimary
		if fraction >= 0.9 {
			color = theme.ColorNameWarning
		}
		m.Usage = usageCard{Visible: true, Name: m.Profile, Used: gigabytes(used), Total: fmt.Sprintf(localize.T("of %s GB"), gigabytes(active.Usage.TotalBytes)), Fraction: fraction, Color: color}
		if active.Usage.ExpiresAt > 0 {
			m.Usage.Expires = fmt.Sprintf(localize.T("Expires: %s"), detailTime(active.Usage.ExpiresAt))
		}
	}

	m.Health = healthCard{
		Counts: fmt.Sprintf(localize.T("Subscriptions %d \u00b7 Data resources %d \u00b7 Filter lists %d"), len(s.Subscriptions), len(s.Resources), len(s.Filters)),
		Issue:  localize.T("No issues"),
		State:  pillOn,
	}
	switch {
	case len(s.Errors) > 0:
		m.Health.Issue, m.Health.State = serviceError(s.Errors[0]), pillFailed
	case s.Binary.LastCompatibilityFailure != "":
		m.Health.Issue, m.Health.State = compatibilityLabel(s.Binary.LastCompatibilityFailure), pillFailed
	}
	return m
}

// switchEvidence lists the probe samples behind a switch on one line and the
// download samples on a second.
func switchEvidence(proxies []core.ProxySnapshot, event core.SwitchSnapshot) string {
	probes := make([]string, 0, len(event.Evidence))
	for _, sample := range event.Evidence {
		probes = append(probes, fmt.Sprintf(localize.T("%s %s %d ms"), core.ProxyLabel(proxies, sample.ProxyID), localize.Code("probe.outcome", sample.Outcome), sample.LatencyMillis))
	}
	downloads := make([]string, 0, len(event.StreamEvidence))
	for _, sample := range event.StreamEvidence {
		downloads = append(downloads, core.ProxyLabel(proxies, sample.ProxyID)+" "+sample.Label())
	}
	lines := make([]string, 0, 2)
	for _, line := range []string{strings.Join(probes, "; "), strings.Join(downloads, "; ")} {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func gigabytes(bytes uint64) string {
	return strconv.FormatFloat(float64(bytes)/(1<<30), 'f', 1, 64)
}

// overviewView holds the Overview widgets; update changes only what differs.
type overviewView struct {
	object fyne.CanvasObject

	profile, mihomo      *widget.Label
	mode, policy         *widget.Select
	showMode, showPolicy func(string)

	node, switchCard           *fyne.Container
	nodeCaption, streamCaption *canvas.Text
	nodeBody                   *fyne.Container
	nodeEmpty, nodeName        *widget.Label
	nodeDetail                 *widget.Label
	nodeLatency, nodeUnit      *canvas.Text
	streamBody                 *fyne.Container
	streamSummary, streamLabel *widget.Label
	streamKBps                 *canvas.Text
	streamMeter                *meter
	switchTitle, switchTime    *widget.Label
	switchEvidence             *widget.Label
	usage                      *fyne.Container
	usageName, usageExpires    *widget.Label
	usageUsed, usageTotal      *canvas.Text
	usageMeter                 *meter
	healthCounts, healthIssue  *widget.Label
	healthDot                  *dot
}

func newOverviewView(openActivity func(), send func(ipc.Command)) *overviewView {
	v := &overviewView{}
	label := func(style fyne.TextStyle) *widget.Label {
		l := widget.NewLabelWithStyle("", fyne.TextAlignLeading, style)
		l.Wrapping = fyne.TextWrapWord
		return l
	}
	fact := func(caption string) (*fyne.Container, *widget.Label) {
		value := label(fyne.TextStyle{Bold: true})
		value.Wrapping = fyne.TextWrapOff
		value.Truncation = fyne.TextTruncateEllipsis
		return newCard(caption, value), value
	}
	configure := func(patch *ipc.ConfigPatch) {
		send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: patch})
	}
	// One-field patches: everything else, the stream URL included, stays as
	// configured.
	v.mode, v.showMode = newChoiceSelect(core.ProxyModes, proxyModeLabel, func(mode string) {
		configure(&ipc.ConfigPatch{ProxyMode: &mode})
	})
	v.policy, v.showPolicy = newChoiceSelect(monitor.SwitchPolicies, switchPolicyName, func(policy string) {
		configure(&ipc.ConfigPatch{MonitorSwitchPolicy: &policy})
	})
	for _, sel := range []*widget.Select{v.mode, v.policy} {
		sel.PlaceHolder = "-"
		sel.Disable()
	}
	profileCard, profile := fact(localize.T("Profile"))
	modeCard := newCard(localize.T("Mode"), cardSelect(v.mode))
	policyCard := newCard(localize.T("Switch policy"), cardSelect(v.policy))
	mihomoCard, mihomo := fact("Mihomo")
	v.profile, v.mihomo = profile, mihomo

	v.nodeName = label(fyne.TextStyle{Bold: true})
	v.nodeDetail = label(fyne.TextStyle{})
	v.nodeEmpty = label(fyne.TextStyle{})
	v.nodeEmpty.SetText(localize.T("No proxy group yet"))
	latency, number, unit := newValue("-", "")
	v.nodeLatency, v.nodeUnit = number, unit
	v.nodeBody = container.New(layout.NewCustomPaddedVBoxLayout(0), v.nodeName, latency, v.nodeDetail)
	v.nodeBody.Hide()
	v.node, v.nodeCaption = newTitledCard(localize.T("Current node"), v.nodeEmpty, v.nodeBody)

	v.streamSummary, v.streamLabel = label(fyne.TextStyle{}), label(fyne.TextStyle{})
	kbps, kbpsNumber, _ := newValue("0", "KB/s")
	v.streamKBps = kbpsNumber
	v.streamMeter = newMeter()
	v.streamBody = container.New(layout.NewCustomPaddedVBoxLayout(0), kbps, v.streamLabel, v.streamMeter.object)
	v.streamBody.Hide()
	stream, streamCaption := newTitledCard(localize.T("URL test"), v.streamSummary, v.streamBody)
	v.streamCaption = streamCaption

	v.switchTitle, v.switchTime = label(fyne.TextStyle{Bold: true}), label(fyne.TextStyle{})
	// One line each for probes and downloads, truncated rather than wrapped:
	// a wrapped label measures its height at a stale width.
	v.switchEvidence = label(fyne.TextStyle{})
	v.switchEvidence.Wrapping = fyne.TextWrapOff
	v.switchEvidence.Truncation = fyne.TextTruncateEllipsis
	v.switchTitle.SetText(localize.T("No switch yet"))
	v.switchCard = newCard(localize.T("Last switch"), v.switchTitle, v.switchTime, v.switchEvidence)

	v.usageName, v.usageExpires = label(fyne.TextStyle{}), label(fyne.TextStyle{})
	used, usedNumber, usedTotal := newValue("0", "")
	v.usageUsed, v.usageTotal = usedNumber, usedTotal
	v.usageMeter = newMeter()
	v.usage = newCard(localize.T("Subscription usage"), v.usageName, used, v.usageMeter.object, v.usageExpires)
	v.usage.Hide()

	v.healthCounts, v.healthIssue = label(fyne.TextStyle{}), label(fyne.TextStyle{})
	v.healthDot = newDot()
	activity := widget.NewButton(localize.T("View activity"), openActivity)
	activity.Importance = widget.LowImportance
	issue := container.NewBorder(nil, nil, container.New(layout.NewCustomPaddedLayout(0, 0, theme.InnerPadding(), 0), v.healthDot.object), nil, v.healthIssue)
	health := newTapArea(newCard(localize.T("Health"), v.healthCounts, issue, container.NewHBox(activity)), openActivity)

	facts := container.NewGridWithColumns(4, profileCard, modeCard, policyCard, mihomoCard)
	content := container.NewVBox(facts, container.NewGridWithColumns(2, v.node, stream), v.switchCard, container.NewGridWithColumns(2, v.usage, health))
	v.object = container.NewVScroll(container.NewPadded(content))
	v.update(buildOverview(core.Snapshot{}))
	v.disconnect()
	return v
}

func setLabel(label *widget.Label, text string) {
	if label.Text != text {
		label.SetText(text)
	}
}

func setText(text *canvas.Text, value string) {
	if text.Text != value {
		text.Text = value
		text.Refresh()
	}
}

func setVisible(object fyne.CanvasObject, visible bool) {
	if object.Visible() == visible {
		return
	}
	if visible {
		object.Show()
	} else {
		object.Hide()
	}
}

func (v *overviewView) update(m overviewModel) {
	setLabel(v.profile, m.Profile)
	v.showMode(m.Mode)
	v.showPolicy(m.Policy)
	v.mode.Enable()
	v.policy.Enable()
	setLabel(v.mihomo, m.Mihomo)

	setVisible(v.nodeEmpty, !m.Node.Visible)
	setVisible(v.nodeBody, m.Node.Visible)
	setLabel(v.nodeName, m.Node.Node)
	setCaption(v.nodeCaption, joinNonEmpty(localize.T("Current node"), m.Node.Group))
	setLabel(v.nodeDetail, joinNonEmpty(m.Node.Detail, m.Node.Automation))
	setText(v.nodeLatency, m.Node.Latency)
	setText(v.nodeUnit, m.Node.Unit)

	setCaption(v.streamCaption, m.Stream.Title)
	setVisible(v.streamSummary, !m.Stream.Visible)
	setVisible(v.streamBody, m.Stream.Visible)
	setLabel(v.streamSummary, m.Stream.Summary)
	setLabel(v.streamLabel, m.Stream.Label)
	setText(v.streamKBps, m.Stream.KBps)
	if m.Stream.Visible {
		v.streamMeter.set(m.Stream.Fraction, m.Stream.Color)
	}

	setLabel(v.switchTitle, m.Switch.Title)
	setLabel(v.switchTime, joinNonEmpty(m.Switch.Time, m.Switch.Reason))
	setLabel(v.switchEvidence, m.Switch.Evidence)
	setVisible(v.switchTime, !m.Switch.Empty)
	setVisible(v.switchEvidence, m.Switch.Evidence != "")

	setVisible(v.usage, m.Usage.Visible)
	setLabel(v.usageName, m.Usage.Name)
	setText(v.usageUsed, m.Usage.Used)
	setText(v.usageTotal, m.Usage.Total)
	setLabel(v.usageExpires, m.Usage.Expires)
	setVisible(v.usageExpires, m.Usage.Expires != "")
	if m.Usage.Visible {
		v.usageMeter.set(m.Usage.Fraction, m.Usage.Color)
	}

	setLabel(v.healthCounts, m.Health.Counts)
	setLabel(v.healthIssue, m.Health.Issue)
	v.healthDot.set(m.Health.State.color())
}

// disconnect greys the health dot and locks the selects: without a live
// snapshot there is no health to report and no service to send a pick to.
func (v *overviewView) disconnect() {
	v.healthDot.set(pillOff.color())
	v.mode.Disable()
	v.policy.Disable()
}

// proxyModeLabel is a proxy mode's display name.
func proxyModeLabel(mode string) string {
	return localize.Code("proxy.mode", mode)
}

func joinNonEmpty(parts ...string) string {
	kept := parts[:0:0]
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " \u00b7 ")
}
