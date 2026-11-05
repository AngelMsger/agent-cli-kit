package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The --verbose log names each request and its outcome, with no header, no
// body and no password from the URL.
func TestVerboseLogNamesTheRequestButNoPassword(t *testing.T) {
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

// A redirect the default Doer refuses is the answer, so --verbose logs its
// status and the reason, without the redirect's own URL and its query.
func TestVerboseLogShowsARefusedRedirectsStatus(t *testing.T) {
	t.Parallel()
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the refused target must not be reached")
	}))
	defer dest.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/sso?ticket=sign-in-secret", http.StatusFound)
	}))
	defer src.Close()

	var log strings.Builder
	c := New(Options{SameOriginRedirects: true, Verbose: &log})
	req, _ := http.NewRequest(http.MethodGet, src.URL+"/api", nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	got := log.String()
	want := "GET " + src.URL + "/api -> 302 (redirect rejected: refusing cross-origin redirect from "
	if !strings.HasPrefix(got, want) || strings.Contains(got, "sign-in-secret") || strings.Contains(got, "error") {
		t.Fatalf("verbose log = %q, want a line starting %q without the ticket", got, want)
	}
}
