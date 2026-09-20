package citation

import (
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

//go:generate go run ./internal/generate

const (
	cffVersionField = "cff-version"
	typeCode        = "type"
	objectType      = "object"
	arrayType       = "array"
	stringType      = "string"
	integerType     = "integer"
	numberType      = "number"
	uriFormat       = "uri"
	decimalBase     = 10
)

type schemaRule struct {
	kind         string
	format       string
	properties   map[string]*schemaRule
	required     []string
	closed       bool
	items        *schemaRule
	minItems     int
	uniqueItems  bool
	minLength    int
	maxLength    int
	minimum      int
	maximum      int
	hasMinimum   bool
	hasMaximum   bool
	pattern      *regexp.Regexp
	enum         []string
	alternatives []*schemaRule
	oneOf        bool
}

// Validate checks the declared schema without modifying metadata.
// Unsupported versions receive a diagnostic rather than current-version rules.
func (d *Document) Validate() []Diagnostic {
	if d == nil {
		return []Diagnostic{{Code: "document", Message: "document is nil"}}
	}
	v := d.Get(cffVersionField)
	if v.kind == Missing {
		return []Diagnostic{{Code: "required", Path: cffVersionField, Message: "required field is missing"}}
	}
	if v.kind != String {
		return []Diagnostic{{Code: typeCode, Path: cffVersionField, Message: "expected a string", Position: v.pos}}
	}
	if v.text != "1.2.0" {
		return []Diagnostic{{Code: "unsupported_version", Path: cffVersionField, Message: "unsupported CFF version: " + v.text, Position: v.pos}}
	}
	check := validator{limit: d.maxDiagnostics}
	if check.limit == 0 {
		check.limit = 100
	}
	check.value(d.root, cff120, "")
	slices.SortStableFunc(check.diagnostics, func(a, b Diagnostic) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		if a.Column != b.Column {
			return a.Column - b.Column
		}
		return strings.Compare(a.Path, b.Path)
	})
	if check.truncated {
		check.diagnostics = append(check.diagnostics, Diagnostic{Code: "diagnostic_limit", Message: "additional diagnostics omitted"})
	}
	return check.diagnostics
}

type validator struct {
	diagnostics []Diagnostic
	limit       int
	truncated   bool
}

func (c *validator) add(v Value, path, code, message string) {
	if len(c.diagnostics) >= c.limit {
		c.truncated = true
		return
	}
	c.diagnostics = append(c.diagnostics, Diagnostic{Code: code, Path: path, Message: message, Position: v.pos})
}

func (c *validator) value(v Value, rule *schemaRule, path string) {
	if len(c.diagnostics) >= c.limit {
		c.truncated = true
		return
	}
	if len(rule.alternatives) != 0 {
		c.alternative(v, rule, path)
		return
	}
	if !matchesType(v, rule.kind) {
		c.add(v, path, typeCode, "expected "+rule.kind)
		return
	}
	if len(rule.enum) != 0 && !slices.Contains(rule.enum, v.text) {
		c.add(v, path, "enum", "value is outside the allowed enumeration")
	}
	if v.kind == String {
		c.string(v, rule, path)
	}
	if v.kind == Number {
		c.number(v, rule, path)
	}
	if v.kind == Mapping {
		c.mapping(v, rule, path)
	}
	if v.kind == Sequence {
		c.sequence(v, rule, path)
	}
}

func matchesType(v Value, kind string) bool {
	switch kind {
	case "":
		return true
	case objectType:
		return v.kind == Mapping
	case arrayType:
		return v.kind == Sequence
	case stringType:
		return v.kind == String
	case numberType, integerType:
		if v.kind != Number {
			return false
		}
		_, exponent, ok := decimal(v.text)
		return ok && (kind == numberType || exponent.Sign() >= 0)
	default:
		return false
	}
}

func (c *validator) alternative(v Value, rule *schemaRule, path string) {
	matched := 0
	var closest []Diagnostic
	for _, option := range rule.alternatives {
		branch := validator{limit: c.limit}
		branch.value(v, option, path)
		if len(branch.diagnostics) == 0 {
			matched++
		} else if closest == nil || len(branch.diagnostics) < len(closest) {
			closest = branch.diagnostics
		}
	}
	if matched > 0 && (!rule.oneOf || matched == 1) {
		return
	}
	if matched == 0 && len(closest) != 0 {
		for _, issue := range closest {
			c.add(Value{pos: issue.Position}, issue.Path, issue.Code, issue.Message)
		}
		return
	}
	c.add(v, path, "alternatives", "value must match exactly one allowed alternative")
}

func (c *validator) string(v Value, rule *schemaRule, path string) {
	length := utf8.RuneCountInString(v.text)
	if length < rule.minLength {
		c.add(v, path, "min_length", "string is too short")
	}
	if rule.maxLength > 0 && length > rule.maxLength {
		c.add(v, path, "max_length", "string is too long")
	}
	if rule.pattern != nil && !rule.pattern.MatchString(v.text) {
		c.add(v, path, "pattern", "string does not match the required pattern")
	}
	switch rule.format {
	case "date":
		if _, err := time.Parse("2006-01-02", v.text); err != nil {
			c.add(v, path, "format", "expected a calendar date in YYYY-MM-DD form")
		}
	case uriFormat:
		if !validURI(v.text) {
			c.add(v, path, "format", "expected an absolute URI")
		}
	}
}

