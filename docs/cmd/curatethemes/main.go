// curatethemes audits every scheme in the tinted-theming/schemes submodule
// against the docs' base16 role mapping (see page.tmpl and chroma.css) and
// regenerates the curated rotation lists in js/switcher.js.
//
// The site uses slots like so: base00 is the page and code background,
// base01/base02 are secondary backgrounds (sidebar, blockquotes, selection,
// inline code), base03 colors code comments, base04 colors secondary labels
// (sidebar/TOC headers, footer), base05 colors prose, base09/base0A are the
// accents, base0D is the link color, and base08-0F are syntax foregrounds.
// A scheme joins the rotation only if every one of those pairings clears a
// WCAG-contrast floor chosen for the role: 4.5:1 where prose-sized text
// must be comfortable, 3:1 for labels and links, a 2:1 visibility floor
// for syntax tokens and accents, lower still for deliberately-muted
// comments. Backgrounds are checked from the other
// direction: base01/base02 must stay close to base00, which is what rules
// out schemes that repurpose them as saturated accents (papercolor-light,
// edge-light).
//
// Usage, from docs/ with the schemes submodule initialized:
//
//	go run ./cmd/curatethemes           # report failures per scheme
//	go run ./cmd/curatethemes -v        # also report passing schemes
//	go run ./cmd/curatethemes -write    # rewrite the lists in js/switcher.js
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// slotRe matches the base16 slots of both base16 and base24 scheme files;
// genthemes maps base24 down the same way when generating the CSS.
var slotRe = regexp.MustCompile(`(?m)^\s*base(0[0-9A-Fa-f]):\s*"?#?([0-9a-fA-F]{6}|[0-9a-fA-F]{3})"?`)

type scheme struct {
	name  string
	slots map[string]string // "00".."0F" -> 6-digit hex
}

func parseScheme(path string) (*scheme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := &scheme{
		name:  strings.TrimSuffix(filepath.Base(path), ".yaml"),
		slots: map[string]string{},
	}
	for _, m := range slotRe.FindAllStringSubmatch(string(data), -1) {
		hex := m[2]
		if len(hex) == 3 {
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		}
		s.slots[strings.ToUpper(m[1])] = strings.ToLower(hex)
	}
	if len(s.slots) != 16 {
		return nil, fmt.Errorf("%s: found %d of 16 base16 slots", path, len(s.slots))
	}
	return s, nil
}

