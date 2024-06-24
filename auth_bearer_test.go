// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONPathLookup(t *testing.T) {
	body := map[string]any{
		"sub": "alice",
		"user": map[string]any{
			"name": "bob",
		},
		"active": true,
	}

	t.Run("topLevel", func(t *testing.T) {
		v, exists, err := jsonPathLookup(body, ".sub")
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, "alice", v)
	})

	t.Run("nested", func(t *testing.T) {
		v, exists, err := jsonPathLookup(body, ".user.name")
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, "bob", v)
	})

	t.Run("missing", func(t *testing.T) {
		_, exists, err := jsonPathLookup(body, ".user.email")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("invalid", func(t *testing.T) {
		_, _, err := jsonPathLookup(body, "sub")
		require.Error(t, err)
	})
}

func TestEnvConfigRegisterBearerAuth(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		var conf EnvConfig
		require.NoError(t, EnvConfigLoad(&conf))
		assert.Equal(t, 2*time.Second, conf.SIPRegisterBearerAuth.Timeout)
		assert.Equal(t, ".sub", conf.SIPRegisterBearerAuth.IdentityField)
		assert.Equal(t, ".active", conf.SIPRegisterBearerAuth.ActiveField)
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("SIP_REGISTER_BEARER_AUTH_URL", "https://identity.example.test/introspect")
		t.Setenv("SIP_REGISTER_BEARER_AUTH_TIMEOUT", "750ms")
		t.Setenv("SIP_REGISTER_BEARER_AUTH_IDENTITY_FIELD", ".claims.sip_user")
		t.Setenv("SIP_REGISTER_BEARER_AUTH_ACTIVE_FIELD", "")
		t.Setenv("SIP_REGISTER_BEARER_AUTH_HEADER", "X-Auth: secret")

		var conf EnvConfig
		require.NoError(t, EnvConfigLoad(&conf))
		assert.Equal(t, "https://identity.example.test/introspect", conf.SIPRegisterBearerAuth.URL)
		assert.Equal(t, 750*time.Millisecond, conf.SIPRegisterBearerAuth.Timeout)
		assert.Equal(t, ".claims.sip_user", conf.SIPRegisterBearerAuth.IdentityField)
		assert.Empty(t, conf.SIPRegisterBearerAuth.ActiveField)
		assert.Equal(t, "X-Auth: secret", conf.SIPRegisterBearerAuth.Header)
	})
}

func TestUserAuthStoreBearerHTTP(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "Bearer token123", r.Header.Get("Authorization"))
			require.Equal(t, "provider-secret", r.Header.Get("X-Provider-Auth"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"active":true,"claims":{"sip_user":"alice"}}`))
		}))
		t.Cleanup(provider.Close)

		authStore := NewUserAuthStoreBearerHTTP(SIPRegisterBearerAuthConfig{
			URL:           provider.URL,
			IdentityField: ".claims.sip_user",
			ActiveField:   ".active",
			Header:        "X-Provider-Auth: provider-secret",
		})

		require.NoError(t, authStore.UserAuthenticateBearer(context.Background(), UserBearerAuth{
			User:  "alice",
			Token: "token123",
		}))
	})

	t.Run("invite", func(t *testing.T) {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "Bearer invite-token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"active":true,"sub":"carrier"}`))
		}))
		t.Cleanup(provider.Close)

		authStore := NewUserAuthStoreBearerHTTP(SIPRegisterBearerAuthConfig{
			URL:           provider.URL,
			IdentityField: ".sub",
			ActiveField:   ".active",
		})

		require.NoError(t, authStore.UserAuthenticateBearer(context.Background(), UserBearerAuth{
			User:   "carrier",
			Token:  "invite-token",
			Method: "INVITE",
		}))
	})

	t.Run("identityMismatch", func(t *testing.T) {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"active":true,"sub":"bob"}`))
		}))
		t.Cleanup(provider.Close)

		authStore := NewUserAuthStoreBearerHTTP(SIPRegisterBearerAuthConfig{
			URL:           provider.URL,
			IdentityField: ".sub",
			ActiveField:   ".active",
		})

		require.ErrorIs(t, authStore.UserAuthenticateBearer(context.Background(), UserBearerAuth{
			User:  "alice",
			Token: "token123",
		}), ErrBearerAuthInvalid)
	})

	t.Run("inactive", func(t *testing.T) {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"active":false,"sub":"alice"}`))
		}))
		t.Cleanup(provider.Close)

		authStore := NewUserAuthStoreBearerHTTP(SIPRegisterBearerAuthConfig{
			URL:           provider.URL,
			IdentityField: ".sub",
			ActiveField:   ".active",
		})

		require.ErrorIs(t, authStore.UserAuthenticateBearer(context.Background(), UserBearerAuth{
			User:  "alice",
			Token: "token123",
		}), ErrBearerAuthInvalid)
	})

	t.Run("providerServerErrorIsNotUnavailable", func(t *testing.T) {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "failed", http.StatusInternalServerError)
		}))
		t.Cleanup(provider.Close)

		authStore := NewUserAuthStoreBearerHTTP(SIPRegisterBearerAuthConfig{
			URL: provider.URL,
		})

		err := authStore.UserAuthenticateBearer(context.Background(), UserBearerAuth{
			User:  "alice",
			Token: "token123",
		})

		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrBearerAuthUnavailable))
		assert.False(t, errors.Is(err, ErrBearerAuthInvalid))
	})

	t.Run("decodeErrorIsNotUnavailable", func(t *testing.T) {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`not-json`))
		}))
		t.Cleanup(provider.Close)

		authStore := NewUserAuthStoreBearerHTTP(SIPRegisterBearerAuthConfig{
			URL: provider.URL,
		})

		err := authStore.UserAuthenticateBearer(context.Background(), UserBearerAuth{
			User:  "alice",
			Token: "token123",
		})

		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrBearerAuthUnavailable))
		assert.False(t, errors.Is(err, ErrBearerAuthInvalid))
	})
}
