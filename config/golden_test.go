package config

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// The CLI owns its YAML shape; the kit owns reading and writing it. These
// golden files pin the bytes WriteFile produces for the two shapes in the
// family: contexts only, and contexts plus a legacy flat root that is read
// once and rewritten in the contexts form.

// plainShape is a file with contexts and defaults only, and no legacy root.
type plainShape struct {
	CurrentContext string `yaml:"current_context,omitempty"`
	Contexts       []struct {
		Name   string        `yaml:"name"`
		Server string        `yaml:"server,omitempty"`
		Auth   toolAuthShape `yaml:"auth,omitempty"`
	} `yaml:"contexts,omitempty"`
	Defaults struct {
		Format     string `yaml:"format,omitempty"`
		Timeout    string `yaml:"timeout,omitempty"`
		MaxRetries int    `yaml:"max_retries,omitempty"`
		ReadOnly   bool   `yaml:"read_only,omitempty"`
	} `yaml:"defaults,omitempty"`
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("%s changed:\n--- got\n%s--- want\n%s", name, got, want)
	}
}

// written writes shape into a fresh directory, checks the file mode and that
// no temporary file is left behind, and returns the bytes.
func written(t *testing.T, write func(dir string) error) []byte {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := write(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Errorf("directory holds %v, want only config.yaml", entries)
	}
	info, err := os.Stat(FilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %o, want 600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(FilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGoldenContexts(t *testing.T) {
	useTool(t, nil)
	in := toolFile{
		CurrentContext: "prod",
		Contexts: []toolContext{
			{Name: "default", BaseURL: "https://dc.example.com/deploy", Flavor: flavorSelf, Auth: toolAuth{Scheme: "pat"}},
			{Name: "prod", BaseURL: "https://site.cloud.example", Flavor: flavorAuto, DetectedFlavor: flavorCloud,
				Auth: toolAuth{Scheme: "basic", Username: "alice@example.com", CredentialURL: "https://help.example.com/tokens"}},
		},
		Defaults: toolDefaults{Project: "KEY", Format: "table", PageSize: 50, Timeout: 90 * time.Second, MaxRetries: 5, ReadOnly: true},
	}
	golden(t, "contexts.yaml", written(t, func(dir string) error { return writeTool(dir, in) }))

	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("testdata", "contexts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, FilePath(dir), string(raw))
	got, ok, err := readTool(dir)
	if err != nil || !ok || !reflect.DeepEqual(got, in) {
		t.Fatalf("read back %+v, %v, %v; want %+v", got, ok, err, in)
	}
	r, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
	if err != nil {
		t.Fatal(err)
	}
	want := Values{
		KeyServer: "https://site.cloud.example", "flavor": flavorAuto, "detected_flavor": flavorCloud,
		KeyAuthScheme: "basic", "auth.username": "alice@example.com", KeyCredentialURL: "https://help.example.com/tokens",
		"defaults.project": "KEY", "defaults.format": "table", "defaults.page_size": "50", "defaults.timeout": "1m30s",
		"defaults.max_retries": "5", "defaults.read_only": "true",
	}
	if !reflect.DeepEqual(r.Values, want) {
		t.Errorf("Load values:\n got %v\nwant %v", r.Values, want)
	}
	for k := range want {
		if r.Sources[k] != "file" {
			t.Errorf("source of %s = %q, want file", k, r.Sources[k])
		}
	}
}

func TestGoldenMinimal(t *testing.T) {
	useTool(t, nil)
	in := toolFile{CurrentContext: "default", Contexts: []toolContext{{Name: "default"}}}
	golden(t, "minimal.yaml", written(t, func(dir string) error { return writeTool(dir, in) }))
}

func TestGoldenLegacyFlatRewrite(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("testdata", "legacy-flat.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, FilePath(dir), string(raw))
	f, ok, err := readTool(dir)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	golden(t, "legacy-flat-rewritten.yaml", written(t, func(dir string) error { return writeTool(dir, f) }))
}

func TestGoldenPlain(t *testing.T) {
	useTool(t, nil)
	var in plainShape
	in.CurrentContext = "team"
	in.Contexts = append(in.Contexts, struct {
		Name   string        `yaml:"name"`
		Server string        `yaml:"server,omitempty"`
		Auth   toolAuthShape `yaml:"auth,omitempty"`
	}{Name: "team", Server: "http://jobs.example.com:8080", Auth: toolAuthShape{Scheme: "token", Username: "alice"}})
	in.Defaults.Timeout = "45s"
	in.Defaults.ReadOnly = true
	golden(t, "plain.yaml", written(t, func(dir string) error { return WriteFile(dir, &in) }))

	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("testdata", "plain.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, FilePath(dir), string(raw))
	var out plainShape
	if ok, err := ReadFile(dir, &out); err != nil || !ok || !reflect.DeepEqual(out, in) {
		t.Fatalf("read back %+v, %v, %v", out, ok, err)
	}
}
