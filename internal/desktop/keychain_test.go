package desktop

import (
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/egoist/mygo"
	"github.com/zalando/go-keyring"
	"yeek/internal/engine"
)

func TestMain(m *testing.M) {
	newSecrets = func(string) engine.SecretStore { return &memorySecrets{} }
	os.Exit(m.Run())
}

type memorySecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memorySecrets) Get(service, account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[service+"\x00"+account]
	if !ok {
		return "", engine.ErrSecretNotFound
	}
	return v, nil
}

func (s *memorySecrets) Set(service, account, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[service+"\x00"+account] = value
	return nil
}

type fakeLegacy struct {
	m      map[string]string
	getErr error
	gets   int
}

func (f *fakeLegacy) Get(service, account string) (string, error) {
	f.gets++
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.m[service+"\x00"+account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeLegacy) Set(service, account, value string) error {
	f.m[service+"\x00"+account] = value
	return nil
}

func (f *fakeLegacy) Delete(service, account string) error {
	if _, ok := f.m[service+"\x00"+account]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.m, service+"\x00"+account)
	return nil
}

type fakeStore struct {
	m      map[string][]byte
	setErr error
}

type fakeService struct {
	s       *fakeStore
	service string
}

func (f fakeService) Get(key string) ([]byte, error) {
	v, ok := f.s.m[f.service+"\x00"+key]
	if !ok {
		return nil, mygo.ErrSecretNotFound
	}
	return v, nil
}

func (f fakeService) Set(key string, value []byte) error {
	if f.s.setErr != nil {
		return f.s.setErr
	}
	f.s.m[f.service+"\x00"+key] = value
	return nil
}

func testKeychain(t *testing.T, legacy *fakeLegacy, store *fakeStore) *keychain {
	k := newKeychain(t.TempDir())
	k.legacy = legacy
	k.store = func(service string) secretService { return fakeService{store, service} }
	return k
}

const ownKey = engine.EncryptionKeyService + "\x00" + engine.EncryptionKeyAccount

func TestKeychainMovesOwnKeyOnce(t *testing.T) {
	legacy := &fakeLegacy{m: map[string]string{ownKey: "YKM_KEY"}}
	store := &fakeStore{m: map[string][]byte{}}
	k := testKeychain(t, legacy, store)
	for range 2 {
		v, err := k.Get(engine.EncryptionKeyService, engine.EncryptionKeyAccount)
		if err != nil || v != "YKM_KEY" {
			t.Fatal(v, err)
		}
	}
	if len(legacy.m) != 0 || string(store.m[ownKey]) != "YKM_KEY" || legacy.gets != 1 {
		t.Fatal(legacy.m, store.m, legacy.gets)
	}
}

func TestKeychainNoKeyIsNotFound(t *testing.T) {
	k := testKeychain(t, &fakeLegacy{m: map[string]string{}}, &fakeStore{m: map[string][]byte{}})
	if _, err := k.Get(engine.EncryptionKeyService, engine.EncryptionKeyAccount); !errors.Is(err, engine.ErrSecretNotFound) {
		t.Fatal(err)
	}
}

func TestKeychainKeepsKeyWhenMoveFails(t *testing.T) {
	legacy := &fakeLegacy{m: map[string]string{ownKey: "YKM_KEY"}}
	store := &fakeStore{m: map[string][]byte{}, setErr: errors.New("keychain locked")}
	k := testKeychain(t, legacy, store)
	v, err := k.Get(engine.EncryptionKeyService, engine.EncryptionKeyAccount)
	if err != nil || v != "YKM_KEY" || legacy.m[ownKey] != "YKM_KEY" {
		t.Fatal(v, err, legacy.m)
	}
	store.setErr = nil
	if v, err := k.Get(engine.EncryptionKeyService, engine.EncryptionKeyAccount); err != nil || v != "YKM_KEY" || len(legacy.m) != 0 {
		t.Fatal(v, err, legacy.m)
	}
}

func TestKeychainUnreadableOldKeyIsAnError(t *testing.T) {
	legacy := &fakeLegacy{m: map[string]string{}, getErr: errors.New("user canceled")}
	store := &fakeStore{m: map[string][]byte{}}
	k := testKeychain(t, legacy, store)
	if _, err := k.Get(engine.EncryptionKeyService, engine.EncryptionKeyAccount); err == nil || errors.Is(err, engine.ErrSecretNotFound) {
		t.Fatal(err)
	}
	store.m[ownKey] = []byte("YKM_NEW")
	if v, err := k.Get(engine.EncryptionKeyService, engine.EncryptionKeyAccount); err != nil || v != "YKM_NEW" {
		t.Fatal(v, err)
	}
}

func TestKeychainReadsOthersInPlace(t *testing.T) {
	legacy := &fakeLegacy{m: map[string]string{"other\x00me": "old"}}
	store := &fakeStore{m: map[string][]byte{"api\x00token": []byte("go-keyring-base64:c2VjcmV0"), "hex\x00token": []byte("go-keyring-encoded:736563726574")}}
	k := testKeychain(t, legacy, store)
	if _, err := k.Get("other", "me"); !errors.Is(err, engine.ErrSecretNotFound) || legacy.m["other\x00me"] != "old" || legacy.gets != 0 {
		t.Fatal(err, legacy.m, legacy.gets)
	}
	for _, key := range []string{"api", "hex"} {
		if v, err := k.Get(key, "token"); err != nil || v != "secret" {
			t.Fatal(key, v, err)
		}
	}
}
