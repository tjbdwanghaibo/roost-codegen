package roost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const ManifestName = "roost.yaml"

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var releaseVersionPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// minimumVersions is the oldest framework set supported by this generator.
// "latest" remains the default update policy; these concrete versions are
// used only as an offline/bootstrap go.mod baseline and as compatibility
// guards for users that intentionally pin a release.
var minimumVersions = VersionSpec{
	Core:    "v1.8.0",
	Kit:     "v1.8.0",
	Skill:   "v1.9.0",
	Codegen: "v1.7.0",
}

type Manifest struct {
	Schema     int                    `yaml:"schema"`
	Project    ProjectSpec            `yaml:"project"`
	Versions   VersionSpec            `yaml:"versions"`
	SharedMods []string               `yaml:"shared_mods,omitempty"`
	Services   map[string]ServiceSpec `yaml:"services"`
	Features   []string               `yaml:"features,omitempty"`
	Sagas      []string               `yaml:"sagas,omitempty"`
	IDs        map[string]IDSpace     `yaml:"ids,omitempty"`
}

type ProjectSpec struct {
	Name   string `yaml:"name"`
	Module string `yaml:"module"`
}

type VersionSpec struct {
	Core    string `yaml:"core"`
	Kit     string `yaml:"kit"`
	Skill   string `yaml:"skill"`
	Codegen string `yaml:"codegen"`
}

type ServiceSpec struct {
	Mods []string `yaml:"mods,omitempty"`
}

type IDSpace struct {
	Min    int64              `yaml:"min,omitempty"`
	Max    int64              `yaml:"max,omitempty"`
	Groups map[string]IDRange `yaml:"groups,omitempty"`
}

type IDRange struct {
	Min int64 `yaml:"min"`
	Max int64 `yaml:"max"`
}

func DefaultManifest(name, module string, services, mods, features []string) Manifest {
	if module == "" {
		module = "github.com/tjbdwanghaibo/" + name
	}
	if len(services) == 0 {
		services = []string{"game"}
	}
	if len(features) == 0 {
		features = []string{"protocol", "config", "entity", "nest", "event", "dao", "errcode"}
	}
	shared := []string{"lock", "ops", "statslog"}
	if len(mods) == 0 {
		mods = []string{"configdata", "etcd", "redis", "mongo", "nats", "sync", "remote_entity", "checkpoint", "nestwal", "nest"}
	}
	svc := make(map[string]ServiceSpec, len(services))
	for _, service := range services {
		svc[service] = ServiceSpec{Mods: append([]string(nil), mods...)}
	}
	return Manifest{
		Schema:     1,
		Project:    ProjectSpec{Name: name, Module: module},
		Versions:   VersionSpec{Core: "latest", Kit: "latest", Skill: "latest", Codegen: "latest"},
		SharedMods: shared,
		Services:   svc,
		Features:   uniqueSorted(features),
		IDs: map[string]IDSpace{
			"protocol":  {Groups: map[string]IDRange{"game": {Min: 10000, Max: 19999}}},
			"entity":    {Min: 1000, Max: 1999},
			"component": {Min: 2000, Max: 2999},
			"errcode":   {Min: 100000, Max: 199999},
		},
	}
}

