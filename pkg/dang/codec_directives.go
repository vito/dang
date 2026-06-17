package dang

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/vito/dang/v2/pkg/hm"
)

// A Codec is one of Dang's string-shaped data-interchange formats — JSON, YAML,
// or TOML. It wraps the format's builtin ScalarKind module and owns that
// format's field mapping: the reading of the @FORMAT.field / @FORMAT.ignore
// directives. A DeferredValue carries the Codec that produced it (rather than a
// format string) so materialization can apply the right directives when reading
// fields back, with the set of formats kept closed and the directive scope named
// in exactly one place — the module.
type Codec struct {
	module *Type
}

var (
	jsonCodec = Codec{JSONModule}
	yamlCodec = Codec{YAMLModule}
	tomlCodec = Codec{TOMLModule}
)

// codecs returns every codec in declaration order, for registration and for
// validation that must cover all formats.
func codecs() []Codec { return []Codec{jsonCodec, yamlCodec, tomlCodec} }

// scope is the directive scope this codec reads and the name its module is
// published under, e.g. "JSON".
func (c Codec) scope() string { return c.module.Named }

// registerCodecFieldDirectives declares @field and @ignore on each codec
// module. Their argument values are read back as literals by Codec.options and
// checked by validateCodecFieldDirectives, so the arguments are declared without
// static types (Type_ == nil): the format module's own scope cannot resolve
// names like String, and the literal-aware validation gives clearer errors than
// generic type unification would.
func registerCodecFieldDirectives() {
	for _, c := range codecs() {
		c.module.AddDirective("field", &DirectiveDecl{
			Name: "field",
			Args: []*FieldDecl{
				{
					Name:      &Symbol{Name: "name"},
					DocString: "the key this field is encoded as and decoded from",
				},
				{
					Name:      &Symbol{Name: "omitNull"},
					DocString: "omit this field from output when its value is null",
				},
				{
					Name:      &Symbol{Name: "omitEmpty"},
					DocString: "omit this field from output when its value is empty (null, \"\", 0, false, or an empty list/map)",
				},
			},
			Locations: []DirectiveLocation{{Name: "FIELD_DEFINITION"}},
			DocString: fmt.Sprintf("Controls how a field is encoded to and decoded from %s.", c.scope()),
		})
		c.module.AddDirective("ignore", &DirectiveDecl{
			Name:      "ignore",
			Locations: []DirectiveLocation{{Name: "FIELD_DEFINITION"}},
			DocString: fmt.Sprintf("Excludes a field from %s encoding and decoding.", c.scope()),
		})
	}
}

// codecFieldOpts is the resolved effect a codec's directives have on a single
// field.
type codecFieldOpts struct {
	rename    string
	renamed   bool
	omitNull  bool
	omitEmpty bool
	ignore    bool
}

// key returns the serialized key for a field given its declared name.
func (o codecFieldOpts) key(field string) string {
	if o.renamed {
		return o.rename
	}
	return field
}

// options reads this codec's directives off a field's directive applications.
// Applications scoped to other codecs are skipped. Values come straight from
// literal nodes; validateCodecFieldDirectives has already guaranteed (at compile
// time) that they are literals of the right kind, so anything unexpected here is
// simply ignored.
func (c Codec) options(directives []*DirectiveApplication) codecFieldOpts {
	var o codecFieldOpts
	for _, d := range directives {
		if d.Scope == nil || d.Scope.Name != c.scope() {
			continue
		}
		switch d.Name {
		case "ignore":
			o.ignore = true
		case "field":
			for _, arg := range d.Args {
				switch arg.Key {
				case "name":
					if s, ok := arg.Value.(*String); ok {
						o.rename = s.Value
						o.renamed = true
					}
				case "omitNull":
					if b, ok := arg.Value.(*Boolean); ok {
						o.omitNull = b.Value
					}
				case "omitEmpty":
					if b, ok := arg.Value.(*Boolean); ok {
						o.omitEmpty = b.Value
					}
				}
			}
		}
	}
	return o
}

// fieldKey resolves, for a field of target being decoded with this codec, which
// key to read it from and whether it is excluded entirely. An ignored field is
// never read from the input, so it falls back to its default or null. A codec
// with no module, or an absent target, maps every field to its own name.
func (c Codec) fieldKey(target *Type, field string) (key string, ignore bool) {
	if c.module == nil || target == nil {
		return field, false
	}
	opts := c.options(target.GetDirectives(field))
	return opts.key(field), opts.ignore
}

// validateCodecFieldDirectives checks every field's @FORMAT.field /
// @FORMAT.ignore directives, for each codec. It runs during type inference so
// mistakes surface as compile errors with source locations: non-literal or
// malformed names, omitNull on a non-null field, ignore combined with field, and
// two fields colliding on the same serialized key.
func validateCodecFieldDirectives(mod *Type) error {
	for _, c := range codecs() {
		if err := c.validateFieldDirectives(mod); err != nil {
			return err
		}
	}
	return nil
}

