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

type keychain struct {
	moved  string
	mu     sync.Mutex
	legacy legacySecrets
	store  func(service string) secretService
}

type legacySecrets interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

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
		old, err := k.migrate(service, account)
		if err != nil && old != "" {
			return old, nil
		}
		unread = err
	}
	value, err := k.store(service).Get(account)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		if unread != nil {
			return "", unread
		}
		return "", engine.ErrSecretNotFound
	}
	if err != nil {
		return "", err
	}
	return goKeyringDecode(string(value)), nil
}

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
