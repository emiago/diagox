// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

// TODO
// Send OPTIONS packet for keep alive. Mostly for UDP is needed < 1min
// Set Expires header to default 3600 sec. Client should reregister sooner

var (
	parserContact = sip.DefaultHeadersParser()["contact"]
)

type Registrar struct {
	Hostname    string
	Registry    RegistryStore
	DigestStore UserDigestAuthStore
	BearerStore UserBearerAuthStore

	digestChallenge map[string]digest.Challenge

	digestAuthSrv *diago.DigestAuthServer
	log           *slog.Logger
}

type digestChallenge struct {
	m  map[string]digestChallenge
	mu sync.Mutex
}

func NewRegistrar(hostname string, registry RegistryStore, digestStore UserDigestAuthStore, bearerStore UserBearerAuthStore) *Registrar {
	logger := log
	if logger == nil {
		logger = slog.Default()
	}
	return &Registrar{
		Hostname:        hostname,
		Registry:        registry,
		DigestStore:     digestStore,
		BearerStore:     bearerStore,
		digestChallenge: make(map[string]digest.Challenge),
		digestAuthSrv:   diago.NewDigestServer(),
		log:             logger.With("caller", "registrar"),
	}
}

func (p *Registrar) RegisterHandler(req *sip.Request, tx sip.ServerTransaction) {
	p.registerHandler(req, tx)
}

