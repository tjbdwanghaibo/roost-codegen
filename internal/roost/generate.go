package roost

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tjbdwanghaibo/roost-codegen/internal/attribute"
	"github.com/tjbdwanghaibo/roost-codegen/internal/dao"
	"github.com/tjbdwanghaibo/roost-codegen/internal/entity"
	codeerr "github.com/tjbdwanghaibo/roost-codegen/internal/errcode"
	"github.com/tjbdwanghaibo/roost-codegen/internal/eventgen"
	"github.com/tjbdwanghaibo/roost-codegen/internal/nest"
	"github.com/tjbdwanghaibo/roost-codegen/internal/protocol"
	"github.com/tjbdwanghaibo/roost-codegen/internal/tablegen"
	"github.com/tjbdwanghaibo/roost-codegen/internal/webroute"
)

type GenerateOptions struct {
	Changed bool
	Check   bool
	DryRun  bool
	// Force makes every generator rewrite its outputs even when the content
	// is unchanged. The default relies on content-hash short-circuiting, so
	// an idempotent regeneration leaves file mtimes alone and incremental
	// builds stay warm.
	Force  bool
	Stdout io.Writer
}

type generator struct {
	Feature  string
	Name     string
	Prefixes []string
	Run      func(io.Writer) error
}

func forceArg(args []string, force bool) []string {
	if force {
		return append(args, "-force")
	}
	return args
}

func generatorsFor(m Manifest, force bool) []generator {
	return []generator{
		{Feature: "dao", Name: "dao", Prefixes: []string{"db/def/"}, Run: func(w io.Writer) error {
			return dao.Run(forceArg([]string{"-def", "./db/def", "-out", "./db"}, force), w)
		}},
		{Feature: "event", Name: "event", Prefixes: []string{"event/def/"}, Run: func(w io.Writer) error {
			args := forceArg([]string{"-def", "./event/def", "-out", "./event"}, force)
			if info, err := os.Stat("./game"); err == nil && info.IsDir() {
				args = append(args, "-game", "./game")
			}
			return eventgen.Run(args, w)
		}},
		{Feature: "errcode", Name: "errcode", Prefixes: []string{"game/", "internal/", "service/"}, Run: func(w io.Writer) error {
			return codeerr.Run([]string{"-root", ".", "-out", "docs/generated/errcode.csv"}, w)
		}},
		{Feature: "protocol", Name: "protocol", Prefixes: []string{"protocol/def/"}, Run: func(w io.Writer) error {
			return protocol.Run(forceArg([]string{"-def", "./protocol/def", "-bind", "", "-handlers", "", "-robot-protocol", ""}, force), w)
		}},
		{Feature: "entity", Name: "entity", Prefixes: []string{"game/entities/", "game/components/"}, Run: func(w io.Writer) error { return entity.Run(forceArg([]string{"-dir", "./game"}, force), w) }},
		{Feature: "nest", Name: "nest", Prefixes: []string{"game/entities/", "game/components/", "game/handler/"}, Run: func(w io.Writer) error { return nest.Run(forceArg([]string{"-dir", "./game"}, force), w) }},
		{Feature: "attribute", Name: "attribute", Prefixes: []string{"game/gameplay/attribute/"}, Run: func(w io.Writer) error {
			return attribute.Run(forceArg([]string{"-dir", "./game/gameplay/attribute"}, force), w)
		}},
		// tablegen keeps its historical unconditional -force: its outputs use
		// exists-refuses-overwrite semantics rather than content hashing, so
		// dropping force would fail every regeneration after a schema change.
		{Feature: "config", Name: "config-template", Prefixes: []string{"configs/schema/"}, Run: func(w io.Writer) error {
			return tablegen.Run([]string{"-meta", "./configs/schema", "-csv-template", "./configs/table_template", "-force"}, w)
		}},
		{Feature: "config", Name: "config-data", Prefixes: []string{"configs/schema/", "configs/table/"}, Run: func(w io.Writer) error {
			if empty, err := dirHasNoDataFiles("./configs/table"); err != nil {
				return err
			} else if empty {
				return nil
			}
			return tablegen.Run([]string{"-meta", "./configs/schema", "-csv", "./configs/table", "-json", "./configs/data", "-force"}, w)
		}},
		{Feature: "config", Name: "config-go", Prefixes: []string{"configs/schema/"}, Run: func(w io.Writer) error {
			return tablegen.Run([]string{"-meta", "./configs/schema", "-out", "./configs/generated", "-force"}, w)
		}},
		{Feature: "webroute", Name: "webroute", Prefixes: []string{"service/"}, Run: func(w io.Writer) error { return webroute.Run(forceArg([]string{"-dir", "./service"}, force), w) }},
	}
}

