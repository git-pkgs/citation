package citation

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// ParseOptions bounds work for untrusted inputs. Zero fields select defaults.
type ParseOptions struct {
	MaxBytes       int64
	MaxDepth       int
	MaxNodes       int
	MaxScalarBytes int
	MaxNumberBytes int
	MaxDiagnostics int
}

const (
	defaultMaxBytes       = 1_048_576
	defaultMaxScalarBytes = 4_194_304
)

func (o ParseOptions) defaults() (ParseOptions, error) {
	if o.MaxBytes < 0 || o.MaxBytes == math.MaxInt64 || o.MaxDepth < 0 || o.MaxNodes < 0 || o.MaxScalarBytes < 0 || o.MaxNumberBytes < 0 || o.MaxDiagnostics < 0 {
		return o, failure(ErrOptions, "options", "limits must be positive finite values", Position{})
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = defaultMaxBytes
	}
	if o.MaxDepth == 0 {
		o.MaxDepth = 64
	}
	if o.MaxNodes == 0 {
		o.MaxNodes = 100000
	}
	if o.MaxScalarBytes == 0 {
		o.MaxScalarBytes = defaultMaxScalarBytes
	}
	if o.MaxDiagnostics == 0 {
		o.MaxDiagnostics = 100
	}
	if o.MaxNumberBytes == 0 {
		o.MaxNumberBytes = 4096
	}
	return o, nil
}

func Parse(data []byte) (*Document, error) { return ParseWithOptions(data, ParseOptions{}) }

func ParseWithOptions(data []byte, opts ParseOptions) (*Document, error) {
	opts, err := opts.defaults()
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > opts.MaxBytes {
		return nil, failure(ErrLimit, "byte_limit", "input exceeds byte limit", Position{})
	}
	root, err := parseYAML(data, opts)
	if err != nil {
		return nil, err
	}
	if root.kind != Mapping {
		return nil, failure(ErrType, "root_type", "document root must be a mapping", root.pos)
	}
	return &Document{root: root, maxDiagnostics: opts.MaxDiagnostics}, nil
}

func Read(r io.Reader, opts ParseOptions) (*Document, error) {
	opts, err := opts.defaults()
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, failure(ErrType, "reader", "reader is nil", Position{})
	}
	data, err := io.ReadAll(io.LimitReader(r, opts.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("citation: read: %w", err)
	}
	return ParseWithOptions(data, opts)
}

func ReadFile(path string, opts ParseOptions) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("citation: open: %w", err)
	}
	doc, readErr := Read(f, opts)
	if err := errors.Join(readErr, f.Close()); err != nil {
		return nil, err
	}
	return doc, nil
}
