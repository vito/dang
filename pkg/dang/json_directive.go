package dang

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/vito/dang/pkg/hm"
)

type jsonNameDirective struct {
	Name  string
	Node  Node
	Found bool
}

func jsonNameDirectiveFrom(directives []*DirectiveApplication) jsonNameDirective {
	for _, directive := range directives {
		if directive.Scope != nil || directive.Name != "json" {
			continue
		}
		for _, arg := range directive.Args {
			if arg.Key != "name" {
				continue
			}
			if name, ok := arg.Value.(*String); ok {
				return jsonNameDirective{Name: name.Value, Node: arg.Value, Found: true}
			}
			return jsonNameDirective{Node: arg.Value, Found: true}
		}
		return jsonNameDirective{Node: directive, Found: true}
	}
	return jsonNameDirective{}
}

func jsonFieldName(fieldName string, directives []*DirectiveApplication) string {
	if directive := jsonNameDirectiveFrom(directives); directive.Found && directive.Name != "" {
		return directive.Name
	}
	return fieldName
}

func validateJSONFieldNames(mod *Module) error {
	seen := map[string]string{}
	for fieldName, scheme := range mod.Bindings(PrivateVisibility) {
		if !isJSONMarshalField(scheme) {
			continue
		}

		directive := jsonNameDirectiveFrom(mod.GetDirectives(fieldName))
		jsonName := fieldName
		errorNode := Node(nil)
		if directive.Found {
			if directive.Name == "" {
				return NewInferError(fmt.Errorf("@json name must be a non-empty string literal"), directive.Node)
			}
			if err := validateJSONName(directive.Name); err != nil {
				return NewInferError(err, directive.Node)
			}
			jsonName = directive.Name
			errorNode = directive.Node
		}

		if previousField, found := seen[jsonName]; found {
			err := fmt.Errorf("JSON field name %q is used by both %q and %q", jsonName, previousField, fieldName)
			if errorNode != nil {
				return NewInferError(err, errorNode)
			}
			return err
		}
		seen[jsonName] = fieldName
	}
	return nil
}

func isJSONMarshalField(scheme *hm.Scheme) bool {
	fieldType, mono := scheme.Type()
	if !mono {
		return false
	}
	_, isFunction := unwrapNonNull(fieldType).(*hm.FunctionType)
	return !isFunction
}

func validateJSONName(name string) error {
	if !utf8.ValidString(name) {
		return fmt.Errorf("@json name must be valid UTF-8")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("@json name %q contains invalid control character U+%04X", name, r)
		}
	}
	return nil
}