// https://datatracker.ietf.org/doc/html/rfc3261#section-10
func (p *Registrar) registerHandler(req *sip.Request, tx sip.ServerTransaction) {
	via := req.Via()
	callId := req.CallID()
	from := req.From()
	to := req.To()
	if callId == nil || from == nil || to == nil || via == nil {
		tx.Respond(sip.NewResponseFromRequest(req, 400, "Bad Request", nil))
		return
	}
	// TODO: this is memory expensive. We need to find better logger or cache
	log := p.log.With("call_id", callId.Value(), "from", from.Address.User)
	// TODO rfc5626
	// https://datatracker.ietf.org/doc/html/rfc5626#section-3.2
	// This is when we have contact header
	// <sip:line1@192.0.2.2;transport=tcp>; reg-id=1;
	// ;+sip.instance="<urn:uuid:00000000-0000-1000-8000-000A95A0E128>"
	// end we can use this reg-id and uuid to replace existing Contact

	// 1. inspect request uri
	if p.Hostname != "" && req.Recipient.Host != p.Hostname {
		log.Info("incorrect sip domain")
		tx.Respond(sip.NewResponseFromRequest(req, 403, "Forbidden", nil))
		return
	}

	// 2 Require field
	// TODO

	// TODO check some minimal interval allowance and set Min-Expires header
	contHdrs := req.GetHeaders("Contact")
	if len(contHdrs) == 0 {
		tx.Respond(sip.NewResponseFromRequest(req, 400, "Bad Request - Contact header missing", nil))
		return
	}

	// 5 extracting To AOR
	aor := to.Address
	// Validate AOR?

	expiry := 60 * time.Minute // TODO: make this configurable
	if h := req.GetHeader("Expires"); h != nil {
		var err error
		expiry, err = time.ParseDuration(h.Value() + "s")
		if err != nil {
			tx.Respond(sip.NewResponseFromRequest(req, 400, "Bad Request - Expires header malformed", nil))
			return
		}
	}

	if ok := p.authorizeRegister(req, tx, from.Address.User, expiry, log); !ok {
		return
	}

	// 6. Check Contact header
	hdrs := make([]*sip.ContactHeader, len(contHdrs))
	for i, h := range contHdrs {
		if ch, ok := h.(*sip.ContactHeader); ok {
			hdrs[i] = sip.HeaderClone(ch).(*sip.ContactHeader)
			continue
		}
		// ch := sip.ContactHeader{}
		h, err := parserContact([]byte(h.Name()), h.Value())
		if err != nil {
			log.Info("Parsing contact header failed", "error", err)
			tx.Respond(sip.NewResponseFromRequest(req, 400, "Bad Request - Contact header malformed", nil))
			return
		}
		ch, ok := h.(*sip.ContactHeader)
		if !ok {
			panic("Should not happen")
		}
		hdrs[i] = ch
	}

	// Make sure aor has no port
	// regId := aor.User + "@" + aor.Host
	regId := createRegId(aor.User, p.Hostname)

	// Are we removing DEREGISTERING?
	// https://datatracker.ietf.org/doc/html/rfc3261#section-10.2.2
	if expiry == 0 && len(hdrs) == 1 && hdrs[0].Address.Wildcard {
		log.Debug("Register binding delete", "reg.id", regId)
		if err := p.Registry.RegisterBindingDelete(regId); err != nil {
			log.Error("Register binding delete failed", "error", err)
		}
		tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
		return

	}

	// 7. Processing each contact header
	contacts := make([]sip.Uri, 0, len(hdrs))

	// Check does binding exists
	// storedContacts := p.store.GetAORContacts(aor.User)
	for _, h := range hdrs {

		/*  If the address-of-record in the To header field of a REGISTER request
		is a SIPS URI, then any Contact header field values in the request
		SHOULD also be SIPS URIs.  Clients should only register non-SIPS URIs
		under a SIPS address-of-record when the security of the resource
		represented by the contact address is guaranteed by other means. */

		// TODO Check prioritization
		// https://datatracker.ietf.org/doc/html/rfc3261#section-10.2.1.2

		expires, exists := h.Params.Get("expires")

		// Are we removing DEREGISTERING?
		// https://datatracker.ietf.org/doc/html/rfc3261#section-10.2.2
		if expires == "0" {
			continue
		}
		if exists && expires != "" {
			// TODO
			exp, err := time.ParseDuration(expires + "s")
			if err != nil {
				log.Error("Failed to parse expires param", "expires", expires+"s", "error", err)
				tx.Respond(sip.NewResponseFromRequest(req, 400, "Bad Request - Expires header in contact malformed", nil))
				return
			}

			// Based on contract update our minimum expiry
			expiry = min(expiry, exp)
		}

		// // Check bindings
		// var found sip.Uri
		// for _, c := range storedContacts {
		// 	if c.String() == h.String() {
		// 		found = c
		// 	}
		// }
		// if via.Params != nil {
		// 	// User is behind NAT, lets rewrite contact
		// 	_, exists := via.Params.Get("rport")
		// 	if exists {
		// 		// Can we now actually detect that client is behind NAT
		// 		host, port, _ := sip.ParseAddr(req.Source())
		// 		h.Address.Host = host
		// 		h.Address.Port = port
		// 		log.Info("Rewriting ontact header due to NAT", "host", host, "port", port)
		// 	}
		// }
		addr := *h.Address.Clone()
		contacts = append(contacts, addr)
	}

	// The registrar MAY choose an expiration less than the requested
	// expiration interval.  If and only if the requested expiration
	// interval is greater than zero AND smaller than one hour AND
	// less than a registrar-configured minimum, the registrar MAY
	// reject the registration with a response of 423 (Interval Too
	// Brief).  This response MUST contain a Min-Expires header field
	// that states the minimum expiration interval the registrar is
	// willing to honor.  It then skips the remaining steps.
	if expiry < 30*time.Second {
		res := sip.NewResponseFromRequest(req, 423, "Interval Too Brief", nil)
		res.AppendHeader(sip.NewHeader("Min-Expires", "30"))
		tx.Respond(res)
		return
	}

	// 8. Returning 200

	callid := req.CallID()
	// TODO compare callid

	binding := RegisterBinding{
		Aor:        aor,
		CallID:     callid.String(),
		Contacts:   contacts,
		Expiry:     expiry,
		SourceAddr: req.Source(),
		Transport:  req.Transport(),
	}

	log.Debug("Register binding add", "reg.id", regId, "aor", aor.String())
	if err := p.Registry.RegisterBindingSet(regId, binding); err != nil {
		tx.Respond(sip.NewResponseFromRequest(req, 500, "Internal Server Error", nil))
		return
	}
	// Each Contact value MUST feature an "expires"
	//  parameter indicating its expiration interval chosen by the
	//  registrar.  The response SHOULD include a Date header field.

	ok200 := sip.NewResponseFromRequest(req, 200, "OK", nil)

	for _, h := range hdrs {
		ch := h.Clone()
		ch.Params.Add("expires", fmt.Sprintf("%d", int(expiry.Seconds())))
		ok200.AppendHeader(ch)
	}
	log.Info("Registered", "aor", aor.String(), "contacts", binding.Contacts, "addr", binding.SourceAddr, "tran", binding.Transport)
	tx.Respond(ok200)
}

