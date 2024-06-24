// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
	"github.com/icholy/digest"
)

type AuthorizerDigest struct {
	challenges map[string]digest.Challenge
	passwords  map[string]string
}

func NewAuthorizerDigest(userpass map[string]string) *AuthorizerDigest {
	return &AuthorizerDigest{
		challenges: make(map[string]digest.Challenge),
		passwords:  userpass,
	}
}

func (a *AuthorizerDigest) Authorize(inDialog *diago.DialogServerSession) (authorized bool) {
	challenges := a.challenges
	passwords := a.passwords
	callid := inDialog.InviteRequest.CallID().Value()

	if authHDR := inDialog.InviteRequest.GetHeader("Authorization"); authHDR != nil {
		chal, exists := challenges[callid]
		if !exists {
			inDialog.Respond(400, "Challenge timeout", nil)
			return
		}

		dig := authHDR.Value()
		cred, err := digest.ParseCredentials(dig)
		if err != nil {
			inDialog.Respond(400, "Invalid creds", nil)
			return
		}

		pass, exists := passwords[cred.Username]
		if !exists {
			inDialog.Respond(sip.StatusForbidden, "Forbidden", nil)
			return
		}

		digCred, err := digest.Digest(&chal, digest.Options{
			Method:   sip.INVITE.String(),
			URI:      cred.URI,
			Username: cred.Username,
			Password: pass,
		})

		if err != nil {
			log.Error("Calc digest failed", "error", err)
			inDialog.Respond(401, "Bad credentials", nil)
			// tx.Respond(sip.NewResponseFromRequest(req, 401, "Bad credentials", nil))
			return
		}

		if cred.Response != digCred.Response {
			inDialog.Respond(401, "Unathorized", nil)
			return
		}

		// Now do something with call
		return true
	}

	// Load configuration
	// Authorize Invite
	chal := digest.Challenge{
		Realm:     "gopbx",
		Domain:    []string{"localhost"},
		Nonce:     uuid.NewString(),
		Algorithm: "MD5",
	}

	challenges[callid] = chal
	inDialog.Respond(sip.StatusUnauthorized, "Unauthorized", nil, sip.NewHeader("WWW-Authenticate", chal.String()))
	time.AfterFunc(10*time.Second, func() {
		delete(challenges, callid)
	})
	return
}
