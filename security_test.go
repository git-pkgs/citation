package citation_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/git-pkgs/citation"
)

func TestParseBlankLineMemory(t *testing.T) {
	const padding = 1 << 19
	const budget = 16 << 20
	for _, prefix := range []string{"", "abstract: |+\n", "abstract: >+\n"} {
		t.Run(prefix, func(t *testing.T) {
			data := []byte(prefix + strings.Repeat("\n", padding) + minimal)
			var doc *citation.Document
			var err error
			allocated := allocatedBytes(func() { doc, err = citation.Parse(data) })
			if err != nil {
				t.Fatal(err)
			}
			if issues := doc.Validate(); len(issues) != 0 {
				t.Fatal(issues)
			}
			wantLine := padding + 3
			if prefix != "" {
				wantLine++
				if doc.Get("abstract").Text() != strings.Repeat("\n", padding) {
					t.Fatal("block scalar blank lines changed")
				}
			}
			if doc.Title() != exampleTitle || doc.Get("title").Position().Line != wantLine {
				t.Fatalf("content after blank lines: %q at %+v", doc.Title(), doc.Get("title").Position())
			}
			if allocated > budget {
				t.Fatalf("%d input bytes allocated %d bytes, budget %d", len(data), allocated, budget)
			}
		})
	}
}

func TestValidateNestedAliasMemory(t *testing.T) {
	const budget = 8 << 20
	doc, err := citation.Parse(nestedAliasInput())
	if err != nil {
		t.Fatal(err)
	}
	var issues []citation.Diagnostic
	allocated := allocatedBytes(func() { issues = doc.Validate() })
	if len(issues) != 1 || issues[0].Code != "type" || issues[0].Path != "authors[0]" {
		t.Fatalf("unexpected diagnostics: %+v", issues)
	}
	if allocated > budget {
		t.Fatalf("validation allocated %d bytes, budget %d", allocated, budget)
	}
}

func allocatedBytes(run func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func nestedAliasInput() []byte {
	return []byte("cff-version: 1.2.0\ntitle: Example\nmessage: &s " + strings.Repeat("x", 65536) + "\nauthors: " + strings.Repeat("[", 60) + strings.TrimSuffix(strings.Repeat("*s,", 60), ",") + strings.Repeat("]", 60) + "\n")
}

func BenchmarkParseBlankLines(b *testing.B) {
	for _, size := range []struct {
		name  string
		bytes int
	}{{"64KiB", 1 << 16}, {"1MiB", 1 << 20}} {
		b.Run(size.name, func(b *testing.B) {
			data := []byte(minimal + strings.Repeat("\n", size.bytes-len(minimal)))
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				if _, err := citation.Parse(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkValidateNestedAliases(b *testing.B) {
	doc, err := citation.Parse(nestedAliasInput())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if issues := doc.Validate(); len(issues) != 1 {
			b.Fatal(issues)
		}
	}
}
