// Command form is a sign-up form with live validation, keyboard focus
// navigation and a modal confirmation dialog.
//
//	go run ella.to/microui/examples/form
//
// It demonstrates:
//
//   - a two-column label/field layout
//   - Tab / Shift+Tab (and Enter) moving focus between textboxes, by
//     collecting each field's LastID during the frame and calling SetFocus
//     once the whole form has been built
//   - inline validation messages in color, drawn by temporarily overriding a
//     Style color (draw commands capture colors when they are emitted)
//   - the generic value controls: an int bound with Slider, a float64 with
//     NumberEx
//   - a password-strength meter drawn with DrawRect and Color.Lerp
//   - a Submit button that looks and behaves disabled until the form is valid
//   - a modal dialog: a full-screen, chrome-less window kept on top with
//     BringToFront and a semi-transparent backdrop, which blocks the form
//     behind it until OK or Esc closes it
//
// Ctrl-C quits.
package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

const labelW = 11 // width of the label column

var (
	okColor    = microui.Hex(0x7ccf8a)
	errColor   = microui.Hex(0xe06c75)
	dimColor   = microui.Hex(0x8a919c)
	accent     = microui.Hex(0x61afef)
	dimButton  = microui.Hex(0x3b3f46)
	backdrop   = microui.Hex(0x000000).WithAlpha(160) // blended over the form
	fieldNames = [...]string{"name", "email", "password"}
)

// Form holds the sign-up form's state, persistent across frames.
type Form struct {
	Name, Email, Password string
	Age                   int
	Height                float64
	Agree                 bool

	touched  [len(fieldNames)]bool // a field shows errors once edited
	fields   []microui.ID          // this frame's textbox ids, in Tab order
	moveFrom microui.ID            // field whose Enter should advance focus
	started  bool
	modal    bool
	submits  int

	size func() (w, h int) // screen size, for the full-screen modal
}

func newForm(size func() (w, h int)) *Form {
	return &Form{Age: 25, Height: 1.70, size: size}
}

// ---- validation ---------------------------------------------------------------

// Each validator returns "" when the value is acceptable, else a message.

func validateName(s string) string {
	if strings.TrimSpace(s) == "" {
		return "required"
	}
	return ""
}

func validateEmail(s string) string {
	local, domain, ok := strings.Cut(s, "@")
	switch {
	case s == "":
		return "required"
	case !ok || local == "" || strings.ContainsAny(s, " \t") ||
		!strings.Contains(domain, ".") ||
		strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, "."):
		return "use an address like ada@example.com"
	}
	return ""
}

func validatePassword(s string) string {
	if n := utf8.RuneCountInString(s); n < 8 {
		return fmt.Sprintf("%d more character%s needed", 8-n, plural(8-n))
	}
	return ""
}

func validateAge(n int) string {
	if n < 18 {
		return "you must be 18 or older"
	}
	return ""
}

// problems returns the number of reasons the form cannot be submitted.
func (f *Form) problems() int {
	n := 0
	for _, msg := range []string{
		validateName(f.Name), validateEmail(f.Email),
		validatePassword(f.Password), validateAge(f.Age),
	} {
		if msg != "" {
			n++
		}
	}
	if !f.Agree {
		n++
	}
	return n
}

// passwordStrength scores s in [0,1] from its length and character variety.
func passwordStrength(s string) float64 {
	var lower, upper, digit, other bool
	for _, r := range s {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
	}
	score := min(float64(utf8.RuneCountInString(s))/16, 1) * 0.6
	for _, b := range []bool{lower, upper, digit, other} {
		if b {
			score += 0.1
		}
	}
	return min(score, 1)
}

// ---- UI -----------------------------------------------------------------------

// UI builds one frame: the form window, then the modal on top of it.
func (f *Form) UI(ctx *microui.Context) {
	f.formWindow(ctx)
	f.modalWindow(ctx)
}

