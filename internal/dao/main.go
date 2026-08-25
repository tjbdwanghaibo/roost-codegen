// tool/dao generates DAO accessor code (setters, marshal/unmarshal, DaoInterface).
//
// Usage:
//
//	go run ./tool/dao -def ./db/def -out ./db -pkg db
//
// Scans def directory for //cube:dao and //cube:redisdao markers, generates:
//   - gen_<name>_dao.go   — private storage, typed accessors/mutators, Init, Marshal, Unmarshal
//   - gen_<name>_nested.go — private nested storage and typed accessors/mutators
//   - gen_<name>_redis_dao.go — typed Redis DAO wrapper
package dao

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tjbdwanghaibo/roost-codegen/internal/project"
)

func Run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("dao", flag.ContinueOnError)
	flags.SetOutput(stdout)
	defDir := flags.String("def", "./db/def", "directory containing DB definitions")
	outDir := flags.String("out", "./db", "output directory for generated code")
	pkg := flags.String("pkg", "", "generated package name (default: detect from output directory)")
	force := flags.Bool("force", false, "force regeneration")
	if err := flags.Parse(args); err != nil {
		return err
	}

	absDefDir, err := filepath.Abs(*defDir)
	if err != nil {
		return fmt.Errorf("resolve definition directory: %w", err)
	}
	absOutDir, err := filepath.Abs(*outDir)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(absOutDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if *pkg == "" {
		info, err := project.Discover(absOutDir)
		if err != nil {
			return fmt.Errorf("discover output project: %w", err)
		}
		*pkg, err = info.PackageName(absOutDir)
		if err != nil {
			return fmt.Errorf("detect output package: %w", err)
		}
	}

	// Parse definitions
	defs, err := parseDefDir(absDefDir)
	if err != nil {
		return fmt.Errorf("parse definitions: %w", err)
	}

	if len(defs.Daos) == 0 && len(defs.RedisDaos) == 0 && len(defs.Nested) == 0 {
		_, _ = fmt.Fprintf(stdout, "no markers found in %s\n", absDefDir)
		return nil
	}

	// Generate DAO files
	for _, dao := range defs.Daos {
		outFile := filepath.Join(absOutDir, fmt.Sprintf("gen_%s_dao.go", toSnake(dao.Name)))
		changed, err := generateDao(dao, defs, *pkg, outFile, *force)
		if err != nil {
			return fmt.Errorf("generate dao %s: %w", dao.Name, err)
		}
		if changed {
			_, _ = fmt.Fprintf(stdout, "generated: %s\n", outFile)
		} else {
			_, _ = fmt.Fprintf(stdout, "unchanged: %s\n", outFile)
		}
	}

	for _, dao := range defs.RedisDaos {
		outFile := filepath.Join(absOutDir, fmt.Sprintf("gen_%s_redis_dao.go", toSnake(dao.Name)))
		changed, err := generateRedisDao(dao, *pkg, outFile, *force)
		if err != nil {
			return fmt.Errorf("generate redis dao %s: %w", dao.Name, err)
		}
		if changed {
			_, _ = fmt.Fprintf(stdout, "generated: %s\n", outFile)
		} else {
			_, _ = fmt.Fprintf(stdout, "unchanged: %s\n", outFile)
		}
	}

	// Generate nested struct files
	for _, nested := range defs.Nested {
		outFile := filepath.Join(absOutDir, fmt.Sprintf("gen_%s_nested.go", toSnake(nested.Name)))
		changed, err := generateNested(nested, *pkg, outFile, *force)
		if err != nil {
			return fmt.Errorf("generate nested %s: %w", nested.Name, err)
		}
		if changed {
			_, _ = fmt.Fprintf(stdout, "generated: %s\n", outFile)
		} else {
			_, _ = fmt.Fprintf(stdout, "unchanged: %s\n", outFile)
		}
	}
	return nil
}
