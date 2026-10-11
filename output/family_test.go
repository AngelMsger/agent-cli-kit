package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestEmitListEnvelope(t *testing.T) {
	var buf bytes.Buffer
	items := []map[string]any{{"name": "app"}, {"name": "web"}}
	if err := EmitList(items, "", false, Options{Format: FormatJSON, Writer: &buf}); err != nil {
		t.Fatal(err)
	}
	var env struct {
		Items   []map[string]any `json:"items"`
		HasMore bool             `json:"has_more"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not the list envelope: %v\n%s", err, buf.String())
	}
	if len(env.Items) != 2 || env.HasMore {
		t.Errorf("unexpected envelope: %+v", env)
	}
}

func TestFieldProjection(t *testing.T) {
	var buf bytes.Buffer
	v := map[string]any{"name": "app", "secret": "x", "nested": map[string]any{"keep": 1}}
	if err := Emit(v, Options{Format: FormatJSON, Writer: &buf, Fields: []string{"name", "nested.keep"}}); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["secret"]; ok {
		t.Error("projection should have dropped 'secret'")
	}
	if out["name"] != "app" {
		t.Errorf("projection dropped 'name': %+v", out)
	}
	if _, ok := out["nested.keep"]; !ok {
		t.Errorf("projection missing flattened dot-path: %+v", out)
	}
}

func TestNDJSONStreamsRows(t *testing.T) {
	var buf bytes.Buffer
	items := []map[string]any{{"a": 1}, {"a": 2}}
	if err := EmitList(items, "", false, Options{Format: FormatNDJSON, Writer: &buf}); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Count(buf.Bytes(), []byte("\n"))
	if lines != 2 {
		t.Errorf("ndjson should emit one line per row, got %d lines:\n%s", lines, buf.String())
	}
}

func TestBadFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := Emit(map[string]any{}, Options{Format: "yaml", Writer: &buf}); err == nil {
		t.Error("expected error for unknown format")
	}
}

func TestNDJSONPaginationNoticeSurvivesProjectionAndEmptyPages(t *testing.T) {
	for _, items := range [][]map[string]any{nil, {{"name": "rule", "extra": true}}} {
		var data, notices bytes.Buffer
		if err := EmitList(items, "opaque-cursor", true, Options{
			Format: FormatNDJSON, Writer: &data, NoticeWriter: &notices, Fields: []string{"name"},
		}); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data.Bytes(), []byte("opaque-cursor")) || bytes.Contains(data.Bytes(), []byte("extra")) {
			t.Fatalf("metadata contaminated rows: %s", data.String())
		}
		if bytes.Count(data.Bytes(), []byte("\n")) != len(items) {
			t.Fatal("row count changed")
		}
		var notice struct {
			Notice struct {
				Pagination struct {
					Next    string `json:"next"`
					HasMore bool   `json:"has_more"`
				} `json:"pagination"`
			} `json:"_notice"`
		}
		if err := json.Unmarshal(notices.Bytes(), &notice); err != nil {
			t.Fatal(err)
		}
		if notice.Notice.Pagination.Next != "opaque-cursor" || !notice.Notice.Pagination.HasMore {
			t.Fatalf("cursor lost: %s", notices.String())
		}
	}
	var data, notices bytes.Buffer
	if err := EmitList(nil, "", false, Options{Format: FormatNDJSON, Writer: &data, NoticeWriter: &notices}); err != nil {
		t.Fatal(err)
	}
	if notices.Len() != 0 {
		t.Fatal("a complete stream should not emit a continuation notice")
	}
}

func TestPaginationUsesTheCommandsContinuationFlag(t *testing.T) {
	for _, format := range []string{FormatNDJSON, FormatTable} {
		t.Run(format, func(t *testing.T) {
			var data, notices bytes.Buffer
			if err := EmitList([]map[string]any{{"name": "row"}}, "7", true, Options{
				Format: format, Writer: &data, NoticeWriter: &notices, NextFlag: "--offset",
			}); err != nil {
				t.Fatal(err)
			}
			combined := data.String() + notices.String()
			if !strings.Contains(combined, "--offset") || strings.Contains(combined, "--cursor") {
				t.Fatalf("wrong continuation flag: %s", combined)
			}
		})
	}
}

type failingPaginationWriter struct{}

func (failingPaginationWriter) Write([]byte) (int, error) {
	return 0, errors.New("row write failed")
}

func TestFailedNDJSONWriteDoesNotEmitContinuation(t *testing.T) {
	var notices bytes.Buffer
	err := EmitList([]map[string]any{{"name": "row"}}, "next", true, Options{
		Format: FormatNDJSON, Writer: failingPaginationWriter{}, NoticeWriter: &notices,
	})
	if err == nil || notices.Len() != 0 {
		t.Fatalf("err=%v notices=%s", err, notices.String())
	}
}

func TestEmitListNestedFieldsAreItemRelativeAndFlattened(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	items := []sampleRec{mk("1", "Hello", "ENG")}
	if err := EmitList(items, "", false, Options{
		Format: FormatJSON, Fields: []string{"space.key"}, Writer: &buf,
	}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0]["space.key"] != "ENG" {
		t.Fatalf("nested list projection = %v, want literal space.key", got.Items)
	}
	if _, nested := got.Items[0]["space"]; nested {
		t.Fatalf("nested list projection unexpectedly preserved an object: %v", got.Items)
	}
}

func TestNDJSONPaginationNotice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		items []map[string]any
	}{
		{name: "projected rows", items: []map[string]any{{"id": "1", "title": "hidden"}}},
		{name: "empty filtered page"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rows, notices bytes.Buffer
			if err := EmitList(tc.items, "opaque-cursor", true, Options{
				Format: FormatNDJSON, Fields: []string{"id"}, Writer: &rows, NoticeWriter: &notices,
			}); err != nil {
				t.Fatal(err)
			}
			wantRows := ""
			if len(tc.items) > 0 {
				wantRows = "{\"id\":\"1\"}\n"
			}
			if rows.String() != wantRows {
				t.Fatalf("stdout = %q, want only projected rows %q", rows.String(), wantRows)
			}
			var got struct {
				Notice struct {
					Pagination struct {
						Next    string `json:"next"`
						HasMore bool   `json:"has_more"`
					} `json:"pagination"`
					NextSteps []string `json:"next_steps"`
				} `json:"_notice"`
			}
			if err := json.Unmarshal(notices.Bytes(), &got); err != nil {
				t.Fatalf("stderr must contain one JSON notice: %v", err)
			}
			if got.Notice.Pagination.Next != "opaque-cursor" || !got.Notice.Pagination.HasMore {
				t.Fatalf("pagination lost: %s", notices.String())
			}
			if len(got.Notice.NextSteps) != 1 || got.Notice.NextSteps[0] != "Pass next as --cursor to retrieve the next page." {
				t.Fatalf("recovery instructions missing: %s", notices.String())
			}
		})
	}
}

// The notice is one compact line, and an opaque token survives unescaped even
// when the rows are pretty-printed.
func TestNDJSONPaginationNoticeIsOneCompactLine(t *testing.T) {
	t.Parallel()
	var rows, notices bytes.Buffer
	if err := EmitList([]map[string]any{{"id": "1"}}, "opaque<cursor>&start=25", true, Options{
		Format: FormatNDJSON, Writer: &rows, NoticeWriter: &notices, Pretty: true,
	}); err != nil {
		t.Fatal(err)
	}
	want := `{"_notice":{"next_steps":["Pass next as --cursor to retrieve the next page."],` +
		`"pagination":{"has_more":true,"next":"opaque<cursor>&start=25"}}}` + "\n"
	if notices.String() != want {
		t.Fatalf("notice = %q, want %q", notices.String(), want)
	}
}

func TestPaginationNoticeOnlyForIncompleteNDJSON(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		format  string
		items   []map[string]any
		next    string
		hasMore bool
		want    string
	}{
		{name: "final NDJSON page", format: FormatNDJSON, next: "next"},
		{name: "final NDJSON page with rows", format: FormatNDJSON, items: []map[string]any{{"id": "1"}}, next: "next", want: "{\"id\":\"1\"}\n"},
		{name: "unpaginated NDJSON result", format: FormatNDJSON, items: []map[string]any{{"id": "1"}}, want: "{\"id\":\"1\"}\n"},
		{name: "JSON envelope", format: FormatJSON, next: "next", hasMore: true,
			want: "{\n  \"has_more\": true,\n  \"items\": [],\n  \"next\": \"next\"\n}\n"},
		{name: "table footer", format: FormatTable, next: "next", hasMore: true,
			want: "(no results)\n\n(more results — re-run with --cursor next)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rows, notices bytes.Buffer
			if err := EmitList(tc.items, tc.next, tc.hasMore, Options{
				Format: tc.format, Writer: &rows, NoticeWriter: &notices,
			}); err != nil {
				t.Fatal(err)
			}
			if notices.Len() != 0 {
				t.Fatalf("unexpected notice: %s", notices.String())
			}
			if rows.String() != tc.want {
				t.Fatalf("output = %q, want %q", rows.String(), tc.want)
			}
		})
	}
}

type brokenPaginationWriter struct{}

func (brokenPaginationWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestNDJSONOutputFailureDoesNotAdvertiseContinuation(t *testing.T) {
	t.Parallel()
	var notices bytes.Buffer
	err := EmitList([]map[string]any{{"id": "1"}}, "next", true, Options{
		Format: FormatNDJSON, Writer: brokenPaginationWriter{}, NoticeWriter: &notices,
	})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output failure = %v, want closed pipe", err)
	}
	if notices.Len() != 0 {
		t.Fatalf("failed output advertised continuation: %s", notices.String())
	}
}

func TestNDJSONNoticeFailurePreservesSuccessfulRows(t *testing.T) {
	t.Parallel()
	var rows bytes.Buffer
	if err := EmitList([]map[string]any{{"id": "1"}}, "next", true, Options{
		Format: FormatNDJSON, Writer: &rows, NoticeWriter: brokenPaginationWriter{},
	}); err != nil {
		t.Fatal(err)
	}
	if rows.String() != "{\"id\":\"1\"}\n" {
		t.Fatalf("rows = %q", rows.String())
	}
}

func TestPaginationContinuationFlag(t *testing.T) {
	t.Parallel()
	for _, format := range []string{FormatNDJSON, FormatTable} {
		t.Run(format, func(t *testing.T) {
			var rows, notices bytes.Buffer
			if err := EmitList(nil, "25", true, Options{
				Format: format, Writer: &rows, NoticeWriter: &notices, NextFlag: "--offset",
			}); err != nil {
				t.Fatal(err)
			}
			guidance := rows.String() + notices.String()
			if !strings.Contains(guidance, "--offset") || strings.Contains(guidance, "--cursor") {
				t.Fatalf("wrong continuation flag: %s", guidance)
			}
		})
	}
}

func TestNDJSONPagination(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []map[string]any
	}{
		{"projected page", []map[string]any{{"name": "a", "private": "drop"}}},
		{"filtered empty page", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data, notices bytes.Buffer
			err := EmitList(tc.rows, "opaque-next", true, Options{
				Format: FormatNDJSON, Writer: &data, NoticeWriter: &notices, Fields: []string{"name"},
			})
			if err != nil {
				t.Fatal(err)
			}
			wantRows := ""
			if len(tc.rows) > 0 {
				wantRows = "{\"name\":\"a\"}\n"
			}
			if data.String() != wantRows {
				t.Fatalf("stdout = %q, want %q", data.String(), wantRows)
			}
			var notice struct {
				Notice struct {
					Pagination struct {
						Next    string `json:"next"`
						HasMore bool   `json:"has_more"`
					} `json:"pagination"`
					NextSteps []string `json:"next_steps"`
				} `json:"_notice"`
			}
			if err := json.Unmarshal(notices.Bytes(), &notice); err != nil {
				t.Fatalf("invalid notice: %v: %s", err, notices.String())
			}
			if notice.Notice.Pagination.Next != "opaque-next" || !notice.Notice.Pagination.HasMore ||
				len(notice.Notice.NextSteps) != 1 || !strings.Contains(notice.Notice.NextSteps[0], "--cursor") {
				t.Fatalf("missing pagination: %s", notices.String())
			}
		})
	}
}

func TestPaginationCompletedPage(t *testing.T) {
	for _, rows := range [][]map[string]any{nil, {{"name": "done"}}} {
		var data, notices bytes.Buffer
		if err := EmitList(rows, "", false, Options{Format: FormatNDJSON, Writer: &data, NoticeWriter: &notices}); err != nil {
			t.Fatal(err)
		}
		if notices.Len() != 0 {
			t.Fatalf("completed page has a notice: %s", notices.String())
		}
	}
}

type failedPaginationWriter struct{}

func (failedPaginationWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestPaginationWriteFailures(t *testing.T) {
	rows := []map[string]any{{"name": "a"}}
	var notices bytes.Buffer
	err := EmitList(rows, "next", true, Options{Format: FormatNDJSON, Writer: failedPaginationWriter{}, NoticeWriter: &notices})
	if !errors.Is(err, io.ErrClosedPipe) || notices.Len() != 0 {
		t.Fatalf("failed rows must not announce continuation: error=%v notice=%s", err, notices.String())
	}
	var data bytes.Buffer
	err = EmitList(rows, "next", true, Options{Format: FormatNDJSON, Writer: &data, NoticeWriter: failedPaginationWriter{}})
	if err != nil || data.String() != "{\"name\":\"a\"}\n" {
		t.Fatalf("notice failure changed successful rows: error=%v stdout=%s", err, data.String())
	}
}

// The notice is one compact JSON line, so a consumer can split stderr by line,
// and next is passed through byte for byte: no HTML escaping, no colour.
func TestPaginationNoticeIsOneCompactLine(t *testing.T) {
	const next = "a<b>&c=d"
	var data, notices bytes.Buffer
	if err := EmitList([]map[string]any{{"name": "a"}}, next, true, Options{
		Format: FormatNDJSON, Writer: &data, NoticeWriter: &notices, Pretty: true,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(notices.String(), "\n") != 1 || !strings.HasSuffix(notices.String(), "\n") ||
		!strings.Contains(notices.String(), `"next":"`+next+`"`) || strings.Contains(notices.String(), "\x1b") {
		t.Fatalf("notice is not one compact unescaped line: %q", notices.String())
	}
}

// An unpaginated command passes an empty page descriptor. It must not gain a
// continuation notice in any format, and neither must a single-object result.
func TestUnpaginatedResultsEmitNoNotice(t *testing.T) {
	t.Parallel()
	for _, format := range []string{FormatNDJSON, FormatJSON, FormatTable} {
		var data, notices bytes.Buffer
		if err := EmitList([]map[string]any{{"name": "record"}}, "", false, Options{
			Format: format, Writer: &data, NoticeWriter: &notices,
		}); err != nil {
			t.Fatal(err)
		}
		if notices.Len() != 0 || strings.Contains(data.String(), "--cursor") {
			t.Fatalf("%s: unpaginated list advertised a page: data=%q notice=%q", format, data.String(), notices.String())
		}
	}
	var data, notices bytes.Buffer
	if err := Emit(map[string]any{"name": "record"}, Options{
		Format: FormatNDJSON, Writer: &data, NoticeWriter: &notices,
	}); err != nil {
		t.Fatal(err)
	}
	if data.String() != "{\"name\":\"record\"}\n" || notices.Len() != 0 {
		t.Fatalf("single result: data=%q notice=%q", data.String(), notices.String())
	}
}
