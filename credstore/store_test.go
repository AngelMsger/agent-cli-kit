package credstore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zalando/go-keyring"
)

const testService = "example-cli"

// stubKeyring records whether it was touched, so a test can prove the store did
// NOT reach the OS keychain.
type stubKeyring struct {
	err      error
	touched  bool
	services map[string]bool
	items    map[string]string
}

func newStub(err error) *stubKeyring {
	return &stubKeyring{err: err, services: map[string]bool{}, items: map[string]string{}}
}

func (s *stubKeyring) Get(service, account string) (string, error) {
	s.touched = true
	s.services[service] = true
	if s.err != nil {
		return "", s.err
	}
	v, ok := s.items[account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (s *stubKeyring) Set(service, account, secret string) error {
	s.touched = true
	s.services[service] = true
	if s.err != nil {
		return s.err
	}
	s.items[account] = secret
	return nil
}

func (s *stubKeyring) Delete(service, account string) error {
	s.touched = true
	s.services[service] = true
	if s.err != nil {
		return s.err
	}
	delete(s.items, account)
	return nil
}

// newTestStore returns a store over stub whose availability probe answers
// available, as NewStore's would on a host with or without a default keychain.
func newTestStore(dir string, stub Backend, available bool) *Store {
	s := NewStoreWithBackend(dir, testService, stub)
	s.probe = func() bool { return available }
	return s
}

func TestSavePrefersTheKeychain(t *testing.T) {
	stub := newStub(nil)
	s := newTestStore(t.TempDir(), stub, true)

	backend, err := s.Save("host:basic", "hunter2")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if backend != BackendKeychain {
		t.Fatalf("Save() backend = %q, want %q", backend, BackendKeychain)
	}
	got, err := s.Load("host:basic")
	if err != nil || got != "hunter2" {
		t.Fatalf("Load() = %q, %v; want the stored secret", got, err)
	}
}

func TestFallsBackToFileWhenTheKeychainErrors(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(dir, newStub(errors.New("keychain is locked")), true)

	backend, err := s.Save("host:token", "tok")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if backend != BackendFile {
		t.Fatalf("Save() backend = %q, want %q", backend, BackendFile)
	}
	got, err := s.Load("host:token")
	if err != nil || got != "tok" {
		t.Fatalf("Load() = %q, %v; want the file-stored secret", got, err)
	}
}

// The point of the whole exercise: with no default keychain, the store must not
// touch the OS keychain at all. On macOS that write is what raises the
// "Keychain Not Found / Reset To Defaults" system dialog and exits 154.
func TestNeverTouchesAnUnavailableKeychain(t *testing.T) {
	stub := newStub(nil)
	s := newTestStore(t.TempDir(), stub, false)

	backend, err := s.Save("host:session", "blob")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if backend != BackendFile {
		t.Fatalf("Save() backend = %q, want %q", backend, BackendFile)
	}
	if _, err := s.Load("host:session"); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := s.Delete("host:session"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if stub.touched {
		t.Fatal("the store reached the OS keychain despite no default keychain being available")
	}
}

func TestMissingSecretIsNotAnAccessError(t *testing.T) {
	s := newTestStore(t.TempDir(), newStub(nil), true)
	if _, err := s.Load("host:basic"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Load() error = %v, want ErrSecretNotFound", err)
	}
}

// A keychain that is present but inaccessible must NOT be reported as "no
// credential configured" — that would send the user off reconfiguring a
// credential that is already there.
func TestInaccessibleKeychainReportsAccessError(t *testing.T) {
	s := newTestStore(t.TempDir(), newStub(errors.New("sandboxed")), true)
	_, err := s.Load("host:basic")
	var access *StoreAccessError
	if !errors.As(err, &access) {
		t.Fatalf("Load() error = %v, want a StoreAccessError", err)
	}
	if access.Backend != BackendKeychain {
		t.Fatalf("access error backend = %q, want %q", access.Backend, BackendKeychain)
	}
}

func TestDeleteRemovesFromBothBackends(t *testing.T) {
	dir := t.TempDir()
	stub := newStub(nil)
	s := newTestStore(dir, stub, true)
	if _, err := s.Save("host:basic", "keychain-copy"); err != nil {
		t.Fatal(err)
	}
	if err := s.fileSave("host:basic", "file-copy"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("host:basic"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := s.Load("host:basic"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Load() after Delete = %v, want ErrSecretNotFound", err)
	}
}

func TestFallbackFileIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows fallback security uses per-user DPAPI, not POSIX mode bits")
	}
	dir := t.TempDir()
	s := newTestStore(dir, newStub(nil), false)
	if _, err := s.Save("host:basic", "hunter2"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credentials file mode = %o, want 600", perm)
	}
}

// TestStoreUsesItsServiceName: each CLI keeps its secrets under its own keychain
// service, so two CLIs on one machine never read each other's entries.
func TestStoreUsesItsServiceName(t *testing.T) {
	stub := newStub(nil)
	s := newTestStore(t.TempDir(), stub, true)
	if _, err := s.Save("host:basic", "hunter2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("host:basic"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("host:basic"); err != nil {
		t.Fatal(err)
	}
	if len(stub.services) != 1 || !stub.services[testService] {
		t.Fatalf("keychain services used = %v, want only %q", stub.services, testService)
	}
}

// NewStore wires the platform probe and runs it at most once per store; a
// store over a caller-supplied backend never probes.
func TestProbeRunsOncePerSystemStoreOnly(t *testing.T) {
	calls := 0
	prev := probeKeychain
	probeKeychain = func() bool { calls++; return false }
	t.Cleanup(func() { probeKeychain = prev })

	s := NewStore(t.TempDir(), testService)
	for i := 0; i < 3; i++ {
		if _, err := s.Save("host:basic", "hunter2"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("probe ran %d times for one store, want 1", calls)
	}

	stub := newStub(nil)
	if _, err := NewStoreWithBackend(t.TempDir(), testService, stub).Save("host:basic", "x"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !stub.touched {
		t.Fatalf("a store over a supplied backend must skip the probe and use the backend (probe calls %d, touched %v)", calls, stub.touched)
	}
}
