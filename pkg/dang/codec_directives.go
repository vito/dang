package dang

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/vito/dang/v2/pkg/hm"
)

// codecFormats are the builtin string-shaped data-interchange formats that
// expose field directives. Each name matches a ScalarKind module
// (JSONModule/YAMLModule/TOMLModule) and is the directive scope used to apply
// its options, e.g. @JSON.field or @YAML.ignore. Keeping the directive scoped
// to the format means each codec controls its own serialized shape without a
// global directive name, and one format's renames never leak into another.
var codecFormats = []string{"JSON", "YAML", "TOML"}

func codecModules() []*Type { return []*Type{JSONModule, YAMLModule, TOMLModule} }

// registerCodecFieldDirectives declares @field and @ignore on each codec
// module. Their argument values are read back as literals by codecFieldOptions
// and checked by validateCodecFieldDirectives, so the arguments are declared
// without static types (Type_ == nil): the format module's own scope cannot
// resolve names like String, and the literal-aware validation gives clearer
// errors than generic type unification would.
func registerCodecFieldDirectives() {
	for _, mod := range codecModules() {
		format := mod.Named
		mod.AddDirective("field", &DirectiveDecl{
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
			},
			Locations: []DirectiveLocation{{Name: "FIELD_DEFINITION"}},
			DocString: fmt.Sprintf("Controls how a field is encoded to and decoded from %s.", format),
		})
		mod.AddDirective("ignore", &DirectiveDecl{
			Name:      "ignore",
			Locations: []DirectiveLocation{{Name: "FIELD_DEFINITION"}},
			DocString: fmt.Sprintf("Excludes a field from %s encoding and decoding.", format),
		})
	}
}

// codecFieldOpts is the resolved effect a format's directives have on a single
// field.
type codecFieldOpts struct {
	rename   string
	renamed  bool
	omitNull bool
	ignore   bool
}

// key returns the serialized key for a field given its declared name.
func (o codecFieldOpts) key(field string) string {
	if o.renamed {
		return o.rename
	}
	return field
}

// codecFieldOptions reads the options a given format imposes on a field from
// its directive applications. Applications scoped to other formats are skipped.
// Values come straight from literal nodes; validateCodecFieldDirectives has
// already guaranteed (at compile time) that they are literals of the right
// kind, so anything unexpected here is simply ignored.
func codecFieldOptions(directives []*DirectiveApplication, format string) codecFieldOpts {
	var o codecFieldOpts
	for _, d := range directives {
		if d.Scope == nil || d.Scope.Name != format {
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
				}
			}
		}
	}
	return o
}

// codecDecodeField resolves, for a field being decoded from a format, which
// key to read it from and whether it is excluded entirely. An ignored field is
// never read from the input, so it falls back to its default or null.
func codecDecodeField(mod *Type, name, format string) (key string, ignore bool) {
	if mod == nil {
		return name, false
	}
	opts := codecFieldOptions(mod.GetDirectives(name), format)
	return opts.key(name), opts.ignore
}

// validateCodecFieldDirectives checks every field's @FORMAT.field /
// @FORMAT.ignore directives, for each codec format. It runs during type
// inference so mistakes surface as compile errors with source locations:
// non-literal or malformed names, omitNull on a non-null field, ignore combined
// with field, and two fields colliding on the same serialized key.
func validateCodecFieldDirectives(mod *Type) error {
	for _, format := range codecFormats {
		if err := validateCodecFormatDirectives(mod, format); err != nil {
			return err
		}
	}
	return nil
}

func validateCodecFormatDirectives(mod *Type, format string) error {
	seen := map[string]string{} // serialized key -> declaring field
	for fieldName, scheme := range mod.Bindings(PrivateVisibility) {
		if !isCodecDataField(scheme) {
			continue
		}

		var fieldApp, ignoreApp *DirectiveApplication
		for _, d := range mod.GetDirectives(fieldName) {
			if d.Scope == nil || d.Scope.Name != format {
				continue
			}
			switch d.Name {
			case "field":
				fieldApp = d
			case "ignore":
				ignoreApp = d
			}
		}

		if ignoreApp != nil && fieldApp != nil {
			return NewInferError(fmt.Errorf("@%s.ignore cannot be combined with @%s.field on the same field", format, format), fieldApp)
		}
		if ignoreApp != nil {
			// Excluded from this format entirely; contributes no key.
			continue
		}

		key := fieldName
		var keyNode Node
		if fieldApp != nil {
			nameArg, omitNullArg := codecFieldArgs(fieldApp)
			if nameArg != nil {
				lit, ok := nameArg.(*String)
				if !ok {
					return NewInferError(fmt.Errorf("@%s.field name must be a string literal", format), nameArg)
				}
				if lit.Value == "" {
					return NewInferError(fmt.Errorf("@%s.field name must be a non-empty string literal", format), nameArg)
				}
				if err := validateCodecName(format, lit.Value); err != nil {
					return NewInferError(err, nameArg)
				}
				key = lit.Value
				keyNode = nameArg
			}
			if omitNullArg != nil {
				lit, ok := omitNullArg.(*Boolean)
				if !ok {
					return NewInferError(fmt.Errorf("@%s.field omitNull must be a boolean literal", format), omitNullArg)
				}
				if lit.Value && !isNullableScheme(scheme) {
					return NewInferError(fmt.Errorf("@%s.field omitNull has no effect on non-null field %q", format, fieldName), omitNullArg)
				}
			}
		}

		if prev, dup := seen[key]; dup {
			errNode := Node(fieldApp)
			if keyNode != nil {
				errNode = keyNode
			}
			return NewInferError(fmt.Errorf("%s field name %q is used by both %q and %q", format, key, prev, fieldName), errNode)
		}
		seen[key] = fieldName
	}
	return nil
}

// codecFieldArgs returns the value nodes of @FORMAT.field's name and omitNull
// arguments, or nil for any that are absent.
func codecFieldArgs(app *DirectiveApplication) (name, omitNull Node) {
	for _, arg := range app.Args {
		switch arg.Key {
		case "name":
			name = arg.Value
		case "omitNull":
			omitNull = arg.Value
		}
	}
	return name, omitNull
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
func validateCodecName(format, name string) error {
	if !utf8.ValidString(name) {
		return fmt.Errorf("@%s.field name must be valid UTF-8", format)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("@%s.field name %q contains invalid control character U+%04X", format, name, r)
		}
	}
	return nil
}
