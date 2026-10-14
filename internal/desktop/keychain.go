package desktop

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/zalando/go-keyring"
	"yeek/internal/engine"
)

// keychain is the engine's SecretStore: mygo.Secrets, which keeps secrets
// as Yaak's keyring crate does, by service and account in the login
// Keychain on macOS and the Credential Manager on Windows.
//
// Yeek's own key (engine.EncryptionKeyService) was kept with go-keyring
// before, which on macOS stores it through /usr/bin/security, as
// "go-keyring-base64:…" in an item any program can read with that tool,
// and on Windows under "service:account" rather than keytar's
// "service/account". It moves once (migrate), before it is first read.
type keychain struct {
	// moved is the file noting that the key moved.
	moved string
	mu    sync.Mutex
	// legacy is go-keyring's store, which tests replace.
	legacy legacySecrets
	store  func(service string) secretService
}

// legacySecrets is the part of go-keyring the move takes.
type legacySecrets interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

// secretService is the part of mygo.SecretsModule the keychain takes.
type secretService interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
}

type goKeyring struct{}

func (goKeyring) Get(service, account string) (string, error) { return keyring.Get(service, account) }
func (goKeyring) Set(service, account, value string) error {
	return keyring.Set(service, account, value)
}
func (goKeyring) Delete(service, account string) error { return keyring.Delete(service, account) }

// newSecrets makes the engine's SecretStore for a data directory; tests
// replace it, so that they keep secrets in memory.
var newSecrets = func(dataDir string) engine.SecretStore { return newKeychain(dataDir) }

func newKeychain(dataDir string) *keychain {
	return &keychain{
		moved:  filepath.Join(dataDir, "keychain-moved"),
		legacy: goKeyring{},
		store:  func(service string) secretService { return mygo.Secrets.Service(service) },
	}
}

func (k *keychain) Get(service, account string) (string, error) {
	var unread error
	if service == engine.EncryptionKeyService && account == engine.EncryptionKeyAccount {
		// A key that cannot move yet is used where it is, and the move is
		// tried again next time.
		old, err := k.migrate(service, account)
		if err != nil && old != "" {
			return old, nil
		}
		unread = err
	}
	value, err := k.store(service).Get(account)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		if unread != nil {
			// Not "not found": the engine would make a new key in place
			// of the one it could not read, and lose what that encrypted.
			return "", unread
		}
		return "", engine.ErrSecretNotFound
	}
	if err != nil {
		return "", err
	}
	return goKeyringDecode(string(value)), nil
}

// goKeyringDecode is what go-keyring's Get makes of a value it stored on
// macOS, prefixed with how it encoded it; other values are as they are.
func goKeyringDecode(value string) string {
	if rest, ok := strings.CutPrefix(value, "go-keyring-base64:"); ok {
		if b, err := base64.StdEncoding.DecodeString(rest); err == nil {
			return string(b)
		}
	}
	if rest, ok := strings.CutPrefix(value, "go-keyring-encoded:"); ok {
		if b, err := hex.DecodeString(rest); err == nil {
			return string(b)
		}
	}
	return value
}

func (k *keychain) Set(service, account, value string) error {
	return k.store(service).Set(account, []byte(value))
}

// migrate moves Yeek's own secret from go-keyring's store to the system's
// credential store through mygo.Secrets, once. It reads and deletes the
// old item with go-keyring, whose /usr/bin/security the item trusts on
// macOS, so the system asks nothing; the item added anew trusts Yeek
// alone. Should adding it fail, the old item is put back. It returns the
// secret it read from go-keyring's store, if any.
func (k *keychain) migrate(service, account string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, err := os.Stat(k.moved); err == nil {
		return "", nil
	}
	old, err := k.legacy.Get(service, account)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		old = ""
	case err != nil:
		return "", fmt.Errorf("read OS keychain: %w", err)
	default:
		if err := k.legacy.Delete(service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return old, fmt.Errorf("move OS keychain key: %w", err)
		}
		if err := k.store(service).Set(account, []byte(old)); err != nil {
			if back := k.legacy.Set(service, account, old); back != nil {
				return old, fmt.Errorf("move OS keychain key: %w (and putting it back: %v)", err, back)
			}
			return old, fmt.Errorf("move OS keychain key: %w", err)
		}
	}
	return old, os.WriteFile(k.moved, nil, 0o600)
}
