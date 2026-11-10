package analysis

import (
	"context"
	"strings"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

// Complete lists completions for the cursor at pos in text, source whose
// code sees scope: the members of a receiver after a dot (as TypeMembers
// lists them), a function's remaining arguments in a call, or the names in
// scope matching a bare identifier. pos is 1-based, with Column counting
// bytes. Classifying the cursor needs tree-sitter, so without cgo there are
// never any completions.
func Complete(ctx context.Context, scope dang.TypeScope, text string, pos Pos) dang.CompletionResult {
	cc, replaceFrom := dang.CompletionContextAt(text, pos.Line-1, pos.Column-1)
	if cc == nil || scope == nil {
		return dang.CompletionResult{}
	}

	var items []dang.Completion
	switch cc.Kind {
	case dang.ContextDotMember:
		if t := dang.InferReceiverType(ctx, scope, cc.ReceiverText); t != nil {
			items = completions(typeMembers(t), cc.Partial)
		}
	case dang.ContextArgument:
		if t := dang.InferReceiverType(ctx, scope, cc.FuncText); t != nil {
			items = dang.ArgsOf(t, cc.Partial, cc.ProvidedArgs)
		}
	case dang.ContextBareIdent:
		if cc.Partial != "" {
			items = completions(lexicalMembers(scope), cc.Partial)
		}
	}

	return dang.CompletionResult{
		Items:       items,
		ReplaceFrom: replaceFrom,
	}
}

// lexicalMembers lists the public bindings of scope that make sense as an
// expression: not type definitions (scalars, enums, ...), nor ID scalars.
func lexicalMembers(scope dang.TypeScope) []Member {
	var members []Member
	for _, m := range scopeMembers(scope, dang.PublicVisibility, false) {
		if m.TypeOnly || dang.IsIDTypeName(m.Name) {
			continue
		}
		if m.HMType != nil && dang.IsTypeDefBinding(hm.NewScheme(nil, m.HMType)) {
			continue
		}
		members = append(members, m)
	}
	return members
}

// completions turns the members named with prefix (case-insensitively)
// into completions. Types only usable in type position are left out.
func completions(members []Member, prefix string) []dang.Completion {
	prefix = strings.ToLower(prefix)
	var items []dang.Completion
	for _, m := range members {
		if m.TypeOnly || !strings.HasPrefix(strings.ToLower(m.Name), prefix) {
			continue
		}
		detail := m.Type
		if m.HMType != nil {
			detail = dang.FormatFunctionType(m.HMType)
		}
		items = append(items, dang.Completion{
			Label:         m.Name,
			Detail:        detail,
			Documentation: m.Doc,
			IsFunction:    m.Kind == KindFunction,
		})
	}
	return items
}
