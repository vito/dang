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

// MigrateSource rewrites source written in Dang version from as version to.
// filename is only used in errors.
//
// It needs no per-change migration code: every version parses into the same
// AST (old forms desugar to their current equivalents in the parser; see
// Version), so migrating is parsing as from and formatting as to. What it
// cannot do is fix code that a later version merely rejects, e.g. because a
// type check became stricter; that surfaces as a compile error after
// migrating.
//
// The result is checked twice. It must mean the same as the source: parsed
// as version to, it must be the same AST, source locations aside, as the
// source formatted and parsed as version from. That catches text the
// formatter echoes verbatim, like a #nofmt region, whose meaning changed
// between the versions; such a region has to be migrated by hand. And it must
// be stable: formatting it again as version to must reproduce it exactly.
//
// Source that is spelled the same in both versions is returned as is, not
// reformatted, so moving to a version that changed no syntax this file uses
// leaves it alone.
func MigrateSource(filename string, source []byte, from, to Version) (string, error) {
	if to.Before(from) {
		return "", fmt.Errorf("cannot migrate from Dang %s back to %s", from, to)
	}
	migrated, err := formatSource(filename, source, from, to)
	if err != nil {
		return "", fmt.Errorf("%w as Dang %s: %w", errUnparseable, from, EnhanceParseError(err, filename, source))
	}
	got, err := Parse(filename, []byte(migrated), WithVersion(to))
	if err != nil {
		return "", fmt.Errorf("migrated source does not parse as Dang %s: %w", to, EnhanceParseError(err, filename, []byte(migrated)))
	}
	if from.orCurrent() != to.orCurrent() {
		// Compare against the source formatted in its own version, so that
		// formatting's own normalizations (e.g. dropping empty parens) don't
		// count as differences.
		formatted, err := formatSource(filename, source, from, from)
		if err != nil {
			return "", err
		}
		want, err := Parse(filename, []byte(formatted), WithVersion(from))
		if err != nil {
			return "", fmt.Errorf("formatted source does not parse as Dang %s: %w", from, err)
		}
		if loc, differs := firstASTDifference(want.(Node), got.(Node)); differs {
			where := "it"
			if loc != nil {
				where = fmt.Sprintf("line %d", loc.Line)
			}
			return "", fmt.Errorf("migrating to Dang %s would change the meaning of %s (in the source as formatted: is it in a #nofmt region?); migrate it by hand", to, where)
		}
		// Nothing in the file is spelled differently in the two versions
		// (e.g. a minor version that changed no syntax), so leave it as it
		// is rather than reformatting it, provided it means the same as
		// written. The formatted texts agreeing is not enough: formatting
		// normalizes old spellings like 1.0's comma separators.
		if formatted == migrated && sameMeaning(filename, source, from, to) {
			return string(source), nil
		}
	}
	again, err := formatSource(filename, []byte(migrated), to, to)
	if err != nil {
		return "", err
	}
	if again != migrated {
		return "", fmt.Errorf("migrated source is not stable when formatted as Dang %s", to)
	}
	return migrated, nil
}

var sourceLocationPtrType = reflect.TypeOf((*SourceLocation)(nil))

// sameMeaning reports whether source parses, unchanged, to the same AST in
// versions from and to.
func sameMeaning(filename string, source []byte, from, to Version) bool {
	inFrom, err := Parse(filename, source, WithVersion(from))
	if err != nil {
		return false
	}
	inTo, err := Parse(filename, source, WithVersion(to))
	if err != nil {
		return false
	}
	_, differs := firstASTDifference(inFrom.(Node), inTo.(Node))
	return !differs
}

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
// Dang version. The dang.toml and every .dang file it governs change
// together: changing only the config would change what the files mean.
type ProjectMigration struct {
	// ConfigPath is the dang.toml whose version changes. It is created if
	// the project had none (see MigrateOptions.UngovernedFrom).
	ConfigPath string
	From, To   Version
	// Config is the new dang.toml contents.
	Config []byte
	// Files lists every .dang file the config governs that parses as From,
	// changed or not.
	Files []FileMigration
	// Unparseable lists the governed files that do not parse as From (e.g.
	// deliberately broken test fixtures). They can't be migrated, so Write
	// leaves them alone, but bumping the version still changes how they
	// parse.
	Unparseable []UnparseableFile
}

// An UnparseableFile is a governed file that can't be migrated because it
// does not parse in the project's version.
type UnparseableFile struct {
	Path string
	Err  error
}

