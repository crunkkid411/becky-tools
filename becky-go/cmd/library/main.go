// becky-library — becky's favorites + templates: star the kits/sounds/genres you
// reach for, and save/recall named arrangement starters in one command. Backs the
// canvas "favorites" surface; everything persists under ~/.becky/library.
//
//	becky-library star kit "X:/kits/808" "My 808"
//	becky-library favorites [kit|sound|sample|genre|progression]
//	becky-library unstar kit "X:/kits/808"
//	becky-library save "My Crunkcore Starter" --project beat.json --genre crunkcore
//	becky-library templates
//	becky-library load "My Crunkcore Starter" --out recalled.json
//	becky-library remove "My Crunkcore Starter"
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/dawmodel"
	"becky-go/internal/library"
	"becky-go/internal/pathx"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	lib, err := library.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	switch args[0] {
	case "star":
		return star(lib, args[1:])
	case "unstar":
		return unstar(lib, args[1:])
	case "favorites", "favs", "list":
		return favorites(lib, args[1:])
	case "save":
		return save(lib, args[1:])
	case "templates":
		return templates(lib)
	case "load":
		return load(lib, args[1:])
	case "remove", "rm":
		return remove(lib, args[1:])
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "becky-library: unknown command %q\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-library — favorites + templates (your starters, one command away)")
	fmt.Fprintln(os.Stderr, "  star <kit|sound|sample|genre|progression> <value> [label]")
	fmt.Fprintln(os.Stderr, "  unstar <category> <value>")
	fmt.Fprintln(os.Stderr, "  favorites [category]")
	fmt.Fprintln(os.Stderr, "  save <name> --project p.json [--genre g]")
	fmt.Fprintln(os.Stderr, "  templates")
	fmt.Fprintln(os.Stderr, "  load <name> --out p.json")
	fmt.Fprintln(os.Stderr, "  remove <name>")
}

func star(lib *library.Library, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "becky-library star: need <category> <value> [label]")
		return 2
	}
	label := ""
	if len(args) >= 3 {
		label = strings.Join(args[2:], " ")
	}
	if err := lib.Star(args[0], args[1], label); err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	fmt.Printf("★ starred %s: %s\n", args[0], args[1])
	return 0
}

func unstar(lib *library.Library, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "becky-library unstar: need <category> <value>")
		return 2
	}
	if err := lib.Unstar(args[0], args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	fmt.Printf("unstarred %s: %s\n", args[0], args[1])
	return 0
}

func favorites(lib *library.Library, args []string) int {
	cat := ""
	if len(args) > 0 {
		cat = args[0]
	}
	favs, err := lib.Favorites(cat)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	if len(favs) == 0 {
		fmt.Println("no favorites yet — star one with: becky-library star <category> <value> [label]")
		return 0
	}
	for _, f := range favs {
		label := f.Label
		if label != "" {
			label = "  (" + label + ")"
		}
		fmt.Printf("★ [%s] %s%s\n", f.Category, f.Value, label)
	}
	return 0
}

func save(lib *library.Library, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "becky-library save: need a name")
		return 2
	}
	name := args[0]
	project, genre := "", ""
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--project":
			if i+1 < len(rest) {
				project = rest[i+1]
				i++
			}
		case "--genre":
			if i+1 < len(rest) {
				genre = rest[i+1]
				i++
			}
		}
	}
	if project == "" {
		fmt.Fprintln(os.Stderr, "becky-library save: --project p.json is required")
		return 2
	}
	arr, err := loadArr(project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	meta, err := lib.SaveTemplate(name, genre, arr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	fmt.Printf("✓ saved template %q (%d tracks, %d notes) — recall with: becky-library load %q\n",
		meta.Name, meta.Tracks, meta.Notes, meta.Slug)
	return 0
}

func templates(lib *library.Library) int {
	list, err := lib.ListTemplates()
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Println("no templates yet — save one with: becky-library save <name> --project p.json")
		return 0
	}
	for _, m := range list {
		g := ""
		if m.Genre != "" {
			g = "  " + m.Genre
		}
		fmt.Printf("• %-28s %d trk / %d notes%s\n", m.Name, m.Tracks, m.Notes, g)
	}
	return 0
}

func load(lib *library.Library, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "becky-library load: need a template name")
		return 2
	}
	name := args[0]
	out := ""
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--out" && i+1 < len(rest) {
			out = rest[i+1]
			i++
		}
	}
	arr, meta, err := lib.LoadTemplate(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	if out == "" {
		out = meta.Slug + ".json"
	}
	data, _ := json.MarshalIndent(arr, "", "  ")
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	fmt.Printf("✓ recalled %q → %s\n", meta.Name, pathx.Base(out))
	return 0
}

func remove(lib *library.Library, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "becky-library remove: need a template name")
		return 2
	}
	if err := lib.RemoveTemplate(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "becky-library:", err)
		return 1
	}
	fmt.Printf("removed template %q\n", args[0])
	return 0
}

func loadArr(path string) (*dawmodel.Arrangement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pathx.Base(path), err)
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("parse %s: not a valid arrangement (%w)", pathx.Base(path), err)
	}
	return &arr, nil
}
