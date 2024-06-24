// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

var (
	ErrBearerAuthInvalid     = errors.New("bearer auth invalid")
	ErrBearerAuthUnavailable = errors.New("bearer auth unavailable")
)

type UserAuthStoreBearerHTTP struct {
	HTTPClient *http.Client
	Config     SIPRegisterBearerAuthConfig
}

type UserBearerAuth struct {
	User   string
	Token  string
	Method string
}

func authenticateBearer(ctx context.Context, store UserBearerAuthStore, auth UserBearerAuth) error {
	if store == nil {
		return ErrBearerAuthUnavailable
	}
	return store.UserAuthenticateBearer(ctx, auth)
}

func NewUserAuthStoreBearerHTTP(conf SIPRegisterBearerAuthConfig) *UserAuthStoreBearerHTTP {
	timeout := conf.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &UserAuthStoreBearerHTTP{
		HTTPClient: &http.Client{Timeout: timeout, Transport: &http.Transport{
			MaxConnsPerHost:     100,
			MaxIdleConnsPerHost: 100,
		}},
		Config: conf,
	}
}

func (s *UserAuthStoreBearerHTTP) UserAuthenticateBearer(ctx context.Context, auth UserBearerAuth) error {
	if s.Config.URL == "" {
		return ErrBearerAuthUnavailable
	}
	method := auth.Method
	if method == "" {
		method = "REGISTER"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Config.URL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+auth.Token)
	if s.Config.Header != "" {
		name, value, err := bearerAuthProviderHeader(s.Config.Header)
		if err != nil {
			return fmt.Errorf("provider header: %w", err)
		}
		req.Header.Set(name, value)
	}

	slog.Debug("bearer auth provider request",
		"method", req.Method,
		"url", req.URL.String(),
		"headers", redactedHTTPHeaders(req.Header),
		"user", auth.User,
		"sip_method", method,
	)

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("provider request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	slog.Debug("bearer auth provider response",
		"status", resp.StatusCode,
		"headers", redactedHTTPHeaders(resp.Header),
		"body", string(bodyBytes),
	)

	if resp.StatusCode >= 500 {
		return fmt.Errorf("provider status %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%w: provider status %d", ErrBearerAuthInvalid, resp.StatusCode)
	}

	var body any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	activeField := strings.TrimSpace(s.Config.ActiveField)
	if activeField != "" {
		active, exists, err := jsonPathLookup(body, activeField)
		if err != nil {
			return fmt.Errorf("%w: active field: %v", ErrBearerAuthInvalid, err)
		}
		if exists {
			activeBool, ok := active.(bool)
			if !ok || !activeBool {
				return fmt.Errorf("%w: inactive token", ErrBearerAuthInvalid)
			}
		}
	}

	identityField := strings.TrimSpace(s.Config.IdentityField)
	if identityField == "" {
		identityField = ".sub"
	}
	identity, exists, err := jsonPathLookup(body, identityField)
	if err != nil {
		return fmt.Errorf("%w: identity field: %v", ErrBearerAuthInvalid, err)
	}
	if !exists {
		return fmt.Errorf("%w: identity field missing", ErrBearerAuthInvalid)
	}
	identityString, ok := identity.(string)
	if !ok {
		return fmt.Errorf("%w: identity field is not string", ErrBearerAuthInvalid)
	}
	if identityString != auth.User {
		return fmt.Errorf("%w: identity mismatch", ErrBearerAuthInvalid)
	}

	return nil
}

func redactedHTTPHeaders(headers http.Header) map[string][]string {
	result := make(map[string][]string, len(headers))
	for name, values := range headers {
		copied := make([]string, 0, len(values))
		for _, value := range values {
			if isSensitiveHeader(name) {
				copied = append(copied, maskSecret(value))
				continue
			}
			copied = append(copied, value)
		}
		result[name] = copied
	}
	return result
}

func bearerAuthProviderHeader(header string) (string, string, error) {
	name, value, found := strings.Cut(header, ":")
	if !found {
		return "", "", fmt.Errorf("expected header in name: value format")
	}
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" {
		return "", "", fmt.Errorf("header name is empty")
	}
	return name, value, nil
}

func isSensitiveHeader(name string) bool {
	name = strings.ToLower(name)
	return name == "authorization" ||
		name == "proxy-authorization" ||
		name == "cookie" ||
		name == "set-cookie" ||
		strings.Contains(name, "token") ||
		strings.Contains(name, "secret") ||
		strings.Contains(name, "key")
}

func maskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return "***"
	}
	return secret[:4] + "***" + secret[len(secret)-4:]
}

func jsonPathLookup(v any, path string) (any, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return v, true, nil
	}
	if !strings.HasPrefix(path, ".") {
		return nil, false, fmt.Errorf("path must start with dot")
	}

	current := v
	for _, part := range strings.Split(strings.TrimPrefix(path, "."), ".") {
		if part == "" {
			return nil, false, fmt.Errorf("empty path segment")
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		next, exists := obj[part]
		if !exists {
			return nil, false, nil
		}
		current = next
	}

	return current, true, nil
}
