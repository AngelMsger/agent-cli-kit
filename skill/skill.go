// Package skill provides read-only companion Skill metadata and state checks.
// Callers supply filesystems, paths and loaded values; this package never reads
// process environment, discovers a home directory, or installs/removes files.
package skill

import (
	"io/fs"
	"strings"
)

// Version preserves the family's simple version-line format, including unknown
// documents. It intentionally does not reinterpret the document as YAML/semver.
func Version(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "version:") {
			version := strings.TrimSpace(strings.TrimPrefix(line, "version:"))
			if version != "" {
				return "v" + version
			}
		}
	}
	return "(unknown)"
}

// VersionsEqual compares the established optional-v version spellings.
func VersionsEqual(left, right string) bool {
	normalize := func(value string) string { return strings.TrimPrefix(strings.TrimSpace(value), "v") }
	return normalize(left) != "" && normalize(left) == normalize(right)
}

// LoadState describes the version reported by an agent context.
type LoadState struct {
	Loaded  bool
	Version string
	Status  string
}

// Loaded interprets an explicit handshake value. The legacy boolean "1" is
// loaded but unknown, even when the embedded version happens to be v1.
func Loaded(value, embedded string) LoadState {
	version := strings.TrimSpace(value)
	if version == "" {
		return LoadState{Status: "not_loaded"}
	}
	status := "outdated"
	if version == "1" {
		status = "unknown"
	} else if VersionsEqual(version, embedded) {
		status = "current"
	}
	return LoadState{Loaded: true, Version: version, Status: status}
}

// Installation is read-only observed state, independent of a CLI output schema.
type Installation struct {
	Status    string
	Version   string
	Alignment string
}

// Inspect reads a Skill document from an explicit filesystem and fs-relative
// path. Read failures retain the existing not_installed classification; this
// observation never authorizes deleting or replacing the target.
func Inspect(fsys fs.FS, path, embedded string) Installation {
	result := Installation{Status: "not_installed"}
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return result
	}
	result.Status = "installed"
	result.Version = Version(data)
	result.Alignment = "outdated"
	if result.Version == "(unknown)" {
		result.Alignment = "unknown"
	} else if VersionsEqual(result.Version, embedded) {
		result.Alignment = "current"
	}
	return result
}
