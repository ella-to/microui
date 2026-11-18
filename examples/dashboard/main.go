// Command dashboard is a live, system-monitor style dashboard: gauges,
// sparklines, a bar chart and an event log fed by a background goroutine.
//
//	go run ella.to/microui/examples/dashboard
//
// It demonstrates the one concurrency rule of immediate-mode UIs: a
// *microui.Context and every piece of state the UI reads are owned by the UI
// goroutine. Background work (here, a simulated metrics producer) never
// touches that state directly; it sends values over a channel, and the UI
// drains the channel without blocking at the start of each frame. The few
// settings flowing the other way (rate, pause) cross over as atomics.
//
// The windows tile themselves to the terminal and re-tile when it is resized.
// Press Ctrl-C to quit.
package main

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// ---- the producer (runs on its own goroutine) -------------------------------

// sample is one tick of simulated metrics. It is a plain value: once sent over
// the channel the producer never touches it again, so no locking is needed.
type sample struct {
	cpu, mem float64 // percent, 0..100
	rps      float64 // requests per second
	event    string  // optional log line ("" when nothing happened)
}

// controls are the settings the UI shares with the producer. They are atomics
// because both goroutines access them; everything else is single-owner.
type controls struct {
	rateHz atomic.Int64 // samples per second
	paused atomic.Bool
}

var events = []string{
	"GET /api/users 200 (12ms)",
	"GET /api/orders 200 (31ms)",
	"POST /api/login 401",
	"cache miss: session:42",
	"worker 3 restarted",
	"WARN slow query 812ms",
	"deploy v1.4.2 rolled out",
	"GC pause 3.1ms",
}

// produce sends simulated samples to out at ctl.rateHz until ctx is done. It
// owns its random walk state; only finished samples leave the goroutine.
func produce(ctx context.Context, out chan<- sample, ctl *controls, rng *rand.Rand) {
	cpu, mem, phase := 35.0, 48.0, 0.0
	rate := max(ctl.rateHz.Load(), 1)
	tick := time.NewTicker(time.Second / time.Duration(rate))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		// pick up rate changes from the UI
		if r := max(ctl.rateHz.Load(), 1); r != rate {
			rate = r
			tick.Reset(time.Second / time.Duration(rate))
		}
		if ctl.paused.Load() {
			continue
		}

		cpu = min(max(cpu+rng.NormFloat64()*6, 2), 98)
		mem = min(max(mem+rng.NormFloat64()*0.8, 20), 95)
		phase += 0.15
		s := sample{
			cpu: cpu,
			mem: mem,
			rps: max(0, 250+150*math.Sin(phase)+rng.NormFloat64()*25),
		}
		if rng.Float64() < 0.2 {
			s.event = events[rng.IntN(len(events))]
		}

		// Never block forever: if the UI has gone away, ctx is done.
		select {
		case out <- s:
		case <-ctx.Done():
			return
		}
	}
}

// ---- UI-owned state ---------------------------------------------------------

// ring is a fixed-capacity FIFO keeping the most recent values. It is only
// ever used from the UI goroutine.
type ring[T any] struct {
	buf      []T
	start, n int
}

func newRing[T any](capacity int) *ring[T] { return &ring[T]{buf: make([]T, capacity)} }

// push appends v, evicting the oldest value when full.
func (r *ring[T]) push(v T) {
	if r.n < len(r.buf) {
		r.buf[(r.start+r.n)%len(r.buf)] = v
		r.n++
		return
	}
	r.buf[r.start] = v
	r.start = (r.start + 1) % len(r.buf)
}

func (r *ring[T]) len() int { return r.n }

// last returns the newest value (the zero value when empty).
func (r *ring[T]) last() T {
	var zero T
	if r.n == 0 {
		return zero
	}
	return r.buf[(r.start+r.n-1)%len(r.buf)]
}

// appendTail appends the newest k values, oldest first, to dst.
func (r *ring[T]) appendTail(dst []T, k int) []T {
	k = min(k, r.n)
	for i := r.n - k; i < r.n; i++ {
		dst = append(dst, r.buf[(r.start+i)%len(r.buf)])
	}
	return dst
}