func LoadManifest(root string) (Manifest, error) {
	manifest, err := decodeManifest(root)
	if err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// loadManifestForUpgrade decodes an older manifest without applying the
// current version floor first. The upgrade command merges the requested
// policies and validates the resulting manifest before it is saved. YAML
// fields remain strict so this path cannot silently discard unknown data.
func loadManifestForUpgrade(root string) (Manifest, error) {
	return decodeManifest(root)
}

func decodeManifest(root string) (Manifest, error) {
	path := filepath.Join(root, ManifestName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	var manifest Manifest
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return manifest, nil
}

func (m Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	header := fmt.Sprintf(`# Roost project manifest. Run make sync after editing.
# versions.* defaults to latest; go.mod records the concrete release resolved
# for one build. Minimums: core %s, kit %s, skill %s, codegen %s.
`, minimumVersions.Core, minimumVersions.Kit, minimumVersions.Skill, minimumVersions.Codegen)
	return append([]byte(header), raw...), nil
}

func (m Manifest) Validate() error {
	var joined error
	if m.Schema != 1 {
		joined = errors.Join(joined, fmt.Errorf("unsupported schema %d", m.Schema))
	}
	if !validName(m.Project.Name) {
		joined = errors.Join(joined, fmt.Errorf("invalid project name %q", m.Project.Name))
	}
	if strings.TrimSpace(m.Project.Module) == "" || strings.ContainsAny(m.Project.Module, " \\:") {
		joined = errors.Join(joined, fmt.Errorf("invalid module %q", m.Project.Module))
	}
	if len(m.Services) == 0 {
		joined = errors.Join(joined, errors.New("at least one service is required"))
	}
	for name, service := range m.Services {
		if !validName(name) {
			joined = errors.Join(joined, fmt.Errorf("invalid service name %q", name))
		}
		if _, err := resolveMods(service.Mods); err != nil {
			joined = errors.Join(joined, fmt.Errorf("service %s: %w", name, err))
		}
		shared := make(map[string]bool, len(m.SharedMods))
		for _, mod := range m.SharedMods {
			shared[mod] = true
		}
		for _, mod := range service.Mods {
			if shared[mod] {
				joined = errors.Join(joined, fmt.Errorf("service %s repeats shared mod %q", name, mod))
			}
		}
	}
	if _, err := resolveMods(m.SharedMods); err != nil {
		joined = errors.Join(joined, fmt.Errorf("shared_mods: %w", err))
	}
	for _, feature := range m.Features {
		if !knownFeatures[feature] {
			joined = errors.Join(joined, fmt.Errorf("unknown feature %q", feature))
		}
	}
	for _, version := range []struct {
		name    string
		value   string
		minimum string
	}{
		{name: "core", value: m.Versions.Core, minimum: minimumVersions.Core},
		{name: "kit", value: m.Versions.Kit, minimum: minimumVersions.Kit},
		{name: "skill", value: m.Versions.Skill, minimum: minimumVersions.Skill},
		{name: "codegen", value: m.Versions.Codegen, minimum: minimumVersions.Codegen},
	} {
		if err := validateVersionPolicy(version.name, version.value, version.minimum); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	for _, sagaName := range m.Sagas {
		if !validName(sagaName) {
			joined = errors.Join(joined, fmt.Errorf("invalid saga %q", sagaName))
		}
	}
	if len(m.Sagas) > 0 {
		hasSagaMod := false
		for _, service := range m.Services {
			hasSagaMod = hasSagaMod || contains(service.Mods, "saga")
		}
		if !hasSagaMod {
			joined = errors.Join(joined, errors.New("sagas require the saga mod on at least one service"))
		}
	}
	if hasReplicationFeature(m) && !versionAtLeast(m.Versions.Kit, 1, 1, 0) {
		joined = errors.Join(joined, fmt.Errorf("replication features require roost-kit >= v1.1.0; got %q", m.Versions.Kit))
	}
	if contains(allProjectMods(m), "saga") && (!versionAtLeast(m.Versions.Core, 1, 4, 0) || !versionAtLeast(m.Versions.Kit, 1, 4, 0)) {
		joined = errors.Join(joined, fmt.Errorf("saga requires roost-core and roost-kit >= v1.4.0; got core=%q kit=%q", m.Versions.Core, m.Versions.Kit))
	}
	for kind, space := range m.IDs {
		if space.Min != 0 || space.Max != 0 {
			if space.Min <= 0 || space.Max < space.Min {
				joined = errors.Join(joined, fmt.Errorf("ids.%s has invalid range", kind))
			}
		}
		for group, r := range space.Groups {
			if !validName(group) || r.Min <= 0 || r.Max < r.Min {
				joined = errors.Join(joined, fmt.Errorf("ids.%s.groups.%s has invalid range", kind, group))
			}
		}
	}
	return joined
}

func validName(name string) bool { return namePattern.MatchString(strings.TrimSpace(name)) }

func hasReplicationFeature(m Manifest) bool {
	return contains(m.Features, "replication-quic") || contains(m.Features, "replication-kcp") || contains(m.Features, "replication-udp")
}

func versionAtLeast(version string, major, minor, patch int) bool {
	if strings.EqualFold(strings.TrimSpace(version), "latest") {
		return true
	}
	gotMajor, gotMinor, gotPatch, ok := releaseVersion(version)
	if !ok {
		return false
	}
	if gotMajor != major {
		return gotMajor > major
	}
	if gotMinor != minor {
		return gotMinor > minor
	}
	return gotPatch >= patch
}

func validateVersionPolicy(name, value, minimum string) error {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "latest") {
		return nil
	}
	major, _, _, ok := releaseVersion(value)
	if !ok {
		return fmt.Errorf("versions.%s must be latest or a semantic release such as %s; got %q", name, minimum, value)
	}
	minimumMajor, minimumMinor, minimumPatch, _ := releaseVersion(minimum)
	if major != minimumMajor {
		return fmt.Errorf("versions.%s major v%d is incompatible with the current module path; use latest or v%d.x", name, major, minimumMajor)
	}
	if !versionAtLeast(value, minimumMajor, minimumMinor, minimumPatch) {
		return fmt.Errorf("versions.%s requires >= %s or latest; got %q", name, minimum, value)
	}
	return nil
}

func releaseVersion(version string) (major, minor, patch int, ok bool) {
	match := releaseVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return 0, 0, 0, false
	}
	if _, err := fmt.Sscanf(match[1]+"."+match[2]+"."+match[3], "%d.%d.%d", &major, &minor, &patch); err != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}

func resolvedModuleVersion(policy, minimum string) string {
	if strings.EqualFold(strings.TrimSpace(policy), "latest") {
		return minimum
	}
	return strings.TrimSpace(policy)
}

func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return uniqueSorted(out)
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
