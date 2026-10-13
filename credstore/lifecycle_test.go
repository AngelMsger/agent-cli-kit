package credstore

import (
	"encoding/json"
	"errors"
	cerrors "github.com/angelmsger/agent-cli-kit/errors"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSaveRefusesAmbiguousFallback(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-readable", true: "temporarily-unreadable"}[unreadable], func(t *testing.T) {
			dir := t.TempDir()
			stub := newStub(nil)
			stub.items["account"] = "old-secret"
			stub.setErr = errors.New("new-secret write failed")
			if unreadable {
				stub.getErr = errors.New("old-secret read failed")
			}
			s := newTestStore(dir, stub, true)
			backend, err := s.Save("account", "new-secret")
			var access *StoreAccessError
			if backend != "" || !errors.As(err, &access) {
				t.Fatalf("save=(%q,%v), want explicit failure", backend, err)
			}
			if strings.Contains(err.Error(), "old-secret") || strings.Contains(err.Error(), "new-secret") || !strings.Contains(err.Error(), "host user environment") {
				t.Fatalf("unsafe or unactionable error: %v", err)
			}
			if _, err := os.Stat(s.credentialsPath()); !os.IsNotExist(err) {
				t.Fatalf("fallback was changed: %v", err)
			}
			stub.getErr = nil // A fresh process can read the recovered backend.
			fresh := newTestStore(dir, stub, true)
			got, err := fresh.Load("account")
			if err != nil || got != "old-secret" {
				t.Fatal("failed save changed the prior credential")
			}
		})
	}
}

func TestConfirmedMissingFallbackSurvivesFreshStore(t *testing.T) {
	stub := newStub(nil)
	stub.setErr = errors.New("write denied")
	dir := t.TempDir()
	s := newTestStore(dir, stub, true)
	if backend, err := s.Save("account", "new-secret"); err != nil || backend != BackendFile {
		t.Fatalf("save=%s,%v", backend, err)
	}
	got, err := newTestStore(dir, stub, true).Load("account")
	if err != nil || got != "new-secret" {
		t.Fatal("fresh store did not resolve the saved fallback")
	}
}

func TestDeleteReportsKeychainFailureAndStillClearsFile(t *testing.T) {
	stub := newStub(nil)
	stub.items["account"] = "secret"
	stub.deleteErr = errors.New("secret delete denied")
	s := newTestStore(t.TempDir(), stub, true)
	if err := s.fileSave("account", "file-secret"); err != nil {
		t.Fatal(err)
	}
	err := s.Delete("account")
	var access *StoreAccessError
	if !errors.As(err, &access) || access.Backend != BackendKeychain {
		t.Fatalf("want keychain failure, got %v", err)
	}
	if strings.Contains(err.Error(), "secret delete denied") {
		t.Fatalf("backend data exposed: %v", err)
	}
	if _, err := s.fileLoad("account"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("file not cleared: %v", err)
	}
	if _, err := s.Load("account"); err != nil {
		t.Fatal("test must leave undeleted keychain credential readable")
	}
}