const (
	historyLen = 256
	maxLogLen  = 200
)

// App is the dashboard. Every field is owned by the UI goroutine except ctl,
// which is shared with the producer through atomics.
type App struct {
	samples <-chan sample
	ctl     *controls
	size    func() (w, h int) // the drawable area, e.g. term.Size

	rateHz int  // bound to the rate slider; mirrored into ctl each frame
	paused bool // bound to the pause checkbox; mirrored into ctl

	cpu, mem, rps *ring[float64]
	received      int
	log           []string
	logAdded      bool

	lastW, lastH int       // terminal size the windows were last tiled for
	scratch      []float64 // reused per frame for history slices
}

// NewApp returns a dashboard reading samples and sharing ctl with a producer.
func NewApp(samples <-chan sample, ctl *controls, size func() (int, int)) *App {
	return &App{
		samples: samples,
		ctl:     ctl,
		size:    size,
		rateHz:  int(ctl.rateHz.Load()),
		cpu:     newRing[float64](historyLen),
		mem:     newRing[float64](historyLen),
		rps:     newRing[float64](historyLen),
	}
}

// drain moves every sample currently queued into the UI's own buffers. The
// select/default makes it non-blocking: a frame never waits for the producer.
func (a *App) drain() {
	for {
		select {
		case s := <-a.samples:
			a.received++
			a.cpu.push(s.cpu)
			a.mem.push(s.mem)
			a.rps.push(s.rps)
			if s.event != "" {
				a.addLog(s.event)
			}
		default:
			return
		}
	}
}

func (a *App) addLog(line string) {
	a.log = append(a.log, line)
	if over := len(a.log) - maxLogLen; over > 0 {
		a.log = append(a.log[:0], a.log[over:]...)
	}
	a.logAdded = true
}

// Window names double as their identities.
const (
	winCPU      = "CPU & Memory"
	winRequests = "Requests"
	winControls = "Controls"
	winEvents   = "Events"
)

// tile returns each window's rectangle for a w*h terminal.
func tile(w, h int) map[string]microui.Rect {
	topH := max(h*3/5, 10)
	leftW := w / 2
	ctrlW := max(w/3, 28)
	return map[string]microui.Rect{
		winCPU:      {X: 0, Y: 0, W: leftW, H: topH},
		winRequests: {X: leftW, Y: 0, W: w - leftW, H: topH},
		winControls: {X: 0, Y: topH, W: ctrlW, H: h - topH},
		winEvents:   {X: ctrlW, Y: topH, W: w - ctrlW, H: h - topH},
	}
}

// retile repositions the windows whenever the terminal size changes. A
// window's Rect argument is only its *initial* geometry, so to move a live
// window we write its retained container directly.
func (a *App) retile(ctx *microui.Context) map[string]microui.Rect {
	w, h := a.size()
	rects := tile(w, h)
	if w != a.lastW || h != a.lastH {
		for name, r := range rects {
			ctx.GetContainer(name).Rect = r
		}
		a.lastW, a.lastH = w, h
	}
	return rects
}

// ui builds one frame. It must run on the UI goroutine (renderer.Run calls it).
func (a *App) ui(ctx *microui.Context) {
	a.drain()
	rects := a.retile(ctx)

	a.cpuWindow(ctx, rects[winCPU])
	a.requestsWindow(ctx, rects[winRequests])
	a.controlsWindow(ctx, rects[winControls])
	a.eventsWindow(ctx, rects[winEvents])

	// Publish this frame's settings to the producer.
	a.ctl.rateHz.Store(int64(a.rateHz))
	a.ctl.paused.Store(a.paused)
}

var (
	cpuColor = microui.Hex(0x4fb3d9)
	memColor = microui.Hex(0xb48ead)
	rpsColor = microui.Hex(0x7fc97f)
)