func createRegId(user, host string) string {
	return user + "@" + host
}

// func ReadHeaderByType[T sip.Header](m sip.Message, name string) ([]T, error) {
// 	hdrs := m.GetHeaders(name)
// 	ret := make([]T, 0, len(hdrs))
// 	for _, h := range hdrs {
// 		hh, ok := h.(T)
// 		if !ok {
// 			hh = T
// 		}
// 		ret = append(ret, hh)
// 	}
// 	return ret, nil
// }

type UserDigestAuthStore interface {
	// TODO extend with tenant domain
	UserReadDigestAuth(user string, auth *diago.DigestAuth) error
}

type UserBearerAuthStore interface {
	UserAuthenticateBearer(ctx context.Context, auth UserBearerAuth) error
}

type UserAuthStoreMemory struct {
	mu    sync.RWMutex
	users map[string]diago.DigestAuth
}

func NewUserAuthStoreMemory() *UserAuthStoreMemory {
	return &UserAuthStoreMemory{
		users: make(map[string]diago.DigestAuth),
	}
}

func (s *UserAuthStoreMemory) UserReadDigestAuth(user string, auth *diago.DigestAuth) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, exists := s.users[user]
	if !exists {
		return fmt.Errorf("Auth does not exists")
	}
	*auth = u
	return nil
}

func (s *UserAuthStoreMemory) UserAuthAdd(user string, auth diago.DigestAuth) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[user] = auth
	return nil
}

func (p *Registrar) authorizeRegister(req *sip.Request, tx sip.ServerTransaction, user string, expiry time.Duration, log *slog.Logger) bool {
	authHeader := req.GetHeader("Authorization")
	if authHeader != nil {
		authValue := strings.TrimSpace(authHeader.Value())
		if token, ok := bearerToken(authValue); ok {
			if p.BearerStore == nil {
				log.Error("bearer auth unavailable")
				tx.Respond(sip.NewResponseFromRequest(req, sip.StatusServiceUnavailable, "Service Unavailable", nil))
				return false

			}
			ctx := context.TODO()
			if err := p.BearerStore.UserAuthenticateBearer(ctx, UserBearerAuth{
				User:   user,
				Token:  token,
				Method: sip.REGISTER.String(),
			}); err != nil {
				log.Info("bearer auth failed", "error", err)
				tx.Respond(sip.NewResponseFromRequest(req, sip.StatusUnauthorized, "Unauthorized", nil))
				return false
			}
			return true
		}
	}

	userAuth := diago.DigestAuth{
		Realm: "diagox",
	}
	if p.DigestStore == nil {
		log.Info("digest auth unavailable")
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusNotFound, "Not Found", nil))
		return false
	}
	if err := p.DigestStore.UserReadDigestAuth(user, &userAuth); err != nil {
		log.Info("Failed to find user for register")
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusNotFound, "Not Found", nil))
		return false
	}

	// Digest auth. Our nonce can expire quickly to limit Authorization header replay.
	if userAuth.Expire == 0 {
		userAuth.Expire = min(5*time.Minute, expiry)
	}
	if userAuth.Username == "" {
		// Authorize with empty password, but authorization must be present.
		userAuth.Username = user
		log.Warn("Authorization not present for endpoint")
	}
	if userAuth.Realm == "" {
		userAuth.Realm = "diagox"
	}
	res, err := func() (*sip.Response, error) {
		log.Debug("Authorizing user", "auth", userAuth)
		res, err := p.digestAuthSrv.AuthorizeRequest(req, userAuth)
		if err != nil {
			if errors.Is(err, diago.ErrDigestAuthNoChallenge) {
				req.RemoveHeader("Authorization")
				return p.digestAuthSrv.AuthorizeRequest(req, userAuth)
			}
		}
		return res, err
	}()

	if err != nil {
		if res.StatusCode >= 500 {
			log.Error("fail to digest", "error", err)
		} else {
			log.Info("fail to digest", "error", err)
		}

		tx.Respond(res)
		return false
	}
	if res.StatusCode != sip.StatusOK {
		log.Info("responding non 2xx", "status", int(res.StatusCode))
		tx.Respond(res)
		return false
	}
	return true
}

func bearerToken(authValue string) (string, bool) {
	scheme, token, ok := strings.Cut(authValue, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
