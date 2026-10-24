package credstore

import (
	"errors"
	"testing"
)

func TestLookupReportsEachBackend(t *testing.T) {
	cases := []struct {
		name               string
		keychainErr        error
		keychain, file     bool
		probe              bool
		wantKeychain       Outcome
		wantFile           Outcome
		wantFound, wantErr bool
	}{
		{name: "keychain hit", keychain: true, probe: true, wantKeychain: Hit, wantFile: NotQueried, wantFound: true},
		{name: "keychain miss, file hit", file: true, probe: true, wantKeychain: Miss, wantFile: Hit, wantFound: true},
		{name: "both miss", probe: true, wantKeychain: Miss, wantFile: Miss, wantErr: true},
		{name: "keychain error, file hit", keychainErr: errors.New("locked"), file: true, probe: true, wantKeychain: Failed, wantFile: Hit, wantFound: true},
		{name: "keychain error, file miss", keychainErr: errors.New("locked"), probe: true, wantKeychain: Failed, wantFile: Miss, wantErr: true},
		{name: "skipped, file hit", file: true, wantKeychain: Skipped, wantFile: Hit, wantFound: true},
		{name: "skipped, file miss", wantKeychain: Skipped, wantFile: Miss, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			stub := newStub(nil)
			if tc.keychain {
				stub.items["k"] = "from-keychain"
			}
			if tc.file {
				if err := newTestStore(dir, newStub(errors.New("down")), false).fileSave("k", "from-file"); err != nil {
					t.Fatal(err)
				}
			}
			stub.err = tc.keychainErr
			s := newTestStore(dir, stub, tc.probe)
			l := s.Lookup("k")
			if l.Keychain != tc.wantKeychain || l.File != tc.wantFile || l.Found() != tc.wantFound || (l.Err() != nil) != tc.wantErr {
				t.Fatalf("lookup = %+v, err %v", l, l.Err())
			}
			if tc.keychainErr != nil && !errors.Is(l.KeychainErr, tc.keychainErr) {
				t.Fatalf("keychain error not reported: %+v", l)
			}
			secret, err := s.Load("k")
			if secret != l.Secret || (err == nil) != (l.Err() == nil) {
				t.Fatalf("Load = %q, %v; Lookup = %+v", secret, err, l)
			}
		})
	}
}
