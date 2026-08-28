package roost

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printHelpOverview(stdout)
		return nil
	}
	if args[0] == "help" {
		return runHelp(args[1:], stdout)
	}
	if isHelpToken(args[0]) {
		printHelpOverview(stdout)
		return nil
	}
	if len(args) >= 2 && isHelpToken(args[len(args)-1]) {
		return runContextHelp(args[:len(args)-1], stdout)
	}
	switch args[0] {
	case "project":
		return runProject(args[1:], stdout, stderr)
	case "generate":
		return runGenerate(args[1:], stdout, stderr)
	case "add":
		return runAdd(args[1:], stdout, stderr)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "id":
		return runID(args[1:], stdout, stderr)
	case "format":
		if len(args) != 2 || args[1] != "check" {
			return errors.New("usage: roost format check")
		}
		root, err := projectRoot(".")
		if err != nil {
			return err
		}
		if err := CheckFormat(root); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Go files are formatted")
		return nil
	case "help-make":
		printMakeHelp(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q; run roost help", args[0])
	}
}

func runProject(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: roost project <new|sync|diff|doctor|upgrade|deps>")
	}
	switch args[0] {
	case "new":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return errors.New("usage: roost project new <name> [flags]")
		}
		fs := flag.NewFlagSet("project new", flag.ContinueOnError)
		fs.SetOutput(stderr)
		module := fs.String("module", "", "target Go module")
		out := fs.String("out", "", "target directory")
		services := fs.String("services", "game", "comma-separated services")
		mods := fs.String("mods", "", "comma-separated service Kit mods")
		features := fs.String("features", "", "comma-separated features")
		core := fs.String("roost-core-version", "", "roost-core version")
		kit := fs.String("roost-kit-version", "", "roost-kit version")
		skill := fs.String("roost-skill-version", "", "roost-skill version")
		codegen := fs.String("codegen-version", "", "roost-codegen version")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		result, target, err := NewProject(NewOptions{Name: toSnake(args[1]), Module: *module, Out: *out, Services: splitList(*services), Mods: splitList(*mods), Features: splitList(*features), Versions: VersionSpec{Core: *core, Kit: *kit, Skill: *skill, Codegen: *codegen}})
		if err != nil {
			return err
		}
		manifest, err := LoadManifest(target)
		if err != nil {
			return err
		}
		if err := UpdateFrameworkDependencies(target, manifest, stdout, stderr); err != nil {
			return fmt.Errorf("project files created at %s but framework resolution failed; after connectivity recovers run roost project deps --root %s: %w", target, target, err)
		}
		printSyncResult(stdout, result)
		fmt.Fprintf(stdout, "project ready: %s\n", target)
		return nil
	case "sync", "diff", "doctor", "upgrade", "deps":
		fs := flag.NewFlagSet("project "+args[0], flag.ContinueOnError)
		fs.SetOutput(stderr)
		rootFlag := fs.String("root", ".", "project directory")
		strict := fs.Bool("strict", true, "include generated freshness checks")
		jsonOutput := fs.Bool("json", false, "JSON output")
		dryRun := fs.Bool("dry-run", false, "preview an upgrade without writing files or resolving dependencies")
		core := fs.String("core", "", "new core version")
		kit := fs.String("kit", "", "new kit version")
		skill := fs.String("skill", "", "new skill version")
		codegen := fs.String("codegen", "", "new codegen version")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		root, err := projectRoot(*rootFlag)
		if err != nil {
			return err
		}
		switch args[0] {
		case "sync":
			result, err := SyncProject(root)
			if err != nil {
				return err
			}
			manifest, err := LoadManifest(root)
			if err != nil {
				return err
			}
			if err := UpdateFrameworkDependencies(root, manifest, stdout, stderr); err != nil {
				return fmt.Errorf("project files synchronized but framework resolution failed: %w", err)
			}
			printSyncResult(stdout, result)
			return nil
		case "diff":
			return DiffProject(root, stdout)
		case "doctor":
			return Doctor(root, *strict, *jsonOutput, stdout)
		case "deps":
			manifest, err := LoadManifest(root)
			if err != nil {
				return err
			}
			return UpdateFrameworkDependencies(root, manifest, stdout, stderr)
		case "upgrade":
			m, err := loadManifestForUpgrade(root)
			if err != nil {
				return err
			}
			mergeVersions(&m.Versions, VersionSpec{Core: *core, Kit: *kit, Skill: *skill, Codegen: *codegen})
			if err := m.Validate(); err != nil {
				return fmt.Errorf("validate upgraded manifest: %w", err)
			}
			if *dryRun {
				return diffManifest(root, m, true, stdout)
			}
			if _, _, err := planManifestSync(root, m); err != nil {
				return fmt.Errorf("preflight project upgrade: %w", err)
			}
			if err := saveManifest(root, m); err != nil {
				return err
			}
			result, err := SyncProject(root)
			if err != nil {
				return err
			}
			manifest, err := LoadManifest(root)
			if err != nil {
				return err
			}
			if err := UpdateFrameworkDependencies(root, manifest, stdout, stderr); err != nil {
				return fmt.Errorf("project upgraded but framework resolution failed: %w", err)
			}
			printSyncResult(stdout, result)
			return nil
		}
	}
	return fmt.Errorf("unknown project command %q", args[0])
}

