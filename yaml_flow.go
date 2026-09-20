package citation

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var yamlNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+|(?:[0-9]+\.[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?|[0-9]+[eE][+-]?[0-9]+)$`)

var yamlInteger = regexp.MustCompile(`^[+-]?(?:[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+)$`)

type flowParser struct {
	source        string
	index         int
	owner         *yamlParser
	pos           Position
	inFlow        bool
	positionIndex int
	lastPosition  Position
}

func (f *flowParser) position() Position {
	p := f.lastPosition
	if f.positionIndex == 0 {
		p = f.pos
	}
	for _, r := range f.source[f.positionIndex:f.index] {
		if r == '\n' {
			p.Line++
			p.Column = 1
		} else {
			p.Column++
		}
	}
	f.positionIndex, f.lastPosition = f.index, p
	return p
}

func (f *flowParser) space() {
	for f.index < len(f.source) {
		if strings.ContainsRune(" \t\n", rune(f.source[f.index])) {
			f.index++
			continue
		}
		if f.source[f.index] == '#' {
			for f.index < len(f.source) && f.source[f.index] != '\n' {
				f.index++
			}
			continue
		}
		break
	}
}

func (f *flowParser) parse(depth int) (Value, error) {
	f.space()
	pos := f.position()
	if depth > f.owner.opts.MaxDepth {
		return Value{}, failure(ErrLimit, "depth_limit", "nesting exceeds depth limit", pos)
	}
	if f.index == len(f.source) {
		v := Value{kind: Null, pos: pos}
		return v, f.owner.charge(v, depth)
	}
	switch f.source[f.index] {
	case '[', '{':
		return f.collection(depth)
	case '\'', '"':
		text, err := f.quoted()
		if err != nil {
			return Value{}, err
		}
		v := Value{kind: String, text: text, pos: pos}
		return v, f.owner.charge(v, depth)
	case '*':
		return f.alias(depth)
	case '&', '!':
		anchor, tag, rest, err := properties(f.source[f.index:], pos)
		if err != nil {
			return Value{}, err
		}
		f.index = len(f.source) - len(rest)
		v, err := f.parse(depth)
		if err != nil {
			return Value{}, err
		}
		if tag != "" {
			v, err = f.owner.tag(v, tag)
		}
		if err == nil && anchor != "" {
			f.owner.anchors[anchor] = v
		}
		return v, err
	case ']', '}', ',', '@', '`', '%':
		return Value{}, failure(ErrSyntax, "value", "unexpected character at start of value", pos)
	}
	start := f.index
	for f.index < len(f.source) {
		c := f.source[f.index]
		if f.inFlow && strings.ContainsRune(",[]{}", rune(c)) {
			break
		}
		if c == ':' && (f.index+1 == len(f.source) || strings.ContainsRune(" \t\n", rune(f.source[f.index+1]))) {
			break
		}
		if c == '#' && (f.index == start || strings.ContainsRune(" \t\n", rune(f.source[f.index-1]))) {
			break
		}
		f.index++
	}
	if f.index == start {
		return Value{}, failure(ErrSyntax, "value", "expected a scalar value", pos)
	}
	text := strings.TrimSpace(f.source[start:f.index])
	if f.inFlow && strings.Contains(text, "\n") {
		text = strings.Join(strings.Fields(text), " ")
	}
	v := scalar(text, pos)
	return v, f.owner.charge(v, depth)
}

func scalar(text string, pos Position) Value {
	kind := String
	switch text {
	case "", "~", "null", "Null", "NULL":
		kind = Null
	case "true", "True", "TRUE", "false", "False", "FALSE":
		kind = Boolean
	case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF", "-.inf", "-.Inf", "-.INF", ".nan", ".NaN", ".NAN":
		kind = Number
	default:
		if yamlNumber.MatchString(text) {
			kind = Number
		}
	}
	return Value{kind: kind, text: strings.Clone(text), pos: pos}
}

func (f *flowParser) collection(depth int) (Value, error) {
	open := f.source[f.index]
	closeByte, kind := byte(']'), Sequence
	if open == '{' {
		closeByte, kind = '}', Mapping
	}
	v := Value{kind: kind, pos: f.position()}
	if err := f.owner.charge(v, depth); err != nil {
		return Value{}, err
	}
	f.index++
	previous := f.inFlow
	f.inFlow = true
	defer func() { f.inFlow = previous }()
	seen := make(map[string]bool)
	for {
		f.space()
		if f.index == len(f.source) {
			return Value{}, failure(ErrSyntax, "flow_end", "unterminated flow collection", v.pos)
		}
		if f.source[f.index] == closeByte {
			f.index++
			return v, nil
		}
		if kind == Mapping {
			field, err := f.field(depth + 1)
			if err != nil {
				return Value{}, err
			}
			if seen[field.Name] {
				return Value{}, failure(ErrSyntax, "duplicate_key", "duplicate mapping key: "+field.Name, field.Position)
			}
			seen[field.Name] = true
			v.fields = append(v.fields, field)
		} else {
			item, err := f.parse(depth + 1)
			if err != nil {
				return Value{}, err
			}
			v.items = append(v.items, item)
		}
		f.space()
		if f.index == len(f.source) {
			return Value{}, failure(ErrSyntax, "flow_end", "unterminated flow collection", v.pos)
		}
		if f.source[f.index] == closeByte {
			f.index++
			return v, nil
		}
		if f.source[f.index] != ',' {
			return Value{}, failure(ErrSyntax, "flow_separator", "expected a comma between flow entries", f.position())
		}
		f.index++
	}
}

func (f *flowParser) field(depth int) (Field, error) {
	pos := f.position()
	var key Value
	var err error
	if f.source[f.index] == '\'' || f.source[f.index] == '"' {
		key, err = f.parse(depth)
	} else {
		start := f.index
		for f.index < len(f.source) && !strings.ContainsRune(",{}[]\n", rune(f.source[f.index])) {
			c := f.source[f.index]
			if c == ':' && (f.index+1 == len(f.source) || strings.ContainsRune(" \t\n,[]{}", rune(f.source[f.index+1]))) {
				break
			}
			if c == '#' && (f.index == start || strings.ContainsRune(" \t", rune(f.source[f.index-1]))) {
				break
			}
			f.index++
		}
		key = scalar(strings.TrimSpace(f.source[start:f.index]), pos)
		err = f.owner.charge(key, depth)
	}
	if err != nil {
		return Field{}, err
	}
	if key.kind != String {
		return Field{}, failure(ErrType, "key_type", "mapping keys must be strings", pos)
	}
	if key.text == "<<" {
		return Field{}, failure(ErrUnsupported, "merge_key", "YAML merge keys are unsupported", pos)
	}
	f.space()
	if f.index < len(f.source) && (f.source[f.index] == ',' || f.source[f.index] == '}') {
		value := Value{kind: Null, pos: f.position()}
		return Field{Name: key.text, Value: value, Position: pos}, f.owner.charge(value, depth)
	}
	if f.index == len(f.source) || f.source[f.index] != ':' {
		return Field{}, failure(ErrSyntax, "mapping_colon", "expected a colon after mapping key", pos)
	}
	f.index++
	f.space()
	var value Value
	if f.index < len(f.source) && (f.source[f.index] == ',' || f.source[f.index] == '}') {
		value = Value{kind: Null, pos: f.position()}
		err = f.owner.charge(value, depth)
	} else {
		value, err = f.parse(depth)
	}
	return Field{Name: key.text, Value: value, Position: pos}, err
}

func (f *flowParser) alias(depth int) (Value, error) {
	pos := f.position()
	f.index++
	start := f.index
	for f.index < len(f.source) && !strings.ContainsRune(" \t\n,[]{}", rune(f.source[f.index])) {
		f.index++
	}
	name := f.source[start:f.index]
	v, ok := f.owner.anchors[name]
	if !ok {
		return Value{}, failure(ErrSyntax, "alias", "unresolved or cyclic alias: "+name, pos)
	}
	if err := f.owner.chargeAlias(v, depth); err != nil {
		return Value{}, err
	}
	v.pos = pos
	return v, nil
}

