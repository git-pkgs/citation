package citation_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/git-pkgs/citation"
)

const minimal = "cff-version: 1.2.0\nmessage: Please cite this software.\ntitle: Example\nauthors:\n  - name: Example Research Team\n"
const exampleTitle = "Example"

func TestParseCorpus(t *testing.T) {
	count := 0
	err := filepath.WalkDir("testdata", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path == "testdata/real-world" {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".cff") {
			return nil
		}
		count++
		t.Run(path, func(t *testing.T) {
			doc, err := citation.ReadFile(path, citation.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if doc == nil {
				t.Fatal("nil document without an error")
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count < 100 {
		t.Fatalf("expected pinned corpora, found %d files", count)
	}
}

func TestParseActorsAndUnknownFields(t *testing.T) {
	input := minimal + "contact:\n- family-names: Haines\n  given-names: Robert\n  address: 22 Example Road\nextra: {nested: [one, 2, null]}\nversion: 9007199254740993\n"
	doc, err := citation.Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title() != exampleTitle || doc.CFFVersion() != "1.2.0" {
		t.Fatalf("unexpected document: %v", doc.Fields())
	}
	if got := doc.Authors(); len(got) != 1 || !got[0].IsEntity() || got[0].Name() != "Example Research Team" {
		t.Fatalf("authors: %v", got)
	}
	if got := doc.Contact(); len(got) != 1 || got[0].IsEntity() || got[0].FamilyNames() != "Haines" {
		t.Fatalf("contact: %v", got)
	}
	if got := doc.Version(); got.Kind() != citation.Number || got.Text() != "9007199254740993" {
		t.Fatalf("version: %v", got)
	}
	if got := doc.Get("extra").Get("nested").Items(); len(got) != 3 || got[2].Kind() != citation.Null {
		t.Fatalf("unknown field lost: %v", got)
	}
}

func TestScalarForms(t *testing.T) {
	const folded = "First Second"
	cases := []struct {
		name, input, want string
		kind              citation.Kind
	}{
		{"plain", "title: https://example.org/a#fragment\n", "https://example.org/a#fragment", citation.String},
		{"comment", "title: My title # ignored\n", "My title", citation.String},
		{"single", "title: 'Ada''s work'\n", "Ada's work", citation.String},
		{"escaped", "title: \"\\u00e9\\ntext\"\n", "é\ntext", citation.String},
		{"literal", "title: |\n  First\n  Second\n", "First\nSecond\n", citation.String},
		{"folded", "title: >-\n  First\n  Second\n", folded, citation.String},
		{"plain lines", "title: First\n  Second\n", folded, citation.String},
		{"quoted lines", "title: \"First\n  Second\"\n", folded, citation.String},
		{"yes", "title: yes\n", "yes", citation.String},
		{"boolean", "title: true\n", "true", citation.Boolean},
		{"tag", "title: !!str 1.0\n", "1.0", citation.String},
		{"date", "title: 2024-02-29\n", "2024-02-29", citation.String},
		{"flow", "{\"title\":\"A title\", \"authors\":[{\"name\":\"A team\"}]}\n", "A title", citation.String},
		{"folded paragraphs", "title: >-\n  First\n\n  Second\n", "First\nSecond", citation.String},
		{"folded keep", "title: >+\n  First\n\n", "First\n\n", citation.String},
		{"quoted whitespace", "title: \"First  \n   Second\"\n", folded, citation.String},
		{"plain apostrophe", "title: don't # comment\n", "don't", citation.String},
		{"flow apostrophe", "{title: don't, authors: []}\n", "don't", citation.String},
		{"integer tag", "title: !!int 0xDE\n", "0xDE", citation.Number},
		{"directive comment", "%YAML 1.2 # format\n---\ntitle: Example\n", exampleTitle, citation.String},
		{"literal tab content", "title: |\n  \ttext\n", "\ttext\n", citation.String},
		{"inline tab separation", "title:\tExample\n", exampleTitle, citation.String},
		{"folded indented", "title: >-\n  first\n    indented\n  last\n", "first\n  indented\nlast", citation.String},
		{"literal no final newline", "title: |-\n  first\n  last", "first\nlast", citation.String},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := citation.Parse([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			got := doc.Get("title")
			if got.Kind() != tc.kind || got.Text() != tc.want {
				t.Fatalf("got %v %q, want %v %q", got.Kind(), got.Text(), tc.kind, tc.want)
			}
		})
	}
}

func TestAnchors(t *testing.T) {
	doc, err := citation.Parse([]byte("authors: &team\n- name: Research Team\ncontact: *team\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Contact()[0].Name() != "Research Team" {
		t.Fatal("alias lost content")
	}
	_, err = citation.ParseWithOptions([]byte("a: &a [one, two]\nb: &b [*a, *a]\nc: [*b, *b]\n"), citation.ParseOptions{MaxNodes: 20})
	if !errors.Is(err, citation.ErrLimit) {
		t.Fatalf("alias expansion limit: %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	const indentationCode = "indentation"
	cases := []struct {
		input    string
		category error
		code     string
	}{
		{"", citation.ErrSyntax, "empty_document"},
		{"authors: [", citation.ErrSyntax, "flow_end"},
		{"title: A\ntitle: B", citation.ErrSyntax, "duplicate_key"},
		{"{title: A, title: B}", citation.ErrSyntax, "duplicate_key"},
		{"title: A\n---\ntitle: B", citation.ErrSyntax, "trailing_content"},
		{"title: !ruby/object Foo", citation.ErrUnsupported, "tag"},
		{"authors: *missing", citation.ErrSyntax, "alias"},
		{"authors: &a [*a]", citation.ErrSyntax, "alias"},
		{"<<: {title: A}", citation.ErrUnsupported, "merge_key"},
		{"- A\n- B", citation.ErrType, "root_type"},
		{"1: value", citation.ErrType, "key_type"},
		{"title: !!int 1.5", citation.ErrType, "tag_type"},
		{"a: 1\n\tb: 2\n", citation.ErrSyntax, indentationCode},
		{"a:\n\tb: 2\n", citation.ErrSyntax, indentationCode},
		{"authors:\n- name: T\n  \trole: dev\n", citation.ErrSyntax, indentationCode},
		{"authors:\n- name: T\n\t- name: U\n", citation.ErrSyntax, indentationCode},
		{"a:\n  - 1\n   - 2\n", citation.ErrSyntax, indentationCode},
	}
	for _, tc := range cases {
		t.Run(tc.code+tc.input, func(t *testing.T) {
			doc, err := citation.Parse([]byte(tc.input))
			if doc != nil || !errors.Is(err, tc.category) {
				t.Fatalf("doc=%v, error=%v, want %v", doc, err, tc.category)
			}
			var problem *citation.Error
			if !errors.As(err, &problem) || problem.Code != tc.code {
				t.Fatalf("error code: %v", err)
			}
		})
	}
}

func TestFlowMappingKeys(t *testing.T) {
	const colonKey = "a:b"
	for _, tc := range []struct {
		input, key, text string
		kind             citation.Kind
	}{
		{"{a:b}", colonKey, "", citation.Null},
		{"{https://example.org}", "https://example.org", "", citation.Null},
		{"{a:b: value}", colonKey, "value", citation.String},
		{"{a: b}", "a", "b", citation.String},
		{"{a:}", "a", "", citation.Null},
		{"{a}", "a", "", citation.Null},
		{"{\"a\":b}", "a", "b", citation.String},
		{"{'a:b'}", colonKey, "", citation.Null},
		{"{a: []}", "a", "", citation.Sequence},
		{"{a:b # comment\n}", colonKey, "", citation.Null},
	} {
		t.Run(tc.input, func(t *testing.T) {
			doc, err := citation.Parse([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			v := doc.Get(tc.key)
			if len(doc.Fields()) != 1 || v.Kind() != tc.kind || v.Text() != tc.text {
				t.Fatalf("unexpected mapping: %+v", doc.Fields())
			}
		})
	}
	if _, err := citation.Parse([]byte("{a:b, 'a:b': other}")); !errors.Is(err, citation.ErrSyntax) {
		t.Fatalf("duplicate key was accepted: %v", err)
	}
}

func TestReadLimitsAndErrors(t *testing.T) {
	for _, size := range []int64{int64(len(minimal)), int64(len(minimal) - 1)} {
		_, err := citation.Read(strings.NewReader(minimal), citation.ParseOptions{MaxBytes: size})
		if size == int64(len(minimal)) && err != nil {
			t.Fatal(err)
		}
		if size < int64(len(minimal)) && !errors.Is(err, citation.ErrLimit) {
			t.Fatalf("limit: %v", err)
		}
	}
	_, err := citation.ReadFile(filepath.Join(t.TempDir(), "missing.cff"), citation.ParseOptions{})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lost file error: %v", err)
	}
	want := errors.New("read failed")
	_, err = citation.Read(io.MultiReader(strings.NewReader("title: partial"), errorReader{want}), citation.ParseOptions{})
	if !errors.Is(err, want) {
		t.Fatalf("lost reader error: %v", err)
	}
	_, err = citation.ParseWithOptions([]byte(minimal), citation.ParseOptions{MaxBytes: -1})
	if !errors.Is(err, citation.ErrOptions) {
		t.Fatalf("options: %v", err)
	}
	for _, opts := range []citation.ParseOptions{{MaxDepth: 2}, {MaxNodes: 2}, {MaxScalarBytes: 4}} {
		if _, err := citation.ParseWithOptions([]byte(minimal), opts); !errors.Is(err, citation.ErrLimit) {
			t.Fatalf("%+v: %v", opts, err)
		}
	}
	if _, err := citation.ParseWithOptions([]byte("version: !!int '12345'\n"), citation.ParseOptions{MaxNumberBytes: 4}); !errors.Is(err, citation.ErrLimit) {
		t.Fatalf("tag bypassed number limit: %v", err)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestOwnershipAndConcurrency(t *testing.T) {
	input := []byte(minimal)
	doc, err := citation.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	for i := range input {
		input[i] = 'x'
	}
	fields := doc.Fields()
	fields[0].Name = "changed"
	authors := doc.Get("authors").Items()
	authors[0] = citation.Value{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				parsed, err := citation.Read(bytes.NewBufferString(minimal), citation.ParseOptions{})
				if err != nil || parsed.Title() != doc.Title() || doc.Authors()[0].Name() != "Example Research Team" {
					t.Errorf("shared state: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()
}

func FuzzParse(f *testing.F) {
	for _, input := range []string{minimal, "title: [", "a: &a [*a]", "{title: test}", "title: |\n  test\n"} {
		f.Add([]byte(input))
	}
	for _, path := range []string{"testdata/ruby-cff/files/complete.cff", "testdata/ruby-cff/files/short.cff"} {
		data, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		doc, err := citation.Parse(input)
		if (doc == nil) != (err != nil) {
			t.Fatalf("inconsistent result: %v, %v", doc, err)
		}
		other, otherErr := citation.Read(bytes.NewReader(input), citation.ParseOptions{})
		if (err == nil) != (otherErr == nil) {
			t.Fatalf("reader disagrees: %v, %v", err, otherErr)
		}
		if err == nil && doc.Title() != other.Title() {
			t.Fatal("non-deterministic title")
		}
		if err == nil && !reflect.DeepEqual(doc.Validate(), other.Validate()) {
			t.Fatal("non-deterministic validation")
		}
	})
}

func BenchmarkParse(b *testing.B) {
	data := []byte(minimal)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := citation.Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseComplete(b *testing.B) {
	data, err := os.ReadFile("testdata/ruby-cff/files/complete.cff")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := citation.Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}
