// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"fmt"
	"testing"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/siptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistrar(t *testing.T) {
	ua, _ := sipgo.NewUA()
	client, _ := sipgo.NewClient(ua)

	r := NewRegistrar("", NewRegistryMemory(), NewUserAuthStoreMemory(), nil)

	req := sip.NewRequest(sip.REGISTER, sip.Uri{User: "alice", Host: "localhost"})
	sipgo.ClientRequestRegisterBuild(client, req)

	txRecord := siptest.NewServerTxRecorder(req)
	r.registerHandler(req, txRecord)

	responses := txRecord.Result()
	require.Len(t, responses, 1)
	assert.EqualValues(t, 400, responses[0].StatusCode)
}

func TestServerHandlers(t *testing.T) {
	// Setup server
	uas, _ := sipgo.NewUA()
	srv, _ := sipgo.NewServer(uas)
	handleRegister := func(req *sip.Request, tx sip.ServerTransaction) {
		res := sip.NewResponseFromRequest(req, sip.StatusBadRequest, "Bad Request", nil)
		tx.Respond(res)
	}
	srv.OnRegister(handleRegister)

	// Create request
	req := sip.NewRequest(sip.REGISTER, sip.Uri{User: "alice", Host: "localhost"})

	// Use dummy client to build request headers
	uac, _ := sipgo.NewUA()
	client, _ := sipgo.NewClient(uac)
	sipgo.ClientRequestRegisterBuild(client, req)

	// Create transaction Recorder
	txRecord := siptest.NewServerTxRecorder(req)

	// Run handler and read response
	handleRegister(req, txRecord)
	responses := txRecord.Result()
	require.Len(t, responses, 1)
	assert.EqualValues(t, 400, responses[0].StatusCode)
}

func TestRegistrarBearerAuth(t *testing.T) {
	req := newBearerRegisterRequest("alice", "Bearer token123")

	authStore := &registrarBearerAuthStore{}
	r := NewRegistrar("", NewRegistryMemory(), nil, authStore)

	txRecord := siptest.NewServerTxRecorder(req)
	r.registerHandler(req, txRecord)

	responses := txRecord.Result()
	require.Len(t, responses, 1)
	assert.EqualValues(t, sip.StatusOK, responses[0].StatusCode)
	assert.Equal(t, "token123", authStore.token)
	assert.Equal(t, "alice", authStore.user)
	assert.Equal(t, "REGISTER", authStore.method)
}

func TestRegistrarBearerAuthFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "identityMismatch", err: fmt.Errorf("%w: identity mismatch", ErrBearerAuthInvalid), wantStatus: sip.StatusUnauthorized},
		{name: "inactive", err: fmt.Errorf("%w: inactive token", ErrBearerAuthInvalid), wantStatus: sip.StatusUnauthorized},
		{name: "providerFailure", err: fmt.Errorf("provider request failed"), wantStatus: sip.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newBearerRegisterRequest("alice", "Bearer token123")

			authStore := &registrarBearerAuthStore{err: tt.err}
			r := NewRegistrar("", NewRegistryMemory(), nil, authStore)

			txRecord := siptest.NewServerTxRecorder(req)
			r.registerHandler(req, txRecord)

			responses := txRecord.Result()
			require.Len(t, responses, 1)
			assert.EqualValues(t, tt.wantStatus, responses[0].StatusCode)
		})
	}
}

func TestRegistrarBearerAuthUnavailableWithoutStore(t *testing.T) {
	req := newBearerRegisterRequest("alice", "Bearer token123")
	r := NewRegistrar("", NewRegistryMemory(), nil, nil)

	txRecord := siptest.NewServerTxRecorder(req)
	r.registerHandler(req, txRecord)

	responses := txRecord.Result()
	require.Len(t, responses, 1)
	assert.EqualValues(t, sip.StatusServiceUnavailable, responses[0].StatusCode)
}

type registrarBearerAuthStore struct {
	err    error
	user   string
	token  string
	method string
}

func (s *registrarBearerAuthStore) UserAuthenticateBearer(ctx context.Context, auth UserBearerAuth) error {
	s.user = auth.User
	s.token = auth.Token
	s.method = auth.Method
	return s.err
}

func newBearerRegisterRequest(user string, authorization string) *sip.Request {
	req := sip.NewRequest(sip.REGISTER, sip.Uri{User: user, Host: "localhost"})
	viaParams := sip.NewParams()
	viaParams.Add("branch", "z9hG4bK-test")
	req.AppendHeader(&sip.ViaHeader{ProtocolName: "SIP", ProtocolVersion: "2.0", Transport: "UDP", Host: "127.0.0.1", Params: viaParams})
	fromParams := sip.NewParams()
	fromParams.Add("tag", "fromtag")
	req.AppendHeader(&sip.FromHeader{Address: sip.Uri{User: user, Host: "localhost"}, Params: fromParams})
	req.AppendHeader(&sip.ToHeader{Address: sip.Uri{User: user, Host: "localhost"}})
	callID := sip.CallIDHeader("callid")
	req.AppendHeader(&callID)
	req.AppendHeader(&sip.CSeqHeader{SeqNo: 1, MethodName: sip.REGISTER})
	req.AppendHeader(sip.NewHeader("Contact", "<sip:"+user+"@127.0.0.1:5060>"))
	req.AppendHeader(sip.NewHeader("Authorization", authorization))
	return req
}
