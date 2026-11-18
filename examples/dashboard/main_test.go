package main

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// harness wires an App to a headless terminal and a hand-fed channel, so the
// UI runs deterministically without a producer goroutine.
type harness struct {
	app     *App
	ctx     *microui.Context
	samples chan sample
	ctl     *controls
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	term := renderer.NewHeadless(80, 24) // the smallest supported terminal
	ctx := microui.New()
	renderer.Connect(ctx, term)
	st := term.Style()
	ctx.Style = &st
	ctl := &controls{}
	ctl.rateHz.Store(5)
	samples := make(chan sample, 64)
	return &harness{
		app:     NewApp(samples, ctl, term.Size),
		ctx:     ctx,
		samples: samples,
		ctl:     ctl,
	}
}

func (h *harness) frame() {
	h.ctx.Begin()
	h.app.ui(h.ctx)
	h.ctx.End()
}

// allText joins every text command of the last frame.
func (h *harness) allText() string {
	var b strings.Builder
	for cmd := range h.ctx.Commands() {
		if cmd.Type == microui.CommandText {
			b.WriteString(cmd.Str)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// textPos returns the position of the first text command containing s.
func (h *harness) textPos(s string) (microui.Vec2, bool) {
	for cmd := range h.ctx.Commands() {
		if cmd.Type == microui.CommandText && strings.Contains(cmd.Str, s) {
			return cmd.Pos, true
		}
	}
	return microui.Vec2{}, false
}

// click hovers p for two frames (hover root, then hover) and clicks it.
func (h *harness) click(p microui.Vec2) {
	h.ctx.InputMouseMove(p.X, p.Y)
	h.frame()
	h.frame()
	h.ctx.InputMouseDown(p.X, p.Y, microui.MouseLeft)
	h.frame()
	h.ctx.InputMouseUp(p.X, p.Y, microui.MouseLeft)
	h.frame()
}

func TestDrainMovesQueuedSamplesIntoBuffers(t *testing.T) {
	h := newHarness(t)
	for i := range 5 {
		s := sample{cpu: float64(10 * i), mem: 50, rps: 100}
		if i == 2 {
			s.event = "deploy done"
		}
		h.samples <- s
	}
	h.frame()

	if h.app.received != 5 || h.app.cpu.len() != 5 {
		t.Fatalf("drained %d samples (cpu ring %d), want 5", h.app.received, h.app.cpu.len())
	}
	if got := h.app.cpu.last(); got != 40 {
		t.Fatalf("newest cpu = %v, want 40", got)
	}
	if len(h.app.log) != 1 || h.app.log[0] != "deploy done" {
		t.Fatalf("log = %q, want the one event", h.app.log)
	}
	if len(h.samples) != 0 {
		t.Fatalf("%d samples left queued after a frame", len(h.samples))
	}
	// An empty channel must not block the frame.
	h.frame()
}

func TestRingKeepsNewestValues(t *testing.T) {
	r := newRing[int](3)
	for i := range 5 {
		r.push(i)
	}
	got := r.appendTail(nil, 10)
	if len(got) != 3 || got[0] != 2 || got[2] != 4 || r.last() != 4 {
		t.Fatalf("ring tail = %v (last %d), want [2 3 4]", got, r.last())
	}
}

func TestWindowsRetileOnResize(t *testing.T) {
	h := newHarness(t)
	w, ht := 120, 40
	h.app.size = func() (int, int) { return w, ht }
	h.frame()
	if got, want := h.ctx.GetContainer(winEvents).Rect, tile(120, 40)[winEvents]; got != want {
		t.Fatalf("events window = %+v, want %+v", got, want)
	}
	w, ht = 90, 30
	h.frame()
	if got, want := h.ctx.GetContainer(winEvents).Rect, tile(90, 30)[winEvents]; got != want {
		t.Fatalf("after resize events window = %+v, want %+v", got, want)
	}
}

func TestControlsPublishToProducer(t *testing.T) {
	h := newHarness(t)
	h.frame()
	p, ok := h.textPos("Paused")
	if !ok {
		t.Fatal("Paused checkbox not drawn")
	}
	h.click(p) // the whole row, label included, toggles the checkbox
	if !h.app.paused || !h.ctl.paused.Load() {
		t.Fatalf("pause not published: app=%v atomic=%v", h.app.paused, h.ctl.paused.Load())
	}
	h.app.rateHz = 17
	h.frame()
	if got := h.ctl.rateHz.Load(); got != 17 {
		t.Fatalf("rate atomic = %d, want 17", got)
	}
}

func TestProducerStopsOnCancel(t *testing.T) {
	ctl := &controls{}
	ctl.rateHz.Store(1000)
	out := make(chan sample) // unbuffered: the producer must not wedge on it
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		produce(ctx, out, ctl, rand.New(rand.NewPCG(1, 2)))
	}()
	for range 3 {
		select {
		case s := <-out:
			if s.cpu < 0 || s.cpu > 100 || s.mem < 0 || s.mem > 100 {
				t.Fatalf("sample out of range: %+v", s)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("producer did not send")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("producer did not stop after cancel")
	}
}

func TestRendersWithHistory(t *testing.T) {
	h := newHarness(t)
	rng := rand.New(rand.NewPCG(3, 4))
	for range 40 {
		for range 3 {
			h.samples <- sample{cpu: rng.Float64() * 100, mem: 60, rps: rng.Float64() * 400, event: "tick"}
		}
		h.frame()
	}
	text := h.allText()
	for _, want := range []string{winCPU, winRequests, winEvents, "req/s", "tick"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered screen missing %q", want)
		}
	}
	if !strings.ContainsAny(text, "▁▂▃▄▅▆▇█") {
		t.Error("no sparkline glyphs on screen")
	}
}

func TestTilesFitSmallTerminal(t *testing.T) {
	h := newHarness(t)
	h.frame()
	h.frame() // ContentSize is recorded at the end of a frame
	pad := h.ctx.Style.Padding
	for _, name := range []string{winCPU, winRequests, winControls, winEvents} {
		cnt := h.ctx.GetContainer(name)
		if need := cnt.ContentSize.Y + 2*pad; need > cnt.Body.H {
			t.Errorf("%s needs %d rows but has %d: content is cut off", name, need, cnt.Body.H)
		}
	}
	for _, want := range []string{"Memory history", "Samples", "Clear log"} {
		if _, ok := h.textPos(want); !ok {
			t.Errorf("%q not drawn at 80x24", want)
		}
	}
}
