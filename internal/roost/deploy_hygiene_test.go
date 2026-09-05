package roost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The generated project's own CI runs `docker compose config` on the
// production compose file and `shellcheck` on every deploy script, so a
// generator defect in either shows up as a red pipeline in every consumer.
// These tests pin the two defects that did exactly that on 2026-09-05.

// Compose short-syntax volumes treat a source that does not start with `/`,
// `./` or `../` as a NAMED volume. `${ROOST_CONFIG_ROOT}/config.game.yaml`
// with ROOST_CONFIG_ROOT=configs/service therefore rendered as a reference to
// an undefined volume ("service "game" refers to undefined volume"). Long
// syntax with an explicit `type: bind` has no such ambiguity.
func TestProductionComposeMountsTheConfigAsAnExplicitBind(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", []string{"game", "gate"}, nil, nil)
	compose := renderProductionCompose(m)
	if !strings.Contains(compose, "type: bind") {
		t.Fatalf("config mount is not an explicit bind:\n%s", compose)
	}
	if short := regexp.MustCompile(`(?m)^\s*- \$\{ROOST_CONFIG_ROOT[^\n]*:/etc/roost/config\.yaml`); short.MatchString(compose) {
		t.Fatalf("config mount still uses short syntax, which compose may read as a named volume:\n%s", compose)
	}
	if !strings.Contains(compose, "type: volume") || !strings.Contains(compose, "source: planet-game-wal") {
		t.Fatalf("WAL volume lost its explicit long-syntax mount:\n%s", compose)
	}
}

// Every place the generator points compose at the repository's own configs
// must do so with an absolute path: compose resolves a relative bind source
// against the compose FILE's directory (deploy/docker/), not the caller's cwd.
func TestGeneratedComposeInvocationsUseAnAbsoluteConfigRoot(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", []string{"game"}, nil, nil)
	plan, err := renderProject(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"Makefile", ".github/workflows/ci.yml"} {
		body := string(plan[path].Body)
		if strings.Contains(body, "ROOST_CONFIG_ROOT=configs/service") || strings.Contains(body, "ROOST_CONFIG_ROOT: configs/service") {
			t.Errorf("%s passes a relative ROOST_CONFIG_ROOT to compose:\n%s", path, body)
		}
	}
}

// shellcheck findings in the generated scripts: SC1007 (`CDPATH= cd`) and
// SC2194 (a `case` on a constant word). Both are warnings that fail the
// generated project's "deployment shell syntax" step.
func TestDeployScriptsCarryNoKnownShellcheckFindings(t *testing.T) {
	for name, m := range map[string]Manifest{
		"stateful":  DefaultManifest("planet", "example.com/planet", []string{"game", "gate"}, nil, nil),
		"stateless": DefaultManifest("planet", "example.com/planet", []string{"game"}, []string{"configdata"}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			files := renderProductionDeployment(m)
			var scripts []string
			for path, body := range files {
				if !strings.HasSuffix(path, ".sh") {
					continue
				}
				if strings.Contains(body, "CDPATH= cd") {
					t.Errorf("%s: SC1007 — `CDPATH= cd`", path)
				}
				if regexp.MustCompile(`(?m)^\s*case " [^$"]*" in`).MatchString(body) {
					t.Errorf("%s: SC2194 — case on a constant word", path)
				}
				if strings.Contains(body, `case "$SERVICE" in )`) {
					t.Errorf("%s: empty alternation is a syntax error", path)
				}
				full := filepath.Join(dir, filepath.FromSlash(path))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
				scripts = append(scripts, full)
				if out, err := exec.Command("sh", "-n", full).CombinedOutput(); err != nil {
					t.Errorf("%s: sh -n: %v\n%s", path, err, out)
				}
			}
			if len(scripts) == 0 {
				t.Fatal("no deploy scripts rendered")
			}
			if _, err := exec.LookPath("shellcheck"); err != nil {
				t.Logf("shellcheck not installed; the generated project's CI runs it")
				return
			}
			if out, err := exec.Command("shellcheck", scripts...).CombinedOutput(); err != nil {
				t.Errorf("shellcheck: %v\n%s", err, out)
			}
		})
	}
}

// The compatibility matrix's "minimum" dependency set must be exactly the
// floor the generator enforces, or the job fails at `roost project new`
// ("versions.core requires >= v1.10.0 or latest; got v1.8.0") instead of
// testing anything. Two literals in two files, one fact: pin them together.
func TestFrameworkCompatMinimumSetMatchesTheGeneratorFloor(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "framework-compat.yml"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`-roost-core-version (\S+) -roost-kit-version (\S+) -roost-skill-version (\S+) -codegen-version (\S+)\)`)
	match := re.FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatal("framework-compat.yml has no minimum dependency set")
	}
	got := VersionSpec{Core: match[1], Kit: match[2], Skill: match[3], Codegen: match[4]}
	if got != minimumVersions {
		t.Fatalf("workflow minimum set %+v != generator floor %+v", got, minimumVersions)
	}
}

// Historical generators below v1.11.0 emit go.mod requirements on the
// pre-rename module paths (github.com/tjbdwanghaibo/cube-core, cube-kit);
// `go get` of those fails forever, so a project created by them can never be
// upgraded in CI. The upgrade matrix must start at the first release that
// emitted roost-* paths.
func TestUpgradeCompatHistoryStartsAtTheRoostModulePaths(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "upgrade-compat.yml"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`codegen: \[([^\]]+)\]`)
	match := re.FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatal("upgrade-compat.yml has no codegen matrix")
	}
	for _, entry := range strings.Split(match[1], ",") {
		version := strings.TrimSpace(entry)
		if !versionAtLeast(version, 1, 11, 0) {
			t.Errorf("matrix entry %s predates the roost-* module paths and cannot resolve", version)
		}
	}
}

// `! cmd` as a statement skips errexit (SC2251): the negated grep in
// release.yml reports nothing and the step continues, so the hygiene it
// implements never fails. Bare `*.tar.gz` globs are SC2035.
func TestReleaseWorkflowShellHasNoBareNegationsOrGlobs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "! ") {
			t.Errorf("release.yml:%d: bare negation skips errexit: %s", i+1, trimmed)
		}
		if strings.Contains(trimmed, "sha256sum *.") {
			t.Errorf("release.yml:%d: bare glob: %s", i+1, trimmed)
		}
	}
}
