// Package cliflags prints the commands' options for -h.
package cliflags

import (
	"flag"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
)

// PrintDefaults is flag.PrintDefaults with aliases folded together: flags
// bound to the same variable (-r and -render) are listed once, as
// "-render, -r string", sorted by their longest name.
func PrintDefaults(w io.Writer, fs *flag.FlagSet) {
	type entry struct {
		names []string
		f     *flag.Flag
	}
	var entries []*entry
	byTarget := map[uintptr]*entry{}
	fs.VisitAll(func(f *flag.Flag) {
		if p := target(f.Value); p != 0 {
			if e, ok := byTarget[p]; ok {
				e.names = append(e.names, f.Name)
				return
			}
			e := &entry{names: []string{f.Name}, f: f}
			byTarget[p] = e
			entries = append(entries, e)
			return
		}
		entries = append(entries, &entry{names: []string{f.Name}, f: f})
	})
	for _, e := range entries {
		// Longest name first: the long form reads better, the short one follows.
		slices.SortStableFunc(e.names, func(a, b string) int { return len(b) - len(a) })
	}
	slices.SortFunc(entries, func(a, b *entry) int { return strings.Compare(a.names[0], b.names[0]) })

	for _, e := range entries {
		var b strings.Builder
		fmt.Fprintf(&b, "  -%s", strings.Join(e.names, ", -"))
		typ, usage := flag.UnquoteUsage(e.f)
		if typ != "" {
			fmt.Fprintf(&b, " %s", typ)
		}
		fmt.Fprintf(&b, "\n    \t%s", strings.ReplaceAll(usage, "\n", "\n    \t"))
		if d := e.f.DefValue; d != "" && d != "0" && d != "false" {
			if typ == "string" {
				d = fmt.Sprintf("%q", d)
			}
			fmt.Fprintf(&b, " (default %s)", d)
		}
		fmt.Fprintln(w, b.String())
	}
}

// target is the address of the variable a flag sets. The standard flag
// values are pointers to the variable itself, so aliases share it.
func target(v flag.Value) uintptr {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return 0
	}
	return rv.Pointer()
}