func validURI(text string) bool {
	if strings.ContainsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || r < 32 || r >= 127 }) {
		return false
	}
	u, err := url.Parse(text)
	return err == nil && u.IsAbs()
}

func (c *validator) number(v Value, rule *schemaRule, path string) {
	if !rule.hasMinimum && !rule.hasMaximum {
		return
	}
	if rule.hasMinimum && compareInteger(v.text, rule.minimum) < 0 {
		c.add(v, path, "minimum", "number is below the minimum")
	}
	if rule.hasMaximum && compareInteger(v.text, rule.maximum) > 0 {
		c.add(v, path, "maximum", "number is above the maximum")
	}
}

func compareInteger(text string, bound int) int {
	coefficient, exponent, _ := decimal(text)
	other := strconv.Itoa(bound)
	if coefficient == "0" && bound == 0 {
		return 0
	}
	if strings.HasPrefix(coefficient, "-") != (bound < 0) {
		if strings.HasPrefix(coefficient, "-") {
			return -1
		}
		return 1
	}
	sign := 1
	if bound < 0 {
		sign = -1
		coefficient = coefficient[1:]
		other = other[1:]
	}
	if coefficient == "0" {
		return -sign
	}
	if other == "0" {
		return sign
	}
	digits := new(big.Int).Add(exponent, big.NewInt(int64(len(coefficient))))
	if cmp := digits.Cmp(big.NewInt(int64(len(other)))); cmp != 0 {
		return sign * cmp
	}
	length := max(len(coefficient), len(other))
	left := coefficient + strings.Repeat("0", length-len(coefficient))
	right := other + strings.Repeat("0", length-len(other))
	return sign * strings.Compare(left, right)
}

func fieldPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}

func (c *validator) mapping(v Value, rule *schemaRule, path string) {
	for _, required := range rule.required {
		if v.Get(required).kind == Missing {
			c.add(Value{pos: v.pos}, fieldPath(path, required), "required", "required field is missing")
		}
	}
	for _, field := range v.fields {
		if c.truncated {
			return
		}
		child, ok := rule.properties[field.Name]
		if ok {
			c.value(field.Value, child, fieldPath(path, field.Name))
		} else if rule.closed {
			c.add(field.Value, fieldPath(path, field.Name), "unknown_field", "field is not allowed by this CFF version")
		}
	}
}

func (c *validator) sequence(v Value, rule *schemaRule, path string) {
	if len(v.items) < rule.minItems {
		c.add(v, path, "min_items", "array has too few entries")
	}
	seen := make(map[string]bool)
	for i, item := range v.items {
		if c.truncated {
			return
		}
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		if rule.items != nil {
			before := len(c.diagnostics)
			c.value(item, rule.items, itemPath)
			if c.truncated || len(c.diagnostics) != before {
				continue
			}
		}
		if rule.uniqueItems {
			key := canonical(item)
			if seen[key] {
				c.add(item, itemPath, "unique_items", "duplicate array entry")
			}
			seen[key] = true
		}
	}
}

func canonical(v Value) string {
	var b strings.Builder
	writeCanonical(&b, v)
	return b.String()
}

func writeCanonical(b *strings.Builder, v Value) {
	fmt.Fprintf(b, "%d:", v.kind)
	switch v.kind {
	case Number:
		coefficient, exponent, ok := decimal(v.text)
		if ok {
			writeCanonicalText(b, coefficient+"e"+exponent.String())
		} else {
			writeCanonicalText(b, v.text)
		}
	case Null:
	case Boolean:
		writeCanonicalText(b, strings.ToLower(v.text))
	case Sequence:
		fmt.Fprintf(b, "%d:", len(v.items))
		for _, item := range v.items {
			writeCanonical(b, item)
		}
	case Mapping:
		fields := v.Fields()
		slices.SortFunc(fields, func(a, b Field) int { return strings.Compare(a.Name, b.Name) })
		fmt.Fprintf(b, "%d:", len(fields))
		for _, field := range fields {
			writeCanonicalText(b, field.Name)
			writeCanonical(b, field.Value)
		}
	default:
		writeCanonicalText(b, v.text)
	}
}

func writeCanonicalText(b *strings.Builder, text string) {
	fmt.Fprintf(b, "%d:", len(text))
	b.WriteString(text)
}

// decimal retains an exact coefficient and exponent without expanding powers of ten.
func decimal(text string) (string, *big.Int, bool) {
	exponent := new(big.Int)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(strings.TrimPrefix(text, "+"), "-")
	if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0o") {
		n, ok := new(big.Int).SetString(text, 0)
		if !ok {
			return "", exponent, false
		}
		text = n.String()
	} else if index := strings.IndexAny(text, "eE"); index >= 0 {
		if _, ok := exponent.SetString(text[index+1:], decimalBase); !ok {
			return "", exponent, false
		}
		text = text[:index]
	}
	if index := strings.IndexByte(text, '.'); index >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(text)-index-1)))
		text = text[:index] + text[index+1:]
	}
	if strings.ContainsFunc(text, func(r rune) bool { return r < '0' || r > '9' }) {
		return "", exponent, false
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		return "0", new(big.Int), true
	}
	trimmed := strings.TrimRight(text, "0")
	exponent.Add(exponent, big.NewInt(int64(len(text)-len(trimmed))))
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed, exponent, true
}
