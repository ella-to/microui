//go:build !(js && wasm)

// Command wasm runs a microui UI in the browser on an HTML <canvas>. It only
// does something when built for js/wasm; on other platforms it prints how.
//
//	GOOS=js GOARCH=wasm go build -o examples/wasm/main.wasm ./examples/wasm
package main

import "fmt"

func main() {
	fmt.Print(`This example runs in a web browser. Build and serve it with:

  GOOS=js GOARCH=wasm go build -o examples/wasm/main.wasm ./examples/wasm
  cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/wasm/
  go run ./examples/wasm/serve examples/wasm   # or any static file server

then open http://localhost:8080. See examples/wasm/README.md.
`)
}
