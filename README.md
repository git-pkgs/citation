# citation

A Go library for parsing `CITATION.cff` files and validating citation metadata, with no third-party dependencies. Reads bytes, readers, or files and preserves source positions, unknown fields, and numeric spelling.

## Installation

```bash
go get github.com/git-pkgs/citation
```

## Usage

```go
package main

import (
	"fmt"
	"log"

	"github.com/git-pkgs/citation"
)

func main() {
	doc, err := citation.ReadFile("CITATION.cff", citation.ParseOptions{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Title: %s\n", doc.Title())
	fmt.Printf("CFF version: %s\n", doc.CFFVersion())
	for _, author := range doc.Authors() {
		if author.IsEntity() {
			fmt.Println(author.Name())
		} else {
			fmt.Println(author.GivenNames(), author.FamilyNames())
		}
	}
	for _, issue := range doc.Validate() {
		fmt.Println(issue.Path, issue.Code, issue.Message)
	}
}
```

Use `Parse(data)` for bytes from a Git blob or another source, `ParseWithOptions(data, opts)` to set limits, or `Read(reader, opts)` for an `io.Reader`. `Read` leaves the reader open. Repository discovery, blob caching, and concurrency limits remain with the caller.

Documents are immutable and can be read concurrently. Parsing copies retained text, so callers may reuse input buffers after a call returns. Collection accessors return copies of their slices.

### Metadata

`Authors`, `Contact`, `References`, and `PreferredCitation` provide typed views of common fields. Use `Get` to access any field, including unknown fields and invalid metadata:

```go
fmt.Println(doc.Get("doi").Text())
for _, keyword := range doc.Get("keywords").Items() {
	fmt.Println(keyword.Text())
}
```

Values expose `Kind`, `Text`, `Items`, `Fields`, and `Position`. A missing field has kind `Missing`, distinct from an explicit YAML `Null`.

### Errors

The library returns errors without logging or terminating the process. Use `errors.Is` with `ErrSyntax`, `ErrType`, `ErrUnsupported`, `ErrLimit`, or `ErrOptions` to distinguish failures. `errors.As` exposes a `*citation.Error` with a diagnostic, and file and reader errors retain their underlying causes.

## Compatibility

Parsing and validation are separate. `Validate` checks CFF 1.2.0 against rules generated from the checked-in official schema. Other versions produce an `unsupported_version` diagnostic; parsing preserves their declared version and metadata. A successful parse alone does not establish CFF validity.

The parser supports UTF-8 YAML block and flow collections, multiline scalars, standard scalar escapes, core tags, anchors, and aliases. Duplicate keys, multiple documents, cycles, and unresolved aliases are errors. Merge keys, custom tags, and non-UTF-8 encodings are unsupported. Full YAML conformance has not been established. Historical schema validation, editing, YAML output, and citation formatting are not implemented yet.

## Limits

Default limits are 1 MiB of input, depth 64, 100,000 nodes including alias expansion, 4 MiB of scalar content, 4,096 bytes per numeric scalar, and 100 validation diagnostics. `ParseOptions` allows larger finite limits; zero selects the defaults and negative limits are errors. Validation adds a `diagnostic_limit` entry when further problems are omitted. A blocked reader requires a deadline supplied by its caller.

## Testing

```bash
go test ./...
go test -race ./...
go test -fuzz=FuzzParse -fuzztime=30s
```

Tests use checked-in fixtures and run offline. `go generate ./...` regenerates validation rules from the checked-in schema and rejects unknown schema keywords.

Run the example with `go run ./examples/read path/to/CITATION.cff`, or supply `-` to read a Git blob through standard input.

## License

[MIT](LICENSE)
