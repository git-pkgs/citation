package citation_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-pkgs/citation"
)

type corpusFixture struct {
	File                   string            `json:"file"`
	SHA256                 string            `json:"sha256"`
	DeclaredVersion        *string           `json:"declared_version"`
	YAMLStatus             string            `json:"yaml_status"`
	SchemaStatus           string            `json:"schema_status"`
	ExpectedMetadata       map[string]string `json:"expected_metadata"`
	ExpectedAuthors        []map[string]any  `json:"expected_authors"`
	ExpectedReferenceCount int               `json:"expected_reference_count"`
}

const unsupportedVersionCode = "unsupported_version"

func TestDownloadedCorpus(t *testing.T) {
	data, err := os.ReadFile("testdata/real-world/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Fixtures []corpusFixture `json:"fixtures"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Fixtures) != 21 {
		t.Fatalf("missing downloaded candidates: %d", len(manifest.Fixtures))
	}
	for _, fixture := range manifest.Fixtures {
		t.Run(fixture.File, func(t *testing.T) {
			checkCorpusFixture(t, fixture)
		})
	}
}

func checkCorpusFixture(t *testing.T, fixture corpusFixture) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata/real-world", fixture.File))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != fixture.SHA256 {
		t.Fatal("original downloaded bytes changed")
	}
	doc, err := citation.Parse(data)
	if fixture.YAMLStatus == "invalid" {
		if err == nil || !errors.Is(err, citation.ErrSyntax) {
			t.Fatalf("malformed fixture: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if fixture.DeclaredVersion != nil && doc.CFFVersion() != *fixture.DeclaredVersion {
		t.Fatalf("declared version changed: %s", doc.CFFVersion())
	}
	for field, want := range fixture.ExpectedMetadata {
		if got := doc.Get(field).Text(); got != want {
			t.Errorf("%s differs from reference: got %q, want %q", field, got, want)
		}
	}
	checkCorpusAuthors(t, doc, fixture)
	if got := len(doc.References()); got != fixture.ExpectedReferenceCount {
		t.Errorf("references=%d, want %d", got, fixture.ExpectedReferenceCount)
	}
	checkCorpusValidation(t, doc, fixture.SchemaStatus)
}

func checkCorpusValidation(t *testing.T, doc *citation.Document, status string) {
	t.Helper()
	issues := doc.Validate()
	if doc.CFFVersion() != "1.2.0" {
		if len(issues) != 1 || issues[0].Code != unsupportedVersionCode {
			t.Fatalf("wrong historical schema applied: %+v", issues)
		}
	} else if (len(issues) == 0) != (status == "valid") {
		t.Fatalf("reference status=%s, diagnostics=%+v", status, issues)
	}
}

func checkCorpusAuthors(t *testing.T, doc *citation.Document, fixture corpusFixture) {
	t.Helper()
	authors := doc.Authors()
	if len(authors) != len(fixture.ExpectedAuthors) {
		t.Fatalf("authors=%d, want %d", len(authors), len(fixture.ExpectedAuthors))
	}
	for i, expected := range fixture.ExpectedAuthors {
		for field, value := range expected {
			if want, ok := value.(string); ok {
				if got := authors[i].Get(field).Text(); got != want {
					t.Errorf("authors[%d].%s=%q, want %q", i, field, got, want)
				}
			}
		}
	}
}

func TestPreferredCitationFromRepository(t *testing.T) {
	doc, err := citation.ReadFile("testdata/real-world/simonehagey--orbdot.cff", citation.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	preferred, ok := doc.PreferredCitation()
	if !ok || preferred.Title() != doc.Title() || preferred.Type() != "article" {
		t.Fatalf("preferred citation: %+v", preferred)
	}
	if authors := preferred.Authors(); len(authors) != 2 || authors[0].FamilyNames() != "Hagey" {
		t.Fatalf("preferred authors: %+v", authors)
	}
	if got := preferred.Get("publisher").Get("name"); got.Text() != "Open Journals" || got.Position().Line != 26 {
		t.Fatalf("publisher metadata: %+v", got)
	}
}
