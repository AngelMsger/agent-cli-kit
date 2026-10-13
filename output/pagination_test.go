package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// The pagination contract has one behavior matrix; exact serialization is
// pinned separately in wire_test.go.
func TestListPaginationStates(t *testing.T) {
	t.Parallel()
	for _, state := range []struct {
		name string
		rows bool
		next string
		more bool
	}{
		{"incomplete projected rows", true, "opaque<cursor>&start=25", true},
		{"incomplete filtered empty", false, "opaque<cursor>&start=25", true},
		{"complete rows with cursor", true, "unused", false},
		{"complete empty with cursor", false, "unused", false},
		{"unpaginated rows", true, "", false},
		{"complete empty", false, "", false},
	} {
		for _, format := range []string{FormatNDJSON, FormatJSON, FormatTable} {
			for _, flag := range []string{"", "--offset"} {
				name := state.name + "/" + format + "/" + map[bool]string{true: "default", false: "offset"}[flag == ""]
				t.Run(name, func(t *testing.T) {
					var data, notices bytes.Buffer
					var items []map[string]any
					if state.rows {
						items = []map[string]any{{"name": "record", "private": "drop"}}
					}
					err := EmitList(items, state.next, state.more, Options{Format: format, Writer: &data, NoticeWriter: &notices, Fields: []string{"name"}, NextFlag: flag, Pretty: true})
					if err != nil {
						t.Fatal(err)
					}
					nextFlag := flag
					if nextFlag == "" {
						nextFlag = "--cursor"
					}
					if format == FormatNDJSON && state.more {
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
							t.Fatalf("invalid notice: %v: %s", err, &notices)
						}
						if notice.Notice.Pagination.Next != state.next || !notice.Notice.Pagination.HasMore || len(notice.Notice.NextSteps) != 1 || notice.Notice.NextSteps[0] != "Pass next as "+nextFlag+" to retrieve the next page." {
							t.Fatalf("continuation changed: %s", &notices)
						}
					} else if notices.Len() != 0 {
						t.Fatalf("unexpected pagination notice: %s", &notices)
					}
					switch format {
					case FormatNDJSON:
						want := ""
						if state.rows {
							want = "{\"name\":\"record\"}\n"
						}
						if data.String() != want {
							t.Fatalf("stdout=%q want only rows %q", data.String(), want)
						}
					case FormatTable:
						want := "(no results)\n"
						if state.rows {
							want = "name\n------\nrecord\n"
						}
						if state.more {
							want += fmt.Sprintf("\n(more results — re-run with %s %s)\n", nextFlag, state.next)
						}
						if data.String() != want {
							t.Fatalf("table=%q want %q", data.String(), want)
						}
					case FormatJSON:
						var envelope map[string]json.RawMessage
						if err := json.Unmarshal(data.Bytes(), &envelope); err != nil {
							t.Fatal(err)
						}
						wantItems := "[]"
						if state.rows {
							wantItems = `[{"name":"record"}]`
						}
						var compact bytes.Buffer
						if err := json.Compact(&compact, envelope["items"]); err != nil {
							t.Fatal(err)
						}
						if compact.String() != wantItems || string(envelope["has_more"]) != fmt.Sprint(state.more) {
							t.Fatalf("envelope changed: %s", &data)
						}
						var next string
						if state.next != "" {
							if err := json.Unmarshal(envelope["next"], &next); err != nil {
								t.Fatal(err)
							}
							if next != state.next || len(envelope) != 3 {
								t.Fatalf("cursor envelope changed: %s", &data)
							}
						} else if len(envelope) != 2 {
							t.Fatalf("unpaginated envelope changed: %s", &data)
						}
					}
				})
			}
		}
	}
}

type paginationWriter func([]byte) (int, error)

func (w paginationWriter) Write(p []byte) (int, error) { return w(p) }

func TestListPaginationWriteFailures(t *testing.T) {
	t.Parallel()
	const first = "{\"name\":\"first\"}\n"
	const all = first + "{\"name\":\"second\"}\n"
	for _, tc := range []struct {
		name        string
		failAt      int
		noticeFails bool
	}{
		{"first stdout write", 1, false},
		{"second stdout write", 2, false},
		{"notice write", 0, true},
		{"notice after all rows", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data, notices bytes.Buffer
			writes := 0
			stdout := paginationWriter(func(p []byte) (int, error) {
				writes++
				if writes == tc.failAt {
					return 0, io.ErrClosedPipe
				}
				return data.Write(p)
			})
			stderr := paginationWriter(func(p []byte) (int, error) {
				if data.String() != all {
					t.Errorf("notice preceded completion of stdout: %q", data.String())
				}
				if tc.noticeFails {
					return 0, io.ErrClosedPipe
				}
				return notices.Write(p)
			})
			err := EmitList([]map[string]any{{"name": "first"}, {"name": "second"}}, "next", true, Options{Format: FormatNDJSON, Writer: stdout, NoticeWriter: stderr})
			if tc.failAt > 0 {
				wantRows := ""
				if tc.failAt == 2 {
					wantRows = first
				}
				if !errors.Is(err, io.ErrClosedPipe) || notices.Len() != 0 || data.String() != wantRows {
					t.Fatalf("failed stdout: err=%v stdout=%q notice=%q", err, data.String(), notices.String())
				}
			} else {
				if err != nil || data.String() != all {
					t.Fatalf("notice changed successful rows: err=%v stdout=%q", err, data.String())
				}
				if !tc.noticeFails && notices.Len() == 0 {
					t.Fatal("successful incomplete page omitted notice")
				}
			}
		})
	}
}

func TestSingleResultHasNoPagination(t *testing.T) {
	t.Parallel()
	var data, notices bytes.Buffer
	if err := Emit(map[string]any{"name": "record"}, Options{Format: FormatNDJSON, Writer: &data, NoticeWriter: &notices}); err != nil {
		t.Fatal(err)
	}
	if data.String() != "{\"name\":\"record\"}\n" || notices.Len() != 0 || strings.Contains(data.String(), "--cursor") {
		t.Fatalf("single result: stdout=%q notice=%q", data.String(), notices.String())
	}
}
