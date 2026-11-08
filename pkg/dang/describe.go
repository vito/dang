package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
)

// DescribeOptions bounds what Describe renders. A zero field takes its
// default; a negative one lifts the limit.
type DescribeOptions struct {
	// MaxString is the number of characters a string keeps (default 512).
	// A longer one keeps its head and tail around a marker counting what
	// was left out.
	MaxString int

	// MaxItems is the number of list elements, map entries and object
	// fields shown (default 32). The rest are counted, as "… (N more)".
	MaxItems int

	// MaxDepth is how deep nested lists, maps and objects are rendered
	// (default 4). Below it, they are summarized by their size.
	MaxDepth int

	// MaxBytes caps the whole rendering (default 16 KiB).
	MaxBytes int

	// Width is the line width a list, map or object fits in before it is
	// broken across lines, one element per line (default 80).
	Width int

	// Force evaluates lazy values instead of describing them as such: a
	// GraphQL object handle is fetched by selecting each of its scalar
	// fields that takes no required arguments (IDs, Void fields and secret
	// plaintext excluded), and pending object fields are initialized.
	// Forcing runs queries, which for an API like Dagger's means running
	// builds. A handle a mutation returned is never forced, since every
	// query off it sends the mutation again. Without Force, Describe runs
	// nothing.
	Force bool

	// PublicOnly leaves out the private (`let`) fields of objects.
	PublicOnly bool
}

const (
	defaultDescribeMaxString = 512
	defaultDescribeMaxItems  = 32
	defaultDescribeMaxDepth  = 4
	defaultDescribeMaxBytes  = 16 << 10
	defaultDescribeWidth     = 80

	// describeMaxArgString caps strings in a query chain's arguments, which
	// are context rather than the value itself (IDs can be kilobytes).
	describeMaxArgString = 64
)

func (o DescribeOptions) withDefaults() DescribeOptions {
	def := func(v *int, d int) {
		switch {
		case *v == 0:
			*v = d
		case *v < 0:
			*v = int(^uint(0) >> 1)
		}
	}
	def(&o.MaxString, defaultDescribeMaxString)
	def(&o.MaxItems, defaultDescribeMaxItems)
	def(&o.MaxDepth, defaultDescribeMaxDepth)
	def(&o.MaxBytes, defaultDescribeMaxBytes)
	def(&o.Width, defaultDescribeWidth)
	return o
}

// Describe renders a value with its type for a reader such as an LLM, as
// "TYPE = VALUE": strings quoted, lists, maps and objects recursed into,
// everything capped by opts. t is the value's static type, e.g. as inferred
// for the expression that produced it; nil uses the value's own.
//
// Laziness is shown, not hidden: a GraphQL object handle is rendered as
// `<lazy: chain>`, the query chain it would run, and an object field not
// initialized yet as `<lazy>`. Describe evaluates neither unless
// opts.Force is set, so describing a Dagger Container runs no build.
//
// The rendering is deterministic: lists keep their order, maps their
// insertion order, objects their declaration order, and query arguments are
// sorted by name.
//
// Describe differs from Repr, which renders the literal a value reads back as
// with no caps, types or laziness, and from toString/JSON.encode, which
// force a GraphQL object to get its ID.
func Describe(ctx context.Context, v Value, t hm.Type, opts DescribeOptions) string {
	d := &describer{ctx: ctx, opts: opts.withDefaults()}
	if t == nil && v != nil {
		t = v.Type()
	}
	var out strings.Builder
	if typ := describeTypeName(t); typ != "" {
		out.WriteString(typ)
		out.WriteString(" = ")
	}
	col := utf8.RuneCountInString(out.String())
	out.WriteString(d.layout(d.describe(v, 0), 0, col))
	if g, ok := v.(GraphQLValue); ok && !d.opts.Force {
		out.WriteString("\n(not evaluated")
		if g.Field != nil && isListTypeRef(g.Field.TypeRef) {
			out.WriteString("; select fields with .{{…}} to run it")
		} else if fields := graphQLForceHint(g); len(fields) > 0 {
			out.WriteString("; select a field such as ")
			for i, f := range fields {
				if i > 0 {
					out.WriteString(" or ")
				}
				out.WriteString("." + f)
			}
			out.WriteString(" to run it")
		}
		out.WriteString(")")
	}
	return capBytes(out.String(), d.opts.MaxBytes)
}

// describeTypeName prints t, or "" when there is nothing useful to print (a
// bare type variable, such as the type of an untyped null).
func describeTypeName(t hm.Type) string {
	if t == nil {
		return ""
	}
	if _, ok := t.(hm.TypeVariable); ok {
		return ""
	}
	return t.String()
}

