package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateMatchesPinnedRules(t *testing.T) {
	input, err := os.ReadFile("../schema/1.2.0.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../schema_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	writeSchema(t, input)
	if err := run(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("schema_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("generated rules differ; run go generate ./...")
	}
}

func TestGenerateRejectsUnsupportedRules(t *testing.T) {
	for _, input := range []string{
		`{"type":"string","unknownConstraint":true}`,
		`{"type":"string","pattern":"(?=a)"}`,
		`{"type":"string","format":"unknown"}`,
		`{"$ref":"https://example.org/schema"}`,
		`{"$ref":"#/definitions/missing"}`,
	} {
		t.Run(input, func(t *testing.T) {
			writeSchema(t, []byte(input))
			if err := run(); err == nil {
				t.Fatal("accepted an unsupported schema")
			}
			if _, err := os.Stat("schema_generated.go"); !os.IsNotExist(err) {
				t.Fatalf("generated output after failure: %v", err)
			}
		})
	}
}

func writeSchema(t *testing.T, input []byte) {
	t.Helper()
	const directoryMode = 0o755
	const fileMode = 0o644
	dir := t.TempDir()
	path := filepath.Join(dir, "internal", "schema")
	if err := os.MkdirAll(path, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "1.2.0.json"), input, fileMode); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}
