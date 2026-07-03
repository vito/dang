package dang

import (
	"fmt"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// unionOrigin records where a control-flow arm contributed a member to a
// widened union. It rides opaquely on hm.UnionType so that a type mismatch
// at a distant use site can cite the arms that built the union, not just
// the union itself.
type unionOrigin struct {
	Desc string
	Loc  *SourceLocation
}

// armOrigin builds the opaque source annotation for a widening arm.
// Returns nil (record nothing) when there is no location to cite.
func armOrigin(desc string, loc *SourceLocation) any {
	if loc == nil {
		return nil
	}
	return unionOrigin{Desc: desc, Loc: loc}
}

// nodeOrigin is armOrigin for an AST node's own location.
func nodeOrigin(desc string, node Node) any {
	if node == nil {
		return nil
	}
	return armOrigin(desc, node.GetSourceLocation())
}

// mergeControlResultTypesTagged is mergeControlResultTypes with provenance:
// when the arms diverge and widen to a union, each member records which arm
// it came from. When hm.MergeTypes finds a common supertype instead, no
// union exists and the sources are simply unused.
func mergeControlResultTypesTagged(current hm.Type, currentSrc any, next hm.Type, nextSrc any) hm.Type {
	merged, _, err := hm.MergeTypes(current, next)
	if err == nil {
		return merged
	}
	return hm.NewUnionTypeWithSources([]hm.Type{current, next}, []any{currentSrc, nextSrc})
}

// unionProvenanceNotes renders "  - T from the <arm> at <loc>" note lines
// for every union member with recorded provenance in the given types,
// unwrapping non-null and container wrappers. Empty when nothing has
// provenance, so callers can append it unconditionally.
func unionProvenanceNotes(types ...hm.Type) string {
	var notes strings.Builder
	for _, t := range types {
		collectUnionNotes(&notes, t)
	}
	return notes.String()
}

func collectUnionNotes(notes *strings.Builder, t hm.Type) {
	switch tt := t.(type) {
	case hm.NonNullType:
		collectUnionNotes(notes, tt.Type)
	case ListType:
		collectUnionNotes(notes, tt.Type)
	case MapType:
		collectUnionNotes(notes, tt.Type)
	case *hm.UnionType:
		for i, opt := range tt.Options {
			origin, ok := tt.OptionSource(i).(unionOrigin)
			if !ok || origin.Loc == nil {
				continue
			}
			fmt.Fprintf(notes, "\n  - %s from the %s at %s:%d:%d",
				opt, origin.Desc, origin.Loc.Filename, origin.Loc.Line, origin.Loc.Column)
		}
	}
}

// withUnionProvenance appends provenance notes for any widened unions in
// the given types to an error message, or returns the error unchanged when
// there is nothing to cite.
func withUnionProvenance(err error, types ...hm.Type) error {
	notes := unionProvenanceNotes(types...)
	if notes == "" {
		return err
	}
	return fmt.Errorf("%s%s", err, notes)
}