func Generate(root string, options GenerateOptions) error {
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	manifest, err := LoadManifest(root)
	if err != nil {
		return err
	}
	if options.Check {
		return checkGenerated(root, manifest, options.Stdout)
	}
	selected := generatorsFor(manifest, options.Force)
	if options.Changed {
		changed, err := gitChanged(root)
		if err != nil {
			return err
		}
		selected = filterChanged(selected, changed)
	}
	return runGenerators(root, manifest, selected, options)
}

func runGenerators(root string, manifest Manifest, generators []generator, options GenerateOptions) error {
	old, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(root); err != nil {
		return err
	}
	defer os.Chdir(old)
	for _, gen := range generators {
		if !hasFeature(manifest, gen.Feature) {
			continue
		}
		if options.DryRun {
			fmt.Fprintf(options.Stdout, "would run: %s\n", gen.Name)
			continue
		}
		fmt.Fprintf(options.Stdout, "==> %s\n", gen.Name)
		if err := gen.Run(options.Stdout); err != nil {
			return fmt.Errorf("generator %s: %w", gen.Name, err)
		}
	}
	return nil
}

func checkGenerated(root string, manifest Manifest, stdout io.Writer) error {
	tmp, err := os.MkdirTemp("", "roost-generated-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := copyProject(root, tmp); err != nil {
		return err
	}
	before, err := snapshotGenerated(tmp)
	if err != nil {
		return err
	}
	// The staleness check compares content snapshots, so hash-short-circuited
	// writes and forced writes produce the same verdict; skip force here.
	if err := runGenerators(tmp, manifest, generatorsFor(manifest, false), GenerateOptions{Stdout: io.Discard}); err != nil {
		return err
	}
	after, err := snapshotGenerated(tmp)
	if err != nil {
		return err
	}
	changed := diffSnapshot(before, after)
	if len(changed) > 0 {
		return fmt.Errorf("generated files are stale: %s", strings.Join(changed, ", "))
	}
	fmt.Fprintln(stdout, "generated files are up to date")
	return nil
}

func copyProject(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == "dist" || entry.Name() == "log" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeAtomic(filepath.Join(dst, rel), raw, 0o644)
	})
}

func snapshotGenerated(root string) (map[string][sha256.Size]byte, error) {
	out := map[string][sha256.Size]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(raw), "Code generated") && !isGeneratedData(path) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = sha256.Sum256(raw)
		return nil
	})
	return out, err
}

func isGeneratedData(path string) bool {
	p := filepath.ToSlash(path)
	return strings.Contains(p, "/configs/data/") && strings.HasSuffix(p, ".json") ||
		strings.Contains(p, "/docs/generated/")
}

func diffSnapshot(before, after map[string][sha256.Size]byte) []string {
	seen := map[string]bool{}
	for p := range before {
		seen[p] = true
	}
	for p := range after {
		seen[p] = true
	}
	var out []string
	for p := range seen {
		if before[p] != after[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func gitChanged(root string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		out = append(out, filepath.ToSlash(path))
	}
	return out, nil
}

func filterChanged(gens []generator, changed []string) []generator {
	var out []generator
	for _, gen := range gens {
		matched := false
		for _, file := range changed {
			for _, prefix := range gen.Prefixes {
				if strings.HasPrefix(file, prefix) {
					matched = true
				}
			}
		}
		if matched {
			out = append(out, gen)
		}
	}
	return out
}

func dirHasNoDataFiles(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return false, nil
		}
	}
	return true, nil
}

var _ = errors.Join
