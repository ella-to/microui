package renderer

import (
	"testing"
	"time"

	"ella.to/microui"
)

// frameInput records what one parse call fed to a context: everything the
// context exposes for a frame.
type frameInput struct {
	text    string
	pressed microui.Key
	down    microui.Key
	mouse   microui.Vec2
	mdown   microui.MouseButton
	mpress  microui.MouseButton
	scroll  microui.Vec2
}

// parseFrames runs the parser over in until it is exhausted, one frame per
// parse call, like Poll does, and returns what each frame saw.
func parseFrames(t *testing.T, in string, now time.Time) []frameInput {
	t.Helper()
	var p inputParser
	ctx := microui.New()
	ctx.TextWidth = func(microui.Font, string) int { return 0 }
	ctx.TextHeight = func(microui.Font) int { return 1 }
	buf := []byte(in)
	var frames []frameInput
	for range 100 {
		res := p.parse(ctx, buf, now)
		ctx.Begin()
		frames = append(frames, frameInput{
			text: ctx.TextInput(), pressed: ctx.KeyPressed(), down: ctx.KeyDown(),
			mouse: ctx.MousePos(), mdown: ctx.MouseDown(), mpress: ctx.MousePressed(),
			scroll: ctx.ScrollDelta(),
		})
		ctx.End()
		if res.heldMods != 0 {
			ctx.InputKeyUp(res.heldMods)
		}
		buf = buf[res.n:]
		if !res.split {
			if len(buf) != 0 {
				t.Fatalf("parse left %q pending without a split", buf)
			}
			return frames
		}
	}
	t.Fatal("parser did not converge")
	return nil
}

func TestParseSplitsConflictingKeys(t *testing.T) {
	// two backspaces in one read must be two frames, not one
	f := parseFrames(t, "\x7f\x7f", time.Now())
	if len(f) != 2 || f[0].pressed != microui.KeyBackspace || f[1].pressed != microui.KeyBackspace {
		t.Fatalf("frames = %+v, want two Backspace frames", f)
	}
}

func TestParseKeepsKeyAndTextOrder(t *testing.T) {
	// "ab", Backspace, "c" must arrive as three frames in that order
	f := parseFrames(t, "ab\x7fc", time.Now())
	if len(f) != 3 || f[0].text != "ab" || f[1].pressed != microui.KeyBackspace || f[2].text != "c" {
		t.Fatalf("frames = %+v", f)
	}
}

func TestParseKeys(t *testing.T) {
	cases := []struct {
		in   string
		want microui.Key
	}{
		{"\x1b[A", microui.KeyUp},
		{"\x1b[B", microui.KeyDown},
		{"\x1bOH", microui.KeyHome}, // SS3 (application cursor mode)
		{"\x1b[4~", microui.KeyEnd},
		{"\x1b[5~", microui.KeyPageUp},
		{"\x1b[6~", microui.KeyPageDown},
		{"\x1b[3~", microui.KeyDelete},
		{"\t", microui.KeyTab},
		{"\x1b[Z", microui.KeyTab | microui.KeyShift},
		{"\x1b[1;2D", microui.KeyLeft | microui.KeyShift},
		{"\x1b[1;5C", microui.KeyRight | microui.KeyCtrl},
		{"\x01", microui.KeySelectAll},
		{"\r", microui.KeyReturn},
		{"\x1b[13;2u", microui.KeyReturn | microui.KeyShift}, // CSI u
		{"\x1b[97;5u", microui.KeySelectAll},                 // CSI u Ctrl-A
	}
	for _, tc := range cases {
		f := parseFrames(t, tc.in, time.Now())
		if len(f) != 1 || f[0].pressed != tc.want {
			t.Errorf("%q: frames = %+v, want pressed %b", tc.in, f, tc.want)
		}
	}
}

