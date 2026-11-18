# microui in the browser (js/wasm)

This example runs a microui UI on an HTML `<canvas>`. The driver
([`canvas.go`](canvas.go)) is a `renderer.Driver` written with nothing but
`syscall/js`: no cgo, no npm, no third-party Go modules. The UI code in
[`main.go`](main.go) is an ordinary `func(*microui.Context)` and would run
unchanged on the terminal driver.

## Build and run

From the repository root:

```sh
# 1. compile the example to WebAssembly
GOOS=js GOARCH=wasm go build -o examples/wasm/main.wasm ./examples/wasm

# 2. copy Go's JS glue next to it (it must match your Go version)
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/wasm/

# 3. serve the directory (any static file server works)
go run ./examples/wasm/serve examples/wasm
```

Open <http://localhost:8080>. Rebuild with step 1 and reload the page after
editing the Go code.

> Go 1.24+ ships `wasm_exec.js` in `$(go env GOROOT)/lib/wasm`; older
> toolchains kept it in `misc/wasm`.

On other platforms `go run ./examples/wasm` just prints these steps (see
[`stub.go`](stub.go)), so `go build ./...` and `go vet ./...` keep working
everywhere.

## How it works

| piece | what it does |
|-------|--------------|
| `TextWidth` | `ctx.measureText`, cached in a map (it crosses the JS boundary and is slow) |
| `TextHeight` | a fixed line height for the 14px UI font |
| `FillRect` / `DrawText` | `fillRect` / `fillText` with `textBaseline = "top"`; alpha blends natively |
| `SetClip` | `save()` + `rect()` + `clip()`, undone with `restore()` (canvas clips only shrink) |
| `DrawIcon` | small vector shapes: ✕, ✓ and the two tree triangles |
| `Size` | CSS pixels; the backing store is scaled by `devicePixelRatio` for sharp text |
| `Poll` | drains a queue filled by DOM listeners (mouse, wheel, keyboard, paste, blur) |
| `Wake` | implements `renderer.Waker`, signalled on every queued event |

**Input batching.** microui sees keys and buttons as per-frame bitmasks, so
two presses of the same key in one frame would collapse into one. `Poll`
therefore ends a frame's batch at the first discrete event (button, key,
text) and leaves the rest queued, so a click also registers where it
happened rather than where the pointer moved next. Mouse motion, wheel
deltas and modifier changes before it coalesce freely.

**Modifiers** get real down/up events (so shift-click on a slider types a
value), Cmd acts as Ctrl on macOS, AltGr is not mistaken for Ctrl+Alt, Ctrl/Cmd-A is `KeySelectAll`, other
Ctrl/Cmd shortcuts (paste, reload, ...) are left to the browser, and pasted
text is flattened to a single line.

**Frame loop.** `main` drives frames from `requestAnimationFrame` instead of
`renderer.Run`. `Run` would also work, because Go's wasm runtime yields to
the browser event loop whenever all goroutines are blocked. But
`requestAnimationFrame` syncs frames to the display and is paused by the
browser in background tabs.
