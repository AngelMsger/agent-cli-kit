package config

import (
	"path/filepath"
	"reflect"
	"testing"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

func TestDeleteContextSaveReload(t *testing.T) {
	useTool(t, nil)
	cases := []struct {
		name, current, remove   string
		names                   []string
		wantCurrent, wantActive string
		wantNames               []string
		wantError               string
	}{
		{name: "same spelling", current: "prod", remove: "prod", names: []string{"prod", "Staging"}, wantCurrent: "Staging", wantActive: "Staging", wantNames: []string{"Staging"}},
		{name: "current spelling differs", current: "PROD", remove: "prod", names: []string{"prod", "Staging"}, wantCurrent: "Staging", wantActive: "Staging", wantNames: []string{"Staging"}},
		{name: "stored spelling differs", current: "prod", remove: "PROD", names: []string{"Prod", "Staging"}, wantCurrent: "Staging", wantActive: "Staging", wantNames: []string{"Staging"}},
		{name: "first remaining context", current: "PROD", remove: "prod", names: []string{"Dev", "prod", "Staging"}, wantCurrent: "Dev", wantActive: "Dev", wantNames: []string{"Dev", "Staging"}},
		{name: "delete noncurrent", current: "PROD", remove: "STAGING", names: []string{"prod", "Staging"}, wantCurrent: "PROD", wantActive: "prod", wantNames: []string{"prod"}},
		{name: "no saved current", remove: "prod", names: []string{"prod", "Staging"}, wantActive: "Staging", wantNames: []string{"Staging"}},
		{name: "last context", current: "PROD", remove: "prod", names: []string{"Prod"}, wantError: "LAST_CONTEXT"},
		{name: "unknown context", current: "PROD", remove: "missing", names: []string{"prod", "Staging"}, wantError: "UNKNOWN_CONTEXT"},
		{name: "empty contexts", remove: "missing", wantError: "UNKNOWN_CONTEXT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			original := toolFile{CurrentContext: tc.current}
			for _, name := range tc.names {
				original.Contexts = append(original.Contexts, toolContext{Name: name, BaseURL: "https://example.test/" + name})
			}
			if err := writeTool(dir, original); err != nil {
				t.Fatal(err)
			}
			file, ok, err := readTool(dir)
			if err != nil || !ok {
				t.Fatalf("read: exists=%v err=%v", ok, err)
			}
			opt := toolLoad{ConfigDir: dir, DotenvPath: filepath.Join(dir, "absent.env")}
			if _, err := loadTool(opt); err != nil {
				t.Fatalf("initial load: %v", err)
			}
			removed, err := DeleteContext(&file, tc.remove)
			if tc.wantError != "" {
				if err == nil || cerrors.AsCLIError(err).Code != tc.wantError {
					t.Fatalf("delete error=%v, want %s", err, tc.wantError)
				}
				if !reflect.DeepEqual(file, original) {
					t.Fatalf("failed deletion changed file: %+v", file)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			target, ok := original.Context(tc.remove)
			if !ok || !reflect.DeepEqual(removed, target) {
				t.Fatalf("removed=%+v, want %+v", removed, target)
			}
			if err := writeTool(dir, file); err != nil {
				t.Fatal(err)
			}
			saved, ok, err := readTool(dir)
			if err != nil || !ok {
				t.Fatalf("read saved: exists=%v err=%v", ok, err)
			}
			if saved.CurrentContext != tc.wantCurrent || !reflect.DeepEqual(saved.ContextNames(), tc.wantNames) {
				t.Fatalf("saved current=%q names=%v, want %q %v", saved.CurrentContext, saved.ContextNames(), tc.wantCurrent, tc.wantNames)
			}
			resolved, err := loadTool(opt)
			if err != nil {
				t.Fatalf("reload after deletion: %v", err)
			}
			if resolved.ActiveContext != tc.wantActive {
				t.Fatalf("active=%q, want %q", resolved.ActiveContext, tc.wantActive)
			}
		})
	}
}
