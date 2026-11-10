package main

import (
	"os"
	"strings"
	"testing"
)

func TestWatchHelpPortableAndEscaped(t *testing.T) {
	path, err := writeWatchHelp(t.TempDir(), `test<&"build`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if strings.Contains(page, "{{BUILD}}") || strings.Contains(page, `test<&"build`) || !strings.Contains(page, "test&lt;&amp;&#34;build") {
		t.Fatal("build not safely rendered")
	}
	for _, external := range []string{"https://", "http://", "<script", "<iframe"} {
		if strings.Contains(strings.ToLower(page), external) {
			t.Fatalf("offline guide contains external/active content %s", external)
		}
	}
}
