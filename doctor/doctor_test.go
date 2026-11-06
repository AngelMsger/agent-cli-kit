package doctor

import (
	"context"
	"net/url"
	"strings"
	"testing"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

func TestStatus(t *testing.T) {
	t.Parallel()
	loc, _ := url.Parse("https://sso.example.com/login")
	redirect := cerrors.New(cerrors.CategoryServer, "HTTP_FOUND", "server returned HTTP 302").
		WithHTTPStatus(302).WithRedirect(loc, cerrors.RedirectGuide{Setting: "--base-url"})
	noTarget := cerrors.New(cerrors.CategoryServer, "HTTP_NOT_MODIFIED", "server returned HTTP 304").
		WithHTTPStatus(304).WithRedirect(nil, cerrors.RedirectGuide{Setting: "--base-url"})
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, StatusOK},
		{"store", cerrors.New(cerrors.CategoryConfig, "CREDENTIAL_STORE_INACCESSIBLE", "x"), StatusInaccessible},
		{"missing", cerrors.New(cerrors.CategoryConfig, "CREDENTIAL_NOT_VISIBLE_OR_MISSING", "x"), StatusMissingOrInaccessible},
		{"auth", cerrors.New(cerrors.CategoryAuth, "X", "x"), StatusRejectedByServer},
		{"permission", cerrors.New(cerrors.CategoryPermission, "X", "x"), StatusRejectedByServer},
		{"redirect", redirect, StatusRedirected},
		{"3xx without a target", noTarget, StatusUnreachable},
		{"network", cerrors.New(cerrors.CategoryNetwork, "X", "x"), StatusUnreachable},
		{"server", cerrors.New(cerrors.CategoryServer, "X", "x"), StatusUnreachable},
		{"config", cerrors.New(cerrors.CategoryConfig, "X", "x"), StatusInvalid},
		{"plain error", context.Canceled, StatusInvalid},
	}
	for _, tc := range tests {
		if got := Status(tc.err); got != tc.want {
			t.Errorf("%s: Status = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDetail(t *testing.T) {
	t.Parallel()
	if got := Detail(nil); got != "" {
		t.Errorf("Detail(nil) = %q", got)
	}
	plain := cerrors.New(cerrors.CategoryNetwork, "NETWORK", "could not reach the server")
	if got := Detail(plain); got != "could not reach the server" {
		t.Errorf("Detail(network) = %q, want the message only", got)
	}
	loc, _ := url.Parse("https://sso.example.com/login?ticket=secret")
	redirect := cerrors.New(cerrors.CategoryServer, "HTTP_FOUND", "server returned HTTP 302").
		WithHTTPStatus(302).WithRedirect(loc, cerrors.RedirectGuide{Setting: "--base-url"})
	got := Detail(redirect)
	if !strings.HasPrefix(got, "server returned HTTP 302. The server redirected the request to https://sso.example.com:") ||
		!strings.Contains(got, "check --base-url first") || strings.Contains(got, "secret") {
		t.Errorf("Detail(redirect) = %q", got)
	}
}

func TestRecoveryScope(t *testing.T) {
	t.Parallel()
	if got := RecoveryScope(nil); got != "" {
		t.Errorf("RecoveryScope(nil) = %q", got)
	}
	e := cerrors.New(cerrors.CategoryConfig, "X", "x").WithRecovery(cerrors.Recovery{Action: "retry_current_command", Scope: "host"})
	if got := RecoveryScope(e); got != "host" {
		t.Errorf("RecoveryScope = %q, want host", got)
	}
	if got := RecoveryScope(cerrors.New(cerrors.CategoryConfig, "X", "x")); got != "" {
		t.Errorf("RecoveryScope without recovery = %q", got)
	}
}
