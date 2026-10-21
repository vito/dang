package dang

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// MigrateSource rewrites source written in edition from as edition to.
// filename is only used in errors.
//
// It needs no per-change migration code: every edition parses into the same
// AST (old forms desugar to their current equivalents in the parser; see
// Edition), so migrating is parsing as from and formatting as to. What it
// cannot do is fix code that a later edition merely rejects, e.g. because a
// type check became stricter; that surfaces as a compile error after
// migrating.
//
// The result is checked twice. It must mean the same as the source: parsed
// as edition to, it must be the same AST, source locations aside, as the
// source formatted and parsed as edition from. That catches text the
// formatter echoes verbatim, like a #nofmt region, whose meaning changed
// between the editions; such a region has to be migrated by hand. And it must
// be stable: formatting it again as edition to must reproduce it exactly.
func MigrateSource(filename string, source []byte, from, to Edition) (string, error) {
	if to.Before(from) {
		return "", fmt.Errorf("cannot migrate from edition %q back to %q", from, to)
	}
	migrated, err := formatSource(filename, source, from, to)
	if err != nil {
		return "", fmt.Errorf("%w as edition %q: %w", errUnparseable, from, EnhanceParseError(err, filename, source))
	}
	got, err := Parse(filename, []byte(migrated), WithEdition(to))
	if err != nil {
		return "", fmt.Errorf("migrated source does not parse as edition %q: %w", to, EnhanceParseError(err, filename, []byte(migrated)))
	}
	if from != to {
		// Compare against the source formatted in its own edition, so that
		// formatting's own normalizations (e.g. dropping empty parens) don't
		// count as differences.
		formatted, err := formatSource(filename, source, from, from)
		if err != nil {
			return "", err
		}
		want, err := Parse(filename, []byte(formatted), WithEdition(from))
		if err != nil {
			return "", fmt.Errorf("formatted source does not parse as edition %q: %w", from, err)
		}
		if loc, differs := firstASTDifference(want.(Node), got.(Node)); differs {
			where := "it"
			if loc != nil {
				where = fmt.Sprintf("line %d", loc.Line)
			}
			return "", fmt.Errorf("migrating to edition %q would change the meaning of %s (in the source as formatted: is it in a #nofmt region?); migrate it by hand", to, where)
		}
	}
	again, err := formatSource(filename, []byte(migrated), to, to)
	if err != nil {
		return "", err
	}
	if again != migrated {
		return "", fmt.Errorf("migrated source is not stable when formatted as edition %q", to)
	}
	return migrated, nil
}

var sourceLocationPtrType = reflect.TypeOf((*SourceLocation)(nil))

// firstASTDifference compares two parsed trees field by field, ignoring
// source locations and inference results, and reports whether they differ,
// with the location of the innermost node enclosing the first difference.
func firstASTDifference(a, b Node) (*SourceLocation, bool) {
	return compareAST(reflect.ValueOf(a), reflect.ValueOf(b), a.GetSourceLocation())
}

func compareAST(a, b reflect.Value, loc *SourceLocation) (*SourceLocation, bool) {
	if a.IsValid() != b.IsValid() {
		return loc, true
	}
	if !a.IsValid() {
		return nil, false
	}
	if a.Type() != b.Type() {
		return loc, true
	}
	switch a.Kind() {
	case reflect.Interface, reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return loc, true
			}
			return nil, false
		}
		if a.CanInterface() {
			if node, ok := a.Interface().(Node); ok {
				if nodeLoc := node.GetSourceLocation(); nodeLoc != nil {
					loc = nodeLoc
				}
			}
		}
		return compareAST(a.Elem(), b.Elem(), loc)
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			field := a.Type().Field(i)
			if field.Type == sourceLocationPtrType || field.Name == "InferredTypeHolder" {
				continue
			}
			if at, differs := compareAST(a.Field(i), b.Field(i), loc); differs {
				return at, true
			}
		}
		return nil, false
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return loc, true
		}
		for i := 0; i < a.Len(); i++ {
			if at, differs := compareAST(a.Index(i), b.Index(i), loc); differs {
				return at, true
			}
		}
		return nil, false
	case reflect.Map:
		if a.Len() != b.Len() {
			return loc, true
		}
		iter := a.MapRange()
		for iter.Next() {
			bv := b.MapIndex(iter.Key())
			if at, differs := compareAST(iter.Value(), bv, loc); differs {
				return at, true
			}
		}
		return nil, false
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return loc, a.IsNil() != b.IsNil()
	case reflect.Bool:
		return loc, a.Bool() != b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return loc, a.Int() != b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return loc, a.Uint() != b.Uint()
	case reflect.Float32, reflect.Float64:
		return loc, a.Float() != b.Float()
	case reflect.Complex64, reflect.Complex128:
		return loc, a.Complex() != b.Complex()
	case reflect.String:
		return loc, a.String() != b.String()
	default:
		return loc, true
	}
}

// A ProjectMigration moves the source governed by one dang.toml to a newer
// edition. The dang.toml and every .dang file it governs change together:
// changing only the config would change what the files mean.
type ProjectMigration struct {
	// ConfigPath is the dang.toml whose edition changes. It is created if
	// the project had none (see MigrateOptions.UngovernedFrom).
	ConfigPath string
	From, To   Edition
	// Config is the new dang.toml contents.
	Config []byte
	// Files lists every .dang file the config governs that parses as From,
	// changed or not.
	Files []FileMigration
	// Unparseable lists the governed files that do not parse as From (e.g.
	// deliberately broken test fixtures). They can't be migrated, so Write
	// leaves them alone, but bumping the edition still changes how they
	// parse.
	Unparseable []UnparseableFile
}

