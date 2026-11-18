package renderer

import (
	"context"
	"errors"
	"testing"

	"ella.to/microui"
)

// fakeDriver is a headless terminal that quits after maxFrames polls and
// records how Run drove it.
type fakeDriver struct {
	*Terminal
	polls, maxFrames int
	closed           int
	err              error
}

func (f *fakeDriver) Poll(*microui.Context) bool { f.polls++; return f.polls > f.maxFrames }
func (f *fakeDriver) Present()                   {}
func (f *fakeDriver) Close() error               { f.closed++; return nil }
func (f *fakeDriver) Err() error                 { return f.err }

func TestRunStopsOnContextCancel(t *testing.T) {
	d := &fakeDriver{Terminal: NewHeadless(20, 5), maxFrames: 1 << 30}
	ctx, cancel := context.WithCancel(context.Background())
	frames := 0
	err := Run(d, func(ui *microui.Context) {
		frames++
		if frames == 3 {
			cancel()
		}
	}, WithContext(ctx), WithFPS(1000))
	if err != nil {
		t.Fatalf("Run returned %v after cancel, want nil", err)
	}
	if frames != 3 {
		t.Fatalf("ran %d frames, want 3", frames)
	}
	if d.closed != 1 {
		t.Fatalf("driver closed %d times, want 1", d.closed)
	}
}

func TestRunReportsDriverError(t *testing.T) {
	boom := errors.New("input failed")
	d := &fakeDriver{Terminal: NewHeadless(20, 5), maxFrames: 2, err: boom}
	setupRan := false
	err := Run(d, func(*microui.Context) {}, WithFPS(1000), WithSetup(func(c *microui.Context) {
		setupRan = c.Style.TitleHeight == 1 // the driver's style is already installed
	}))
	if !errors.Is(err, boom) {
		t.Fatalf("Run returned %v, want %v", err, boom)
	}
	if !setupRan {
		t.Fatal("WithSetup did not run with the driver style installed")
	}
}

func TestRunClosesDriverOnPanic(t *testing.T) {
	d := &fakeDriver{Terminal: NewHeadless(20, 5), maxFrames: 10}
	defer func() {
		if recover() == nil {
			t.Fatal("expected the ui panic to propagate")
		}
		if d.closed != 1 {
			t.Fatalf("driver closed %d times after panic, want 1", d.closed)
		}
	}()
	Run(d, func(*microui.Context) { panic("ui bug") }, WithFPS(1000))
}
