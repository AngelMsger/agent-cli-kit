package credstore

import "errors"

// Outcome is what one backend answered for one account.
type Outcome int

const (
	// NotQueried means the backend was not asked: the keychain already
	// answered, or no lookup was made at all.
	NotQueried Outcome = iota
	// Hit means the backend returned a secret.
	Hit
	// Miss means the backend answered that it holds no secret for the account.
	Miss
	// Failed means the backend could not be read.
	Failed
	// Skipped means the keychain was not attempted because the availability
	// probe ruled it out.
	Skipped
)

// Lookup reports what each backend answered for one account, so a caller can
// tell a secret the keychain itself vouched for from one the file fallback
// supplied while the keychain was failing or skipped. Load reduces it to a
// secret or an error.
type Lookup struct {
	Secret      string
	Keychain    Outcome
	File        Outcome
	KeychainErr error
	FileErr     error
}

// Found reports whether either backend returned a secret.
func (l Lookup) Found() bool { return l.Keychain == Hit || l.File == Hit }

// KeychainAnswered reports whether the keychain itself answered for the
// account, with a secret or with an explicit absence.
func (l Lookup) KeychainAnswered() bool { return l.Keychain == Hit || l.Keychain == Miss }

// Err is the error Load returns for this lookup: nil when a secret was found,
// ErrSecretNotFound when every backend answered that it holds none, and a
// *StoreAccessError otherwise.
func (l Lookup) Err() error {
	if l.Found() {
		return nil
	}
	keyringMissing := l.Keychain == Miss || l.Keychain == Skipped
	fileMissing := l.File == Miss
	switch {
	case keyringMissing && fileMissing:
		return ErrSecretNotFound
	case !keyringMissing && fileMissing:
		return &StoreAccessError{Backend: BackendKeychain, Err: l.KeychainErr}
	case keyringMissing:
		return &StoreAccessError{Backend: BackendFile, Err: l.FileErr}
	default:
		return &StoreAccessError{Backend: "keychain,file", Err: errors.Join(l.KeychainErr, l.FileErr)}
	}
}

// Lookup retrieves the secret for account the way Load does, keychain first
// and then the file, and reports what each backend answered.
func (s *Store) Lookup(account string) Lookup {
	var l Lookup
	if !s.useKeychain() {
		l.Keychain = Skipped
	} else {
		secret, err := s.keyring.Get(s.service, account)
		switch {
		case err == nil:
			l.Secret, l.Keychain = secret, Hit
			return l
		case secretMissing(err):
			l.Keychain = Miss
		default:
			l.Keychain, l.KeychainErr = Failed, err
		}
	}
	// A file fallback may still be usable when the platform keychain is locked
	// or unavailable, so try it before surfacing the keychain failure.
	secret, err := s.fileLoad(account)
	switch {
	case err == nil:
		l.Secret, l.File = secret, Hit
	case errors.Is(err, ErrSecretNotFound):
		l.File = Miss
	default:
		l.File, l.FileErr = Failed, err
	}
	return l
}
