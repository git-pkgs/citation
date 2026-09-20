package citation

import (
	"strings"
	"unicode/utf8"
)

type yamlLine struct {
	text   string
	indent int
	number int
}

type yamlParser struct {
	source      string
	offset      int
	line        yamlLine
	done        bool
	opts        ParseOptions
	nodes       int
	scalarBytes int
	anchors     map[string]Value
}

func parseYAML(data []byte, opts ParseOptions) (Value, error) {
	if !utf8.Valid(data) {
		return Value{}, failure(ErrSyntax, "encoding", "input must be UTF-8", Position{})
	}
	source := strings.TrimPrefix(string(data), "\ufeff")
	source = strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n")
	for _, r := range source {
		if (r < 32 && r != '\n' && r != '\t') || r == 127 {
			return Value{}, failure(ErrSyntax, "control_character", "input contains a forbidden control character", Position{})
		}
	}
	p := yamlParser{source: source, opts: opts, anchors: make(map[string]Value)}
	p.next()
	p.skip()
	if p.done {
		return Value{}, failure(ErrSyntax, "empty_document", "document is empty", Position{})
	}
	if strings.HasPrefix(p.content(), "%") {
		if stripComment(p.content()) != "%YAML 1.2" {
			return Value{}, p.err(ErrUnsupported, "directive", "unsupported YAML directive")
		}
		p.next()
		p.skip()
		if p.done || stripComment(p.content()) != "---" {
			return Value{}, p.err(ErrSyntax, "directive", "YAML directive requires a document start marker")
		}
	}
	if stripComment(p.content()) == "---" {
		p.next()
		p.skip()
	}
	if p.done {
		return Value{}, failure(ErrSyntax, "empty_document", "document is empty", Position{})
	}
	v, err := p.block(p.line.indent, 1)
	if err != nil {
		return Value{}, err
	}
	p.skip()
	if !p.done && stripComment(p.content()) == "..." {
		p.next()
		p.skip()
	}
	if !p.done {
		return Value{}, p.err(ErrSyntax, "trailing_content", "unexpected content after document")
	}
	return v, nil
}

func (p *yamlParser) next() {
	if p.offset > len(p.source) {
		p.done = true
		return
	}
	text, _, _ := strings.Cut(p.source[p.offset:], "\n")
	p.offset += len(text) + 1
	p.line = yamlLine{
		text:   text,
		indent: len(text) - len(strings.TrimLeft(text, " ")),
		number: p.line.number + 1,
	}
}

func (p *yamlParser) content() string {
	if p.done {
		return ""
	}
	l := p.line
	return l.text[l.indent:]
}

func (p *yamlParser) skip() {
	for !p.done {
		if strings.TrimSpace(stripComment(p.content())) != "" {
			return
		}
		p.next()
	}
}

func (p *yamlParser) pos() Position {
	if p.done {
		return Position{}
	}
	l := p.line
	return Position{Line: l.number, Column: l.indent + 1}
}

func (p *yamlParser) err(cause error, code, message string) error {
	return failure(cause, code, message, p.pos())
}

func (p *yamlParser) checkIndent() error {
	if strings.HasPrefix(p.content(), "\t") {
		return p.err(ErrSyntax, "indentation", "tabs cannot indent a YAML block")
	}
	return nil
}

func (p *yamlParser) charge(v Value, depth int) error {
	if v.kind == Number && len(v.text) > p.opts.MaxNumberBytes {
		return failure(ErrLimit, "number_limit", "numeric scalar exceeds byte limit", v.pos)
	}
	if depth > p.opts.MaxDepth {
		return failure(ErrLimit, "depth_limit", "nesting exceeds depth limit", v.pos)
	}
	if p.nodes >= p.opts.MaxNodes {
		return failure(ErrLimit, "node_limit", "document exceeds node limit", v.pos)
	}
	p.nodes++
	if len(v.text) > p.opts.MaxScalarBytes-p.scalarBytes {
		return failure(ErrLimit, "scalar_limit", "document exceeds scalar byte limit", v.pos)
	}
	p.scalarBytes += len(v.text)
	return nil
}

func (p *yamlParser) block(indent, depth int) (Value, error) {
	p.skip()
	pos := p.pos()
	if depth > p.opts.MaxDepth {
		return Value{}, failure(ErrLimit, "depth_limit", "nesting exceeds depth limit", pos)
	}
	if err := p.checkIndent(); err != nil {
		return Value{}, err
	}
	if isSequence(p.content()) {
		return p.sequence(indent, depth)
	}
	if mappingColon(p.content()) >= 0 {
		return p.mapping(indent, depth)
	}
	s := p.content()
	p.next()
	return p.value(s, indent-1, depth, pos)
}