func (f *Form) formWindow(ctx *microui.Context) {
	// 11 rows of height 1 plus spacing, padding and the title: exactly 24
	// lines, so the whole form fits an 80x24 terminal.
	if !ctx.BeginWindowEx("Sign up", microui.Rect{X: 2, Y: 0, W: 62, H: 24}, microui.OptNoClose).Active() {
		return
	}
	defer ctx.EndWindow()

	f.fields = f.fields[:0]
	problems := f.problems()

	// field draws one labelled textbox and records its id for Tab order.
	field := func(i int, label string, value *string, password bool) {
		ctx.LayoutRow([]int{labelW, -1}, 1)
		ctx.Label(label)
		var res microui.Result
		if password {
			res = ctx.TextboxPassword(value)
		} else {
			res = ctx.Textbox(value)
		}
		f.fields = append(f.fields, ctx.LastID())
		if res.Changed() {
			f.touched[i] = true
		}
		if res.Submitted() {
			f.moveFrom = ctx.LastID() // Enter: advance once all ids are known
		}
	}

	field(0, "Name", &f.Name, false)
	message(ctx, validateName(f.Name), f.touched[0], "your full name")
	field(1, "Email", &f.Email, false)
	message(ctx, validateEmail(f.Email), f.touched[1], "we never share it")
	field(2, "Password", &f.Password, true)
	message(ctx, validatePassword(f.Password), f.touched[2], "at least 8 characters")

	ctx.LayoutRow([]int{labelW, -1}, 1)
	ctx.Label("Strength")
	strengthBar(ctx, passwordStrength(f.Password))

	ctx.LayoutRow([]int{labelW, 20, -1}, 1)
	ctx.Label("Age")
	ctx.Slider(&f.Age, 13, 120) // generic: binds an int and snaps to whole years
	status(ctx, validateAge(f.Age), true, "")

	ctx.LayoutRow([]int{labelW, -1}, 1)
	ctx.Label("Height")
	ctx.NumberEx(&f.Height, 0.01, "%.2f m", microui.OptAlignCenter) // drag; shift-click to type
	f.Height = min(max(f.Height, 0.5), 2.5)

	ctx.LayoutRow([]int{labelW, -1}, 1)
	ctx.Label("")
	ctx.Checkbox("I agree to the terms", &f.Agree)

	ctx.LayoutRow([]int{labelW, 12, -1}, 1)
	ctx.Label("")
	if submitButton(ctx, problems == 0) {
		f.openModal(ctx)
	}
	if problems > 0 {
		colorText(ctx, fmt.Sprintf("%d thing%s left to fix", problems, plural(problems)), dimColor)
	} else {
		colorText(ctx, "ready!", okColor)
	}

	if !f.started && len(f.fields) > 0 {
		f.started = true
		ctx.SetFocus(f.fields[0]) // autofocus the first field
	}
	f.moveFocus(ctx, problems == 0)
}

// moveFocus resolves this frame's focus navigation now that every field's id
// is known: Tab / Shift+Tab cycle, Enter advances (and submits from the last
// field when the form is valid).
func (f *Form) moveFocus(ctx *microui.Context, valid bool) {
	n := len(f.fields)
	if n == 0 || f.modal {
		return
	}
	keys := ctx.KeyPressed()
	var from microui.ID
	dir := 1
	switch {
	case f.moveFrom != 0:
		from = f.moveFrom
		f.moveFrom = 0
		if from == f.fields[n-1] && valid {
			f.openModal(ctx)
			return
		}
	case keys&microui.KeyTab != 0:
		from = ctx.Focus()
		if (keys|ctx.KeyDown())&microui.KeyShift != 0 {
			dir = -1
		}
	default:
		return
	}
	i := slices.Index(f.fields, from)
	switch {
	case i < 0 && dir > 0:
		i = 0
	case i < 0:
		i = n - 1
	default:
		i = (i + dir + n) % n
	}
	ctx.SetFocus(f.fields[i])
}

func (f *Form) openModal(ctx *microui.Context) {
	f.modal = true
	f.submits++
	ctx.SetFocus(0) // nothing behind the modal keeps the keyboard
}

