package operator

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/kubetidy/kubetidy/internal/model"
)

// countingLister signals (via a buffered channel) every time List is invoked, which happens
// once per Tick. It lets Run tests synchronise on ticks without sleeping.
type countingLister struct {
	mu        sync.Mutex
	count     int
	ticked    chan struct{}
	workloads []model.Workload
	err       error
}

func (l *countingLister) List(_ context.Context) ([]model.Workload, error) {
	l.mu.Lock()
	l.count++
	l.mu.Unlock()
	select {
	case l.ticked <- struct{}{}:
	default:
	}
	return l.workloads, l.err
}

func (l *countingLister) ticks() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count
}

func discardLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func TestRun_ImmediateTickThenCancel(t *testing.T) {
	lister := &countingLister{ticked: make(chan struct{}, 8)}
	c := NewCollector(lister, &fakeSampler{}, newFakeStore(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	opts := Options{ScrapeInterval: 5 * time.Millisecond, Logger: discardLogger()}

	done := make(chan error, 1)
	go func() { done <- Run(ctx, c, opts) }()

	// Immediate tick.
	select {
	case <-lister.ticked:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timed out waiting for immediate tick")
	}
	// At least one ticker-driven tick.
	select {
	case <-lister.ticked:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timed out waiting for ticker tick")
	}

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if lister.ticks() < 2 {
		t.Errorf("expected at least 2 ticks, got %d", lister.ticks())
	}
}

func TestRun_DefaultsAppliedNilLogger(t *testing.T) {
	// ScrapeInterval <= 0 -> default 30s; nil Logger -> log.Default(). Cancel right after the
	// immediate tick so the long default interval never fires and the test stays fast.
	lister := &countingLister{ticked: make(chan struct{}, 1)}
	c := NewCollector(lister, &fakeSampler{}, newFakeStore(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, c, Options{ScrapeInterval: 0, Logger: nil}) }()

	select {
	case <-lister.ticked:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timed out waiting for immediate tick")
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRun_TickErrorsAreLoggedNotFatal(t *testing.T) {
	// A lister that always errors makes every Tick fail; Run must log and keep looping until
	// the context is cancelled rather than returning the tick error.
	lister := &countingLister{ticked: make(chan struct{}, 8), err: errBoom}
	c := NewCollector(lister, &fakeSampler{}, newFakeStore(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	opts := Options{ScrapeInterval: 5 * time.Millisecond, Logger: discardLogger()}

	done := make(chan error, 1)
	go func() { done <- Run(ctx, c, opts) }()

	for i := 0; i < 2; i++ {
		select {
		case <-lister.ticked:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatalf("timed out waiting for tick %d", i)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestOptions_WithDefaults(t *testing.T) {
	got := Options{}.withDefaults()
	if got.ScrapeInterval != 30*time.Second {
		t.Errorf("default ScrapeInterval = %v, want 30s", got.ScrapeInterval)
	}
	if got.Logger == nil {
		t.Error("default Logger should not be nil")
	}

	custom := Options{ScrapeInterval: 7 * time.Second, Logger: discardLogger()}.withDefaults()
	if custom.ScrapeInterval != 7*time.Second {
		t.Errorf("custom ScrapeInterval overwritten: %v", custom.ScrapeInterval)
	}
}
