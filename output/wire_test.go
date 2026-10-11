package output

import (
	"bytes"
	"testing"
)

// TestListWireFormat pins the list envelope on stdout and the pagination
// notice on stderr. Agents across the CLI family page through results by
// reading exactly these keys.
func TestListWireFormat(t *testing.T) {
	items := []map[string]any{{"key": "A-1", "fields": map[string]any{"summary": "first"}}}

	var stdout bytes.Buffer
	if err := EmitList(items, "c2", true, Options{Format: FormatJSON, Writer: &stdout}); err != nil {
		t.Fatal(err)
	}
	const wantJSON = "{\n  \"has_more\": true,\n  \"items\": [\n    {\n      \"fields\": {\n        \"summary\": \"first\"\n      },\n      \"key\": \"A-1\"\n    }\n  ],\n  \"next\": \"c2\"\n}\n"
	if stdout.String() != wantJSON {
		t.Fatalf("list envelope changed:\n got %q\nwant %q", stdout.String(), wantJSON)
	}

	stdout.Reset()
	if err := EmitList(items[:0], "", false, Options{Format: FormatJSON, Writer: &stdout}); err != nil {
		t.Fatal(err)
	}
	const wantEmpty = "{\n  \"has_more\": false,\n  \"items\": []\n}\n"
	if stdout.String() != wantEmpty {
		t.Fatalf("empty list envelope changed:\n got %q\nwant %q", stdout.String(), wantEmpty)
	}

	var lines, notice bytes.Buffer
	if err := EmitList(items, "c2", true, Options{Format: FormatNDJSON, Writer: &lines, NoticeWriter: &notice, NextFlag: "--page-token"}); err != nil {
		t.Fatal(err)
	}
	const wantLines = "{\"fields\":{\"summary\":\"first\"},\"key\":\"A-1\"}\n"
	if lines.String() != wantLines {
		t.Fatalf("ndjson lines changed:\n got %q\nwant %q", lines.String(), wantLines)
	}
	const wantNotice = "{\"_notice\":{\"next_steps\":[\"Pass next as --page-token to retrieve the next page.\"],\"pagination\":{\"has_more\":true,\"next\":\"c2\"}}}\n"
	if notice.String() != wantNotice {
		t.Fatalf("pagination notice changed:\n got %q\nwant %q", notice.String(), wantNotice)
	}
}
