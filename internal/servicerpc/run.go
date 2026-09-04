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
	// check runs the parser and its refusals without writing anything.
	//
	// It exists as its own mode because the refusals are the part of this
	// generator that makes a judgement — a type that cannot cross a bus
	// faithfully is a design problem, not a generation problem — and an author
	// fixing an interface wants that answer without a diff to review.
	check := flags.Bool("check", false, "validate the interfaces and write nothing")
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
	if *check {
		return nil
	}
	for _, service := range services {
		content, err := Generate(service)
		if err != nil {
			return err
		}
		name := strings.ToLower(service.Interface) + "_rpc_gen.go"
		path := filepath.Join(absDir, name)
		existing, readErr := os.ReadFile(path)
		if readErr == nil && bytes.Equal(existing, content) {
			_, _ = fmt.Fprintf(stdout, "up to date: %s\n", name)
			continue
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		_, _ = fmt.Fprintf(stdout, "generated: %s\n", name)
	}
	return nil
}