func isSequence(s string) bool {
	return s == "-" || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "-\t")
}

func (p *yamlParser) mapping(indent, depth int) (Value, error) {
	v := Value{kind: Mapping, pos: p.pos()}
	if err := p.charge(v, depth); err != nil {
		return Value{}, err
	}
	seen := make(map[string]bool)
	for !p.done {
		p.skip()
		if err := p.checkIndent(); err != nil {
			return Value{}, err
		}
		if p.done || p.line.indent != indent || isSequence(p.content()) {
			break
		}
		s := p.content()
		colon := mappingColon(s)
		if colon < 0 {
			break
		}
		pos := p.pos()
		key, err := p.key(strings.TrimSpace(s[:colon]), pos, depth+1)
		if err != nil {
			return Value{}, err
		}
		if seen[key] {
			return Value{}, failure(ErrSyntax, "duplicate_key", "duplicate mapping key: "+key, pos)
		}
		seen[key] = true
		p.next()
		valuePos := Position{Line: pos.Line, Column: pos.Column + utf8.RuneCountInString(s[:colon+1])}
		rest := s[colon+1:]
		valuePos.Column += len(rest) - len(strings.TrimLeft(rest, " \t"))
		item, err := p.value(strings.TrimSpace(rest), indent, depth+1, valuePos)
		if err != nil {
			return Value{}, err
		}
		v.fields = append(v.fields, Field{Name: key, Value: item, Position: pos})
	}
	return v, nil
}

func (p *yamlParser) key(s string, pos Position, depth int) (string, error) {
	f := flowParser{source: s, owner: p, pos: pos}
	v, err := f.parse(depth)
	if err != nil {
		return "", err
	}
	f.space()
	if f.index != len(s) || v.kind != String {
		return "", failure(ErrType, "key_type", "mapping keys must be strings", pos)
	}
	if v.text == "<<" {
		return "", failure(ErrUnsupported, "merge_key", "YAML merge keys are unsupported", pos)
	}
	return v.text, nil
}

func (p *yamlParser) sequence(indent, depth int) (Value, error) {
	v := Value{kind: Sequence, pos: p.pos()}
	if err := p.charge(v, depth); err != nil {
		return Value{}, err
	}
	for !p.done {
		p.skip()
		if err := p.checkIndent(); err != nil {
			return Value{}, err
		}
		if p.done || p.line.indent != indent || !isSequence(p.content()) {
			break
		}
		s := p.content()
		rest := strings.TrimLeft(s[1:], " \t")
		width := len(s) - len(rest)
		pos := p.pos()
		pos.Column += width
		var item Value
		var err error
		if mappingColon(rest) >= 0 || isSequence(rest) {
			line := &p.line
			line.indent = indent + width
			line.text = strings.Repeat(" ", line.indent) + rest
			item, err = p.block(line.indent, depth+1)
		} else {
			p.next()
			item, err = p.value(rest, indent, depth+1, pos)
		}
		if err != nil {
			return Value{}, err
		}
		v.items = append(v.items, item)
	}
	return v, nil
}

func (p *yamlParser) value(s string, parent, depth int, pos Position) (Value, error) {
	if depth > p.opts.MaxDepth {
		return Value{}, failure(ErrLimit, "depth_limit", "nesting exceeds depth limit", pos)
	}
	s = strings.TrimSpace(stripComment(s))
	anchor, tag, rest, err := properties(s, pos)
	if err != nil {
		return Value{}, err
	}
	s = rest
	var v Value
	switch {
	case s == "":
		p.skip()
		if !p.done && (p.line.indent > parent || (p.line.indent == parent && isSequence(p.content()))) {
			v, err = p.block(p.line.indent, depth)
		} else {
			v = Value{kind: Null, pos: pos}
			err = p.charge(v, depth)
		}
	case s[0] == '|' || s[0] == '>':
		v, err = p.blockScalar(s, parent, depth, pos)
	default:
		if strings.ContainsAny(s[:1], "[{'\"") {
			s, err = p.gather(s)
		} else if s[0] != '*' {
			s, err = p.plainContinuation(s, parent)
		}
		if err == nil {
			f := flowParser{source: s, owner: p, pos: pos}
			v, err = f.parse(depth)
			f.space()
			if err == nil && f.index != len(s) {
				err = failure(ErrSyntax, "trailing_value", "unexpected content after value", pos)
			}
		}
	}
	if err != nil {
		return Value{}, err
	}
	if tag != "" {
		v, err = p.tag(v, tag)
		if err != nil {
			return Value{}, err
		}
	}
	if anchor != "" {
		p.anchors[anchor] = v
	}
	return v, nil
}