// capBytes cuts s to max bytes, at a character boundary, noting how much was
// cut.
func capBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n… (output truncated: %d more bytes)", s[:cut], len(s)-cut)
}

type describer struct {
	ctx  context.Context
	opts DescribeOptions
}

// describeDoc is a value rendered for layout: a leaf, or a group of items
// between open and close that is broken across lines when it doesn't fit.
type describeDoc struct {
	text  string // a leaf's text
	open  string
	close string
	items []describeItem
	group bool
}

type describeItem struct {
	key string // "name: " for fields and entries
	doc describeDoc
}

func leafDoc(s string) describeDoc { return describeDoc{text: s} }

func (d *describer) flat(doc describeDoc) string {
	if !doc.group {
		return doc.text
	}
	var b strings.Builder
	b.WriteString(doc.open)
	for i, item := range doc.items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(item.key)
		b.WriteString(d.flat(item.doc))
	}
	b.WriteString(doc.close)
	return b.String()
}

// layout renders doc starting at column col of a line indented by indent: on
// one line if it fits in opts.Width, else one item per line, indented two
// more spaces.
func (d *describer) layout(doc describeDoc, indent, col int) string {
	flat := d.flat(doc)
	if !doc.group || len(doc.items) == 0 || col+utf8.RuneCountInString(flat) <= d.opts.Width {
		return flat
	}
	pad := strings.Repeat(" ", indent+2)
	var b strings.Builder
	b.WriteString(doc.open)
	b.WriteString("\n")
	for _, item := range doc.items {
		b.WriteString(pad)
		b.WriteString(item.key)
		b.WriteString(d.layout(item.doc, indent+2, indent+2+utf8.RuneCountInString(item.key)))
		b.WriteString(",\n")
	}
	b.WriteString(strings.Repeat(" ", indent))
	b.WriteString(doc.close)
	return b.String()
}

