package main

import (
	"strings"
	"testing"
	"testing/fstest"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

const screenW, screenH = 120, 40

// harness drives the browser headlessly, one frame per call.
type harness struct {
	t   *testing.T
	b   *browser
	ctx *microui.Context
}

func newHarness(t *testing.T, fsys fstest.MapFS) *harness {
	term := renderer.NewHeadless(screenW, screenH)
	ctx := microui.New()
	renderer.Connect(ctx, term)
	st := term.Style()
	ctx.Style = &st
	h := &harness{t: t, b: newBrowser(fsys, "test"), ctx: ctx}
	// Two frames: the first lays windows out before their bodies are known,
	// so control positions only settle on the second.
	h.frame()
	h.frame()
	return h
}

func (h *harness) frame() {
	h.ctx.Begin()
	h.b.ui(h.ctx, screenW, screenH)
	h.ctx.End()
}

// texts returns every string drawn in the last frame.
func (h *harness) texts() []string {
	var out []string
	for cmd := range h.ctx.Commands() {
		if cmd.Type == microui.CommandText {
			out = append(out, cmd.Str)
		}
	}
	return out
}

// find returns where label was drawn in the last frame.
func (h *harness) find(label string) microui.Vec2 {
	h.t.Helper()
	for cmd := range h.ctx.Commands() {
		if cmd.Type == microui.CommandText && cmd.Str == label {
			return cmd.Pos
		}
	}
	h.t.Fatalf("%q not on screen; drawn: %q", label, h.texts())
	return microui.Vec2{}
}

// click hovers label for two frames (hover root, then hover), then presses
// and releases the left button on it.
func (h *harness) click(label string) {
	p := h.find(label)
	h.ctx.InputMouseMove(p.X, p.Y)
	h.frame()
	h.frame()
	h.ctx.InputMouseDown(p.X, p.Y, microui.MouseLeft)
	h.frame()
	h.ctx.InputMouseUp(p.X, p.Y, microui.MouseLeft)
	h.frame()
}

func (h *harness) onScreen(s string) bool {
	for _, t := range h.texts() {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"docs/readme.txt":     {Data: []byte("hello preview\nsecond line")},
		"docs/img.bin":        {Data: []byte{0x89, 'P', 'N', 'G', 0, 1, 2}},
		"other/docs/deep.txt": {Data: []byte("deep")},
		"top.txt":             {Data: []byte("top level")},
	}
}

func TestExpandAndPreview(t *testing.T) {
	h := newHarness(t, testFS())

	// Lazy loading: only the root has been read so far.
	if _, ok := h.b.cache["docs"]; ok {
		t.Fatal("docs was read before its node was expanded")
	}
	if h.onScreen("readme.txt") {
		t.Fatal("collapsed folder's files are visible")
	}

	h.click("docs/")
	if _, ok := h.b.cache["docs"]; !ok {
		t.Fatal("expanding docs/ did not read it")
	}

	h.click("readme.txt")
	if h.b.selected != "docs/readme.txt" {
		t.Fatalf("selected = %q, want docs/readme.txt", h.b.selected)
	}
	if !h.onScreen("hello preview") || !h.onScreen("second line") {
		t.Fatalf("preview not shown; drawn: %q", h.texts())
	}
	if !h.onScreen("▸ readme.txt") {
		t.Fatal("selected file is not marked in the tree")
	}
}

func TestBinaryFileIsNotShown(t *testing.T) {
	h := newHarness(t, testFS())
	h.click("docs/")
	h.click("img.bin")
	if !strings.Contains(h.b.preview, "binary file") {
		t.Fatalf("binary preview = %q, want a binary-file notice", h.b.preview)
	}
	if h.onScreen("PNG") {
		t.Fatal("binary contents leaked into the preview")
	}
}

func TestSameNamedFoldersAreIndependent(t *testing.T) {
	h := newHarness(t, testFS())
	h.click("other/")
	// "docs/" now appears twice; expanding the nested one must not expand
	// the top-level one, which would happen if they shared an id.
	var nested microui.Vec2
	for cmd := range h.ctx.Commands() {
		if cmd.Type == microui.CommandText && cmd.Str == "docs/" {
			nested = cmd.Pos // the last one drawn is other/docs/
		}
	}
	h.ctx.InputMouseMove(nested.X, nested.Y)
	h.frame()
	h.frame()
	h.ctx.InputMouseDown(nested.X, nested.Y, microui.MouseLeft)
	h.frame()
	h.ctx.InputMouseUp(nested.X, nested.Y, microui.MouseLeft)
	h.frame()

	if !h.onScreen("deep.txt") {
		t.Fatal("nested docs/ did not expand")
	}
	if h.onScreen("readme.txt") {
		t.Fatal("expanding other/docs/ also expanded the top-level docs/")
	}
}

func TestRefreshRereads(t *testing.T) {
	fsys := testFS()
	h := newHarness(t, fsys)
	fsys["new.txt"] = &fstest.MapFile{Data: []byte("x")}
	h.frame()
	if h.onScreen("new.txt") {
		t.Fatal("cached listing should not see the new file yet")
	}
	h.click("Refresh")
	if !h.onScreen("new.txt") {
		t.Fatal("Refresh did not re-read the directory")
	}
}

func TestLoadPreviewTruncates(t *testing.T) {
	long := strings.Repeat("line\n", maxPreviewLines+50)
	_, text := loadPreview(fstest.MapFS{"f": {Data: []byte(long)}}, "f")
	if !strings.HasSuffix(text, "(truncated)") {
		t.Fatal("long file was not marked truncated")
	}
	if n := strings.Count(text, "line"); n != maxPreviewLines {
		t.Fatalf("preview has %d lines, want %d", n, maxPreviewLines)
	}
}

func TestFileNamedLikeAButton(t *testing.T) {
	fsys := testFS()
	fsys["Refresh"] = &fstest.MapFile{Data: []byte("a file, not the button")}
	h := newHarness(t, fsys)
	fsys["new.txt"] = &fstest.MapFile{Data: []byte("x")}
	h.click("Refresh") // the first "Refresh" drawn is the button
	if h.b.selected != "" {
		t.Fatalf("clicking the Refresh button opened file %q", h.b.selected)
	}
	if !h.onScreen("new.txt") {
		t.Fatal("the Refresh button did not refresh")
	}
}

func TestWindowsFollowResize(t *testing.T) {
	h := newHarness(t, testFS())
	h.ctx.Begin()
	h.b.ui(h.ctx, 80, 24)
	h.ctx.End()
	if got := h.ctx.GetContainer("Preview").Rect; got.X+got.W != 80 || got.H != 24 {
		t.Fatalf("preview window %+v did not follow the resize to 80x24", got)
	}
}
