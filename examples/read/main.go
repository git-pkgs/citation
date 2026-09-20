// Command read demonstrates parsing a file or a Git blob supplied on standard input.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/git-pkgs/citation"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	const argumentCount = 2
	if len(os.Args) != argumentCount {
		return fmt.Errorf("usage: read <CITATION.cff|->")
	}
	var doc *citation.Document
	var err error
	if os.Args[1] == "-" {
		doc, err = citation.Read(os.Stdin, citation.ParseOptions{})
	} else {
		doc, err = citation.ReadFile(os.Args[1], citation.ParseOptions{})
	}
	if err != nil {
		return err
	}
	result := struct {
		Title       string                `json:"title"`
		CFFVersion  string                `json:"cff_version"`
		Authors     []string              `json:"authors"`
		Diagnostics []citation.Diagnostic `json:"diagnostics,omitempty"`
	}{Title: doc.Title(), CFFVersion: doc.CFFVersion(), Diagnostics: doc.Validate()}
	for _, author := range doc.Authors() {
		name := author.Name()
		if !author.IsEntity() {
			name = author.GivenNames() + " " + author.FamilyNames()
		}
		result.Authors = append(result.Authors, name)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