func moreItem(n int) describeItem {
	return describeItem{doc: leafDoc(fmt.Sprintf("… (%d more)", n))}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "y") {
		return fmt.Sprintf("%d %sies", n, strings.TrimSuffix(noun, "y"))
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (d *describer) describe(v Value, depth int) describeDoc {
	switch v := v.(type) {
	case nil:
		return leafDoc("null")
	case NullValue:
		return leafDoc("null")
	case StringValue:
		return leafDoc(quoteCapped(v.Val, d.opts.MaxString))
	case ScalarValue:
		if v.Encoded {
			return leafDoc(capped(v.Val, d.opts.MaxString))
		}
		return leafDoc(quoteCapped(v.Val, d.opts.MaxString))
	case IntValue, FloatValue, BoolValue, EnumValue:
		return leafDoc(v.String())
	case DeferredValue:
		return leafDoc(capped(v.String(), d.opts.MaxString))
	case ListValue:
		return d.describeList(v.Elements, depth)
	case MapValue:
		if len(v.Keys) == 0 {
			return leafDoc("[:]")
		}
		if depth >= d.opts.MaxDepth {
			return leafDoc(fmt.Sprintf("[… %s]", plural(len(v.Keys), "entry")))
		}
		doc := describeDoc{group: true, open: "[", close: "]"}
		for i, k := range v.Keys {
			if i == d.opts.MaxItems {
				doc.items = append(doc.items, moreItem(len(v.Keys)-i))
				break
			}
			doc.items = append(doc.items, describeItem{
				key: strconv.Quote(k) + ": ",
				doc: d.describe(v.Entries[k], depth+1),
			})
		}
		return doc
	case GraphQLValue:
		return d.describeGraphQL(v, depth)
	case *Object:
		return d.describeObject(v, depth)
	case Callable:
		return leafDoc(fmt.Sprintf("<function: %s>", v.Type()))
	default:
		return leafDoc(capped(v.String(), d.opts.MaxString))
	}
}

func (d *describer) describeList(elems []Value, depth int) describeDoc {
	if len(elems) == 0 {
		return leafDoc("[]")
	}
	if depth >= d.opts.MaxDepth {
		return leafDoc(fmt.Sprintf("[… %s]", plural(len(elems), "item")))
	}
	doc := describeDoc{group: true, open: "[", close: "]"}
	for i, elem := range elems {
		if i == d.opts.MaxItems {
			doc.items = append(doc.items, moreItem(len(elems)-i))
			break
		}
		doc.items = append(doc.items, describeItem{doc: d.describe(elem, depth+1)})
	}
	return doc
}

// describeObject renders a type instance as `Name {field: value, ...}` and
// a record as `{{field: value, ...}}`, with the data fields its type
// declares, in declaration order. Methods and computed members are behavior,
// not state, and are left out; a field not initialized yet is `<lazy>`.
func (d *describer) describeObject(o *Object, depth int) describeDoc {
	open, close := "{{", "}}"
	name := ""
	if t, ok := o.Mod.(*Type); ok && t.Named != "" {
		name = t.String()
		open, close = name+" {", "}"
	} else if o.Mod != nil && o.Mod.Name() != "" {
		name = o.Mod.Name()
		open, close = name+" {", "}"
	}

	vis := PrivateVisibility
	if d.opts.PublicOnly {
		vis = PublicVisibility
	}
	var names []string
	if t, ok := o.Mod.(*Type); ok {
		for field, scheme := range t.Bindings(vis) {
			if ft, _ := scheme.Type(); isMethodType(ft) {
				continue
			}
			names = append(names, field)
		}
	} else {
		// A scope without a declared shape (e.g. a module's value scope):
		// list what it binds.
		names = o.scopeNames(vis)
	}

	if depth >= d.opts.MaxDepth {
		if name != "" {
			return leafDoc(fmt.Sprintf("%s {…}", name))
		}
		return leafDoc("{{…}}")
	}

	// The data fields, without running anything: a field still pending is
	// kept with a nil value, and forced below only if it is shown.
	type field struct {
		name string
		val  Value
	}
	var fields []field
	for _, name := range names {
		val, ok := o.lookupValue(name)
		switch {
		case ok:
			if _, isFn := val.(Callable); isFn {
				continue
			}
		case !o.hasPending(name):
			// Declared but never set (a nullable field left unset).
			continue
		}
		fields = append(fields, field{name: name, val: val})
	}
	if len(fields) == 0 {
		return leafDoc(open + close)
	}

	doc := describeDoc{group: true, open: open, close: close}
	for i, f := range fields {
		if i == d.opts.MaxItems {
			doc.items = append(doc.items, moreItem(len(fields)-i))
			break
		}
		item := describeItem{key: f.name + ": "}
		switch {
		case f.val != nil:
			item.doc = d.describe(f.val, depth+1)
		case !d.opts.Force:
			item.doc = leafDoc("<lazy>")
		default:
			forced, _, err := o.Lookup(d.ctx, f.name)
			if err != nil {
				item.doc = leafDoc(errorLeaf(err))
			} else {
				item.doc = d.describe(forced, depth+1)
			}
		}
		doc.items = append(doc.items, item)
	}
	return doc
}

// scopeNames lists, by name, what o's frames bind with at least visibility
// vis, initialized or not, down to the builtins (which aren't the scope's
// own).
func (o *Object) scopeNames(vis Visibility) []string {
	seen := map[string]bool{}
	var names []string
	add := func(cur *Object, name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		if cur.Visibilities[name] >= vis {
			names = append(names, name)
		}
	}
	cell := o.dynamicScope
	for cur := o; cur != nil && !cur.builtins; {
		cur.mu.Lock()
		for name := range cur.Values {
			add(cur, name)
		}
		for name := range cur.Pending {
			add(cur, name)
		}
		parent, _ := cur.nextFrame(cell)
		cur.mu.Unlock()
		cur = parent
	}
	sort.Strings(names)
	return names
}

// hasPending reports whether name is bound to an initializer that hasn't run
// yet, anywhere on o's chain, without running it.
func (o *Object) hasPending(name string) bool {
	cell := o.dynamicScope
	for cur := o; cur != nil; {
		cur.mu.Lock()
		_, pending := cur.Pending[name]
		_, hasVal := cur.Values[name]
		parent, _ := cur.nextFrame(cell)
		cur.mu.Unlock()
		if pending {
			return true
		}
		if hasVal {
			return false
		}
		cur = parent
	}
	return false
}

func errorLeaf(err error) string {
	return fmt.Sprintf("<error: %s>", errorMessage(err))
}

// errorMessage is the first line of err's message, capped.
func errorMessage(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return capped(msg, describeMaxArgString*2)
}

// describeGraphQL renders a GraphQL object handle: the query chain it holds,
// unevaluated, or with opts.Force, its scalar fields as fetched.
func (d *describer) describeGraphQL(g GraphQLValue, depth int) describeDoc {
	lazy := leafDoc(fmt.Sprintf("<lazy %s: %s>", g.TypeName, d.chain(g, 0)))
	if depth == 0 {
		lazy = leafDoc(fmt.Sprintf("<lazy: %s>", d.chain(g, 0)))
	}
	if !d.opts.Force {
		return lazy
	}
	if g.IsMutation {
		// Every query off a mutation's result sends the mutation again.
		return leafDoc(d.flat(lazy) + " (not forced: it would send the mutation again)")
	}
	if depth >= d.opts.MaxDepth {
		return leafDoc(fmt.Sprintf("%s {…}", g.TypeName))
	}
	doc, err := d.forceGraphQL(g, depth)
	if err != nil {
		return leafDoc(fmt.Sprintf("%s (forcing failed: %s)", d.flat(lazy), errorMessage(err)))
	}
	return doc
}

// graphQLForceFields lists the fields of g's type that forcing selects: those
// with no required arguments whose values are scalars or enums (or lists of
// them). It skips IDs, which are opaque and long; Void fields, which are
// actions rather than data (e.g. Dagger's Service.up, which blocks, or
// EngineCache.prune); fields named plaintext, which reveal secrets (Dagger's
// Secret.plaintext); and deprecated fields.
func graphQLForceFields(g GraphQLValue) []*introspection.Field {
	if g.Schema == nil {
		return nil
	}
	objType := g.Schema.Types.Get(g.TypeName)
	if objType == nil {
		return nil
	}
	var fields []*introspection.Field
	for _, f := range objType.Fields {
		if f.IsDeprecated || f.Name == "plaintext" || !hasZeroRequiredArgs(f) || !isScalarType(f.TypeRef, g.Schema) {
			continue
		}
		name := getTypeName(f.TypeRef)
		if name == "ID" || strings.HasSuffix(name, "ID") || name == "Void" {
			continue
		}
		fields = append(fields, f)
	}
	return fields
}

// graphQLForceHint suggests a few fields that would run g: `sync` first if
// the type has one (as Dagger's objects do), then the first others in
// schema order.
func graphQLForceHint(g GraphQLValue) []string {
	var names []string
	if g.Schema != nil {
		if objType := g.Schema.Types.Get(g.TypeName); objType != nil {
			for _, f := range objType.Fields {
				if f.Name == "sync" && hasZeroRequiredArgs(f) && getTypeName(f.TypeRef) != "Void" {
					names = append(names, "sync")
				}
			}
		}
	}
	for _, f := range graphQLForceFields(g) {
		if len(names) == 2 {
			break
		}
		if f.Name != "sync" {
			names = append(names, f.Name)
		}
	}
	return names
}

func (d *describer) forceGraphQL(g GraphQLValue, depth int) (describeDoc, error) {
	if g.Client == nil {
		return describeDoc{}, fmt.Errorf("no GraphQL client to run the query")
	}
	fields := graphQLForceFields(g)
	if len(fields) == 0 {
		return describeDoc{}, fmt.Errorf("%s has no scalar fields to select", g.TypeName)
	}
	if len(fields) > d.opts.MaxItems {
		fields = fields[:d.opts.MaxItems]
	}
	isList := g.Field != nil && isListTypeRef(g.Field.TypeRef)

	// One query per field, so a field that fails (e.g. a container's stdout
	// before anything ran) doesn't take the others with it.
	raws, _ := evalParallel(d.ctx, len(fields), func(ctx context.Context, i int) (forcedField, error) {
		var raw any
		err := g.QueryChain.Select(fields[i].Name).Bind(&raw).Client(g.Client).Execute(ctx)
		return forcedField{raw: raw, err: err}, nil
	})

	render := func(raw func(f int) (any, error)) describeDoc {
		doc := describeDoc{group: true, open: g.TypeName + " {", close: "}"}
		for i, f := range fields {
			r, err := raw(i)
			if err != nil {
				doc.items = append(doc.items, describeItem{key: f.Name + ": ", doc: leafDoc(errorLeaf(err))})
				continue
			}
			val, err := graphQLResultToValue(r, f.TypeRef, "", g.Schema, g.TypeScope, g.Client)
			if err != nil {
				doc.items = append(doc.items, describeItem{key: f.Name + ": ", doc: leafDoc(errorLeaf(err))})
				continue
			}
			doc.items = append(doc.items, describeItem{key: f.Name + ": ", doc: d.describe(val, depth+1)})
		}
		if len(doc.items) == 0 {
			return leafDoc(g.TypeName + " {}")
		}
		return doc
	}

	if !isList {
		return render(func(i int) (any, error) { return raws[i].raw, raws[i].err }), nil
	}

	// A list handle: each field came back as a list over the elements, so
	// put the elements back together field by field. A field that failed
	// fails in every element.
	n := -1
	var firstErr error
	for _, r := range raws {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		if r.raw == nil {
			return leafDoc("null"), nil
		}
		items, ok := r.raw.([]any)
		if !ok {
			return describeDoc{}, fmt.Errorf("expected a list for %s, got %T", g.TypeName, r.raw)
		}
		if n >= 0 && len(items) != n {
			return describeDoc{}, fmt.Errorf("list of %s changed length between queries", g.TypeName)
		}
		n = len(items)
	}
	if n < 0 {
		return describeDoc{}, firstErr
	}
	if n == 0 {
		return leafDoc("[]"), nil
	}
	doc := describeDoc{group: true, open: "[", close: "]"}
	for e := range n {
		if e == d.opts.MaxItems {
			doc.items = append(doc.items, moreItem(n-e))
			break
		}
		doc.items = append(doc.items, describeItem{doc: render(func(i int) (any, error) {
			if raws[i].err != nil {
				return nil, raws[i].err
			}
			return raws[i].raw.([]any)[e], nil
		})})
	}
	return doc, nil
}

type forcedField struct {
	raw any
	err error
}

// chain renders the query chain a GraphQL handle holds, Dang-style:
// `container.from(address: "alpine").withExec(args: ["echo"])`. Object
// arguments render as their own chains; nothing is run or marshalled.
func (d *describer) chain(g GraphQLValue, depth int) string {
	if g.QueryChain == nil {
		return g.TypeName
	}
	var b strings.Builder
	if g.IsMutation {
		b.WriteString("mutation ")
	}
	for i, step := range g.QueryChain.Steps() {
		if i > 0 {
			b.WriteString(".")
		}
		switch {
		case step.InlineFragment != "":
			b.WriteString("(as " + step.InlineFragment + ")")
		case len(step.Fields) > 0:
			b.WriteString("{{" + strings.Join(step.Fields, ", ") + "}}")
		default:
			if step.Alias != "" {
				b.WriteString(step.Alias + ": ")
			}
			b.WriteString(step.Name)
		}
		if len(step.Args) > 0 {
			b.WriteString("(")
			for j, arg := range step.Args {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(arg.Name)
				b.WriteString(": ")
				b.WriteString(d.chainArg(arg.Value, depth))
			}
			b.WriteString(")")
		}
	}
	return b.String()
}

// chainArg renders an argument as it was handed to the query builder (see
// dangValueToGo).
func (d *describer) chainArg(v any, depth int) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		return quoteCapped(v, describeMaxArgString)
	case gqlEnumValue:
		return v.val
	case gqlObjectMarshaller:
		if depth >= d.opts.MaxDepth {
			return "<" + v.val.TypeName + ">"
		}
		return d.chain(v.val, depth+1)
	case []any:
		parts := make([]string, 0, len(v))
		for i, elem := range v {
			if i == d.opts.MaxItems {
				parts = append(parts, fmt.Sprintf("… (%d more)", len(v)-i))
				break
			}
			parts = append(parts, d.chainArg(elem, depth+1))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+": "+d.chainArg(v[k], depth+1))
		}
		return "{{" + strings.Join(parts, ", ") + "}}"
	case json.Number:
		return v.String()
	default:
		return capped(fmt.Sprint(v), describeMaxArgString)
	}
}

// quoteCapped quotes s as a Dang string literal, keeping max characters of
// it: a longer string keeps its head and tail, each quoted, around a marker
// counting what was left out, so the quoted parts stay valid literals.
func quoteCapped(s string, max int) string {
	head, tail, omitted := splitCapped(s, max)
	if omitted == 0 {
		return strconv.Quote(s)
	}
	return fmt.Sprintf("%s…(%d chars omitted)…%s", strconv.Quote(head), omitted, strconv.Quote(tail))
}

// capped is quoteCapped for text shown unquoted.
func capped(s string, max int) string {
	head, tail, omitted := splitCapped(s, max)
	if omitted == 0 {
		return s
	}
	return fmt.Sprintf("%s…(%d chars omitted)…%s", head, omitted, tail)
}

// splitCapped keeps max characters of s, three quarters from its head and a
// quarter from its tail, where errors and results tend to be.
func splitCapped(s string, max int) (head, tail string, omitted int) {
	n := utf8.RuneCountInString(s)
	if n <= max {
		return s, "", 0
	}
	runes := []rune(s)
	tailLen := max / 4
	headLen := max - tailLen
	return string(runes[:headLen]), string(runes[n-tailLen:]), n - max
}
