package roost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type CheckStatus string

const (
	StatusOK   CheckStatus = "ok"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
)

type CheckItem struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
}

type DoctorReport struct {
	Items []CheckItem `json:"items"`
}

func Doctor(root string, strict, jsonOutput bool, stdout io.Writer) error {
	m, err := LoadManifest(root)
	if err != nil {
		return err
	}
	report := DoctorReport{Items: []CheckItem{{Name: "manifest", Status: StatusOK, Detail: ManifestName}}}
	for _, command := range []string{"go", "git"} {
		if _, err := exec.LookPath(command); err != nil {
			report.Items = append(report.Items, CheckItem{Name: command, Status: StatusFail, Detail: err.Error()})
		} else {
			report.Items = append(report.Items, CheckItem{Name: command, Status: StatusOK, Detail: "available"})
		}
	}
	for _, service := range sortedServiceNames(m) {
		path := filepath.Join(root, "configs", "service", "config."+service+".yaml")
		if err := CheckConfig(path, false); err != nil {
			report.Items = append(report.Items, CheckItem{Name: "config:" + service, Status: StatusFail, Detail: err.Error()})
		} else {
			report.Items = append(report.Items, CheckItem{Name: "config:" + service, Status: StatusOK, Detail: filepath.ToSlash(path)})
		}
	}
	if err := CheckIDs(root, m); err != nil {
		report.Items = append(report.Items, CheckItem{Name: "ids", Status: StatusFail, Detail: err.Error()})
	} else {
		report.Items = append(report.Items, CheckItem{Name: "ids", Status: StatusOK, Detail: "no conflicts"})
	}
	if strict {
		if err := Generate(root, GenerateOptions{Check: true, Stdout: io.Discard}); err != nil {
			report.Items = append(report.Items, CheckItem{Name: "generated", Status: StatusFail, Detail: err.Error()})
		} else {
			report.Items = append(report.Items, CheckItem{Name: "generated", Status: StatusOK, Detail: "up to date"})
		}
	}
	if jsonOutput {
		raw, _ := json.MarshalIndent(report, "", "  ")
		_, _ = fmt.Fprintln(stdout, string(raw))
	} else {
		for _, item := range report.Items {
			fmt.Fprintf(stdout, "%-5s %-20s %s\n", strings.ToUpper(string(item.Status)), item.Name, item.Detail)
		}
	}
	var failed []error
	for _, item := range report.Items {
		if item.Status == StatusFail {
			failed = append(failed, errors.New(item.Name+": "+item.Detail))
		}
	}
	return errors.Join(failed...)
}

func CheckConfig(path string, production bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var value map[string]any
	if err := yaml.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("invalid yaml: %w", err)
	}
	if value["sid"] == nil {
		return errors.New("sid is required")
	}
	if production {
		text := strings.ToLower(string(raw))
		for _, forbidden := range []string{"change_me", "127.0.0.1", "localhost", "dev-"} {
			if strings.Contains(text, forbidden) {
				return fmt.Errorf("production config contains forbidden value %q", forbidden)
			}
		}
	}
	return nil
}

func DiffProject(root string, stdout io.Writer) error {
	m, err := LoadManifest(root)
	if err != nil {
		return err
	}
	return diffManifest(root, m, false, stdout)
}

func diffManifest(root string, m Manifest, includeManifest bool, stdout io.Writer) error {
	plan, err := renderProject(m)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(plan))
	for path := range plan {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	changed := make([]string, 0, len(paths)+1)
	if includeManifest {
		prospective, marshalErr := m.Marshal()
		if marshalErr != nil {
			return marshalErr
		}
		current, readErr := os.ReadFile(filepath.Join(root, ManifestName))
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if readErr != nil || !bytes.Equal(current, prospective) {
			changed = append(changed, ManifestName)
		}
	}
	for _, path := range paths {
		file := plan[path]
		current, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr == nil && !file.Owned {
			continue
		}
		if readErr == nil && file.Owned && !canOverwriteGenerated(path, current) {
			return fmt.Errorf("refusing to overwrite non-generated file %s", path)
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if readErr != nil || string(current) != string(file.Body) {
			changed = append(changed, path)
		}
	}
	for _, path := range changed {
		fmt.Fprintln(stdout, path)
	}
	fmt.Fprintf(stdout, "summary: %d file(s) would change\n", len(changed))
	return nil
}