func TestDeleteReportsFileFailureAndStillClearsKeychain(t *testing.T) {
	stub := newStub(nil)
	stub.items["account"] = "secret"
	s := newTestStore(t.TempDir(), stub, true)
	// A directory at the credential-file path is a deterministic read failure
	// on all platforms, without depending on permissions or root privileges.
	if err := os.Mkdir(s.credentialsPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("account"); err == nil {
		t.Fatal("file failure was hidden")
	}
	if _, exists := stub.items["account"]; exists {
		t.Fatal("keychain cleanup was skipped")
	}
}

func TestDeleteMissingCredentialsIsIdempotent(t *testing.T) {
	stub := newStub(nil)
	stub.deleteErr = keyring.ErrNotFound
	s := newTestStore(t.TempDir(), stub, true)
	for i := 0; i < 2; i++ {
		if err := s.Delete("account"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKeychainSaveRemovesOnlyItsOlderFallback(t *testing.T) {
	stub := newStub(nil)
	dir := t.TempDir()
	s := newTestStore(dir, stub, true)
	for account, secret := range map[string]string{"account": "old-secret", "other": "other-secret"} {
		if err := s.fileSave(account, secret); err != nil {
			t.Fatal(err)
		}
	}
	if backend, err := s.Save("account", "new-secret"); backend != BackendKeychain || err != nil {
		t.Fatalf("save=%s,%v", backend, err)
	}
	fresh := newTestStore(dir, stub, true)
	if got, err := fresh.Load("account"); err != nil || got != "new-secret" {
		t.Fatal("fresh store did not load the saved keychain value")
	}
	if got, err := fresh.fileLoad("other"); err != nil || got != "other-secret" {
		t.Fatal("unrelated account changed")
	}
	stub.getErr = errors.New("keychain unavailable")
	if _, err := fresh.Load("account"); err == nil {
		t.Fatal("old fallback resurfaced during outage")
	}
}

func TestKeychainSaveReportsPartialPersistence(t *testing.T) {
	stub := newStub(nil)
	s := newTestStore(t.TempDir(), stub, true)
	if err := os.Mkdir(s.credentialsPath(), 0700); err != nil {
		t.Fatal(err)
	}
	backend, err := s.Save("account", "new-secret")
	var partial *PartialSaveError
	if backend != BackendKeychain || !errors.As(err, &partial) || partial.Backend != BackendKeychain {
		t.Fatalf("want partial keychain persistence, got %s,%v", backend, err)
	}
	if stub.items["account"] != "new-secret" {
		t.Fatal("expected credential already saved")
	}
	if strings.Contains(err.Error(), "new-secret") || !strings.Contains(err.Error(), "restore configuration-directory access") {
		t.Fatalf("unsafe or unactionable partial error: %v", err)
	}
}

func TestDeletePreservesBothFailures(t *testing.T) {
	stub := newStub(nil)
	stub.deleteErr = errors.New("keychain denied")
	s := newTestStore(t.TempDir(), stub, true)
	if err := os.Mkdir(s.credentialsPath(), 0700); err != nil {
		t.Fatal(err)
	}
	err := s.Delete("account")
	var access *StoreAccessError
	var fileErr *os.PathError
	if !errors.As(err, &access) || !errors.As(err, &fileErr) {
		t.Fatalf("lost a cleanup failure: %v", err)
	}
}

func TestMutationErrorsPreserveCausesWithoutExposingSecrets(t *testing.T) {
	setErr := errors.New("set contains secret-sentinel")
	getErr := &os.PathError{Op: "read", Path: "secret-sentinel", Err: os.ErrPermission}
	deleteErr := errors.New("delete contains secret-sentinel")
	stub := newStub(nil)
	stub.setErr, stub.getErr, stub.deleteErr = setErr, getErr, deleteErr
	s := newTestStore(t.TempDir(), stub, true)
	_, saveErr := s.Save("account", "secret-sentinel")
	if !errors.Is(saveErr, setErr) || !errors.Is(saveErr, getErr) {
		t.Fatalf("save lost causes: %v", saveErr)
	}
	var pathErr *os.PathError
	if !errors.As(saveErr, &pathErr) || pathErr != getErr {
		t.Fatal("save lost typed read cause")
	}
	removedErr := s.Delete("account")
	if !errors.Is(removedErr, deleteErr) {
		t.Fatalf("delete lost cause: %v", removedErr)
	}
	_, loadErr := s.Load("account")
	if !errors.Is(loadErr, getErr) {
		t.Fatal("load lost cause")
	}
	for _, err := range []error{saveErr, removedErr, loadErr} {
		if strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatal("credential material leaked through Error")
		}
		payload, marshalErr := json.Marshal(cerrors.AsCLIError(err).Payload())
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(payload), "secret-sentinel") {
			t.Fatal("credential material leaked through CLI payload")
		}
	}
}