func runGenerate(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootFlag := fs.String("root", ".", "project directory")
	changed := fs.Bool("changed", false, "only generators affected by git changes")
	check := fs.Bool("check", false, "verify in a temporary copy")
	dry := fs.Bool("dry-run", false, "print generation plan")
	force := fs.Bool("force", false, "rewrite outputs even when content is unchanged")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := projectRoot(*rootFlag)
	if err != nil {
		return err
	}
	return Generate(root, GenerateOptions{Changed: *changed, Check: *check, DryRun: *dry, Force: *force, Stdout: stdout})
}

func runAdd(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return errors.New("usage: roost add <service|mod|module|protocol|entity|component|event|table|dao|webroute|errcode|saga> <name> [flags]")
	}
	fs := flag.NewFlagSet("add "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootFlag := fs.String("root", ".", "project directory")
	service := fs.String("service", "", "target service")
	mods := fs.String("mods", "", "comma-separated Kit mods")
	steps := fs.String("steps", "", "comma-separated Saga steps")
	group := fs.String("group", "", "ID allocation group")
	id := fs.Int64("id", 0, "explicit numeric ID")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	root, err := projectRoot(*rootFlag)
	if err != nil {
		return err
	}
	paths, err := Add(root, AddOptions{Kind: args[0], Name: args[1], Service: *service, Mods: splitList(*mods), Steps: splitList(*steps), Group: *group, ID: *id})
	if err != nil {
		return err
	}
	for _, path := range paths {
		fmt.Fprintf(stdout, "created/updated: %s\n", path)
	}
	return nil
}

func runConfig(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "check" {
		return errors.New("usage: roost config check --service <name> [--production]")
	}
	fs := flag.NewFlagSet("config check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootFlag := fs.String("root", ".", "project directory")
	service := fs.String("service", "", "service name")
	production := fs.Bool("production", false, "enforce production-safe values")
	file := fs.String("file", "", "explicit config path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	root, err := projectRoot(*rootFlag)
	if err != nil {
		return err
	}
	path := *file
	if path == "" {
		if *service == "" {
			return errors.New("--service or --file is required")
		}
		path = filepath.Join(root, "configs", "service", "config."+*service+".yaml")
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if err := CheckConfig(path, *production); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "config is valid: %s\n", path)
	return nil
}

func runID(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: roost id <next|check>")
	}
	fs := flag.NewFlagSet("id "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootFlag := fs.String("root", ".", "project directory")
	group := fs.String("group", "", "ID group")
	parseArgs := args[1:]
	if args[0] == "next" {
		parseArgs = normalizeNextIDArgs(parseArgs)
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	root, err := projectRoot(*rootFlag)
	if err != nil {
		return err
	}
	m, err := LoadManifest(root)
	if err != nil {
		return err
	}
	switch args[0] {
	case "check":
		if err := CheckIDs(root, m); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "IDs are valid")
		return nil
	case "next":
		if fs.NArg() != 1 {
			return errors.New("usage: roost id next <kind> [--group <group>]")
		}
		id, err := NextID(root, m, fs.Arg(0), *group)
		if err == nil {
			fmt.Fprintln(stdout, id)
		}
		return err
	default:
		return fmt.Errorf("unknown id command %q", args[0])
	}
}

func normalizeNextIDArgs(args []string) []string {
	if len(args) <= 1 || strings.HasPrefix(args[0], "-") {
		return args
	}
	normalized := make([]string, 0, len(args))
	normalized = append(normalized, args[1:]...)
	normalized = append(normalized, args[0])
	return normalized
}

func printMakeHelp(w io.Writer) {
	fmt.Fprint(w, `Targets: sync project-upgrade deps-update roost-up codegen-up doctor generate generate-changed check-generated config-check id-check build run test test-race new-saga ci dev-up dev-down dev-logs
`)
}

var _ = os.Stdout