// modalWindow draws the confirmation dialog while it is open. It is a
// full-screen window without chrome: covering the whole screen makes it the
// hover root, so nothing behind it reacts to the mouse.
func (f *Form) modalWindow(ctx *microui.Context) {
	if !f.modal {
		return
	}
	w, h := f.size()
	screen := microui.Rect{W: w, H: h}
	const opt = microui.OptNoTitle | microui.OptNoResize | microui.OptNoScroll | microui.OptNoFrame
	if !ctx.BeginWindowEx("modal", screen, opt).Active() {
		return
	}
	defer ctx.EndWindow()

	cnt := ctx.GetCurrentContainer()
	cnt.Rect = screen     // follow terminal resizes
	ctx.BringToFront(cnt) // stay above the form every frame
	ctx.DrawRect(screen, backdrop)

	// 2 cells of margin around 5 rows of content (9 lines with spacing).
	box := microui.Rect{X: (w - 44) / 2, Y: (h - 13) / 2, W: 44, H: 13}
	ctx.DrawRect(box, ctx.Style.Colors[microui.ColorWindowBG])
	ctx.DrawBox(box, accent)

	// Lay the dialog's contents out inside box: an absolute LayoutSetNext
	// followed by a column gives us a fresh layout with box as its body.
	ctx.LayoutSetNext(box.Expand(-2), false)
	ctx.LayoutBeginColumn()
	ctx.LayoutRow([]int{-1}, 1)
	colorText(ctx, "✓ Welcome aboard!", okColor)
	ctx.Label("Name:   " + f.Name)
	ctx.Label("Email:  " + f.Email)
	ctx.Label(fmt.Sprintf("Age:    %d    Height: %.2f m", f.Age, f.Height))
	ctx.LayoutRow([]int{14, 12}, 1)
	ctx.Label("")
	if ctx.Button("OK").Submitted() || ctx.KeyPressed()&microui.KeyEscape != 0 {
		f.modal = false
	}
	ctx.LayoutEndColumn()
}

// ---- small drawing helpers ----------------------------------------------------

// message draws a validation line under a field.
func message(ctx *microui.Context, err string, touched bool, hint string) {
	ctx.LayoutRow([]int{labelW, -1}, 1)
	ctx.Label("")
	status(ctx, err, touched, hint)
}

// status draws a validation result in the next cell: a dim hint until the
// field is touched, then red errors or a green check.
func status(ctx *microui.Context, err string, touched bool, hint string) {
	switch {
	case err == "":
		colorText(ctx, "✓ looks good", okColor)
	case !touched:
		colorText(ctx, hint, dimColor)
	default:
		colorText(ctx, "✗ "+err, errColor)
	}
}

// colorText draws s in the next cell in col by overriding the text color for
// just this one call. Draw commands capture the color when emitted, so
// restoring the style afterwards is safe.
func colorText(ctx *microui.Context, s string, col microui.Color) {
	saved := ctx.Style.Colors[microui.ColorText]
	ctx.Style.Colors[microui.ColorText] = col
	ctx.DrawControlText(s, ctx.LayoutNext(), microui.ColorText, 0)
	ctx.Style.Colors[microui.ColorText] = saved
}

// strengthBar draws a meter filled to s (0..1), colored from red to green.
func strengthBar(ctx *microui.Context, s float64) {
	r := ctx.LayoutNext()
	ctx.DrawRect(r, ctx.Style.Colors[microui.ColorBase])
	fill := r
	fill.W = int(float64(r.W)*s + 0.5)
	ctx.DrawRect(fill, errColor.Lerp(okColor, s))
	word := "weak"
	switch {
	case s >= 0.8:
		word = "strong"
	case s >= 0.5:
		word = "fair"
	}
	ctx.DrawControlText(word, r, microui.ColorText, microui.OptAlignCenter)
}

// submitButton draws Submit; while disabled it is dimmed and OptNoInteract so
// it can never be hovered, focused or clicked.
func submitButton(ctx *microui.Context, enabled bool) bool {
	if enabled {
		return ctx.Button("Submit").Submitted()
	}
	saved := ctx.Style.Colors // Colors is an array, so this is a full copy
	ctx.Style.Colors[microui.ColorButton] = dimButton
	ctx.Style.Colors[microui.ColorText] = dimColor
	ctx.ButtonEx("Submit", 0, microui.OptAlignCenter|microui.OptNoInteract)
	ctx.Style.Colors = saved
	return false
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "form:", err)
		os.Exit(1)
	}
	f := newForm(term.Size)
	if err := renderer.Run(term, f.UI); err != nil {
		fmt.Fprintln(os.Stderr, "form:", err)
		os.Exit(1)
	}
}
