package registry

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// legacyAggregators are the hand-written aggregation points this generator
// replaces. Generating alongside one of them would leave two lists and no way
// for the next person to know which to edit, so the generator refuses and
// says what to remove. The check is by content, not just path: a project may
// keep a file at one of these paths for unrelated reasons.
var legacyAggregators = []struct {
	Path     string
	Marker   string
	Replaces string
}{
	{Path: "game/bootstrap/register.go", Marker: "func RegisterAll(", Replaces: "RegisterAll"},
	{Path: "game/entities/register/register.go", Marker: "func RegisterEntities(", Replaces: "RegisterEntities"},
	{Path: "game/components/register/register.go", Marker: "func RegisterComponents(", Replaces: "RegisterComponents"},
}

// Run scans root and writes the aggregate.
func Run(root string, modulePath string, stdout io.Writer) error {
	if err := checkLegacyAggregators(root); err != nil {
		return err
	}
	registrations, err := Scan(root, modulePath)
	if err != nil {
		return err
	}
	changed, err := Generate(root, registrations)
	if err != nil {
		return err
	}
	if changed && stdout != nil {
		fmt.Fprintf(stdout, "generated: %s (%d registrations)\n", OutputPath, len(registrations))
	}
	return nil
}

func checkLegacyAggregators(root string) error {
	var found []string
	for _, legacy := range legacyAggregators {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(legacy.Path)))
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), legacy.Marker) {
			found = append(found, fmt.Sprintf("%s (%s)", legacy.Path, legacy.Replaces))
		}
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf(`registry: a hand-written registration aggregator is still present:
  %s
%s now owns this list. Migrate by marking each registration function with
//cube:register phase=<phase> and deleting the hand-written aggregator, then
run roost generate again. Generating both would leave two lists with no way to
tell which one to edit`, strings.Join(found, "\n  "), OutputPath)
}