// An UnparseableFile is a governed file that can't be migrated because it
// does not parse in the project's edition.
type UnparseableFile struct {
	Path string
	Err  error
}

// errUnparseable marks a MigrateSource failure caused by the source not
// parsing in its own edition.
var errUnparseable = errors.New("does not parse")

// A FileMigration is one source file's migration.
type FileMigration struct {
	Path     string
	Source   []byte
	Migrated string
}

// Changed reports whether migrating changes the file.
func (f FileMigration) Changed() bool {
	return string(f.Source) != f.Migrated
}

// Write applies the migration: every changed file, then the dang.toml.
func (m *ProjectMigration) Write() error {
	for _, f := range m.Files {
		if !f.Changed() {
			continue
		}
		if err := os.WriteFile(f.Path, []byte(f.Migrated), 0644); err != nil {
			return err
		}
	}
	return os.WriteFile(m.ConfigPath, m.Config, 0644)
}

// MigrateOptions configures PlanMigrations.
type MigrateOptions struct {
	// UngovernedFrom, if set, is the edition of the source under root that
	// no dang.toml governs, such as a module whose embedder pins its edition
	// with ContextWithEdition. That source is then migrated as one project,
	// creating a dang.toml at root that declares the target edition, which
	// takes precedence over the embedder's pin from then on. Without it,
	// ungoverned source is left alone: by its path, it is LatestEdition.
	UngovernedFrom Edition
}

// PlanMigrations plans migrating every project under root to edition to,
// without changing anything. A project is the set of .dang files governed by
// one dang.toml (see EditionForDir); projects already at or past to are left
// alone. Nested repositories (directories with a .git) are not entered.
//
// A project whose dang.toml lives above root is an error rather than a partial
// migration, since bumping its edition would change files outside root. So is
// a file whose migration would change its meaning (see MigrateSource).
func PlanMigrations(root string, to Edition, opts MigrateOptions) ([]*ProjectMigration, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	type project struct {
		configPath string
		from       Edition
		files      []string
	}
	projects := map[string]*project{}
	configForDir := map[string]*project{}
	ungoverned := &project{
		configPath: filepath.Join(root, "dang.toml"),
		from:       opts.UngovernedFrom,
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".dang" {
			return nil
		}
		dir := filepath.Dir(path)
		p, seen := configForDir[dir]
		if !seen {
			configPath, config, err := FindProjectConfig(dir)
			if err != nil {
				return err
			}
			if configPath == "" {
				p = ungoverned
			} else {
				p = projects[configPath]
				if p == nil {
					p = &project{configPath: configPath, from: config.EffectiveEdition()}
					projects[configPath] = p
				}
			}
			configForDir[dir] = p
		}
		p.files = append(p.files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if opts.UngovernedFrom != "" && len(ungoverned.files) > 0 {
		projects[ungoverned.configPath] = ungoverned
	}

	configPaths := make([]string, 0, len(projects))
	for configPath := range projects {
		configPaths = append(configPaths, configPath)
	}
	sort.Strings(configPaths)

	var plans []*ProjectMigration
	for _, configPath := range configPaths {
		p := projects[configPath]
		if !p.from.Before(to) {
			continue
		}
		if rel, err := filepath.Rel(root, filepath.Dir(configPath)); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s is governed by %s, outside %s; migrate its directory instead", p.files[0], configPath, root)
		}

		configSource, err := os.ReadFile(configPath)
		if err != nil && !(p == ungoverned && errors.Is(err, fs.ErrNotExist)) {
			return nil, err
		}
		plan := &ProjectMigration{
			ConfigPath: configPath,
			From:       p.from,
			To:         to,
			Config:     setConfigEdition(configSource, to),
		}
		sort.Strings(p.files)
		for _, path := range p.files {
			source, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			migrated, err := MigrateSource(path, source, p.from, to)
			if errors.Is(err, errUnparseable) {
				plan.Unparseable = append(plan.Unparseable, UnparseableFile{Path: path, Err: err})
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("migrating %s: %w", path, err)
			}
			plan.Files = append(plan.Files, FileMigration{Path: path, Source: source, Migrated: migrated})
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

var (
	configEditionLine = regexp.MustCompile(`(?m)^[ \t]*edition[ \t]*=.*$`)
	configTableHeader = regexp.MustCompile(`(?m)^[ \t]*\[`)
)

// setConfigEdition returns dang.toml contents declaring edition e, editing
// the top-level `edition` key in place (or adding it first) so the rest of
// the file, comments included, is untouched.
func setConfigEdition(config []byte, e Edition) []byte {
	line := fmt.Sprintf("edition = %q", string(e))

	// Top-level keys precede the first table header.
	top := config
	if loc := configTableHeader.FindIndex(config); loc != nil {
		top = config[:loc[0]]
	}
	if loc := configEditionLine.FindIndex(top); loc != nil {
		out := append([]byte{}, config[:loc[0]]...)
		out = append(out, line...)
		return append(out, config[loc[1]:]...)
	}

	header := line + "\n"
	if len(config) > 0 && !strings.HasPrefix(string(config), "\n") {
		header += "\n"
	}
	return append([]byte(header), config...)
}
