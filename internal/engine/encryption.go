package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/chacha20poly1305"
)

const encryptionTag = "yA4k3nC\x01"

var keyEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

type secretStore interface {
	Get(service, user string) (string, error)
	Set(service, user, value string) error
}
type systemSecrets struct{}

func (systemSecrets) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (systemSecrets) Set(service, user, value string) error    { return keyring.Set(service, user, value) }
func encryptBytes(key, plain []byte) ([]byte, error) {
	cipher, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, cipher.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	result := append([]byte(encryptionTag), nonce...)
	return cipher.Seal(result, nonce, plain, nil), nil
}
func decryptBytes(key, data []byte) ([]byte, error) {
	cipher, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	prefix := len(encryptionTag)
	if len(data) < prefix+cipher.NonceSize()+cipher.Overhead() || !bytes.Equal(data[:prefix], []byte(encryptionTag)) {
		return nil, errors.New("invalid encrypted value")
	}
	return cipher.Open(nil, data[prefix:prefix+cipher.NonceSize()], data[prefix+cipher.NonceSize():], nil)
}
func (e *Engine) masterEncryptionKey() ([]byte, error) {
	e.keyMu.Lock()
	defer e.keyMu.Unlock()
	if e.masterKey != nil {
		return bytes.Clone(e.masterKey), nil
	}
	if e.secrets == nil {
		e.secrets = systemSecrets{}
	}
	const service = "app.yeek.desktop.EncryptionKey"
	value, err := e.secrets.Get(service, "encryption-key")
	var key []byte
	if errors.Is(err, keyring.ErrNotFound) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if err = e.secrets.Set(service, "encryption-key", "YKM_"+keyEncoding.EncodeToString(key)); err != nil {
			return nil, fmt.Errorf("save key in OS keychain: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read OS keychain: %w", err)
	} else {
		key, err = keyEncoding.DecodeString(strings.TrimPrefix(value, "YKM_"))
		if err != nil || len(key) != 32 {
			return nil, errors.New("invalid keychain encryption key")
		}
	}
	e.masterKey = bytes.Clone(key)
	return key, nil
}
func (e *Engine) workspaceKey(ctx context.Context, workspace string) ([]byte, error) {
	metas, err := e.Store.List(ctx, "workspace_meta", workspace)
	if err != nil {
		return nil, err
	}
	if len(metas) == 0 || str(obj(metas[0], "encryptionKey"), "encryptedKey") == "" {
		return nil, errors.New("set up this workspace’s encryption key first")
	}
	master, err := e.masterEncryptionKey()
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(str(obj(metas[0], "encryptionKey"), "encryptedKey"))
	if err != nil {
		return nil, err
	}
	return decryptBytes(master, data)
}
func humanKey(key []byte) string {
	encoded := "YK" + keyEncoding.EncodeToString(key)
	parts := []string{}
	for len(encoded) > 0 {
		n := min(6, len(encoded))
		parts = append(parts, encoded[:n])
		encoded = encoded[n:]
	}
	return strings.Join(parts, "-")
}
func parseHumanKey(value string) ([]byte, error) {
	value = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	value = strings.TrimPrefix(value, "YK")
	value = strings.NewReplacer("O", "0", "I", "1", "L", "1").Replace(value)
	key, err := keyEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid workspace encryption key")
	}
	return key, nil
}
func (e *Engine) SetWorkspaceKey(ctx context.Context, workspace, human string) error {
	key, err := parseHumanKey(human)
	if err != nil {
		return err
	}
	return e.setWorkspaceKey(ctx, workspace, key)
}
func (e *Engine) setWorkspaceKey(ctx context.Context, workspace string, key []byte) error {
	w, err := e.Store.Get(ctx, workspace)
	if err != nil {
		return err
	}
	if challenge := str(w, "encryptionKeyChallenge"); challenge != "" {
		data, err := base64.StdEncoding.DecodeString(challenge)
		if err != nil {
			return err
		}
		if _, err = decryptBytes(key, data); err != nil {
			return errors.New("the encryption key does not match this workspace")
		}
	}
	if err = e.Store.EnsureWorkspace(ctx, workspace); err != nil {
		return err
	}
	master, err := e.masterEncryptionKey()
	if err != nil {
		return err
	}
	encrypted, err := encryptBytes(master, key)
	if err != nil {
		return err
	}
	challenge, err := encryptBytes(key, []byte("Yeek workspace encryption"))
	if err != nil {
		return err
	}
	metas, err := e.Store.List(ctx, "workspace_meta", workspace)
	if err != nil {
		return err
	}
	if len(metas) != 1 {
		return errors.New("workspace metadata is missing")
	}
	w["encryptionKeyChallenge"] = base64.StdEncoding.EncodeToString(challenge)
	meta := metas[0]
	meta["encryptionKey"] = Object{"encryptedKey": base64.StdEncoding.EncodeToString(encrypted)}
	return e.Store.Write(ctx, Object{"type": "background"}, func(t *modelTx) error {
		if _, err := t.upsert(ctx, w); err != nil {
			return err
		}
		_, err := t.upsert(ctx, meta)
		return err
	})
}
func (e *Engine) EnableEncryption(ctx context.Context, workspace string) error {
	w, err := e.Store.Get(ctx, workspace)
	if err != nil {
		return err
	}
	if str(w, "encryptionKeyChallenge") != "" {
		_, err = e.workspaceKey(ctx, workspace)
		return err
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	return e.setWorkspaceKey(ctx, workspace, key)
}
func (e *Engine) RevealWorkspaceKey(ctx context.Context, workspace string) (string, error) {
	key, err := e.workspaceKey(ctx, workspace)
	if err != nil {
		return "", err
	}
	return humanKey(key), nil
}
func (e *Engine) SecureValue(ctx context.Context, workspace, value string) (string, error) {
	key, err := e.workspaceKey(ctx, workspace)
	if err != nil {
		return "", err
	}
	data, err := encryptBytes(key, []byte(value))
	if err != nil {
		return "", err
	}
	return "${[ secure(value='YENC_" + base64.StdEncoding.EncodeToString(data) + "') ]}", nil
}
func (e *Engine) DecryptValue(ctx context.Context, workspace, value string) (string, error) {
	key, err := e.workspaceKey(ctx, workspace)
	if err != nil {
		return "", err
	}
	value, ok := strings.CutPrefix(value, "YENC_")
	if !ok {
		return "", errors.New("secure value is not encrypted")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	plain, err := decryptBytes(key, data)
	return string(plain), err
}
