package transport

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The --verbose log names each request and its outcome, never a credential:
// no header, no body, and no password from the URL.
func TestVerboseLogNamesTheRequestButNoCredential(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		doer *fakeDoer
		want string
	}{
		{"status", &fakeDoer{statuses: []int{200}}, "GET https://user:xxxxx@example.test/api?q=1 -> 200\n"},
		{"error", &fakeDoer{statuses: []int{0}, netErr: errors.New("refused")}, "GET https://user:xxxxx@example.test/api?q=1 -> error: refused\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log strings.Builder
			c := New(Options{
				Doer:       tc.doer,
				Decorators: []Decorator{func(r *http.Request) { r.Header.Set("Authorization", "Bearer header-secret") }},
				Verbose:    &log,
			})
			req, err := http.NewRequest(http.MethodGet, "https://user:url-secret@example.test/api?q=1", nil)
			if err != nil {
				t.Fatal(err)
			}
			if resp, err := c.Do(context.Background(), req); err == nil {
				resp.Body.Close()
			}
			if got := log.String(); got != tc.want {
				t.Fatalf("verbose log = %q, want %q", got, tc.want)
			}
		})
	}
}
