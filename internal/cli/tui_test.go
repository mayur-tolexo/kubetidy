package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kubetidy/kubetidy/internal/model"
)

func tuiFixture() model.ScanResult {
	rec := model.Recommendation{
		Workload:       model.Workload{Kind: model.KindDeployment, Name: "checkout-api", Namespace: "shop"},
		ContainerName:  "app",
		Current:        model.ResourceSpec{Requests: model.ResourceAmounts{CPUMillicores: 2000, MemoryBytes: 4 << 30}},
		Proposed:       model.ResourceSpec{Requests: model.ResourceAmounts{CPUMillicores: 10, MemoryBytes: 315 << 20}},
		MonthlySavings: 60,
		Confidence:     model.Confidence{Score: 0.85, Reason: "tier 1, 14d"},
		Tier:           model.TierHistorical,
		Usage:          model.UsageStats{CPUMillicores: model.Percentiles{P95: 2, Max: 2}, MemoryBytes: model.Percentiles{P95: 169 << 20, Max: 147 << 20}, Tier: model.TierHistorical},
		Explanation:    []string{"cpu request = p95 2m + 15% headroom = 10m"},
	}
	return model.ScanResult{Context: "prod", TotalMonthlyWaste: 60, Recommendations: []model.Recommendation{rec}}
}

func TestTUIModel_NavigateAndQuit(t *testing.T) {
	m := newTUIModel(tuiFixture())

	// Size it (list/viewport need a window).
	mi, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = mi.(tuiModel)
	if !m.ready {
		t.Fatal("model should be ready after WindowSizeMsg")
	}
	if !strings.Contains(m.View(), "checkout-api") {
		t.Errorf("list view should list the workload\n%s", m.View())
	}

	// Enter opens the detail pane (the --explain block).
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(tuiModel)
	if !m.showing {
		t.Fatal("enter should open detail view")
	}
	if !strings.Contains(m.View(), "how it's sized") {
		t.Errorf("detail should render the --explain block\n%s", m.View())
	}

	// Esc returns to the list.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mi.(tuiModel)
	if m.showing {
		t.Error("esc should return to the list")
	}

	// Ctrl+C quits.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("ctrl+c should return a quit command")
	}
}
