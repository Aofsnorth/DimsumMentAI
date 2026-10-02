// Command testpartition splits the testexport worklist into per-group file
// lists so the migration can be handed to parallel workers whose write scopes
// do not overlap.
package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// group maps an internal package path to a worker group. The cut keeps every
// source package inside exactly one group, so no two workers rename the same
// helper at the same time.
var group = map[string]string{
	"internal/bot":                    "G1",
	"internal/bot/action":             "G2",
	"internal/bot/agi":                "G2",
	"internal/bot/combat":             "G3",
	"internal/bot/movement":           "G3",
	"internal/bot/movement/animation": "G3",
	"internal/bot/pathfinder":         "G4",
	"internal/bot/interact":           "G4",
	"internal/bot/perception":         "G4",
	"internal/bot/world":              "G4",
	"internal/bot/entity":             "G4",
	"internal/bot/storage":            "G5",
	"internal/bot/survival":           "G5",
	"internal/bot/inventory":          "G5",
	"internal/bot/inventory/furnace":  "G5",
	"internal/bot/inventory/chest":    "G5",
	"internal/bot/inventory/crafting": "G5",
	"internal/bot/network/player":     "G5",
	"internal/bot/gathering":          "G5",
	"internal/bot/exploration":        "G5",
	"internal/bot/planner":            "G5",
	"internal/bot/fov":                "G5",
	"internal/bot/durability":         "G5",
	"internal/bot/building/placer":    "G5",
	"internal/config":                 "G6",
	"internal/connection":             "G6",
	"internal/evidence":               "G6",
}

type job struct{ file, top, method string }

func main() {
	f, err := os.Open("testexport-worklist.txt")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	jobs := map[string][]job{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	flush := func(j job) {
		if j.file == "" {
			return
		}
		g, ok := group[pkgOf(j.file)]
		if !ok {
			g = "G?"
		}
		jobs[g] = append(jobs[g], j)
	}
	var cur job
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "FILE "):
			flush(cur)
			cur = job{file: strings.TrimSpace(strings.TrimPrefix(line, "FILE "))}
		case strings.HasPrefix(line, "  TOP"):
			cur.top = strings.TrimSpace(strings.TrimPrefix(line, "TOP"))
		case strings.HasPrefix(line, "  METHOD"):
			cur.method = strings.TrimSpace(strings.TrimPrefix(line, "METHOD"))
		case strings.TrimSpace(line) == "":
			flush(cur)
			cur = job{}
		}
	}
	flush(cur)

	keys := make([]string, 0, len(jobs))
	for g := range jobs {
		keys = append(keys, g)
	}
	sort.Strings(keys)

	for _, g := range keys {
		list := jobs[g]
		pkgs := map[string]bool{}
		for _, j := range list {
			pkgs[pkgOf(j.file)] = true
		}
		names := make([]string, 0, len(pkgs))
		for p := range pkgs {
			names = append(names, p)
		}
		sort.Strings(names)
		fmt.Printf("%s\t%d files\t%s\n", g, len(list), strings.Join(names, " "))
	}
}

func pkgOf(file string) string {
	if i := strings.LastIndex(file, "/"); i >= 0 {
		return file[:i]
	}
	return file
}
