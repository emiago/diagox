// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/diagotest"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/siptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPBXAuthorizeInboundDialogBearer(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantOK     bool
		wantStatus int
	}{
		{name: "success", wantOK: true},
		{name: "invalid", err: fmt.Errorf("%w: identity mismatch", ErrBearerAuthInvalid), wantStatus: sip.StatusUnauthorized},
		{name: "unavailable", err: fmt.Errorf("%w: provider failed", ErrBearerAuthUnavailable), wantStatus: sip.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialog, tx := newInboundAuthDialog(t, "Bearer invite-token")
			authStore := &inviteBearerAuthStore{err: tt.err}
			pbx := NewPBXMemory(EnvConfig{}, Config{})
			pbx.bearerAuthStore = authStore

			if tt.wantStatus > 0 {
				ackFinalInviteResponse(t, dialog.InviteRequest, tx)
			}
			ok := pbx.authorizeInboundDialog(dialog, ConfigEndpoint{
				Name: "carrier",
				Auth: ConfigAuth{Type: ConfigAuthTypeBearer, Username: "carrier-auth", Password: "secret"},
			}, slog.Default())

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, "carrier-auth", authStore.user)
			assert.Equal(t, "invite-token", authStore.token)
			assert.Equal(t, "INVITE", authStore.method)
			if tt.wantStatus > 0 {
				responses := tx.Result()
				require.NotEmpty(t, responses)
				assert.EqualValues(t, tt.wantStatus, responses[0].StatusCode)
			}
		})
	}
}

func TestPBXAuthorizeInboundDialogDigestFallback(t *testing.T) {
	dialog, tx := newInboundAuthDialog(t, "")
	authStore := &inviteBearerAuthStore{}
	pbx := NewPBXMemory(EnvConfig{}, Config{})
	pbx.bearerAuthStore = authStore
	pbx.digestServer = diago.NewDigestServer()

	ackFinalInviteResponse(t, dialog.InviteRequest, tx)
	ok := pbx.authorizeInboundDialog(dialog, ConfigEndpoint{
		Name: "carrier",
		Auth: ConfigAuth{Username: "carrier-auth", Password: "secret"},
	}, slog.Default())

	assert.False(t, ok)
	assert.Empty(t, authStore.method)
	responses := tx.Result()
	require.NotEmpty(t, responses)
	assert.EqualValues(t, sip.StatusUnauthorized, responses[0].StatusCode)
	assert.NotNil(t, responses[0].GetHeader("WWW-Authenticate"))
}

func TestPBXAuthorizeInboundDialogNoEndpointAuth(t *testing.T) {
	dialog, tx := newInboundAuthDialog(t, "Bearer invite-token")
	authStore := &inviteBearerAuthStore{}
	pbx := NewPBXMemory(EnvConfig{}, Config{})
	pbx.bearerAuthStore = authStore

	ok := pbx.authorizeInboundDialog(dialog, ConfigEndpoint{Name: "carrier"}, slog.Default())

	assert.True(t, ok)
	assert.Empty(t, authStore.method)
	assert.Empty(t, tx.Result())
}

func TestPBXAuthorizeInboundDialogDynamicBearer(t *testing.T) {
	dialog, _ := newInboundAuthDialog(t, "Bearer invite-token")
	authStore := &inviteBearerAuthStore{}
	pbx := NewPBXMemory(EnvConfig{}, Config{})
	pbx.bearerAuthStore = authStore

	ok := pbx.authorizeInboundDialog(dialog, ConfigEndpoint{
		Name: "webrtc_users",
		Match: ConfigMatch{
			Type: "user_dynamic",
		},
		Auth: ConfigAuth{Type: ConfigAuthTypeBearer},
	}, slog.Default())

	assert.True(t, ok)
	assert.Equal(t, dialog.FromUser(), authStore.user)
	assert.Equal(t, "invite-token", authStore.token)
	assert.Equal(t, "INVITE", authStore.method)
}

type inviteBearerAuthStore struct {
	err    error
	user   string
	token  string
	method string
}

func (s *inviteBearerAuthStore) UserAuthenticateBearer(ctx context.Context, auth UserBearerAuth) error {
	s.user = auth.User
	s.token = auth.Token
	s.method = auth.Method
	return s.err
}

func newInboundAuthDialog(t testing.TB, authorization string) (*diago.DialogServerSession, *siptest.ServerTxRecorder) {
	t.Helper()

	req, err := diagotest.NewRequest(sip.INVITE, sip.Uri{User: "123", Host: "localhost"})
	require.NoError(t, err)
	req.SetTransport("tcp")
	req.SetSource("127.0.0.1:5060")
	if authorization != "" {
		req.AppendHeader(sip.NewHeader("Authorization", authorization))
	}

	dialog, tx, err := diagotest.NewDialogServerSession(req)
	require.NoError(t, err)
	return dialog, tx
}

func ackFinalInviteResponse(t testing.TB, inviteReq *sip.Request, tx *siptest.ServerTxRecorder) {
	t.Helper()

	ack := sip.NewRequest(sip.ACK, inviteReq.Recipient)
	ack.SetTransport(inviteReq.Transport())
	ack.SetSource(inviteReq.Source())
	ack.AppendHeader(sip.HeaderClone(inviteReq.Via()))
	ack.AppendHeader(sip.HeaderClone(inviteReq.From()))
	ack.AppendHeader(sip.HeaderClone(inviteReq.To()))
	ack.AppendHeader(sip.HeaderClone(inviteReq.CallID()))

	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-tx.Done():
				return
			case <-ticker.C:
				_ = tx.Receive(ack)
			}
		}
	}()
}
