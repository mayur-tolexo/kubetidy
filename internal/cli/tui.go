package cli

import (
	"bytes"
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kubetidy/kubetidy/internal/model"
	"github.com/kubetidy/kubetidy/internal/report"
)

// recItem adapts a Recommendation to a bubbles/list item.
type recItem struct{ rec model.Recommendation }

func (r recItem) Title() string { return r.rec.Workload.Name }

func (r recItem) Description() string {
	c := r.rec
	return fmt.Sprintf("%s/%s · save %s · %s · cpu %dm→%dm · mem %s→%s",
		c.Workload.Kind, c.Workload.Namespace,
		signedDollars(c.MonthlySavings)+"/mo",
		bandGlyph(c.Confidence.Band())+" "+string(c.Confidence.Band()),
		c.Current.Requests.CPUMillicores, c.Proposed.Requests.CPUMillicores,
		tuiMem(c.Current.Requests.MemoryBytes), tuiMem(c.Proposed.Requests.MemoryBytes),
	)
}

// FilterValue drives the built-in "/" filter: namespace + name + kind.
func (r recItem) FilterValue() string {
	return r.rec.Workload.Namespace + "/" + r.rec.Workload.Name + " " + string(r.rec.Workload.Kind)
}

// tuiModel is the browsable scan TUI: a filterable list of recommendations and a scrollable
// detail pane (the same --explain block). Two views toggle: list ⇄ detail.
type tuiModel struct {
	list      list.Model
	detail    viewport.Model
	recs      []model.Recommendation
	showing   bool // detail view active
	ready     bool
	headerTxt string
}

var tuiHelpStyle = lipgloss.NewStyle().Faint(true)

// newTUIModel builds the model from a scan result.
func newTUIModel(result model.ScanResult) tuiModel {
	items := make([]list.Item, 0, len(result.Recommendations))
	for _, r := range result.Recommendations {
		items = append(items, recItem{rec: r})
	}
	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = fmt.Sprintf("kubetidy · %s · %d recommendations · %s/mo waste",
		ctxOr(result.Context), len(result.Recommendations), dollars(result.TotalMonthlyWaste))
	l.SetShowHelp(true)
	return tuiModel{
		list:      l,
		recs:      result.Recommendations,
		headerTxt: l.Title,
	}
}

func (m tuiModel) Init() tea.Cmd { return nil }

// Update handles input + resize. List view: enter opens detail, / filters, q/ctrl+c quits.
// Detail view: esc returns to the list, q/ctrl+c quits, ↑/↓ scroll.
func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height-1)
		m.detail = viewport.New(msg.Width, msg.Height-2)
		m.ready = true
		if m.showing {
			m.detail.SetContent(m.selectedDetail())
		}
		return m, nil

	case tea.KeyMsg:
		// While filtering, let the list consume keys (so "q"/"enter" edit the filter).
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			if !m.showing && m.list.SelectedItem() != nil {
				m.showing = true
				m.detail.SetContent(m.selectedDetail())
				m.detail.GotoTop()
				return m, nil
			}
		case "esc":
			if m.showing {
				m.showing = false
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	if m.showing {
		m.detail, cmd = m.detail.Update(msg)
	} else {
		m.list, cmd = m.list.Update(msg)
	}
	return m, cmd
}

func (m tuiModel) View() string {
	if !m.ready {
		return "loading…"
	}
	if m.showing {
		return m.detail.View() + "\n" + tuiHelpStyle.Render("  ↑/↓ scroll · esc back · q quit")
	}
	return m.list.View()
}

// selectedDetail renders the --explain block for the highlighted recommendation.
func (m tuiModel) selectedDetail() string {
	it, ok := m.list.SelectedItem().(recItem)
	if !ok {
		return "no selection"
	}
	var b bytes.Buffer
	_ = report.Explain(&b, it.rec)
	return b.String()
}

func bandGlyph(b model.ConfidenceBand) string {
	switch b {
	case model.ConfidenceHigh:
		return "█"
	case model.ConfidenceMedium:
		return "▓"
	default:
		return "▒"
	}
}

func ctxOr(s string) string {
	if s == "" {
		return "(no context)"
	}
	return s
}

// tuiMem renders a byte count adaptively (Mi below 1Gi, Gi above) for the compact list line.
func tuiMem(bytes int64) string {
	const mi = 1 << 20
	const gi = 1 << 30
	switch {
	case bytes <= 0:
		return "0"
	case bytes < gi:
		return fmt.Sprintf("%dMi", (bytes+mi/2)/mi)
	default:
		return fmt.Sprintf("%.1fGi", float64(bytes)/gi)
	}
}
