// Command filebrowser is a terminal file browser: a lazily loaded directory
// tree on the left and a preview of the selected file on the right.
//
//	go run ella.to/microui/examples/filebrowser [dir]
//
// It shows three patterns that come up in any real immediate-mode UI:
//
//   - Recursion: the tree is one recursive function calling BeginTreenode /
//     EndTreenode. There is no widget tree to build or keep in sync; the
//     filesystem itself is the model.
//   - Identity scoping: BeginTreenode pushes the node's id, so everything
//     inside a folder is hashed under it and same-named folders in different
//     places stay independent. Each entry is also wrapped in
//     PushID(fullPath), so a file can never share an id with another control
//     (a file named "Refresh" with the Refresh button, say).
//   - Lazy loading with a cache: a directory is read only the first time its
//     node is expanded, then served from a map until Refresh clears it.
//
// The browser works on an fs.FS (os.DirFS here, fstest.MapFS in the tests).
// Click a folder to expand it, click a file to preview it, PageUp/PageDown
// scroll the preview, Ctrl-C quits.
package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// Preview limits: never read more than maxPreviewBytes, never show more than
// maxPreviewLines.
const (
	maxPreviewBytes = 64 << 10
	maxPreviewLines = 200
)

// browser is the whole application state, persistent across frames.
type browser struct {
	fsys fs.FS
	name string // display name of the root

	// cache holds the entries of every directory read so far, keyed by its
	// slash-separated path within fsys ("." is the root). errs records
	// directories that failed to read, so they are not retried every frame.
	cache map[string][]fs.DirEntry
	errs  map[string]error

	selected    string // path of the previewed file, "" for none
	info        string // size / modification time line
	preview     string // text shown in the preview panel
	resetScroll bool   // scroll the preview back to the top next frame

	lastW, lastH int // screen size the windows were last tiled for
}

func newBrowser(fsys fs.FS, name string) *browser {
	return &browser{
		fsys:  fsys,
		name:  name,
		cache: map[string][]fs.DirEntry{},
		errs:  map[string]error{},
	}
}

// list returns the entries of dir, reading it on first use only. Directories
// sort before files; within each group fs.ReadDir's name order is kept.
func (b *browser) list(dir string) ([]fs.DirEntry, error) {
	if err, ok := b.errs[dir]; ok {
		return nil, err
	}
	if entries, ok := b.cache[dir]; ok {
		return entries, nil
	}
	entries, err := fs.ReadDir(b.fsys, dir)
	if err != nil {
		b.errs[dir] = err
		return nil, err
	}
	slices.SortStableFunc(entries, func(x, y fs.DirEntry) int {
		switch {
		case x.IsDir() == y.IsDir():
			return 0
		case x.IsDir():
			return -1
		default:
			return 1
		}
	})
	b.cache[dir] = entries
	return entries, nil
}

// refresh drops every cached listing; expanded nodes re-read on the next frame.
func (b *browser) refresh() {
	clear(b.cache)
	clear(b.errs)
	if b.selected != "" {
		b.open(b.selected)
	}
}

// tree emits the contents of dir and recurses into expanded subdirectories.
// It is only called for directories whose node is open, which is what makes
// loading lazy: a collapsed folder is never read.
func (b *browser) tree(ctx *microui.Context, dir string) {
	entries, err := b.list(dir)
	if err != nil {
		ctx.Label("error: " + err.Error())
		return
	}
	if len(entries) == 0 {
		ctx.Label("(empty)")
		return
	}
	for _, e := range entries {
		full := path.Join(dir, e.Name())

		// Controls are identified by their label hashed under the id stack.
		// Folders are already distinct (BeginTreenode pushes its own id, so
		// "a/docs/" and "b/docs/" hash under different parents), but a file
		// button's label could match another control in the same scope: a
		// root file named "Refresh" would share the Refresh button's id.
		// Scoping every entry by its full path rules that out.
		ctx.PushID(full)
		if e.IsDir() {
			if ctx.BeginTreenode(e.Name() + "/").Active() {
				b.tree(ctx, full)
				ctx.EndTreenode()
			}
		} else {
			label := e.Name()
			if full == b.selected {
				label = "▸ " + label
			}
			// One row tall, like the folder nodes (Style.HeaderHeight), so
			// the tree stays dense; option 0 keeps the label left-aligned.
			ctx.LayoutRow([]int{-1}, 1)
			if ctx.ButtonEx(label, 0, 0).Submitted() {
				b.open(full)
			}
		}
		ctx.PopID()
	}
}