func TestParseLoneEscapeTimesOut(t *testing.T) {
	var p inputParser
	ctx := microui.New()
	t0 := time.Now()
	res := p.parse(ctx, []byte{0x1b}, t0)
	if res.n != 0 || !res.escWait {
		t.Fatalf("a fresh lone ESC must wait: %+v", res)
	}
	res = p.parse(ctx, []byte{0x1b}, t0.Add(escTimeout))
	if res.n != 1 || ctx.KeyPressed() != microui.KeyEscape {
		t.Fatalf("after the timeout ESC must be the Escape key: %+v pressed=%b", res, ctx.KeyPressed())
	}
}

func TestParseEscapeSequenceAcrossReads(t *testing.T) {
	var p inputParser
	ctx := microui.New()
	t0 := time.Now()
	if res := p.parse(ctx, []byte("\x1b["), t0); res.n != 0 {
		t.Fatalf("partial CSI consumed: %+v", res)
	}
	if res := p.parse(ctx, []byte("\x1b[A"), t0.Add(time.Millisecond)); res.n != 3 || ctx.KeyPressed() != microui.KeyUp {
		t.Fatalf("completed CSI: %+v pressed=%b", res, ctx.KeyPressed())
	}
}

func TestParseBracketedPaste(t *testing.T) {
	f := parseFrames(t, "\x1b[200~line one\r\nline\ttwo\x1b[201~", time.Now())
	if len(f) != 1 || f[0].text != "line one line two" || f[0].pressed != 0 {
		t.Fatalf("paste frames = %+v", f)
	}
	// an unterminated paste waits for the rest
	var p inputParser
	if res := p.parse(microui.New(), []byte("\x1b[200~partial"), time.Now().Add(time.Hour)); res.n != 0 {
		t.Fatalf("unterminated paste consumed early: %+v", res)
	}
}

func TestParseIncompleteUTF8Waits(t *testing.T) {
	var p inputParser
	ctx := microui.New()
	b := []byte("hé")
	res := p.parse(ctx, b[:len(b)-1], time.Now()) // cut inside 'é'
	if res.n != 1 || ctx.TextInput() != "h" {
		t.Fatalf("split rune: %+v text=%q", res, ctx.TextInput())
	}
}

func TestParseMouse(t *testing.T) {
	// press at (5,3) with Shift, motion, release, wheel down
	f := parseFrames(t, "\x1b[<4;6;4M\x1b[<32;8;4M\x1b[<0;8;4m\x1b[<65;2;2M", time.Now())
	if len(f) != 3 {
		t.Fatalf("frames = %+v, want press | motion+release.. split", f)
	}
	if f[0].mpress != microui.MouseLeft || f[0].mouse != (microui.Vec2{X: 5, Y: 3}) {
		t.Fatalf("press frame = %+v", f[0])
	}
	if f[0].down&microui.KeyShift == 0 {
		t.Fatalf("shift-click must hold KeyShift during its frame: %+v", f[0])
	}
	if f[1].down&microui.KeyShift != 0 {
		t.Fatalf("KeyShift must be released after the click frame: %+v", f[1])
	}
	if f[1].mouse != (microui.Vec2{X: 7, Y: 3}) || f[1].mdown != 0 {
		t.Fatalf("motion+release frame = %+v", f[1])
	}
	if f[2].scroll != (microui.Vec2{Y: 3}) || f[2].mouse != (microui.Vec2{X: 1, Y: 1}) {
		t.Fatalf("wheel frame = %+v", f[2])
	}
}

func TestParseControlActions(t *testing.T) {
	var p inputParser
	if res := p.parse(microui.New(), []byte("ab\x03cd"), time.Now()); !res.quit || res.n != 3 {
		t.Fatalf("Ctrl-C: %+v", res)
	}
	if res := p.parse(microui.New(), []byte("\x0c"), time.Now()); !res.redraw {
		t.Fatalf("Ctrl-L: %+v", res)
	}
	if res := p.parse(microui.New(), []byte("\x1a"), time.Now()); !res.suspend {
		t.Fatalf("Ctrl-Z: %+v", res)
	}
}