func (a *App) cpuWindow(ctx *microui.Context, r microui.Rect) {
	if !ctx.BeginWindowEx(winCPU, r, microui.OptNoClose).Active() {
		return
	}
	defer ctx.EndWindow()

	cpu, mem := a.cpu.last(), a.mem.last()
	ctx.LayoutRow([]int{5, -1}, 1)
	ctx.Label("CPU")
	gauge(ctx, fmt.Sprintf("%.0f%%", cpu), cpu/100)
	ctx.Label("MEM")
	gauge(ctx, fmt.Sprintf("%.0f%%", mem), mem/100)

	// Split the remaining height between the two history sparklines. Above
	// them sit four 1-row items (two gauges, two titles) and five spacings.
	inner := ctx.GetCurrentContainer().Body.H - 2*ctx.Style.Padding
	rows := max((inner-4-5*ctx.Style.Spacing)/2, 1)
	ctx.LayoutRow([]int{-1}, 1)
	ctx.Label("CPU history")
	ctx.LayoutRow([]int{-1}, rows)
	a.scratch = a.cpu.appendTail(a.scratch[:0], historyLen)
	sparkline(ctx, a.scratch, 100, cpuColor)
	ctx.LayoutRow([]int{-1}, 1)
	ctx.Label("Memory history")
	ctx.LayoutRow([]int{-1}, rows)
	a.scratch = a.mem.appendTail(a.scratch[:0], historyLen)
	sparkline(ctx, a.scratch, 100, memColor)
}

func (a *App) requestsWindow(ctx *microui.Context, r microui.Rect) {
	if !ctx.BeginWindowEx(winRequests, r, microui.OptNoClose).Active() {
		return
	}
	defer ctx.EndWindow()

	a.scratch = a.rps.appendTail(a.scratch[:0], historyLen)
	peak := 1.0
	for _, v := range a.scratch {
		peak = max(peak, v)
	}
	ctx.LayoutRow([]int{-1}, 1)
	ctx.Label(fmt.Sprintf("now %.0f req/s   peak %.0f", a.rps.last(), peak))
	ctx.LayoutRow([]int{-1}, -1)
	barChart(ctx, a.scratch, peak, rpsColor)
}

func (a *App) controlsWindow(ctx *microui.Context, r microui.Rect) {
	if !ctx.BeginWindowEx(winControls, r, microui.OptNoClose).Active() {
		return
	}
	defer ctx.EndWindow()

	// 1-row controls: four of them fit the 7 rows this window gets at 80x24.
	ctx.LayoutRow([]int{-1}, 1)
	if ctx.Checkbox("Paused", &a.paused).Changed() {
		if a.paused {
			a.addLog("producer paused")
		} else {
			a.addLog("producer resumed")
		}
	}
	ctx.LayoutRow([]int{8, -1}, 1)
	ctx.Label("Rate Hz")
	ctx.Slider(&a.rateHz, 1, 20) // an int slider: snaps to whole numbers
	ctx.Label("Samples")
	ctx.Label(strconv.Itoa(a.received))
	ctx.LayoutRow([]int{-1}, 1)
	if ctx.Button("Clear log").Submitted() {
		a.log = a.log[:0]
	}
}

func (a *App) eventsWindow(ctx *microui.Context, r microui.Rect) {
	if !ctx.BeginWindowEx(winEvents, r, microui.OptNoClose).Active() {
		return
	}
	defer ctx.EndWindow()

	ctx.LayoutRow([]int{-1}, -1)
	ctx.BeginPanel("log")
	panel := ctx.GetCurrentContainer()
	// Follow the tail only if the user has not scrolled up to read history.
	// ContentSize is last frame's, Body and Scroll are this frame's (already
	// clamped), which is exactly what "were we at the bottom?" needs.
	maxScroll := panel.ContentSize.Y + 2*ctx.Style.Padding - panel.Body.H
	atBottom := panel.Scroll.Y >= maxScroll-1
	ctx.LayoutRow([]int{-1}, 1)
	for _, line := range a.log {
		ctx.Label(line)
	}
	ctx.EndPanel()
	if a.logAdded && atBottom {
		panel.Scroll.Y = panel.ContentSize.Y // clamped to the max next frame
	}
	a.logAdded = false
}

