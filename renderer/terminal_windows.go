//go:build windows

package renderer

import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// This file holds the Windows console plumbing: console modes are switched
// with kernel32 calls so the console speaks VT sequences both ways (Windows
// 10 1809+, Windows Terminal). No cgo is involved.

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procGetConsoleCP               = kernel32.NewProc("GetConsoleCP")
	procSetConsoleCP               = kernel32.NewProc("SetConsoleCP")
	procGetConsoleOutputCP         = kernel32.NewProc("GetConsoleOutputCP")
	procSetConsoleOutputCP         = kernel32.NewProc("SetConsoleOutputCP")
)

const (
	enableProcessedInput            = 0x0001
	enableLineInput                 = 0x0002
	enableEchoInput                 = 0x0004
	enableWindowInput               = 0x0008
	enableMouseInput                = 0x0010
	enableQuickEditMode             = 0x0040
	enableExtendedFlags             = 0x0080
	enableVirtualTerminalInput      = 0x0200
	enableProcessedOutput           = 0x0001
	enableVirtualTerminalProcessing = 0x0004
	disableNewlineAutoReturn        = 0x0008
	codePageUTF8                    = 65001
)

// sysState is the console state saved by enterRaw.
type sysState struct {
	inMode, outMode uint32
	inCP, outCP     uintptr
	saved           bool
	sig             chan os.Signal
}

type coord struct{ X, Y int16 }

type smallRect struct{ Left, Top, Right, Bottom int16 }

type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

func stdinHandle() syscall.Handle  { return syscall.Handle(os.Stdin.Fd()) }
func stdoutHandle() syscall.Handle { return syscall.Handle(os.Stdout.Fd()) }

// isTerminal reports whether f is a console.
func isTerminal(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}

func setConsoleMode(h syscall.Handle, mode uint32) error {
	if r, _, err := procSetConsoleMode.Call(uintptr(h), uintptr(mode)); r == 0 {
		return err
	}
	return nil
}

// enterRaw saves the console modes and switches both handles to VT mode with
// line editing, echo and Ctrl-C processing off.
func (t *Terminal) enterRaw() error {
	in, out := stdinHandle(), stdoutHandle()
	if !t.sys.saved {
		if err := syscall.GetConsoleMode(in, &t.sys.inMode); err != nil {
			return err
		}
		if err := syscall.GetConsoleMode(out, &t.sys.outMode); err != nil {
			return err
		}
		t.sys.inCP, _, _ = procGetConsoleCP.Call()
		t.sys.outCP, _, _ = procGetConsoleOutputCP.Call()
		t.sys.saved = true
	}
	inMode := t.sys.inMode&^(enableProcessedInput|enableLineInput|enableEchoInput|
		enableWindowInput|enableMouseInput|enableQuickEditMode) |
		enableExtendedFlags | enableVirtualTerminalInput
	if err := setConsoleMode(in, inMode); err != nil {
		return err
	}
	outMode := t.sys.outMode | enableProcessedOutput | enableVirtualTerminalProcessing | disableNewlineAutoReturn
	if err := setConsoleMode(out, outMode); err != nil {
		_ = setConsoleMode(in, t.sys.inMode)
		return err
	}
	procSetConsoleCP.Call(codePageUTF8)
	procSetConsoleOutputCP.Call(codePageUTF8)
	t.enterModes()
	return nil
}

// leaveRaw restores the console to the state enterRaw saved.
func (t *Terminal) leaveRaw() {
	t.leaveModes()
	if !t.sys.saved {
		return
	}
	_ = setConsoleMode(stdinHandle(), t.sys.inMode)
	_ = setConsoleMode(stdoutHandle(), t.sys.outMode)
	procSetConsoleCP.Call(t.sys.inCP)
	procSetConsoleOutputCP.Call(t.sys.outCP)
}

// size returns the visible console window size, falling back to 80x24.
func (t *Terminal) size() (cols, rows int) {
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(stdoutHandle()), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 80, 24
	}
	cols = int(info.Window.Right-info.Window.Left) + 1
	rows = int(info.Window.Bottom-info.Window.Top) + 1
	if cols < 1 || rows < 1 {
		return 80, 24
	}
	return cols, rows
}

// pollResize reports whether the console size changed. Windows has no
// SIGWINCH, so Poll asks every frame (a single cheap call).
func (t *Terminal) pollResize() bool {
	cols, rows := t.size()
	return cols != t.back.w || rows != t.back.h
}

// watchSignals turns console close/interrupt events into a quit request.
func (t *Terminal) watchSignals() {
	ch := make(chan os.Signal, 2)
	t.sys.sig = ch
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range ch {
			t.quit.Store(true)
			t.signalWake()
		}
	}()
}

// stopSignals undoes watchSignals.
func (t *Terminal) stopSignals() {
	if t.sys.sig != nil {
		signal.Stop(t.sys.sig)
		close(t.sys.sig)
		t.sys.sig = nil
	}
}

// suspend is a no-op: Windows consoles have no job control.
func (t *Terminal) suspend() {}
