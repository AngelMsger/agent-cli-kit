package skill

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestVersionContract(t *testing.T) {
	for _, tc := range []struct{ document, want string }{
		{"---\nversion: 2.3.4\n---\n", "v2.3.4"},
		{"version: 2.3.4\r\n", "v2.3.4"},
		{"version: \nversion: 2\n", "v2"},
		{" version: 2\n", "(unknown)"},
		{"version: v2\n", "vv2"}, // Preserve existing parsing, not semver normalization.
		{"# no version", "(unknown)"},
	} {
		if got := Version([]byte(tc.document)); got != tc.want {
			t.Errorf("Version(%q)=%q, want %q", tc.document, got, tc.want)
		}
	}
}

func TestLoadedContract(t *testing.T) {
	for _, tc := range []struct {
		value, embedded string
		want            LoadState
	}{
		{" \n", "v2", LoadState{Status: "not_loaded"}},
		{"1", "v1", LoadState{true, "1", "unknown"}},
		{" 2 ", "v2", LoadState{true, "2", "current"}},
		{"v2", "2", LoadState{true, "v2", "current"}},
		{"v1", "v2", LoadState{true, "v1", "outdated"}},
		{"garbage", "v2", LoadState{true, "garbage", "outdated"}},
	} {
		if got := Loaded(tc.value, tc.embedded); got != tc.want {
			t.Errorf("Loaded(%q,%q)=%+v, want %+v", tc.value, tc.embedded, got, tc.want)
		}
	}
	if VersionsEqual("", "") || VersionsEqual("v", "") {
		t.Fatal("empty versions matched")
	}
}

type deniedFS struct{}

func (deniedFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }

func TestInspectReadOnlyContract(t *testing.T) {
	for _, tc := range []struct {
		name       string
		filesystem fs.FS
		want       Installation
	}{
		{"missing", fstest.MapFS{}, Installation{Status: "not_installed"}},
		{"denied", deniedFS{}, Installation{Status: "not_installed"}},
		{"unknown", fstest.MapFS{"copy/SKILL.md": {Data: []byte("# doc")}}, Installation{Status: "installed", Version: "(unknown)", Alignment: "unknown"}},
		{"current", fstest.MapFS{"copy/SKILL.md": {Data: []byte("version: 2")}}, Installation{Status: "installed", Version: "v2", Alignment: "current"}},
		{"outdated", fstest.MapFS{"copy/SKILL.md": {Data: []byte("version: 1")}}, Installation{Status: "installed", Version: "v1", Alignment: "outdated"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Inspect(tc.filesystem, "copy/SKILL.md", "v2"); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAgentDirectoriesAndDetection(t *testing.T) {
	want := []struct{ id, home, project string }{
		{"claude-code", ".claude", ".claude/skills"}, {"codex", ".codex", ".agents/skills"},
		{"cursor", ".cursor", ".cursor/skills"}, {"agents", ".agents", ".agents/skills"},
		{"gemini", ".gemini", ".gemini/skills"}, {"github-copilot", ".copilot", ".agents/skills"},
		{"opencode", ".config/opencode", ".opencode/skills"}, {"continue", ".continue", ".continue/skills"},
		{"windsurf", ".codeium/windsurf", ".windsurf/skills"}, {"grok", ".grok", ".grok/skills"},
		{"pi", ".pi/agent", ".pi/skills"}, {"kilo", ".kilocode", ".kilocode/skills"}, {"roo", ".roo", ".roo/skills"},
	}
	wantIDs := make([]string, len(want))
	for i, tc := range want {
		wantIDs[i] = tc.id
	}
	if !reflect.DeepEqual(AgentIDs(), wantIDs) {
		t.Fatalf("agent inventory/order changed: got %v, want %v", AgentIDs(), wantIDs)
	}
	for _, tc := range want {
		a, ok := AgentByID(tc.id)
		if !ok {
			t.Fatal(tc.id)
		}
		if got := a.Destination("home", "companion", false); got != filepath.Join("home", tc.home, "skills", "companion") {
			t.Errorf("%s global %s", tc.id, got)
		}
		if got := a.Destination("ignored", "companion", true); got != filepath.Join(tc.project, "companion") {
			t.Errorf("%s project %s", tc.id, got)
		}
	}
	if _, ok := AgentByID("unknown"); ok {
		t.Fatal("unknown agent accepted")
	}
	project := fstest.MapFS{".agents/marker": {}, "AGENTS.md": {}, ".github/marker": {}}
	found := Detect(project, true)
	var ids []string
	for _, a := range found {
		ids = append(ids, a.ID)
	}
	if !reflect.DeepEqual(ids, []string{"codex", "agents", "github-copilot"}) {
		t.Fatalf("discovery=%v", ids)
	}
	// Multiple matching markers find an agent once, while shared destinations
	// retain each agent identity (no install/uninstall dedup policy is introduced).
	if found[0].Destination("", "companion", true) != found[1].Destination("", "companion", true) {
		t.Fatal("shared project path drift")
	}
	if got := Detect(deniedFS{}, false); len(got) != 0 {
		t.Fatalf("denied detection=%v", got)
	}
	global := fstest.MapFS{".pi/marker": {}, ".codeium/windsurf/marker": {}}
	ids = nil
	for _, a := range Detect(global, false) {
		ids = append(ids, a.ID)
	}
	if !reflect.DeepEqual(ids, []string{"windsurf", "pi"}) {
		t.Fatalf("global discovery=%v", ids)
	}
}

func TestAgentMetadataCopiesCannotChangeOtherCallers(t *testing.T) {
	a := Agents()
	a[0].ID = "changed"
	a[0].homeMarkers[0] = "changed"
	b, _ := AgentByID("claude-code")
	b.projectMarks[0] = "changed"
	c := Agents()[0]
	if c.ID != "claude-code" || c.homeMarkers[0] != ".claude" || c.projectMarks[0] != ".claude" {
		t.Fatalf("shared mutable metadata: %+v", c)
	}
}
