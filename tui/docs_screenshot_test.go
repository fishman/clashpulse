//go:build screenshots

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/internal/docsfixture"
	"github.com/fishman/clashpulse/ipc"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
)

// TestDocsScreenshots renders the real model over the docs fixture and writes
// one SVG per tab. DOCS_SCREENSHOTS overrides the output directory; a relative
// value is taken from the repository root.
func TestDocsScreenshots(t *testing.T) {
	dir := os.Getenv("DOCS_SCREENSHOTS")
	if dir == "" {
		dir = filepath.Join("docs", "assets", "screenshots")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join("..", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := docsfixture.Snapshot()
	model := NewModel().Apply(ipc.Event{Snapshot: snapshot})
	proxies := model.selectTab(TabProxies)
	proxies.Selection[TabProxies] = "proxy:" + snapshot.Groups[0].ID + ":" + snapshot.Groups[0].Selected
	settings := model.selectTab(TabSettings)
	settings.Selection[TabSettings] = "setting:monitor:switch-policy"
	// The Proxies table needs 200 columns before the latency column shows the
	// stability and download statistics the docs describe.
	for _, shot := range []struct {
		name  string
		model Model
		width int
	}{
		{"tui-overview.svg", model, 120},
		{"tui-proxies.svg", proxies, 200},
		{"tui-subscriptions.svg", model.selectTab(TabSubscriptions), 120},
		{"tui-settings.svg", settings, 120},
	} {
		svg := renderSVG(t, shot.model, shot.width, 34)
		if err := os.WriteFile(filepath.Join(dir, shot.name), []byte(svg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const (
	svgCellWidth  = 8.4
	svgCellHeight = 18
	svgPad        = 16
	svgTitle      = 30
)

func renderSVG(t *testing.T, model Model, width, height int) string {
	t.Helper()
	terminal := vt.NewMockTerm(vt.MockOptSize{X: vt.Col(width), Y: vt.Row(height)}, vt.MockOptColors(1<<24))
	screen, err := tcell.NewTerminfoScreenFromTty(terminal, tcell.OptNegotiation(false), tcell.OptAltScreen(false),
		tcell.OptTerm("xterm-256color"), tcell.OptColors(1<<24))
	if err != nil {
		t.Fatal(err)
	}
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	render(screen, model, &renderCache{})

	base, text := styles["normal"].Bg, styles["normal"].Fg
	color := func(c interface{ CSS() string }, fallback string) string {
		if css := c.CSS(); css != "" {
			return strings.ToLower(css)
		}
		return fallback
	}
	frameW := float64(width)*svgCellWidth + 2*svgPad
	frameH := float64(height*svgCellHeight) + 2*svgPad + svgTitle
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%g" height="%g" viewBox="0 0 %g %g">`+"\n", frameW, frameH, frameW, frameH)
	fmt.Fprintf(&b, `<rect width="%g" height="%g" rx="10" fill="%s" stroke="#45475a"/>`+"\n", frameW, frameH, base)
	for i, dot := range []string{"#f38ba8", "#f9e2af", "#a6e3a1"} {
		fmt.Fprintf(&b, `<circle cx="%d" cy="16" r="6" fill="%s"/>`+"\n", 20+i*20, dot)
	}
	fmt.Fprintf(&b, `<text x="%g" y="20" fill="#7f849c" font-family="sans-serif" font-size="12" text-anchor="middle">clashpulse tui</text>`+"\n", frameW/2)
	fmt.Fprintf(&b, `<g transform="translate(%d %d)" font-family="ui-monospace, 'JetBrains Mono', 'DejaVu Sans Mono', Menlo, Consolas, monospace" font-size="14" xml:space="preserve">`+"\n", svgPad, svgPad+svgTitle)

	type cell struct {
		text, fg, bg string
		bold         bool
		width        int
	}
	for y := range height {
		row := make([]cell, 0, width)
		for x := 0; x < width; {
			c := terminal.GetCell(vt.Coord{X: vt.Col(x), Y: vt.Row(y)})
			fg, bg := color(c.S.Fg(), text), color(c.S.Bg(), base)
			if c.S.Attr()&vt.Reverse != 0 {
				fg, bg = bg, fg
			}
			w := max(c.W, 1)
			if c.C == "" {
				c.C = " "
			}
			row = append(row, cell{text: c.C, fg: fg, bg: bg, bold: c.S.Attr()&vt.Bold != 0, width: w})
			x += w
		}
		top := float64(y * svgCellHeight)
		col := 0
		for i := 0; i < len(row); {
			j, span := i, 0
			for j < len(row) && row[j].bg == row[i].bg {
				span += row[j].width
				j++
			}
			if row[i].bg != base {
				fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="%d" fill="%s"/>`+"\n", float64(col)*svgCellWidth, top, float64(span)*svgCellWidth, svgCellHeight, row[i].bg)
			}
			col += span
			i = j
		}
		col = 0
		for i := 0; i < len(row); {
			j, span := i, 0
			var run strings.Builder
			for j < len(row) && row[j].fg == row[i].fg && row[j].bold == row[i].bold {
				run.WriteString(row[j].text)
				span += row[j].width
				j++
			}
			if content := run.String(); strings.TrimSpace(content) != "" {
				weight := ""
				if row[i].bold {
					weight = ` font-weight="bold"`
				}
				fmt.Fprintf(&b, `<text x="%g" y="%g" fill="%s"%s textLength="%g" lengthAdjust="spacingAndGlyphs">%s</text>`+"\n",
					float64(col)*svgCellWidth, top+13.5, row[i].fg, weight, float64(span)*svgCellWidth, xmlEscape(content))
			}
			col += span
			i = j
		}
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}

var xmlEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace
