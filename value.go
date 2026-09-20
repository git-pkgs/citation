// Package citation reads Citation File Format metadata without external dependencies.
package citation

// Kind distinguishes YAML scalar types and collections. A zero Value is missing.
type Kind uint8

const (
	Missing Kind = iota
	Null
	String
	Number
	Boolean
	Sequence
	Mapping
)

// Value retains scalar spelling, collection order, and source position.
// Its contents are immutable; accessors return copies of collection slices.
type Value struct {
	kind   Kind
	text   string
	items  []Value
	fields []Field
	pos    Position
}

type Field struct {
	Name     string
	Value    Value
	Position Position
}

func (v Value) Kind() Kind         { return v.kind }
func (v Value) Text() string       { return v.text }
func (v Value) Position() Position { return v.pos }
func (v Value) Items() []Value     { return append([]Value(nil), v.items...) }
func (v Value) Fields() []Field    { return append([]Field(nil), v.fields...) }

func (v Value) Get(name string) Value {
	for _, field := range v.fields {
		if field.Name == name {
			return field.Value
		}
	}
	return Value{}
}

// Document owns the parsed metadata, including unknown and invalid fields.
type Document struct {
	root           Value
	maxDiagnostics int
}

func (d *Document) Get(name string) Value {
	if d == nil {
		return Value{}
	}
	return d.root.Get(name)
}

func (d *Document) Fields() []Field {
	if d == nil {
		return nil
	}
	return d.root.Fields()
}

func (d *Document) Title() string      { return d.Get("title").Text() }
func (d *Document) CFFVersion() string { return d.Get("cff-version").Text() }
func (d *Document) Version() Value     { return d.Get("version") }
func (d *Document) Authors() []Actor   { return actors(d.Get("authors")) }
func (d *Document) Contact() []Actor   { return actors(d.Get("contact")) }

type Actor struct{ value Value }

func (a Actor) Get(name string) Value { return a.value.Get(name) }
func (a Actor) Name() string          { return a.Get("name").Text() }
func (a Actor) GivenNames() string    { return a.Get("given-names").Text() }
func (a Actor) FamilyNames() string   { return a.Get("family-names").Text() }
func (a Actor) IsEntity() bool        { return a.Get("name").Kind() != Missing }

func actors(value Value) []Actor {
	result := make([]Actor, len(value.items))
	for i, item := range value.items {
		result[i] = Actor{value: item}
	}
	return result
}

type Reference struct{ value Value }

func (r Reference) Get(name string) Value { return r.value.Get(name) }
func (r Reference) Title() string         { return r.Get("title").Text() }
func (r Reference) Type() string          { return r.Get("type").Text() }
func (r Reference) Authors() []Actor      { return actors(r.Get("authors")) }

func (d *Document) PreferredCitation() (Reference, bool) {
	v := d.Get("preferred-citation")
	return Reference{value: v}, v.kind == Mapping
}

func (d *Document) References() []Reference {
	items := d.Get("references").items
	result := make([]Reference, len(items))
	for i, v := range items {
		result[i] = Reference{value: v}
	}
	return result
}
