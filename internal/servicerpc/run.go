// roost servicerpc generates the bus transport for //roost:rpc interfaces.
package servicerpc

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func Run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("servicerpc", flag.ContinueOnError)
	flags.SetOutput(stdout)
	dir := flags.String("dir", ".", "package directory to scan for //roost:rpc interfaces")
	// check validates the interfaces AND compares the generated output against
	// what is on disk, writing nothing and failing on any difference.
	//
	// Both halves are needed and the second one was missing. This mode used to
	// return right after the refusals ran, so it exited 0 on a generated file
	// that was stale, hand-edited, or produced by a different version of this
	// generator — while being documented as the CI drift gate. A gate that
	// cannot fail is worse than no gate: it reports that the committed
	// transport matches the interface when nobody checked.
	//
	// The refusals still run first, and that ordering is deliberate: a type
	// that cannot cross a bus faithfully is a design problem, and an author
	// fixing an interface wants that answer rather than a diff.
	check := flags.Bool("check", false, "validate the interfaces and verify the generated files match, writing nothing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return fmt.Errorf("resolve dir: %w", err)
	}
	services, err := ParseDir(absDir)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		_, _ = fmt.Fprintf(stdout, "no //roost:rpc interfaces in %s\n", absDir)
		return nil
	}
	for _, service := range services {
		_, _ = fmt.Fprintf(stdout, "%s.%s: service_type=%s capability=%s methods=%d\n",
			service.Package, service.Interface, service.ServiceType, service.Capability,
			len(service.Methods))
		for _, method := range service.Methods {
			suffix := ""
			if method.Affinity != "" {
				suffix += fmt.Sprintf(" affinity=%s", method.Affinity)
			}
			if method.Reliable {
				suffix += " reliable"
			}
			_, _ = fmt.Fprintf(stdout, "  %-14s params=%d results=%d%s\n",
				method.Name, len(method.Params), len(method.Results), suffix)
		}
	}
	var stale []string
	for _, service := range services {
		files, err := Generate(service)
		if err != nil {
			return err
		}
		for _, file := range files {
			path := filepath.Join(absDir, file.Name)
			existing, readErr := os.ReadFile(path)
			current := readErr == nil && bytes.Equal(existing, file.Content)
			if current {
				_, _ = fmt.Fprintf(stdout, "up to date: %s\n", file.Name)
				continue
			}
			if *check {
				// Missing and differing are reported apart: one means nobody ran
				// the generator, the other means the file was edited or produced
				// by a different version of it, and the fix is not the same.
				if readErr != nil {
					stale = append(stale, file.Name+" (missing)")
				} else {
					stale = append(stale, file.Name)
				}
				_, _ = fmt.Fprintf(stdout, "STALE: %s\n", file.Name)
				continue
			}
			if err := os.WriteFile(path, file.Content, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			_, _ = fmt.Fprintf(stdout, "generated: %s\n", file.Name)
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("%s: generated transport does not match the interface: %s. "+
			"Run `go generate ./...` and commit the result — a hand-edited generated file is "+
			"reverted by the next run, and a file produced by a different version of this "+
			"generator means the committed transport is not the one this interface describes",
			absDir, strings.Join(stale, ", "))
	}
	return nil
}
