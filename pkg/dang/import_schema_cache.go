package dang

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/vito/dang/v2/pkg/introspection"
)

// A cached schema owns the canonical type objects. Alias views copy only the
// module's binding tables, so changing local names never changes type identity.
type importSchemaModule struct {
	canonical *OverlayTypeScope
	schema    *introspection.Schema
	mu        sync.Mutex
	views     []*importAliasView
}

type importAliasView struct {
	scope        TypeScope
	aliases      map[string]string // configuration snapshot
	valueAliases map[string]string // validated aliases for evaluation
}

func snapshotImportConfigs(configs []ImportConfig) []ImportConfig {
	configs = slices.Clone(configs)
	for i := range configs {
		configs[i].Aliases = maps.Clone(configs[i].Aliases)
	}
	return configs
}

func cachedImportModule(ctx context.Context, name string, schema *introspection.Schema) *importSchemaModule {
	cache, _ := ctx.Value(schemaModuleCacheKey{}).(*sync.Map)
	if cache != nil {
		if mod, ok := cache.Load(name); ok {
			return mod.(*importSchemaModule)
		}
	}
	mod := &importSchemaModule{canonical: TypeScopeFromSchema(name, schema).(*OverlayTypeScope), schema: schema}
	if cache != nil {
		actual, _ := cache.LoadOrStore(name, mod)
		return actual.(*importSchemaModule)
	}
	return mod
}

func (m *importSchemaModule) aliasView(aliases map[string]string) *importAliasView {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, view := range m.views {
		if maps.Equal(view.aliases, aliases) {
			return view
		}
	}
	view := &importAliasView{aliases: maps.Clone(aliases)}

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
		view.valueAliases = aliasImportTypes(scope, m.schema, aliases)
	}
	view.scope = scope
	m.views = append(m.views, view)
	return view
}