// errUnparseable marks a MigrateSource failure caused by the source not
// parsing in its own version.
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
	// UngovernedFrom, if set, is the version of the source under root that
	// no dang.toml governs, such as a module whose embedder pins its version
	// with ContextWithVersion. That source is then migrated as one project,
	// creating a dang.toml at root that declares the target version, which
	// takes precedence over the embedder's pin from then on. Without it,
	// ungoverned source is left alone: by its path, it is CurrentVersion.
	UngovernedFrom Version
}

// PlanMigrations plans migrating every project under root to Dang version to,
// without changing anything. A project is the set of .dang files governed by
// one dang.toml (see VersionForDir); projects already at or past to are left
// alone. Nested repositories (directories with a .git) are not entered.
//
// A project whose dang.toml lives above root is an error rather than a partial
// migration, since bumping its version would change files outside root. So is
// a file whose migration would change its meaning (see MigrateSource).
func PlanMigrations(root string, to Version, opts MigrateOptions) ([]*ProjectMigration, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	type project struct {
		configPath string
		from       Version
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
					p = &project{configPath: configPath, from: config.EffectiveVersion()}
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

	if !opts.UngovernedFrom.IsZero() && len(ungoverned.files) > 0 {
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
			Config:     setConfigVersion(configSource, to),
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
	// configVersionValue matches the value of a `dang = ...` key, so that
	// replacing it keeps the rest of the line (a trailing comment, a CR).
	configVersionValue = regexp.MustCompile(`(?m)^[ \t]*dang[ \t]*=[ \t]*("[^"\r\n]*"|'[^'\r\n]*'|[^\s#]+)`)
	configTableHeader  = regexp.MustCompile(`(?m)^[ \t]*\[`)
)

// setConfigVersion returns dang.toml contents declaring Dang version v,
// editing the value of the top-level `dang` key in place (or adding the key
// first) so the rest of the file, comments included, is untouched.
func setConfigVersion(config []byte, v Version) []byte {
	value := fmt.Sprintf("%q", v.String())

	// Top-level keys precede the first table header.
	top := config
	if loc := configTableHeader.FindIndex(config); loc != nil {
		top = config[:loc[0]]
	}
	if loc := configVersionValue.FindSubmatchIndex(top); loc != nil {
		out := append([]byte{}, config[:loc[2]]...)
		out = append(out, value...)
		return append(out, config[loc[3]:]...)
	}

	header := "dang = " + value + "\n"
	if len(config) > 0 && !strings.HasPrefix(string(config), "\n") {
		header += "\n"
	}
	return append([]byte(header), config...)
}

// A ProjectInit is what InitProject did.
type ProjectInit struct {
	// ConfigPath is the project's dang.toml.
	ConfigPath string
	// Version is the Dang version it now declares.
	Version Version
	// Created reports whether the dang.toml was created; otherwise an
	// existing one was stamped with the version it already meant.
	Created bool
	// AlreadyDeclared reports whether the dang.toml already declared its
	// version, so nothing changed.
	AlreadyDeclared bool
}

// InitProject makes dir a Dang project that declares its Dang version.
//
// Without a dang.toml governing dir, it creates dir/dang.toml declaring
// CurrentVersion, which is what dir's source already means (see
// VersionForDir). A dang.toml in dir that declares no version is stamped with
// the version it already means, DefaultVersion, so that nothing changes
// meaning; `dang migrate` moves it forward. A dir governed by a dang.toml
// further up is an error: a new dang.toml there would cut its source off
// from that project's version and imports.
func InitProject(dir string) (*ProjectInit, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	configPath, config, err := FindProjectConfig(dir)
	if err != nil {
		return nil, err
	}

	if configPath == "" {
		configPath = filepath.Join(dir, "dang.toml")
		if err := os.WriteFile(configPath, setConfigVersion(nil, CurrentVersion), 0644); err != nil {
			return nil, err
		}
		return &ProjectInit{ConfigPath: configPath, Version: CurrentVersion, Created: true}, nil
	}

	if filepath.Dir(configPath) != dir {
		return nil, fmt.Errorf("%s is already part of the project at %s; run init there", dir, configPath)
	}
	if !config.Dang.IsZero() {
		return &ProjectInit{ConfigPath: configPath, Version: config.Dang, AlreadyDeclared: true}, nil
	}

	source, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	version := config.EffectiveVersion()
	if err := os.WriteFile(configPath, setConfigVersion(source, version), 0644); err != nil {
		return nil, err
	}
	return &ProjectInit{ConfigPath: configPath, Version: version}, nil
}