func luminance(hex string) float64 {
	channel := func(s string) float64 {
		v, _ := strconv.ParseInt(s, 16, 64)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(hex[0:2]) + 0.7152*channel(hex[2:4]) + 0.0722*channel(hex[4:6])
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// light reports whether the scheme is a light scheme: prose darker than the
// page background.
func (s *scheme) light() bool {
	return luminance(s.slots["00"]) > luminance(s.slots["05"])
}

type check struct {
	desc     string
	fg, bg   string  // slot numbers
	min, max float64 // min: fg must contrast at least this much; max: at most (backgrounds)
}

var checks = []check{
	// prose (p, li, playground output) and everything inheriting --fg
	{"prose on page", "05", "00", 4.5, 0},
	{"prose on sidebar/blockquote", "05", "01", 4.5, 0},
	{"prose on inline-code/hover bg", "05", "02", 3.0, 0},
	// secondary labels: sidebar/TOC headers, footer, playground labels
	{"labels on page", "04", "00", 3.0, 0},
	{"labels on sidebar", "04", "01", 3.0, 0},
	// code comments are deliberately muted in many well-loved schemes
	// (tender, tokyo-night); only require that they are visible at all
	{"comments on code bg", "03", "00", 1.6, 0},
	// links sit inline with prose but are set apart by hue
	{"links on page", "0D", "00", 3.0, 0},
	// accents: current nav item (base0A on bg3) and the run button
	// (base00 text on base09); decorative, so just a visibility floor
	{"current nav on hover bg", "0A", "02", 2.0, 0},
	{"accent button text", "00", "09", 2.0, 0},
	// syntax tokens on the code background; 2.0 keeps schemes whose whole
	// point is a soft accent (rose-pine-dawn gold) while dropping ones
	// with truly washed-out tokens
	{"syntax 08 on code bg", "08", "00", 2.0, 0},
	{"syntax 09 on code bg", "09", "00", 2.0, 0},
	{"syntax 0A on code bg", "0A", "00", 2.0, 0},
	{"syntax 0B on code bg", "0B", "00", 2.0, 0},
	{"syntax 0C on code bg", "0C", "00", 2.0, 0},
	{"syntax 0D on code bg", "0D", "00", 2.0, 0},
	{"syntax 0E on code bg", "0E", "00", 2.0, 0},
	// base01/base02 are *backgrounds*; schemes that repurpose them as
	// saturated accents put loud colors behind the sidebar and selections
	{"sidebar bg near page bg", "01", "00", 0, 2.0},
	{"selection bg near page bg", "02", "00", 0, 3.5},
}

func audit(s *scheme) []string {
	var failures []string
	for _, c := range checks {
		r := contrast(s.slots[c.fg], s.slots[c.bg])
		if c.min > 0 && r < c.min {
			failures = append(failures, fmt.Sprintf("%s %.2f < %.1f", c.desc, r, c.min))
		}
		if c.max > 0 && r > c.max {
			failures = append(failures, fmt.Sprintf("%s %.2f > %.1f", c.desc, r, c.max))
		}
	}
	return failures
}

// rewriteList replaces the body of `var <name> = [ ... ];` in switcher.js.
func rewriteList(src, name string, styles []string) (string, error) {
	re := regexp.MustCompile(`(?s)(var ` + name + ` = \[\n).*?(\];)`)
	if !re.MatchString(src) {
		return "", fmt.Errorf("could not find `var %s = [...]` in switcher.js", name)
	}
	var body strings.Builder
	for _, s := range styles {
		fmt.Fprintf(&body, "  %q,\n", s)
	}
	return re.ReplaceAllString(src, "${1}"+body.String()+"${2}"), nil
}

func main() {
	verbose := flag.Bool("v", false, "also report passing schemes")
	write := flag.Bool("write", false, "rewrite the curated lists in js/switcher.js")
	flag.Parse()

	// base16 last so it wins name collisions with base24, matching genthemes
	schemes := map[string]*scheme{}
	for _, system := range []string{"base24", "base16"} {
		paths, err := filepath.Glob(filepath.Join("schemes", system, "*.yaml"))
		if err != nil || len(paths) == 0 {
			log.Fatalf("no %s schemes found; is the docs/schemes submodule initialized?", system)
		}
		for _, p := range paths {
			s, err := parseScheme(p)
			if err != nil {
				log.Fatal(err)
			}
			schemes[s.name] = s
		}
	}

	var names []string
	for name := range schemes {
		names = append(names, name)
	}
	sort.Strings(names)

	var dark, light []string
	for _, name := range names {
		s := schemes[name]
		mode := "dark"
		if s.light() {
			mode = "light"
		}
		failures := audit(s)
		if len(failures) == 0 {
			if s.light() {
				light = append(light, name)
			} else {
				dark = append(dark, name)
			}
			if *verbose {
				fmt.Printf("PASS %-5s %s\n", mode, name)
			}
			continue
		}
		fmt.Printf("FAIL %-5s %-40s %s\n", mode, name, strings.Join(failures, "; "))
	}
	fmt.Printf("\n%d dark and %d light schemes pass of %d total\n", len(dark), len(light), len(schemes))

	if !*write {
		return
	}
	path := filepath.Join("js", "switcher.js")
	src, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	out := string(src)
	for _, list := range []struct {
		name   string
		styles []string
	}{{"curatedDarkStyles", dark}, {"curatedLightStyles", light}} {
		out, err = rewriteList(out, list.name, list.styles)
		if err != nil {
			log.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d dark and %d light schemes to %s", len(dark), len(light), path)
}