func (p *yamlParser) plainContinuation(s string, parent int) (string, error) {
	var b strings.Builder
	b.WriteString(s)
	blank := 0
	for !p.done {
		l := p.line
		text := strings.TrimSpace(l.text)
		if text == "" {
			blank++
			p.next()
			continue
		}
		if strings.HasPrefix(text, "#") {
			break
		}
		if l.indent <= parent {
			break
		}
		if mappingColon(text) >= 0 || isSequence(text) {
			return "", p.err(ErrSyntax, "indentation", "unexpected indentation before collection entry")
		}
		if blank > 0 {
			b.WriteString(strings.Repeat("\n", blank))
		} else {
			b.WriteByte(' ')
		}
		b.WriteString(stripComment(text))
		blank = 0
		p.next()
	}
	return b.String(), nil
}

func (p *yamlParser) blockScalar(header string, parent, depth int, pos Position) (Value, error) {
	style := header[0]
	chomp, indent, err := blockHeader(header, parent, pos)
	if err != nil {
		return Value{}, err
	}
	if indent == 0 {
		indent = p.scalarIndent(parent)
	}
	text := p.foldBlock(indent, style)
	if chomp == '-' {
		text = strings.TrimRight(text, "\n")
	}
	if chomp == 0 && text != "" {
		text = strings.TrimRight(text, "\n") + "\n"
	}
	v := Value{kind: String, text: strings.Clone(text), pos: pos}
	return v, p.charge(v, depth)
}

func blockHeader(header string, parent int, pos Position) (byte, int, error) {
	chomp := byte(0)
	indent := 0
	for _, c := range header[1:] {
		switch {
		case (c == '+' || c == '-') && chomp == 0:
			chomp = byte(c)
		case c >= '1' && c <= '9' && indent == 0:
			indent = parent + int(c-'0')
		default:
			return 0, 0, failure(ErrSyntax, "block_header", "invalid block scalar header", pos)
		}
	}
	return chomp, indent, nil
}

func (p *yamlParser) scalarIndent(parent int) int {
	for scan := *p; !scan.done; scan.next() {
		if strings.TrimSpace(scan.line.text) != "" {
			return max(parent+1, scan.line.indent)
		}
	}
	return parent + 1
}

func (p *yamlParser) scalarLine(indent int) (string, bool) {
	if p.done || (strings.TrimSpace(p.line.text) != "" && p.line.indent < indent) {
		return "", false
	}
	// A terminal newline does not introduce another physical line.
	if p.offset > len(p.source) && p.line.text == "" {
		return "", false
	}
	if len(p.line.text) < indent {
		return "", true
	}
	return p.line.text[indent:], true
}

func (p *yamlParser) foldBlock(indent int, style byte) string {
	lastContent := 0
	if style == '>' {
		for scan := *p; !scan.done; scan.next() {
			line, ok := scan.scalarLine(indent)
			if !ok {
				break
			}
			if line != "" {
				lastContent = scan.line.number
			}
		}
	}
	var b strings.Builder
	for {
		line, ok := p.scalarLine(indent)
		if !ok {
			break
		}
		b.WriteString(line)
		number := p.line.number
		p.next()
		next, more := p.scalarLine(indent)
		if style != '>' || !more || strings.HasPrefix(line, " ") || strings.HasPrefix(next, " ") || line == "" || number >= lastContent {
			b.WriteByte('\n')
		} else if next != "" {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func stripComment(s string) string {
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote == '"' && c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if (c == '\'' || c == '"') && startsQuoted(s, i) {
			quote = c
		}
		if c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return strings.TrimRight(s[:i], " \t")
		}
	}
	return s
}

func mappingColon(s string) int {
	quote := byte(0)
	level := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote == '"' && c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			if startsQuoted(s, i) {
				quote = c
			}
		case '[', '{':
			level++
		case ']', '}':
			level--
		case '#':
			if i == 0 || s[i-1] == ' ' {
				return -1
			}
		case ':':
			if level == 0 && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
				return i
			}
		}
	}
	return -1
}

func startsQuoted(s string, index int) bool {
	for index > 0 && (s[index-1] == ' ' || s[index-1] == '\t') {
		index--
	}
	return index == 0 || strings.ContainsRune("[{,:-", rune(s[index-1]))
}
