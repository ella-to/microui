// Command serve is a minimal static file server for trying the wasm example.
//
//	go run ./examples/wasm/serve examples/wasm
//
// It serves the given directory (default ".") on :8080. Any static server
// works; this one just avoids needing anything beyond the Go toolchain. Go's
// file server already sends application/wasm for .wasm files, which
// WebAssembly.instantiateStreaming requires.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	flag.Parse()
	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	fmt.Printf("serving %s on http://%s\n", dir, *addr)
	log.Fatal(http.ListenAndServe(*addr, http.FileServer(http.Dir(dir))))
}
