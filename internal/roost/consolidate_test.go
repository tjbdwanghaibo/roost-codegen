package roost

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProjectFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const legacyGoMod = `module example.com/planet

go 1.27.0

require (
	github.com/tjbdwanghaibo/roost-core v1.12.0
	github.com/tjbdwanghaibo/roost-kit v1.12.6
	github.com/tjbdwanghaibo/roost-skill v1.10.3
	github.com/tjbdwanghaibo/roost-service v1.5.4
)
`

// The upgrader must move every relocated import, decide split packages per
// symbol, keep the identifier a file already uses when the package name
// changes, apply the recorded renames, and leave files on the new layout
// alone.
func TestConsolidateProjectRewritesImportsGoModAndManifest(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", legacyGoMod)
	m := DefaultManifest("planet", "example.com/planet", nil, nil, nil)
	m.Versions.Skill = "v1.10.3"
	raw, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	writeProjectFile(t, root, ManifestName, string(raw))
	mixed := writeProjectFile(t, root, "internal/wiring/wiring.go", `package wiring

import (
	"github.com/tjbdwanghaibo/roost-kit/dataengine"
	kitredis "github.com/tjbdwanghaibo/roost-kit/redis"
	"github.com/tjbdwanghaibo/roost-kit/syncstream"
	"github.com/tjbdwanghaibo/roost-service/servicemods"
	"github.com/tjbdwanghaibo/roost-skill/skill"
)

var (
	_ = skill.Program{}
	_ = kitredis.NewRedisMod
	_ = kitredis.NewClient
	_ = dataengine.NewEntityRepository
	_ = syncstream.HealthOptions{}
	_ = servicemods.ModMail
)
`)
	untouched := writeProjectFile(t, root, "internal/fresh/fresh.go", `package fresh

import (
	"github.com/tjbdwanghaibo/roost-core/skill"
	kitmods "github.com/tjbdwanghaibo/roost-kit/mods"
)

var (
	_ = skill.Program{}
	_ = kitmods.ModBus
)
`)
	before, _ := os.ReadFile(untouched)

	var stdout bytes.Buffer
	result, err := ConsolidateProject(root, false, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0] != "internal/wiring/wiring.go" || !result.GoMod || !result.Manifest {
		t.Fatalf("result = %+v", result)
	}
	rewritten, _ := os.ReadFile(mixed)
	for _, want := range []string{
		`dataengine "github.com/tjbdwanghaibo/roost-core/dataengine/engine"`, // package name changes: keep the identifier
		`kitredis "github.com/tjbdwanghaibo/roost-kit/redis"`,                // Mod glue stays
		`coreredis "github.com/tjbdwanghaibo/roost-core/redis"`,              // moved symbols get a second import
		`_ = coreredis.NewClient`,
		`_ = kitredis.NewRedisMod`,
		`"github.com/tjbdwanghaibo/roost-core/syncstream"`,
		`syncstream.PublisherHealthOptions{}`,                   // recorded rename
		`servicemods "github.com/tjbdwanghaibo/roost-kit/mods"`, // folded package: keep the identifier
		`"github.com/tjbdwanghaibo/roost-core/skill"`,
	} {
		if !strings.Contains(string(rewritten), want) {
			t.Errorf("rewritten file missing %q:\n%s", want, rewritten)
		}
	}
	for _, bad := range []string{"roost-skill", "roost-service", "roost-kit/dataengine", "roost-kit/syncstream", "HealthOptions{}\n"} {
		if strings.Contains(string(rewritten), bad) && bad != "HealthOptions{}\n" {
			t.Errorf("rewritten file still mentions %q:\n%s", bad, rewritten)
		}
	}
	if after, _ := os.ReadFile(untouched); !bytes.Equal(before, after) {
		t.Fatalf("a file already on the new layout was modified:\n%s", after)
	}
	goMod, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	for _, bad := range []string{"roost-skill", "roost-service"} {
		if strings.Contains(string(goMod), bad) {
			t.Errorf("go.mod still requires %s:\n%s", bad, goMod)
		}
	}
	// Versions are left to the dependency resolution step: writing an
	// unpublished boundary release here would break the very go get that
	// follows (upgrade-compat caught exactly that).
	for _, want := range []string{"roost-core v1.12.0", "roost-kit v1.12.6"} {
		if !strings.Contains(string(goMod), want) {
			t.Errorf("go.mod versions must be untouched (%q):\n%s", want, goMod)
		}
	}
	manifest, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Versions.Skill != "" || manifest.Versions.Service != "" {
		t.Fatalf("manifest kept the removed module policies: %+v", manifest.Versions)
	}
	if !strings.Contains(stdout.String(), "rewrote 1 Go file(s), go.mod, roost.yaml") {
		t.Fatalf("report = %q", stdout.String())
	}

	// Idempotent: a second run finds nothing to do.
	again, err := ConsolidateProject(root, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Files) != 0 || again.GoMod || again.Manifest {
		t.Fatalf("second run was not a no-op: %+v", again)
	}
}

// --dry-run reports the same plan without touching a single file.
func TestConsolidateProjectDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", legacyGoMod)
	file := writeProjectFile(t, root, "x.go", "package x\n\nimport \"github.com/tjbdwanghaibo/roost-skill/skill\"\n\nvar _ = skill.Program{}\n")
	before, _ := os.ReadFile(file)
	result, err := ConsolidateProject(root, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || !result.GoMod {
		t.Fatalf("dry run plan = %+v", result)
	}
	after, _ := os.ReadFile(file)
	goMod, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	if !bytes.Equal(before, after) || string(goMod) != legacyGoMod {
		t.Fatal("dry run modified the project")
	}
}

// An import on a removed module that the map does not know is an error, not
// a silent leftover that fails at go build time.
func TestConsolidateProjectRefusesUnmappedRemovedImports(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", legacyGoMod)
	writeProjectFile(t, root, "x.go", "package x\n\nimport \"github.com/tjbdwanghaibo/roost-skill/internal/wire\"\n\nvar _ = wire.Thing\n")
	_, err := ConsolidateProject(root, false, nil)
	if err == nil || !strings.Contains(err.Error(), "roost-skill/internal/wire") {
		t.Fatalf("unmapped removed import accepted: %v", err)
	}
}

// The embedded relocation map itself must stay coherent: every rename points
// at a mapped package and the boundary versions are the generator floors.
func TestConsolidationMapMatchesTheGeneratorFloor(t *testing.T) {
	table, m, err := loadConsolidationMap()
	if err != nil {
		t.Fatal(err)
	}
	if m.Boundary.Core != minimumVersions.Core || m.Boundary.Kit != minimumVersions.Kit || m.Boundary.Codegen != minimumVersions.Codegen {
		t.Fatalf("map boundary %+v != generator floor %+v", m.Boundary, minimumVersions)
	}
	for _, old := range []string{"github.com/tjbdwanghaibo/roost-skill/skill", "github.com/tjbdwanghaibo/roost-service/mail", "github.com/tjbdwanghaibo/roost-kit/nestwal", "github.com/tjbdwanghaibo/roost-kit/redis"} {
		if _, ok := table[old]; !ok {
			t.Errorf("map lacks %s", old)
		}
	}
}
