//go:build !unix && !windows

package renderer

import (
	"errors"
	"os"
)

// On platforms without a supported terminal API (js/wasm, wasip1, plan9)
// NewTerminal always falls back to the headless renderer.

type sysState struct{}

func isTerminal(*os.File) bool { return false }

func (t *Terminal) enterRaw() error {
	return errors.New("renderer: interactive terminals are not supported on this platform")
}

func (t *Terminal) leaveRaw()        {}
func (t *Terminal) size() (int, int) { return 80, 24 }
func (t *Terminal) pollResize() bool { return false }
func (t *Terminal) watchSignals()    {}
func (t *Terminal) stopSignals()     {}
func (t *Terminal) suspend()         {}
