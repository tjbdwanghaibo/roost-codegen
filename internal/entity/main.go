// tool/entity generates entity factory, snapshot, and wiring code.
//
// Usage:
//
//	go run ./tool/entity -dir ./game/player
//
// Or via go:generate in the entity definition file:
//
//	//go:generate go run github.com/tjbdwanghaibo/cube/tool/entity
//
// The generator scans for struct types annotated with the marker comment:
//
//	//cube:entity entityKind=EntityKindPlayer
//	type Player struct { ... }
//
// And produces a <entity>_gen_wire.go file containing:
//   - NewXxx(param) factory function
//   - Base/Dirty and component/DAO accessor methods
//   - Snapshot() method for checkpoint
//   - Hooks wiring (onClear, onDestroy)
package entity

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("entity", flag.ContinueOnError)
	flags.SetOutput(stdout)
	dir := flags.String("dir", "", "directory to scan (default: GOFILE dir or cwd)")
	output := flags.String("output", "", "output file (default: <entity>_gen_wire.go)")
	force := flags.Bool("force", false, "force regeneration even if unchanged")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments %q", flags.Args())
	}

	// Determine scan directory
	scanDir := *dir
	if scanDir == "" {
		// Support go:generate mode
		if gofile := os.Getenv("GOFILE"); gofile != "" {
			scanDir = filepath.Dir(gofile)
		} else {
			scanDir = "."
		}
	}

	scanDir, err := filepath.Abs(scanDir)
	if err != nil {
		return fmt.Errorf("resolve dir: %w", err)
	}

	scanDirs := []string{scanDir}
	if *output == "" {
		scanDirs, err = findEntityDirs(scanDir)
		if err != nil {
			return fmt.Errorf("scan entities: %w", err)
		}
	}
	if len(scanDirs) == 0 {
		fmt.Fprintf(stdout, "no entity markers found in %s\n", scanDir)
		return nil
	}

	for _, dir := range scanDirs {
		// Parse all .go files in the directory
		entities, pkg, err := parseDir(dir)
		if err != nil {
			return fmt.Errorf("parse %s: %w", dir, err)
		}
		if len(entities) == 0 {
			continue
		}

		// Generate for each entity
		for _, ent := range entities {
			outFile := *output
			if outFile == "" {
				outFile = filepath.Join(dir, fmt.Sprintf("%s_gen_wire.go", toSnake(ent.Name)))
			}

			changed, err := generate(ent, pkg, outFile, *force)
			if err != nil {
				return fmt.Errorf("generate %s: %w", ent.Name, err)
			}
			if changed {
				fmt.Fprintf(stdout, "generated: %s\n", outFile)
			} else {
				fmt.Fprintf(stdout, "unchanged: %s\n", outFile)
			}
		}
	}
	return nil
}

func findEntityDirs(root string) ([]string, error) {
	dirs := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root {
				switch entry.Name() {
				case "testdata", "vendor":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") ||
			isGeneratedWireFile(entry.Name()) ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "//cube:entity") {
			dirs[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(dirs))
	for dir := range dirs {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out, nil
}
