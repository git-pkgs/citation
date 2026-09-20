package citation_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-pkgs/citation"
)

func TestValidateOfficialCorpus(t *testing.T) {
	err := filepath.WalkDir("testdata/cff/1.2.0", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".cff") {
			return nil
		}
		t.Run(path, func(t *testing.T) {
			doc, err := citation.ReadFile(path, citation.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			issues := doc.Validate()
			wantValid := strings.Contains(filepath.ToSlash(path), "/pass/")
			if (len(issues) == 0) != wantValid {
				t.Fatalf("valid=%t, diagnostics=%+v", wantValid, issues)
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateVersionAndMetadata(t *testing.T) {
	const versionField = "cff-version"
	const typeCode = "type"
	cases := []struct{ name, input, code, path string }{
		{"valid", minimal, "", ""},
		{"missing", "title: Example\n", "required", versionField},
		{"legacy", strings.Replace(minimal, "1.2.0", "1.0.3", 1), unsupportedVersionCode, versionField},
		{"future", strings.Replace(minimal, "1.2.0", "9.0.0", 1), unsupportedVersionCode, versionField},
		{"unknown", minimal + "unknown: true\n", "unknown_field", "unknown"},
		{"bad date", minimal + "date-released: 2023-02-29\n", "format", "date-released"},
		{"leap date", minimal + "date-released: 2024-02-29\n", "", ""},
		{"license list", minimal + "license: [MIT, Apache-2.0]\n", "", ""},
		{"no authors", "cff-version: 1.2.0\nmessage: Cite\ntitle: Example\nauthors: []\n", "min_items", "authors"},
		{"nested unknown", minimal + "contact: [{name: Team, nickname: Bob}]\n", "unknown_field", "contact[0].nickname"},
		{"type", strings.Replace(minimal, "title: Example", "title: false", 1), typeCode, "title"},
		{"precision", minimal + "version: 123456789012345678901234567890\n", "", ""},
		{"huge exponent", minimal + "version: 1e999999999\n", "", ""},
		{"not finite", minimal + "version: .inf\n", typeCode, "version"},
		{"unique maps", "cff-version: 1.2.0\ntitle: Example\nmessage: Cite\nauthors: [{given-names: Ada, family-names: Lovelace}, {family-names: Lovelace, given-names: Ada}]\n", "unique_items", "authors[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := citation.Parse([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			issues := doc.Validate()
			if tc.code == "" {
				if len(issues) != 0 {
					t.Fatalf("unexpected diagnostics: %+v", issues)
				}
				return
			}
			for _, issue := range issues {
				if issue.Code == tc.code && issue.Path == tc.path {
					return
				}
			}
			t.Fatalf("missing %s at %s: %+v", tc.code, tc.path, issues)
		})
	}
}

func TestValidateReferenceNumbers(t *testing.T) {
	for _, tc := range []struct {
		month string
		valid bool
	}{
		{"12", true}, {"0xC", true}, {"12.0", true}, {"12.00000000000000000000000001", false}, {"1e999999999", false}, {"1e-999999999", false}, {"-0", false},
	} {
		t.Run(tc.month, func(t *testing.T) {
			input := minimal + "preferred-citation:\n  type: article\n  title: A paper\n  authors: [{name: A team}]\n  month: " + tc.month + "\n"
			doc, err := citation.Parse([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			issues := doc.Validate()
			if (len(issues) == 0) != tc.valid {
				t.Fatalf("valid=%t, diagnostics=%+v", tc.valid, issues)
			}
		})
	}
}

func TestValidateStructuralDuplicates(t *testing.T) {
	const contactField = "contact"
	for _, tc := range []struct {
		name, field, entries string
		duplicate            bool
	}{
		{"equal numeric values", contactField, "[{name: Team, post-code: 12}, {post-code: 0xC, name: Team}]", true},
		{"numeric exponents", contactField, "[{name: Team, post-code: 1e2}, {name: Team, post-code: 100.0}]", true},
		{"numeric and string", contactField, "[{name: Team, post-code: 12}, {name: Team, post-code: '12'}]", false},
		{"aliased author", contactField, "[&team {name: Team}, *team]", true},
		{"field boundaries", contactField, "[{name: ab, city: c}, {name: a, city: bc}]", false},
		{"scalar boundaries", "keywords", "['2:3:abc', 'abc', '3:abc']", false},
		{"nested maps", "references", "[{type: article, title: Paper, authors: [{name: Team}]}, {authors: [{name: Team}], title: Paper, type: article}]", true},
		{"sequence order", "references", "[{type: article, title: Paper, authors: [{name: A}, {name: B}]}, {type: article, title: Paper, authors: [{name: B}, {name: A}]}]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := citation.Parse([]byte(minimal + tc.field + ": " + tc.entries + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			issues := doc.Validate()
			if tc.duplicate {
				if len(issues) != 1 || issues[0].Code != "unique_items" || issues[0].Path != tc.field+"[1]" {
					t.Fatalf("missing duplicate: %+v", issues)
				}
			} else if len(issues) != 0 {
				t.Fatalf("unexpected diagnostics: %+v", issues)
			}
		})
	}
}

func TestDiagnosticLimitAndPositions(t *testing.T) {
	doc, err := citation.ParseWithOptions([]byte(minimal+"first: a\nsecond: b\nthird: c\n"), citation.ParseOptions{MaxDiagnostics: 1})
	if err != nil {
		t.Fatal(err)
	}
	issues := doc.Validate()
	if len(issues) != 2 || issues[0].Path != "first" || issues[0].Line != 6 || issues[0].Column != 8 || issues[1].Code != "diagnostic_limit" {
		t.Fatalf("diagnostics: %+v", issues)
	}
	if len((*citation.Document)(nil).Validate()) == 0 {
		t.Fatal("nil document reported valid")
	}
}

func BenchmarkParseValidate(b *testing.B) {
	data := []byte(minimal)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		doc, err := citation.Parse(data)
		if err != nil {
			b.Fatal(err)
		}
		if issues := doc.Validate(); len(issues) != 0 {
			b.Fatal(issues)
		}
	}
}
