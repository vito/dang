package dang

import (
	"fmt"
	"strings"

	"github.com/vito/dang/pkg/hm"
)

// FormatPublicTypeShape formats the public, semantic shape of a type module.
// It is intentionally not source-preserving: method bodies, private fields, and
// non-semantic layout are omitted. Field order follows the module's definition
// order.
func FormatPublicTypeShape(mod *Type) string {
	if mod == nil {
		return ""
	}
	if mod.Canonical != nil {
		mod = mod.Canonical
	}
	if mod.Name() == "" {
		return ""
	}

	var keyword string
	switch mod.Kind {
	case ObjectKind:
		keyword = "type"
	case InterfaceKind:
		keyword = "interface"
	default:
		return ""
	}

	var b strings.Builder
	b.WriteString(keyword)
	b.WriteString(" ")
	b.WriteString(mod.Name())

	if mod.Kind == ObjectKind || mod.Kind == InterfaceKind {
		if interfaces := publicShapeInterfaceNames(mod); len(interfaces) > 0 {
			b.WriteString(" implements ")
			b.WriteString(strings.Join(interfaces, " & "))
		}
	}

	fields := publicShapeFields(mod)
	if len(fields) == 0 {
		b.WriteString(" {}")
		return b.String()
	}

	b.WriteString(" {\n")
	for i, field := range fields {
		if field.docString != "" {
			writePublicShapeDocString(&b, field.docString, "  ")
		}
		b.WriteString("  ")
		b.WriteString(field.signature)
		if i < len(fields)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n}")
	return b.String()
}

type publicShapeField struct {
	signature string
	docString string
}

func publicShapeFields(mod *Type) []publicShapeField {
	var fields []publicShapeField
	for name, scheme := range mod.Bindings(PublicVisibility) {
		docString, _ := mod.GetDocString(name)
		fields = append(fields, publicShapeField{
			signature: formatPublicShapeField(name, scheme),
			docString: docString,
		})
	}
	return fields
}

func publicShapeInterfaceNames(mod *Type) []string {
	interfaces := mod.GetInterfaces()
	if len(interfaces) == 0 {
		return nil
	}

	names := make([]string, 0, len(interfaces))
	for _, iface := range interfaces {
		if iface == nil || iface.Name() == "" {
			continue
		}
		names = append(names, iface.Name())
	}
	return names
}

func formatPublicShapeField(name string, scheme *hm.Scheme) string {
	if scheme == nil {
		return fmt.Sprintf("pub %s", name)
	}

	fieldType, _ := scheme.Type()
	if fnType, ok := fieldType.(*hm.FunctionType); ok {
		args := formatPublicShapeFunctionArgs(fnType)
		if args == "" {
			return fmt.Sprintf("pub %s: %s", name, formatPublicShapeType(fnType.Ret(false)))
		}
		return fmt.Sprintf("pub %s(%s): %s", name, args, formatPublicShapeType(fnType.Ret(false)))
	}

	return fmt.Sprintf("pub %s: %s", name, formatPublicShapeType(fieldType))
}

func formatPublicShapeFunctionArgs(fnType *hm.FunctionType) string {
	if fnType == nil {
		return ""
	}

	var args []string
	if rec, ok := fnType.Arg().(*RecordType); ok {
		for _, field := range rec.Fields {
			argType, _ := field.Value.Type()
			args = append(args, fmt.Sprintf("%s: %s", field.Key, formatPublicShapeType(argType)))
		}
	} else {
		arg := strings.TrimPrefix(strings.TrimSuffix(fnType.Arg().String(), "}"), "{")
		if arg != "" {
			args = append(args, arg)
		}
	}

	if block := fnType.Block(); block != nil {
		args = append(args, "&block: "+formatPublicShapeFunctionType(block))
	}

	return strings.Join(args, ", ")
}

func formatPublicShapeFunctionType(fnType *hm.FunctionType) string {
	if fnType == nil {
		return ""
	}
	return fmt.Sprintf("(%s): %s", formatPublicShapeFunctionArgs(fnType), formatPublicShapeType(fnType.Ret(false)))
}

func formatPublicShapeType(t hm.Type) string {
	switch typ := t.(type) {
	case nil:
		return "unknown"
	case hm.NonNullType:
		inner := formatPublicShapeType(typ.Type)
		if _, ok := typ.Type.(*hm.UnionType); ok {
			return fmt.Sprintf("(%s)!", inner)
		}
		return inner + "!"
	case ListType:
		return fmt.Sprintf("[%s]", formatPublicShapeType(typ.Type))
	case GraphQLListType:
		return fmt.Sprintf("[%s]", formatPublicShapeType(typ.Type))
	case *hm.UnionType:
		parts := make([]string, len(typ.Options))
		for i, option := range typ.Options {
			parts[i] = formatPublicShapeType(option)
		}
		return strings.Join(parts, " | ")
	case *hm.FunctionType:
		return formatPublicShapeFunctionType(typ)
	case *Type:
		if typ.Name() != "" {
			return typ.Name()
		}
		return typ.String()
	default:
		return typ.String()
	}
}

func writePublicShapeDocString(b *strings.Builder, docString string, indent string) {
	b.WriteString(indent)
	b.WriteString("\"\"\"\n")
	for _, line := range strings.Split(docString, "\n") {
		b.WriteString(indent)
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString(indent)
	b.WriteString("\"\"\"\n")
}
