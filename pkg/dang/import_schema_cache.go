package dang

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/vito/dang/v2/pkg/introspection"
)

// A cached schema owns the canonical type objects. Alias views copy only the
// module's binding tables, so changing local names never changes type identity.
type importSchemaModule struct {
	canonical *OverlayTypeScope
	schema    *introspection.Schema
	views     sync.Map // alias-map key -> *importAliasView
}

type importAliasView struct {
	scope   TypeScope
	aliases map[string]string // immutable snapshot, also used by evaluation
}

func snapshotImportConfigs(configs []ImportConfig) []ImportConfig {
	configs = slices.Clone(configs)
	for i := range configs {
		configs[i].Aliases = maps.Clone(configs[i].Aliases)
	}
	return configs
}

func sharedImportModule(ctx context.Context, name string) *importSchemaModule {
	cache, _ := ctx.Value(schemaModuleCacheKey{}).(*sync.Map)
	if cache == nil {
		return nil
	}
	v, ok := cache.Load(name)
	if !ok {
		return nil
	}
	return v.(*importSchemaModule)
}

func cacheImportModule(ctx context.Context, name string, mod *importSchemaModule) *importSchemaModule {
	if cache, _ := ctx.Value(schemaModuleCacheKey{}).(*sync.Map); cache != nil {
		// Concurrent importers must publish views over the same canonical types.
		actual, _ := cache.LoadOrStore(name, mod)
		return actual.(*importSchemaModule)
	}
	return mod
}

func (m *importSchemaModule) aliasView(aliases map[string]string) *importAliasView {
	aliases = maps.Clone(aliases)
	var key strings.Builder
	for _, alias := range slices.Sorted(maps.Keys(aliases)) {
		target := aliases[alias]
		// Length prefixes avoid collisions even for arbitrary configuration strings.
		fmt.Fprintf(&key, "%d:%s%d:%s", len(alias), alias, len(target), target)
	}
	if view, ok := m.views.Load(key.String()); ok {
		return view.(*importAliasView)
	}

	var scope TypeScope = m.canonical
	if len(aliases) > 0 {
		// Copy every container the alias installer mutates. The actual types,
		// schemes, query scope, and prelude remain shared with the canonical module.
		primary := *m.canonical.primary.(*Type)
		primary.objects = maps.Clone(primary.objects)
		primary.typeOrigins = maps.Clone(primary.typeOrigins)
		primary.vars = maps.Clone(primary.vars)
		primary.varOrder = slices.Clone(primary.varOrder)
		primary.valueOrigins = maps.Clone(primary.valueOrigins)
		primary.visibility = maps.Clone(primary.visibility)
		scope = &OverlayTypeScope{primary: &primary, lexical: m.canonical.lexical}
		aliasImportTypes(scope, m.schema, aliases)
	}
	view := &importAliasView{scope: scope, aliases: aliases}
	actual, _ := m.views.LoadOrStore(key.String(), view)
	return actual.(*importAliasView)
}