func (c Codec) validateFieldDirectives(mod *Type) error {
	scope := c.scope()
	// serialized key -> the field that first claimed it, plus the node to blame
	// on a collision (its @field name argument or application, if any).
	type keyClaim struct {
		field string
		node  Node
	}
	seen := map[string]keyClaim{}
	for fieldName, scheme := range mod.Bindings(PrivateVisibility) {
		if !isCodecDataField(scheme) {
			continue
		}

		var fieldApp, ignoreApp *DirectiveApplication
		for _, d := range mod.GetDirectives(fieldName) {
			if d.Scope == nil || d.Scope.Name != scope {
				continue
			}
			switch d.Name {
			case "field":
				// Reject duplicates: the runtime reader (Codec.options) merges
				// arguments across every application, so a second @field would
				// otherwise smuggle unvalidated arguments (a non-literal name,
				// omitNull on a non-null field, ...) past the checks below while
				// still taking effect.
				if fieldApp != nil {
					return NewInferError(fmt.Errorf("@%s.field cannot be applied more than once on the same field", scope), d)
				}
				fieldApp = d
			case "ignore":
				if ignoreApp != nil {
					return NewInferError(fmt.Errorf("@%s.ignore cannot be applied more than once on the same field", scope), d)
				}
				ignoreApp = d
			}
		}

		if ignoreApp != nil && fieldApp != nil {
			return NewInferError(fmt.Errorf("@%s.ignore cannot be combined with @%s.field on the same field", scope, scope), fieldApp)
		}
		if ignoreApp != nil {
			// Excluded from this format entirely; contributes no key.
			continue
		}

		key := fieldName
		var keyNode Node
		if fieldApp != nil {
			nameArg, omitNullArg, omitEmptyArg := codecFieldArgs(fieldApp)
			if nameArg != nil {
				lit, ok := nameArg.(*String)
				if !ok {
					return NewInferError(fmt.Errorf("@%s.field name must be a string literal", scope), nameArg)
				}
				if lit.Value == "" {
					return NewInferError(fmt.Errorf("@%s.field name must be a non-empty string literal", scope), nameArg)
				}
				if err := validateCodecName(scope, lit.Value); err != nil {
					return NewInferError(err, nameArg)
				}
				key = lit.Value
				keyNode = nameArg
			}
			omitNull := false
			if omitNullArg != nil {
				lit, ok := omitNullArg.(*Boolean)
				if !ok {
					return NewInferError(fmt.Errorf("@%s.field omitNull must be a boolean literal", scope), omitNullArg)
				}
				if lit.Value && !isNullableScheme(scheme) {
					return NewInferError(fmt.Errorf("@%s.field omitNull has no effect on non-null field %q", scope, fieldName), omitNullArg)
				}
				omitNull = lit.Value
			}
			if omitEmptyArg != nil {
				lit, ok := omitEmptyArg.(*Boolean)
				if !ok {
					return NewInferError(fmt.Errorf("@%s.field omitEmpty must be a boolean literal", scope), omitEmptyArg)
				}
				if lit.Value && omitNull {
					return NewInferError(fmt.Errorf("@%s.field omitNull and omitEmpty cannot both be set; omitEmpty already omits null", scope), omitEmptyArg)
				}
			}
		}

		// Blame the most specific node available: this field's @field name
		// argument, else its @field application. A plain field carries neither;
		// it can only collide with a renamed field (two plain fields can't share
		// a key), so the colliding field's node is non-nil. Avoid wrapping a
		// typed-nil *DirectiveApplication, which would slip past NewInferError's
		// nil guard and panic in GetSourceLocation.
		claimNode := keyNode
		if claimNode == nil && fieldApp != nil {
			claimNode = fieldApp
		}
		if prev, dup := seen[key]; dup {
			errNode := claimNode
			if errNode == nil {
				errNode = prev.node
			}
			return NewInferError(fmt.Errorf("%s field name %q is used by both %q and %q", scope, key, prev.field, fieldName), errNode)
		}
		seen[key] = keyClaim{field: fieldName, node: claimNode}
	}
	return nil
}

// codecFieldArgs returns the value nodes of @FORMAT.field's name, omitNull, and
// omitEmpty arguments, or nil for any that are absent.
func codecFieldArgs(app *DirectiveApplication) (name, omitNull, omitEmpty Node) {
	for _, arg := range app.Args {
		switch arg.Key {
		case "name":
			name = arg.Value
		case "omitNull":
			omitNull = arg.Value
		case "omitEmpty":
			omitEmpty = arg.Value
		}
	}
	return name, omitNull, omitEmpty
}

// isCodecDataField reports whether a binding participates in serialization.
// Functions/methods never do, so directives on them carry no key.
func isCodecDataField(scheme *hm.Scheme) bool {
	t, mono := scheme.Type()
	if !mono {
		return false
	}
	_, isFn := unwrapNonNull(t).(*hm.FunctionType)
	return !isFn
}

// isNullableScheme reports whether a field can hold null, controlling whether
// omitNull is meaningful. Non-monomorphic schemes are treated as nullable so an
// unresolved type never produces a spurious error.
func isNullableScheme(scheme *hm.Scheme) bool {
	t, mono := scheme.Type()
	if !mono {
		return true
	}
	_, nonNull := t.(hm.NonNullType)
	return !nonNull
}

// validateCodecName rejects field names that would corrupt the encoded output:
// invalid UTF-8 or control characters.
func validateCodecName(scope, name string) error {
	if !utf8.ValidString(name) {
		return fmt.Errorf("@%s.field name must be valid UTF-8", scope)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("@%s.field name %q contains invalid control character U+%04X", scope, name, r)
		}
	}
	return nil
}
