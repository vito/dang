package dang

import (
	"cmp"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/hashicorp/golang-lru/v2/simplelru"
)

// The cache retains a parsed tree before inference, never a module's schema,
// client or values. Each use constructs independent pointers and containers
// and supplies the current file path for source locations.
type parseBlueprint struct {
	syntax   reflect.Value
	pointers int
}

func (template parseBlueprint) copy(filename reflect.Value) (reflect.Value, error) {
	copied, _, err := copySyntax(template.syntax, filename, nil, 0, template.pointers)
	return copied, err
}

type syntaxCopyState struct {
	filename reflect.Value
	copies   map[uintptr]reflect.Value
	regions  []syntaxRegion
	weight   *int64
	budget   int64
}

type syntaxRegion struct{ start, end uintptr }

var syntaxFilenameField, _ = reflect.TypeFor[SourceLocation]().FieldByName("Filename")

// The Dang version is part of the key: the same source parses differently in
// different versions.
type parseBlueprintKey struct {
	digest  [32]byte
	version Version
}

type blueprintEntry struct {
	create parseBlueprint
	weight int64
}

// SyntaxCache stores syntax before inference. Clients and evaluated values
// belong to each call. Construct a cache with NewSyntaxCache.
type SyntaxCache struct {
	mu      sync.Mutex
	entries *simplelru.LRU[parseBlueprintKey, blueprintEntry]
	weight  int64
	budget  int64
}

func newSyntaxCache(entries int, budget int64) *SyntaxCache {
	cache := &SyntaxCache{budget: budget}
	cache.entries, _ = simplelru.NewLRU[parseBlueprintKey, blueprintEntry](entries,
		func(_ parseBlueprintKey, entry blueprintEntry) { cache.weight -= entry.weight })
	return cache
}

func (cache *SyntaxCache) get(key parseBlueprintKey) (parseBlueprint, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.entries.Get(key)
	return entry.create, ok
}

func (cache *SyntaxCache) add(key parseBlueprintKey, create parseBlueprint, weight int64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	// Concurrent misses may build the same entry; the first one is enough.
	if cache.entries.Contains(key) || weight > cache.budget {
		return
	}
	cache.weight += weight
	cache.entries.Add(key, blueprintEntry{create: create, weight: weight})
	for cache.weight > cache.budget {
		cache.entries.RemoveOldest()
	}
}

// NewSyntaxCache creates a concurrent cache for syntax reused across RunDir calls.
// It retains at most 128 entries with a combined estimated weight of 64 MiB.
func NewSyntaxCache() *SyntaxCache { return newSyntaxCache(128, 64<<20) }

type syntaxCacheKey struct{}

// WithSyntaxCache lets RunDir reuse parsed source through cache. Each use gets
// fresh syntax nodes before type checking, imports and initialization. Share
// the cache across calls that repeatedly evaluate the same module sources.
func WithSyntaxCache(ctx context.Context, cache *SyntaxCache) context.Context {
	return context.WithValue(ctx, syntaxCacheKey{}, cache)
}

func parseFileWithSyntaxCache(ctx context.Context, filename string, version Version) (any, error) {
	cache, _ := ctx.Value(syntaxCacheKey{}).(*SyntaxCache)
	if cache == nil {
		return ParseFileWithRecovery(filename, GlobalStore("filePath", filename), WithVersion(version))
	}
	return parseFileBlueprintWithCache(filename, version, cache)
}

func parseFileBlueprintWithCache(filename string, version Version, cache *SyntaxCache) (any, error) {
	source, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	key := parseBlueprintKey{digest: sha256.Sum256(source), version: version.orCurrent()}
	if blueprint, ok := cache.get(key); ok {
		copied, err := blueprint.copy(reflect.ValueOf(filename))
		if err == nil {
			return copied.Interface().(*FileBlock), nil
		}
	}
	parsed, err := ParseWithRecovery(filename, source, GlobalStore("filePath", filename), WithVersion(version))
	if err != nil {
		return nil, err
	}
	block, ok := parsed.(*FileBlock)
	if !ok {
		return nil, fmt.Errorf("parsed result for %s is not a FileBlock", filename)
	}
	weight := int64(128) // key and LRU entry
	blueprint, err := syntaxBlueprintWeighted(reflect.ValueOf(block), &weight, cache.budget)
	if err != nil {
		// Unsupported fields fall back to normal parsing. This result has
		// not been inferred yet and belongs only to the current call.
		return block, nil
	}
	cache.add(key, blueprint, weight)
	return block, nil
}

