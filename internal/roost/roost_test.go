package roost

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveModsAddsRequiredDependencies(t *testing.T) {
	got, err := resolveMods([]string{"remote_entity", "sync"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"redis", "nats", "remote_entity", "sync"} {
		if !contains(got, want) {
			t.Fatalf("resolved mods %v do not contain %s", got, want)
		}
	}
}

func TestNewProjectSyncPreservesBusinessFiles(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planet")
	result, root, err := NewProject(NewOptions{
		Name:     "planet",
		Module:   "example.com/planet",
		Out:      target,
		Services: []string{"game", "gate"},
		Mods:     []string{"configdata", "sync", "remote_entity"},
		Features: []string{"config", "nest"},
	})
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	if len(result.Created) == 0 || root != target {
		t.Fatalf("unexpected result: %#v root=%s", result, root)
	}
	for _, rel := range []string{
		"roost.yaml",
		"Makefile",
		"internal/bootstrap/generated.go",
		"internal/service/game/service.go",
		"configs/generated/gen_table_config.go",
		"game/bootstrap/nest.go",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s missing: %v", rel, err)
		}
	}

	servicePath := filepath.Join(root, "internal", "service", "game", "service.go")
	raw, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n// business-owned\n")...)
	if err := os.WriteFile(servicePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncProject(root); err != nil {
		t.Fatalf("sync project: %v", err)
	}
	after, _ := os.ReadFile(servicePath)
	if !bytes.Contains(after, []byte("business-owned")) {
		t.Fatal("sync overwrote the business service file")
	}
}

func TestAddProtocolAllocatesIDAndCheckDetectsDuplicate(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planet")
	_, root, err := NewProject(NewOptions{Name: "planet", Module: "example.com/planet", Out: target, Features: []string{"protocol"}})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := Add(root, AddOptions{Kind: "protocol", Name: "PlayerLogin", Group: "game"})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("paths = %v", paths)
	}
	raw, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(paths[0])))
	if !strings.Contains(string(raw), "id=10000") {
		t.Fatalf("protocol did not receive first ID:\n%s", raw)
	}
	m, _ := LoadManifest(root)
	if err := CheckIDs(root, m); err != nil {
		t.Fatalf("id check: %v", err)
	}
	duplicate := filepath.Join(root, "protocol", "def", "duplicate.go")
	if err := os.WriteFile(duplicate, []byte("package protocoldef\n//cube:msg id=10000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckIDs(root, m); err == nil {
		t.Fatal("expected duplicate ID failure")
	}
}

func TestLoadManifestRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	raw := []byte("schema: 1\nproject:\n  name: planet\n  module: example.com/planet\nversions: {}\nservices:\n  game: {}\nunknown: true\n")
	if err := os.WriteFile(filepath.Join(root, ManifestName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(root); err == nil {
		t.Fatal("expected strict YAML decode error")
	}
}

func TestProductionConfigRejectsDevelopmentValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("sid: 1\nredis:\n  addr: 127.0.0.1:6379\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckConfig(path, true); err == nil {
		t.Fatal("expected production config rejection")
	}
}

func TestAddCamelCaseProtocolUsesSnakeFileAndPascalTypes(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planet")
	_, root, err := NewProject(NewOptions{Name: "planet", Module: "example.com/planet", Out: target, Features: []string{"protocol"}})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := Add(root, AddOptions{Kind: "protocol", Name: "PlayerLogin", Group: "game"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths[0], "protocol/def/player_login.go"; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	raw, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(paths[0])))
	if !strings.Contains(string(raw), "type PlayerLoginRequest struct") {
		t.Fatalf("unexpected protocol:\n%s", raw)
	}
}

func TestReplicationPresetCompilesAsGoSource(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", []string{"game"}, []string{"etcd"}, []string{"replication-quic", "replication-kcp", "replication-udp"})
	m.Versions.Kit = "v1.1.0"
	plan, err := renderProject(m)
	if err != nil {
		t.Fatal(err)
	}
	file, ok := plan["internal/transport/generated.go"]
	if !ok || !bytes.Contains(file.Body, []byte("func NewQUIC")) || !bytes.Contains(file.Body, []byte("func NewKCP")) || !bytes.Contains(file.Body, []byte("func NewUDP")) || !bytes.Contains(file.Body, []byte("func NewRoomSink")) {
		t.Fatalf("replication preset missing:\n%s", file.Body)
	}
}