func (p *yamlParser) chargeAlias(v Value, depth int) error {
	if err := p.charge(v, depth); err != nil {
		return err
	}
	for _, child := range v.items {
		if err := p.chargeAlias(child, depth+1); err != nil {
			return err
		}
	}
	for _, field := range v.fields {
		if err := p.charge(Value{kind: String, text: field.Name, pos: field.Position}, depth+1); err != nil {
			return err
		}
		if err := p.chargeAlias(field.Value, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func properties(s string, pos Position) (anchor, tag, rest string, err error) {
	rest = s
	for len(rest) > 0 && (rest[0] == '&' || rest[0] == '!') {
		i := strings.IndexAny(rest, " \t\n")
		if i < 0 {
			i = len(rest)
		}
		property := rest[:i]
		if property[0] == '&' {
			if anchor != "" || len(property) == 1 || strings.ContainsAny(property, ",[]{}") {
				return "", "", "", failure(ErrSyntax, "anchor", "invalid anchor property", pos)
			}
			anchor = strings.Clone(property[1:])
		} else {
			if tag != "" {
				return "", "", "", failure(ErrSyntax, "tag", "duplicate tag property", pos)
			}
			tag = property
		}
		rest = strings.TrimLeft(rest[i:], " \t\n")
	}
	return anchor, tag, rest, nil
}

func applyTag(v Value, tag string) (Value, error) {
	tag = strings.TrimPrefix(strings.TrimSuffix(tag, ">"), "!<tag:yaml.org,2002:")
	tag = strings.TrimPrefix(tag, "!!")
	switch tag {
	case "str":
		if v.kind != Mapping && v.kind != Sequence {
			v.kind = String
			return v, nil
		}
	case "null":
		if scalar(v.text, v.pos).kind == Null {
			v.kind = Null
			return v, nil
		}
	case "bool":
		if scalar(v.text, v.pos).kind == Boolean {
			v.kind = Boolean
			return v, nil
		}
	case "int":
		if yamlInteger.MatchString(v.text) {
			v.kind = Number
			return v, nil
		}
	case "float":
		if scalar(v.text, v.pos).kind == Number {
			v.kind = Number
			return v, nil
		}
	case "map":
		if v.kind == Mapping {
			return v, nil
		}
	case "seq":
		if v.kind == Sequence {
			return v, nil
		}
	default:
		return Value{}, failure(ErrUnsupported, "tag", "unsupported YAML tag: "+tag, v.pos)
	}
	return Value{}, failure(ErrType, "tag_type", "value does not match YAML tag: "+tag, v.pos)
}

func (p *yamlParser) tag(v Value, tag string) (Value, error) {
	v, err := applyTag(v, tag)
	if err == nil && v.kind == Number && len(v.text) > p.opts.MaxNumberBytes {
		return Value{}, failure(ErrLimit, "number_limit", "numeric scalar exceeds byte limit", v.pos)
	}
	return v, err
}

func (f *flowParser) quoted() (string, error) {
	quote := f.source[f.index]
	f.index++
	var b strings.Builder
	for f.index < len(f.source) {
		c := f.source[f.index]
		f.index++
		if c == quote {
			if quote == '\'' && f.index < len(f.source) && f.source[f.index] == '\'' {
				b.WriteByte('\'')
				f.index++
				continue
			}
			return b.String(), nil
		}
		if c == '\n' {
			f.foldQuoted(&b)
			continue
		}
		if c == ' ' || c == '\t' {
			start := f.index - 1
			for f.index < len(f.source) && (f.source[f.index] == ' ' || f.source[f.index] == '\t') {
				f.index++
			}
			if f.index == len(f.source) || f.source[f.index] != '\n' {
				b.WriteString(f.source[start:f.index])
			}
			continue
		}
		if c == '\\' && quote == '"' {
			if err := f.escape(&b); err != nil {
				return "", err
			}
		} else {
			b.WriteByte(c)
		}
	}
	return "", failure(ErrSyntax, "quote", "unterminated quoted scalar", f.pos)
}

func (f *flowParser) foldQuoted(b *strings.Builder) {
	newlines := 0
	for f.index < len(f.source) && strings.ContainsRune(" \t\n", rune(f.source[f.index])) {
		if f.source[f.index] == '\n' {
			newlines++
		}
		f.index++
	}
	if newlines == 0 {
		b.WriteByte(' ')
	} else {
		b.WriteString(strings.Repeat("\n", newlines))
	}
}

func (f *flowParser) escape(b *strings.Builder) error {
	if f.index == len(f.source) {
		return failure(ErrSyntax, "escape", "incomplete escape", f.position())
	}
	c := f.source[f.index]
	f.index++
	const escapes = "0abtnvfre \"/\\N_LP"
	values := [...]rune{0, 7, 8, 9, 10, 11, 12, 13, 27, ' ', '"', '/', '\\', 0x85, 0xa0, 0x2028, 0x2029}
	if i := strings.IndexByte(escapes, c); i >= 0 {
		b.WriteRune(values[i])
		return nil
	}
	if c == '\n' {
		for f.index < len(f.source) && (f.source[f.index] == ' ' || f.source[f.index] == '\t') {
			f.index++
		}
		return nil
	}
	digits := 0
	switch c {
	case 'x':
		digits = 2
	case 'u':
		digits = 4
	case 'U':
		digits = 8
	}
	if digits == 0 || digits > len(f.source)-f.index {
		return failure(ErrSyntax, "escape", "invalid escape", f.position())
	}
	n, err := strconv.ParseUint(f.source[f.index:f.index+digits], 16, 32)
	if err != nil || !utf8.ValidRune(rune(n)) {
		return failure(ErrSyntax, "escape", "invalid Unicode escape", f.position())
	}
	f.index += digits
	b.WriteRune(rune(n))
	return nil
}

func (p *yamlParser) gather(first string) (string, error) {
	var b strings.Builder
	scan := flowBalance{}
	line := first
	for {
		b.WriteString(line)
		scan.line(line)
		if scan.quote == 0 && scan.level <= 0 {
			return b.String(), nil
		}
		if p.done {
			return "", p.err(ErrSyntax, "flow_end", "unterminated quoted scalar or flow collection")
		}
		b.WriteByte('\n')
		line = p.line.text
		p.next()
	}
}

type flowBalance struct {
	quote byte
	level int
}

func (s *flowBalance) line(line string) {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if s.quote != 0 {
			if c == '\\' && s.quote == '"' {
				i++
				continue
			}
			if c == s.quote {
				if s.quote == '\'' && i+1 < len(line) && line[i+1] == '\'' {
					i++
				} else {
					s.quote = 0
				}
			}
			continue
		}
		switch c {
		case '\'', '"':
			if startsQuoted(line, i) {
				s.quote = c
			}
		case '[', '{':
			s.level++
		case ']', '}':
			s.level--
		case '#':
			if i == 0 || line[i-1] == ' ' {
				return
			}
		}
	}
}