// ---- custom widgets built from primitives -----------------------------------

// gaugeColor shades from green through yellow to red as frac rises.
func gaugeColor(frac float64) microui.Color {
	green, yellow, red := microui.Hex(0x2e9e5b), microui.Hex(0xc9a227), microui.Hex(0xd64545)
	if frac < 0.6 {
		return green.Lerp(yellow, frac/0.6)
	}
	return yellow.Lerp(red, (frac-0.6)/0.4)
}

// gauge draws a horizontal bar filled to frac (0..1) in the next layout cell,
// with label centered on top.
func gauge(ctx *microui.Context, label string, frac float64) {
	r := ctx.LayoutNext()
	frac = min(max(frac, 0), 1)
	ctx.DrawRect(r, ctx.Style.Colors[microui.ColorBase])
	fill := r
	fill.W = int(frac*float64(r.W) + 0.5)
	ctx.DrawRect(fill, gaugeColor(frac))
	ctx.DrawControlText(label, r, microui.ColorText, microui.OptAlignCenter)
}

// sparkGlyphs are the eighth-height block elements, index = eighths filled.
var sparkGlyphs = []rune(" ▁▂▃▄▅▆▇█")

// sparkline draws vals (newest last, right-aligned) as a block-glyph chart
// filling the next layout cell. Each cell row resolves 8 levels, so a cell
// r.H rows tall shows r.H*8 levels.
func sparkline(ctx *microui.Context, vals []float64, maxV float64, color microui.Color) {
	r := ctx.LayoutNext()
	if r.W <= 0 || r.H <= 0 || maxV <= 0 {
		return
	}
	if len(vals) > r.W {
		vals = vals[len(vals)-r.W:]
	}
	levels := r.H * 8
	line := make([]rune, len(vals))
	font := ctx.Style.Font
	x := r.X + r.W - len(vals)
	for row := range r.H {
		below := (r.H - 1 - row) * 8 // levels under this row
		for i, v := range vals {
			lv := int(v/maxV*float64(levels) + 0.5)
			line[i] = sparkGlyphs[min(max(lv-below, 0), 8)]
		}
		ctx.DrawText(font, string(line), microui.Vec2{X: x, Y: r.Y + row}, color)
	}
}

// barChart draws vals as vertical 1-cell bars with 1-cell gaps, newest on
// the right, scaled so maxV fills the cell height. Unlike sparkline it uses
// filled rectangles, so it works with any renderer, not just text ones.
func barChart(ctx *microui.Context, vals []float64, maxV float64, color microui.Color) {
	r := ctx.LayoutNext()
	if r.W <= 0 || r.H <= 0 || maxV <= 0 {
		return
	}
	n := min(len(vals), (r.W+1)/2)
	vals = vals[len(vals)-n:]
	dim := color.Lerp(ctx.Style.Colors[microui.ColorWindowBG], 0.5)
	for i, v := range vals {
		h := max(int(v/maxV*float64(r.H)+0.5), 1)
		bar := microui.Rect{X: r.X + r.W - (n-i)*2 + 1, Y: r.Y + r.H - h, W: 1, H: h}
		c := dim
		if i == n-1 {
			c = color // highlight the newest bar
		}
		ctx.DrawRect(bar, c)
	}
}

// ---- main -------------------------------------------------------------------

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctl := &controls{}
	ctl.rateHz.Store(8)
	samples := make(chan sample, 64)

	done := make(chan struct{})
	go func() {
		defer close(done)
		produce(ctx, samples, ctl, rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())))
	}()

	app := NewApp(samples, ctl, term.Size)
	err = renderer.Run(term, app.ui, renderer.WithFPS(20), renderer.WithContext(ctx))

	// Stop the producer and wait for it, so nothing outlives the UI.
	cancel()
	<-done
	if err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		os.Exit(1)
	}
}