// open selects the file at p and loads its preview.
func (b *browser) open(p string) {
	b.selected = p
	b.resetScroll = true
	b.info, b.preview = loadPreview(b.fsys, p)
}

// loadPreview reads at most maxPreviewBytes of p and returns an info line and
// the text to show. Binary files (any NUL byte) are described, not shown.
func loadPreview(fsys fs.FS, p string) (info, text string) {
	fi, err := fs.Stat(fsys, p)
	if err != nil {
		return "", "error: " + err.Error()
	}
	info = fmt.Sprintf("%s   %s", humanSize(fi.Size()), fi.ModTime().Format("2006-01-02 15:04"))

	f, err := fsys.Open(p)
	if err != nil {
		return info, "error: " + err.Error()
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPreviewBytes))
	if err != nil {
		return info, "error: " + err.Error()
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return info, fmt.Sprintf("(binary file, %s — not shown)", humanSize(fi.Size()))
	}

	// Terminals draw tabs and carriage returns badly; normalize them.
	s := strings.NewReplacer("\t", "    ", "\r", "").Replace(string(data))
	lines := strings.Split(s, "\n")
	truncated := fi.Size() > maxPreviewBytes
	if len(lines) > maxPreviewLines {
		lines, truncated = lines[:maxPreviewLines], true
	}
	text = strings.Join(lines, "\n")
	if truncated {
		text += "\n… (truncated)"
	}
	return info, text
}

// humanSize formats a byte count as B/KB/MB/GB.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ui builds one frame for a w x h cell screen.
func (b *browser) ui(ctx *microui.Context, w, h int) {
	treeW := max(w*2/5, 24)
	files := microui.Rect{W: treeW, H: h}
	preview := microui.Rect{X: treeW, W: w - treeW, H: h}
	if w != b.lastW || h != b.lastH {
		// A window's rect argument is only its initial geometry; to follow a
		// terminal resize, write the retained containers directly.
		ctx.GetContainer("Files").Rect = files
		ctx.GetContainer("Preview").Rect = preview
		b.lastW, b.lastH = w, h
	}

	if ctx.BeginWindowEx("Files", files, microui.OptNoClose).Active() {
		ctx.LayoutRow([]int{-9, -1}, 0)
		ctx.Label(b.name)
		if ctx.Button("Refresh").Submitted() {
			b.refresh()
		}
		ctx.LayoutRow([]int{-1}, 0)
		b.tree(ctx, ".")
		ctx.EndWindow()
	}

	if ctx.BeginWindowEx("Preview", preview, microui.OptNoClose).Active() {
		ctx.LayoutRow([]int{-1}, 1)
		if b.selected == "" {
			ctx.Label("Select a file to preview it.")
			ctx.EndWindow()
			return
		}
		ctx.Label(b.selected)
		ctx.Label(b.info)

		ctx.LayoutRow([]int{-1}, -1)
		ctx.BeginPanel("preview-text")
		panel := ctx.GetCurrentContainer()
		ctx.LayoutRow([]int{-1}, -1)
		// Text puts Style.Spacing between lines, which in a terminal is a
		// blank row; drop it so the file reads single-spaced.
		spacing := ctx.Style.Spacing
		ctx.Style.Spacing = 0
		ctx.Text(b.preview)
		ctx.Style.Spacing = spacing
		ctx.EndPanel()

		// Keyboard scrolling: the panel's retained Scroll is plain state we may
		// adjust; microui clamps it to the content on the next frame.
		switch keys := ctx.KeyPressed(); {
		case b.resetScroll:
			panel.Scroll.Y = 0
			b.resetScroll = false
		case keys&microui.KeyPageDown != 0:
			panel.Scroll.Y += max(panel.Body.H-1, 1)
		case keys&microui.KeyPageUp != 0:
			panel.Scroll.Y = max(panel.Scroll.Y-max(panel.Body.H-1, 1), 0)
		}
		ctx.EndWindow()
	}
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		fmt.Fprintln(os.Stderr, "filebrowser: not a directory:", root)
		os.Exit(1)
	}
	b := newBrowser(os.DirFS(root), root)

	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "filebrowser:", err)
		os.Exit(1)
	}
	err = renderer.Run(term, func(ctx *microui.Context) {
		w, h := term.Size()
		b.ui(ctx, w, h)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "filebrowser:", err)
		os.Exit(1)
	}
}
