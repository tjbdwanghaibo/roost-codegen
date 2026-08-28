package roost

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateFrameworkDependenciesResolvesAllDirectModulesTogether(t *testing.T) {
	root := t.TempDir()
	goMod := []byte("module example.com/planet\n\ngo 1.25\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := DefaultManifest("planet", "example.com/planet", nil, nil, nil)
	var commands [][]string
	runner := func(_ context.Context, gotRoot string, _, _ io.Writer, args ...string) error {
		if gotRoot != root {
			t.Fatalf("command root = %q, want %q", gotRoot, root)
		}
		commands = append(commands, append([]string(nil), args...))
		return nil
	}
	if err := updateFrameworkDependencies(root, manifest, io.Discard, io.Discard, runner); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{
			"get",
			"github.com/tjbdwanghaibo/cube-core@latest",
			"github.com/tjbdwanghaibo/cube-kit@latest",
			"github.com/tjbdwanghaibo/roost-skill@latest",
		},
		{"mod", "tidy"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestUpdateFrameworkDependenciesUsesExplicitPolicies(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := DefaultManifest("planet", "example.com/planet", nil, nil, nil)
	manifest.Versions.Core = "v1.9.0"
	manifest.Versions.Kit = "v1.10.0"
	manifest.Versions.Skill = "v1.9.0"
	var get []string
	runner := func(_ context.Context, _ string, _, _ io.Writer, args ...string) error {
		if len(args) > 0 && args[0] == "get" {
			get = append([]string(nil), args...)
		}
		return nil
	}
	if err := updateFrameworkDependencies(root, manifest, io.Discard, io.Discard, runner); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(get, " ")
	for _, want := range []string{"cube-core@v1.9.0", "cube-kit@v1.10.0", "roost-skill@v1.9.0"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("go get command missing %q: %v", want, get)
		}
	}
}

func TestUpdateFrameworkDependenciesRollsBackModuleFiles(t *testing.T) {
	root := t.TempDir()
	modPath := filepath.Join(root, "go.mod")
	sumPath := filepath.Join(root, "go.sum")
	oldMod := []byte("module example.com/planet\n\ngo 1.25\n")
	oldSum := []byte("old checksum\n")
	if err := os.WriteFile(modPath, oldMod, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sumPath, oldSum, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := func(_ context.Context, _ string, _, _ io.Writer, _ ...string) error {
		if err := os.WriteFile(modPath, []byte("corrupt\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(sumPath, []byte("partial\n"), 0o644); err != nil {
			return err
		}
		return errors.New("registry unavailable")
	}
	err := updateFrameworkDependencies(root, DefaultManifest("planet", "example.com/planet", nil, nil, nil), io.Discard, io.Discard, runner)
	if err == nil || !strings.Contains(err.Error(), "registry unavailable") {
		t.Fatalf("expected resolver error, got %v", err)
	}
	assertFileContent(t, modPath, oldMod)
	assertFileContent(t, sumPath, oldSum)
}

func TestManifestVersionPolicies(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", nil, nil, nil)
	if err := m.Validate(); err != nil {
		t.Fatalf("latest policy rejected: %v", err)
	}
	m.Versions.Skill = "v1.6.9"
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), minimumVersions.Skill) {
		t.Fatalf("unsupported skill version accepted: %v", err)
	}
	m.Versions.Skill = "main"
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "latest or a semantic release") {
		t.Fatalf("non-release policy accepted: %v", err)
	}
	m.Versions.Skill = "v2.0.0"
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "module path") {
		t.Fatalf("incompatible major accepted: %v", err)
	}
}

func TestDependencyCommandForcesWorkspaceIsolation(t *testing.T) {
	got := appendWithoutGoWork([]string{"PATH=test", "GOWORK=D:/parent/go.work", "gowork=other"}, "GOWORK=off")
	if !reflect.DeepEqual(got, []string{"PATH=test", "GOWORK=off"}) {
		t.Fatalf("isolated environment = %#v", got)
	}
}

func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
