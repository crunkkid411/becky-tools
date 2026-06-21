// becky-cubase — find Jordan's plugin chains inside his Cubase files. He doesn't
// remember what's in his templates; this reads his .cpr / .trackpreset / .vstpreset
// files and reports the VST plugins they use, ranked by how often they appear — so his
// critical few surface at the top and he just confirms them. Read-only, offline,
// degrade-never-crash. Run it on his Windows machine (where the files are); the
// extraction core is unit-tested on synthetic data.
//
//	becky-cubase scan                     scan the default Cubase template/preset folders
//	becky-cubase scan --dir "D:\Projects" scan a specific folder (recursively)
//	becky-cubase scan --json              machine-readable output
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/cubasescan"
	"becky-go/internal/pathx"
)

// scanExts are the Cubase file types that carry plugin/track info.
var scanExts = map[string]bool{".cpr": true, ".trackpreset": true, ".vstpreset": true, ".track": true}

const maxFileBytes = 80 << 20 // 80 MB cap per file (skip anything bigger)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	if args[0] == "preset" {
		return preset(args[1:])
	}
	if args[0] != "scan" {
		usage()
		return 2
	}
	dir, asJSON := "", false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			if i+1 < len(args) {
				dir = args[i+1]
				i++
			}
		case "--json":
			asJSON = true
		}
	}

	var roots []string
	if dir != "" {
		roots = []string{dir}
	} else {
		roots = defaultCubasePaths()
	}

	var reports []cubasescan.FileReport
	pluginFiles := map[string]int{} // plugin → how many files use it
	scanned := 0
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil // skip unreadable / dirs (degrade)
			}
			if !scanExts[strings.ToLower(filepath.Ext(p))] {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil || info.Size() > maxFileBytes {
				return nil
			}
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			rep := cubasescan.Scan(p, data)
			scanned++
			if len(rep.Plugins) > 0 {
				reports = append(reports, rep)
				for _, pl := range rep.Plugins {
					pluginFiles[strings.ToLower(pl)]++
				}
			}
			return nil
		})
	}

	if asJSON {
		out := map[string]any{"scanned": scanned, "files": reports, "plugin_counts": pluginFiles}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return 0
	}

	if scanned == 0 {
		fmt.Println("no Cubase files found. Point it at your projects/templates:")
		fmt.Println("  becky-cubase scan --dir \"C:\\Users\\you\\Documents\\Cubase Projects\"")
		fmt.Println("default folders checked:")
		for _, r := range roots {
			fmt.Println("  " + r)
		}
		return 0
	}

	// Rank plugins by how many of his files use them — the critical few rise to the top.
	type pc struct {
		name  string
		count int
	}
	var ranked []pc
	for name, n := range pluginFiles {
		ranked = append(ranked, pc{name, n})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}
		return ranked[i].name < ranked[j].name
	})

	fmt.Printf("scanned %d Cubase file(s); found %d distinct plugin(s).\n", scanned, len(ranked))
	fmt.Println("YOUR MOST-USED PLUGINS (confirm the critical ones):")
	for _, r := range ranked {
		fmt.Printf("  %3d×  %s\n", r.count, r.name)
	}
	fmt.Println("\nper file:")
	for _, rep := range reports {
		fmt.Printf("  %s\n      %s\n", pathx.Base(rep.Path), strings.Join(rep.Plugins, ", "))
	}
	return 0
}

// defaultCubasePaths returns the usual Windows locations for Cubase templates,
// presets, and projects (best-effort; missing ones are simply skipped).
func defaultCubasePaths() []string {
	var out []string
	appdata := os.Getenv("APPDATA")
	userprofile := os.Getenv("USERPROFILE")
	if appdata != "" {
		out = append(out, filepath.Join(appdata, "Steinberg")) // templates + track presets live under here
	}
	if userprofile != "" {
		out = append(out,
			filepath.Join(userprofile, "Documents", "Steinberg"),
			filepath.Join(userprofile, "Documents", "VST3 Presets"),
			filepath.Join(userprofile, "Documents", "Cubase Projects"),
		)
	}
	return out
}

// preset reads one .vstpreset and reports the plugin id + whether its dialed-in
// settings (the state chunk) are extractable for transplant into the same plugin.
func preset(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "becky-cubase preset <file.vstpreset>")
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-cubase:", err)
		return 1
	}
	p, ok := cubasescan.ParseVSTPreset(data)
	if !ok {
		fmt.Fprintln(os.Stderr, "becky-cubase: not a valid .vstpreset (VST3) file")
		return 1
	}
	fmt.Printf("plugin class id: %s\n", p.ClassID)
	if p.CompBytes > 0 {
		fmt.Printf("✓ dialed-in settings extracted: %d-byte state chunk — transplantable into the SAME plugin\n", p.CompBytes)
		fmt.Println("  (becky-canvas loads it via the C++ VST3 host: vst.state.load → IComponent::setState)")
	} else {
		fmt.Println("⚠ no processor-state chunk found (the preset may store only controller params)")
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-cubase — find your plugin chains + dialed-in settings inside your Cubase files")
	fmt.Fprintln(os.Stderr, "  becky-cubase scan [--dir <folder>] [--json]   list the plugins your templates use")
	fmt.Fprintln(os.Stderr, "  becky-cubase preset <file.vstpreset>          show a plugin's id + extract its settings chunk")
}
