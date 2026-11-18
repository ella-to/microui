package main

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestRenderProducesWellFormedSVG(t *testing.T) {
	out := render(340, 300, ui)
	dec := xml.NewDecoder(strings.NewReader(out))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("SVG is not well-formed XML: %v", err)
		}
	}
	for _, want := range []string{"Settings", "Notifications", "Save", "0.70", ">3<", "✓"} {
		if !strings.Contains(out, want) {
			t.Errorf("SVG missing %q", want)
		}
	}
}

func TestTextIsEscaped(t *testing.T) {
	logText = "a < b & c"
	defer func() { logText = "" }()
	out := render(340, 300, ui)
	if !strings.Contains(out, "a &lt; b &amp; c") {
		t.Fatal("text content was not XML-escaped")
	}
}
