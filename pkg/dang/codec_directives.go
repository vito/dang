package dang

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/vito/dang/v2/pkg/hm"
)

// FieldNaming is how data names a type's fields: the key each field is
// written under and read from, and whether it is written at all. Decoded
// data carries the naming of the decoder that produced it, so a later cast
// reads it the way its decode would have, and encoding applies the
// encoder's. A nil FieldNaming names every field after itself.
type FieldNaming interface {
	FieldOptions(owner TypeScope, field string) FieldOptions
}

// FieldOptions is a FieldNaming's reading of one field.
type FieldOptions struct {
	// Key is the field's key in the data; "" means the field's own name.
	Key string
	// Ignore leaves the field out of the data, both ways.
	Ignore bool
	// OmitNull and OmitEmpty leave the field out of encoded data when its
	// value is null, or empty (see isEmptyValue).
	OmitNull  bool
	OmitEmpty bool
}

// key returns the field's key given its declared name.
func (o FieldOptions) key(field string) string {
	if o.Key != "" {
		return o.Key
	}
	return field
}

// fieldOptions is naming's reading of field of owner; the zero options for
// a nil naming or owner.
func fieldOptions(naming FieldNaming, owner TypeScope, field string) FieldOptions {
	if naming == nil || owner == nil {
		return FieldOptions{}
	}
	return naming.FieldOptions(owner, field)
}

// fieldKey resolves, for a field of owner being decoded with naming, which
// key to read it from and whether it is excluded entirely. An ignored field
// is never read from the input, so it falls back to its default or null.
func fieldKey(naming FieldNaming, owner *Type, field string) (key string, ignore bool) {
	opts := fieldOptions(naming, owner, field)
	return opts.key(field), opts.Ignore
}

// registeredNamings are the field namings of every decoder, for the static
// decode-target check, which cannot tell which decoder data will come from.
var registeredNamings []FieldNaming

func fieldNamings() []FieldNaming { return registeredNamings }

// directiveNaming is the field naming a namespace's @field and @ignore
// directives declare, as JSON, YAML and TOML do: `@JSON.field(name: "x")`.
// Directives are matched by the namespace they resolved to, not by name.
type directiveNaming struct {
	scope *Type
}

var (
	jsonNaming = directiveNaming{JSONModule}
	yamlNaming = directiveNaming{YAMLModule}
	tomlNaming = directiveNaming{TOMLModule}
)

// FieldOptions reads this naming's directives off the field's directive
// applications, skipping those of other scopes. Values come straight from
// literal nodes; the directives' validator has already guaranteed (at
// compile time) that they are literals of the right kind, so anything
// unexpected here is simply ignored.
func (n directiveNaming) FieldOptions(owner TypeScope, field string) FieldOptions {
	var o FieldOptions
	for _, d := range owner.GetDirectives(field) {
		if d.ScopeType() != n.scope {
			continue
		}
		switch d.Name {
		case "ignore":
			o.Ignore = true
		case "field":
			for _, arg := range d.Args {
				switch arg.Key {
				case "name":
					if s, ok := arg.Value.(*String); ok {
						o.Key = s.Value
					}
				case "omitNull":
					if b, ok := arg.Value.(*Boolean); ok {
						o.OmitNull = b.Value
					}
				case "omitEmpty":
					if b, ok := arg.Value.(*Boolean); ok {
						o.OmitEmpty = b.Value
					}
				}
			}
		}
	}
	return o
}

// registerFieldDirectives declares a naming's @field and @ignore on its
// namespace and registers the naming. Their argument
// values are read back as literals by FieldOptions and checked by the
// directives' validator, so the arguments are declared without static types
// (Type_ == nil): the namespace's own scope cannot resolve names like
// String, and the literal-aware validation gives clearer errors than generic
// type unification would.
func registerFieldDirectives(n directiveNaming) {
	scope := n.scope
	validator := &DirectiveValidator{Check: n.validate}
	scope.AddDirective("field", &DirectiveDecl{
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
		DocString: fmt.Sprintf("Controls how a field is encoded to and decoded from %s.", scope.Named),
		Validate:  validator,
	})
	scope.AddDirective("ignore", &DirectiveDecl{
		Name:      "ignore",
		Locations: []DirectiveLocation{{Name: "FIELD_DEFINITION"}},
		DocString: fmt.Sprintf("Excludes a field from %s encoding and decoding.", scope.Named),
		Validate:  validator,
	})
	registeredNamings = append(registeredNamings, n)
}

// validate checks every field's @field / @ignore directives of this naming's
// scope: non-literal or malformed names, omitNull on a non-null field,
// ignore combined with field, and two fields colliding on the same key.
func (n directiveNaming) validate(mod *Type) error {
	scope := n.scope.Named
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
			if d.ScopeType() != n.scope {
				continue
			}
			switch d.Name {
			case "field":
				// Reject duplicates: FieldOptions merges arguments across
				// every application, so a second @field would otherwise
				// smuggle unvalidated arguments (a non-literal name,
				// omitNull on a non-null field, ...) past the checks below
				// while still taking effect.
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
// unresolved type never produces a spurious error, and so is Decoded!, whose
// data may be null.
func isNullableScheme(scheme *hm.Scheme) bool {
	t, mono := scheme.Type()
	if !mono {
		return true
	}
	nn, nonNull := t.(hm.NonNullType)
	if !nonNull {
		return true
	}
	_, decoded := nn.Type.(DecodedType)
	return decoded
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
