package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// spinner is a tiny, dependency-free progress indicator written to stderr. It animates only
// when stderr is an interactive terminal, so piped/redirected output (and JSON mode) stays
// clean. It exists to kill the "is it stuck?" feeling during a scan: cluster discovery and
// the per-workload usage queries can take many seconds on a large cluster.
type spinner struct {
	mu      sync.Mutex
	status  string
	frames  []string
	stopCh  chan struct{}
	doneCh  chan struct{}
	enabled bool
}

// newSpinner returns a spinner. It is enabled only when stderr is a TTY; otherwise every
// method is a no-op and nothing is written.
func newSpinner(initial string) *spinner {
	return &spinner{
		status:  initial,
		frames:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		enabled: term.IsTerminal(int(os.Stderr.Fd())),
	}
}

// start begins the animation loop in a goroutine. Safe to call once.
func (s *spinner) start() {
	if !s.enabled {
		return
	}
	go func() {
		defer close(s.doneCh)
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.mu.Lock()
				frame := s.frames[i%len(s.frames)]
				status := s.status
				s.mu.Unlock()
				// \r returns to column 0; \033[K clears to end of line.
				fmt.Fprintf(os.Stderr, "\r\033[K%s %s", frame, status)
				i++
			}
		}
	}()
}

// update changes the message shown next to the spinner.
func (s *spinner) update(status string) {
	if !s.enabled {
		return
	}
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

// scanPhrases narrate the scan as it advances — a little personality for the wait. The phrase
// is chosen by progress fraction, so it reads like a story from start to finish.
var scanPhrases = []string{
	"reading usage from the cluster…",
	"crunching p50 / p95 / p99…",
	"hunting over-provisioned pods…",
	"pricing the waste in dollars…",
	"checking memory for OOM traps…",
	"scoring cluster efficiency…",
	"tidying up…",
}

// scanProgress renders a catchy progress line: a mini bar, the count, and a phrase that advances
// with the scan, e.g. "⟦▰▰▰▰▰▱▱▱▱▱⟧ 51/102 · pricing the waste in dollars…".
func scanProgress(done, total int) string {
	const w = 14
	if total < 1 {
		total = 1
	}
	if done > total {
		done = total
	}
	filled := done * w / total
	bar := strings.Repeat("▰", filled) + strings.Repeat("▱", w-filled)
	idx := done * len(scanPhrases) / total
	if idx >= len(scanPhrases) {
		idx = len(scanPhrases) - 1
	}
	return fmt.Sprintf("⟦%s⟧ %d/%d · %s", bar, done, total, scanPhrases[idx])
}

// finish halts the animation and clears the spinner line so the report renders cleanly.
func (s *spinner) finish() {
	if !s.enabled {
		return
	}
	close(s.stopCh)
	<-s.doneCh
	fmt.Fprint(os.Stderr, "\r\033[K")
}
