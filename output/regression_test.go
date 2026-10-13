package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNestedProjectionAcrossFormats(t *testing.T) {
	item := map[string]any{"id": 7, "author": map[string]any{"name": "Alice"}}
	for _, format := range []string{FormatJSON, FormatNDJSON, FormatTable} {
		for _, shape := range []string{"object", "slice", "list"} {
			t.Run(format+"/"+shape, func(t *testing.T) {
				var buf bytes.Buffer
				opt := Options{Format: format, Writer: &buf, Fields: []string{"id", "author.name", "absent"}}
				var err error
				switch shape {
				case "object":
					err = Emit(item, opt)
				case "slice":
					err = Emit([]any{item}, opt)
				case "list":
					err = EmitList([]any{item}, "", false, opt)
				}
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(buf.String(), "Alice") || !strings.Contains(buf.String(), "7") {
					t.Fatalf("missing projected values: %s", &buf)
				}
				if format != FormatTable && (!strings.Contains(buf.String(), `"author.name"`) || strings.Contains(buf.String(), `"absent"`)) {
					t.Fatalf("projection contract changed: %s", &buf)
				}
			})
		}
	}
}

func TestNumericPrecisionAcrossFormats(t *testing.T) {
	const raw = `{"low":9007199254740991,"edge":9007199254740992,"high":9007199254740993,"min":-9223372036854775808,"max":9223372036854775807,"decimal":0.1234567890123456789,"exponent":1.234567890123456789e+30}`
	var want map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{FormatJSON, FormatNDJSON, FormatTable} {
		for _, project := range []bool{false, true} {
			t.Run(format+map[bool]string{false: "/all", true: "/fields"}[project], func(t *testing.T) {
				var buf bytes.Buffer
				opt := Options{Format: format, Writer: &buf}
				if project {
					opt.Fields = []string{"low", "edge", "high", "min", "max", "decimal", "exponent"}
				}
				if err := Emit(json.RawMessage(raw), opt); err != nil {
					t.Fatal(err)
				}
				for key, value := range want {
					if !strings.Contains(buf.String(), string(value)) {
						t.Errorf("%s lost precision: %s", key, &buf)
					}
				}
			})
		}
	}
	var buf bytes.Buffer
	if err := EmitList([]int64{9007199254740993}, "", false, Options{Writer: &buf}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "9007199254740993") {
		t.Fatalf("typed integer lost precision: %s", &buf)
	}
}

func TestTableLiteralDotKey(t *testing.T) {
	var buf bytes.Buffer
	if err := Emit(map[string]any{"literal.key": "value", " spaced ": "preserved"}, Options{Format: FormatTable, Writer: &buf}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "value") || !strings.Contains(buf.String(), "preserved") {
		t.Fatalf("literal key disappeared: %s", &buf)
	}
}
