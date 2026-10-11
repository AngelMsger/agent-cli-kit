package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is a parsed config file: named contexts, the shared defaults and the
// name of the current context. C is the CLI's context struct, which tags its
// name `config:"name"`; D is its defaults struct.
type File[C, D any] struct {
	CurrentContext string
	Contexts       []C
	Defaults       D
}

func nameOf[C any](c C) string { return get(reflect.ValueOf(&c).Elem(), KeyName) }

// Context returns the context whose name matches, ignoring case. The result
// carries the stored name, which is the one to persist in any reference.
func (f File[C, D]) Context(name string) (C, bool) {
	for _, c := range f.Contexts {
		if strings.EqualFold(nameOf(c), name) {
			return c, true
		}
	}
	var zero C
	return zero, false
}

// ContextNames returns every context name, in file order.
func (f File[C, D]) ContextNames() []string {
	names := make([]string, len(f.Contexts))
	for i, c := range f.Contexts {
		names[i] = nameOf(c)
	}
	return names
}

// Upsert replaces the context with the same name, ignoring case, in place, or
// appends c.
func (f *File[C, D]) Upsert(c C) {
	name := nameOf(c)
	for i, existing := range f.Contexts {
		if strings.EqualFold(nameOf(existing), name) {
			f.Contexts[i] = c
			return
		}
	}
	f.Contexts = append(f.Contexts, c)
}

// Remove deletes the context with the given name, ignoring case, and reports
// whether one was removed. It leaves CurrentContext alone.
func (f *File[C, D]) Remove(name string) bool {
	for i, c := range f.Contexts {
		if strings.EqualFold(nameOf(c), name) {
			f.Contexts = append(f.Contexts[:i], f.Contexts[i+1:]...)
			return true
		}
	}
	return false
}

// DefaultDir returns ~/<Paths.Dir>.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, schema().Paths.Dir), nil
}

// LegacyDir returns ~/<Paths.Legacy>, or "" when no legacy directory is
// registered.
func LegacyDir() (string, error) {
	legacy := schema().Paths.Legacy
	if legacy == "" {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, legacy), nil
}

// ResolveDir picks the directory to use when the user named none: the default
// directory, unless it has no config file and the legacy directory has one.
func ResolveDir() (string, error) {
	dir, err := DefaultDir()
	if err != nil || schema().Paths.Legacy == "" {
		return dir, err
	}
	if _, err := os.Stat(FilePath(dir)); err == nil {
		return dir, nil
	}
	legacy, err := LegacyDir()
	if err != nil {
		return dir, nil
	}
	if _, err := os.Stat(FilePath(legacy)); err == nil {
		return legacy, nil
	}
	return dir, nil
}

// FilePath returns the config file's path inside dir.
func FilePath(dir string) string { return filepath.Join(dir, schema().Paths.File) }

// ReadFile parses the config file in dir into shape, the CLI's YAML document
// type. It reports false, with no error, when the file does not exist.
func ReadFile(dir string, shape any) (bool, error) {
	raw, err := os.ReadFile(FilePath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := yaml.Unmarshal(raw, shape); err != nil {
		return false, err
	}
	return true, nil
}

// WriteFile writes shape as the config file in dir. It creates dir with mode
// 0700, writes a 0600 temporary file beside the target and renames it into
// place, so a failed write never truncates the existing file. Secrets never
// belong in shape.
func WriteFile(dir string, shape any) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	out, err := yaml.Marshal(shape)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), FilePath(dir))
}
