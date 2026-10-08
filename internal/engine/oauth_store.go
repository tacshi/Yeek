package engine

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) StoredOAuthToken(ctx context.Context, auth Object, options OAuthOptions) (*OAuthToken, error) {
	if err := validateOAuthPairs(auth); err != nil {
		return nil, err
	}
	return e.readOAuthToken(ctx, oauthTokenKey(auth, options))
}
func (e *Engine) readOAuthToken(ctx context.Context, key string) (*OAuthToken, error) {
	model, err := e.Store.Get(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	master, err := e.masterEncryptionKey()
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(str(model, "encrypted"))
	if err != nil {
		return nil, errors.New("saved OAuth token is invalid; delete it and fetch again")
	}
	data, err = decryptBytes(master, data)
	if err != nil {
		return nil, errors.New("saved OAuth token could not be decrypted; delete it and fetch again")
	}
	var envelope struct {
		Key   string     `json:"key"`
		Token OAuthToken `json:"token"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil || envelope.Key != key || envelope.Token.Value() == "" {
		return nil, errors.New("saved OAuth token does not match this configuration")
	}
	return &envelope.Token, nil
}
func (e *Engine) saveOAuthToken(ctx context.Context, key string, options OAuthOptions, token OAuthToken, generation uint64) error {
	master, err := e.masterEncryptionKey()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(Object{"key": key, "token": token})
	if err != nil {
		return err
	}
	encrypted, err := encryptBytes(master, plain)
	if err != nil {
		return err
	}
	e.oauthMu.Lock()
	defer e.oauthMu.Unlock()
	if e.oauthGenerations[key] != generation {
		return errors.New("token was cleared while authorization was running")
	}
	return e.Store.Write(ctx, Object{"type": "oauth"}, func(tx *modelTx) error {
		if options.WorkspaceID != "" {
			workspace, err := tx.get(ctx, options.WorkspaceID)
			if err != nil || str(workspace, "model") != "workspace" {
				return errors.New("OAuth workspace was deleted")
			}
		}
		if options.ContextID != "" {
			owner, err := tx.get(ctx, options.ContextID)
			if err != nil || (str(owner, "id") != options.WorkspaceID && str(owner, "workspaceId") != options.WorkspaceID) {
				return errors.New("OAuth request or authentication scope was deleted")
			}
		}
		_, err := tx.upsert(ctx, Object{"model": "oauth_token", "id": key, "workspaceId": options.WorkspaceID, "parentId": nilIfBlank(options.ContextID), "encrypted": base64.StdEncoding.EncodeToString(encrypted)})
		return err
	})
}
func (e *Engine) removeOAuthToken(ctx context.Context, key string) error {
	err := e.Store.Write(ctx, Object{"type": "oauth"}, func(tx *modelTx) error {
		if _, err := tx.get(ctx, key); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		return tx.delete(ctx, key)
	})
	return err
}
func (e *Engine) DeleteOAuthToken(ctx context.Context, auth Object, options OAuthOptions) error {
	key := oauthTokenKey(auth, options)
	e.oauthMu.Lock()
	defer e.oauthMu.Unlock()
	if e.oauthGenerations == nil {
		e.oauthGenerations = map[string]uint64{}
	}
	e.oauthGenerations[key]++
	return e.removeOAuthToken(ctx, key)
}

func oauthJWTClaims(value string) Object {
	pieces := strings.Split(value, ".")
	if len(pieces) != 3 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(pieces[1])
	if err != nil {
		return nil
	}
	var claims Object
	if json.Unmarshal(data, &claims) != nil {
		return nil
	}
	return claims
}
func newOAuthToken(response Object, name string) (OAuthToken, error) {
	if name == "" {
		name = "access_token"
	}
	if str(response, name) == "" {
		return OAuthToken{}, fmt.Errorf("token response is missing %s", name)
	}
	token := OAuthToken{Response: clone(response), ObtainedAt: time.Now().UTC(), TokenName: name}
	if value, exists := response["expires_in"]; exists && value != nil {
		seconds, err := strconv.ParseFloat(importText(value), 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds >= float64(math.MaxInt64)/float64(time.Second) {
			return OAuthToken{}, errors.New("expires_in must be a nonnegative number of seconds")
		}
		token.ExpiresAt = token.ObtainedAt.Add(time.Duration(seconds * float64(time.Second)))
	}
	// JWT claims are used only for local expiry hints, not for identity verification.
	if claims := oauthJWTClaims(token.Value()); claims != nil {
		if expiration, ok := claims["exp"].(float64); ok && expiration > 0 && expiration < float64(math.MaxInt64)/1e9 {
			expires := time.Unix(int64(expiration), 0).UTC()
			if token.ExpiresAt.IsZero() || expires.Before(token.ExpiresAt) {
				token.ExpiresAt = expires
			}
		}
	}
	return token, nil
}