func syntaxBlueprint(value reflect.Value) (parseBlueprint, error) {
	weight := int64(0)
	return syntaxBlueprintWeighted(value, &weight, 64<<20)
}

func syntaxBlueprintWeighted(value reflect.Value, weight *int64, budget int64) (parseBlueprint, error) {
	template, pointerCount, err := copySyntax(value, reflect.Value{}, weight, budget, 0)
	if err != nil {
		return parseBlueprint{}, err
	}
	return parseBlueprint{syntax: template, pointers: pointerCount}, nil
}

// The cached template is never returned to a caller. Validate its storage and
// detach strings once; later calls only copy it and supply their source path.
func copySyntax(value, filename reflect.Value, weight *int64, budget int64, pointerCount int) (reflect.Value, int, error) {
	state := syntaxCopyState{filename: filename, copies: make(map[uintptr]reflect.Value, pointerCount), weight: weight, budget: budget}
	result := reflect.New(value.Type()).Elem()
	if err := state.copyInto(result, value); err != nil {
		return reflect.Value{}, 0, err
	}
	slices.SortFunc(state.regions, func(a, b syntaxRegion) int { return cmp.Compare(a.start, b.start) })
	for i := 1; i < len(state.regions); i++ {
		if state.regions[i].start < state.regions[i-1].end {
			return reflect.Value{}, 0, fmt.Errorf("overlapping syntax storage")
		}
	}
	return result, len(state.copies), nil
}

func (state *syntaxCopyState) charge(amount int64) error {
	if state.weight != nil {
		*state.weight += amount
		if *state.weight > state.budget {
			return fmt.Errorf("parse blueprint exceeds cache budget")
		}
	}
	return nil
}

func (state *syntaxCopyState) remember(value, result reflect.Value, size uintptr) {
	state.copies[value.Pointer()] = result
	if state.weight != nil && size > 0 {
		state.regions = append(state.regions, syntaxRegion{value.Pointer(), value.Pointer() + size})
	}
}

func (state *syntaxCopyState) copyInto(result, value reflect.Value) error {
	if err := state.charge(128); err != nil {
		return err
	}
	if value.IsZero() {
		return nil
	}
	typ := value.Type()
	switch value.Kind() {
	case reflect.Pointer:
		if copied, ok := state.copies[value.Pointer()]; ok {
			if copied.Type() != typ {
				return fmt.Errorf("overlapping syntax pointer types")
			}
			result.Set(copied)
			return nil
		}
		result.Set(reflect.New(typ.Elem()))
		state.remember(value, result, typ.Elem().Size())
		return state.copyInto(result.Elem(), value.Elem())
	case reflect.Interface:
		concrete := reflect.New(value.Elem().Type()).Elem()
		if err := state.copyInto(concrete, value.Elem()); err != nil {
			return err
		}
		result.Set(concrete)
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if !result.Field(i).CanSet() {
				if !value.Field(i).IsZero() {
					return fmt.Errorf("nonzero private parse field %s.%s", typ, typ.Field(i).Name)
				}
				continue
			}
			if typ == reflect.TypeFor[SourceLocation]() && i == syntaxFilenameField.Index[0] && state.filename.IsValid() {
				result.Field(i).Set(state.filename)
			} else if err := state.copyInto(result.Field(i), value.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice:
		length, capacity := value.Len(), value.Cap()
		if capacity > 0 && typ.Elem().Size() > 0 {
			if copied, ok := state.copies[value.Pointer()]; ok {
				if copied.Type() != typ || copied.Cap() != capacity {
					return fmt.Errorf("overlapping syntax slice capacities")
				}
				result.Set(copied.Slice3(0, length, capacity))
				return nil
			}
		}
		if err := state.charge(int64(capacity) * 8); err != nil {
			return err
		}
		full := reflect.MakeSlice(typ, capacity, capacity)
		result.Set(full.Slice3(0, length, capacity))
		if capacity > 0 && typ.Elem().Size() > 0 {
			state.remember(value, full, uintptr(capacity)*typ.Elem().Size())
		}
		source := value.Slice(0, capacity)
		for i := range capacity {
			if err := state.copyInto(full.Index(i), source.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Func:
		if typ != reflect.TypeFor[BinaryOperatorEvaluator]() {
			return fmt.Errorf("unsupported parse function %s", typ)
		}
		result.Set(value)
	case reflect.String:
		if err := state.charge(int64(value.Len())); err != nil {
			return err
		}
		if state.weight != nil {
			result.SetString(strings.Clone(value.String()))
		} else {
			result.Set(value)
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		result.Set(value)
	default:
		return fmt.Errorf("unsupported parse field %s", typ)
	}
	return nil
}