func TestReplicationRequiresFixedKitRelease(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", []string{"game"}, []string{"etcd"}, []string{"replication-quic"})
	m.Versions.Kit = "v1.0.5"
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "v1.1.0") {
		t.Fatalf("expected kit release guard, got %v", err)
	}
	m.Versions.Kit = "v1.1.0"
	if err := m.Validate(); err != nil {
		t.Fatalf("fixed kit release rejected: %v", err)
	}
}

func TestProductionRenderingIncludesDurabilityAndTopologyGuards(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", []string{"game"}, []string{"mongo", "redis", "nats", "checkpoint", "nestwal"}, []string{"config", "nest"})
	production := renderServiceConfig(m, "game", true)
	for _, want := range []string{
		"require_replica_set: true",
		"transaction_timeout: 30s",
		"transaction_receipt_ttl: 720h",
		"dir: /var/lib/roost/wal",
		"max_disk_bytes: 8589934592",
		"duplicate_window: 10m",
		"replicas: 3",
		"aof_replicas: 1",
	} {
		if !strings.Contains(production, want) {
			t.Errorf("production config missing %q:\n%s", want, production)
		}
	}
	if strings.Contains(production, "127.0.0.1") || strings.Contains(production, "file: true") {
		t.Fatalf("development topology leaked into production config:\n%s", production)
	}
	dockerfile := renderDockerfile(m)
	if !strings.Contains(dockerfile, `VOLUME ["/var/lib/roost/wal"]`) || !strings.Contains(dockerfile, "USER nonroot:nonroot") {
		t.Fatalf("durable non-root Dockerfile missing:\n%s", dockerfile)
	}
	compose := renderCompose(m)
	if !strings.Contains(compose, `"--appendfsync", "everysec"`) {
		t.Fatalf("Redis AOF configuration missing from compose:\n%s", compose)
	}
	if !strings.Contains(compose, `"-sd", "/data"`) || !strings.Contains(compose, "mongo-data:/data/db") {
		t.Fatalf("development dependencies are not durable:\n%s", compose)
	}
	if m.Versions.Core != "v1.3.0" || m.Versions.Kit != "v1.3.0" || m.Versions.Codegen != "v1.3.0" {
		t.Fatalf("release defaults = %+v", m.Versions)
	}
}

func TestBootstrapImportsTransitiveModDependencies(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", []string{"game"}, []string{"nestwal"}, []string{"nest"})
	bootstrap := renderBootstrap(m)
	for _, want := range []string{
		`kitcheckpoint "github.com/tjbdwanghaibo/cube-kit/checkpoint"`,
		`kitmongo "github.com/tjbdwanghaibo/cube-kit/mongo"`,
		`kitredis "github.com/tjbdwanghaibo/cube-kit/redis"`,
		`kitnats "github.com/tjbdwanghaibo/cube-kit/nats"`,
		"kitcheckpoint.NewMod(",
	} {
		if !strings.Contains(bootstrap, want) {
			t.Errorf("bootstrap missing transitive dependency %q:\n%s", want, bootstrap)
		}
	}
}

func TestSyncPreflightsConflictsBeforeWriting(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planet")
	_, root, err := NewProject(NewOptions{Name: "planet", Module: "example.com/planet", Out: target, Features: []string{"config"}})
	if err != nil {
		t.Fatal(err)
	}
	makefile := filepath.Join(root, "Makefile")
	before, err := os.ReadFile(makefile)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := filepath.Join(root, "internal", "bootstrap", "generated.go")
	if err := os.WriteFile(bootstrap, []byte("package bootstrap\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Services["gate"] = ServiceSpec{Mods: []string{"etcd"}}
	if err := saveManifest(root, m); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncProject(root); err == nil {
		t.Fatal("expected ownership conflict")
	}
	after, err := os.ReadFile(makefile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("Makefile changed before a later preflight conflict")
	}
}
