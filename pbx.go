// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"runtime/debug"
	"strings"
	"time"

	"github.com/emiago/diagox/testdata"

	"github.com/emiago/diago"
	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

type PBX struct {
	tu              *diago.Diago
	cdrStore        CDRStorage
	sipTracer       SIPTracer
	cache           DialogExternalCache
	env             EnvConfig
	router          Router
	registry        RegistryStore
	bearerAuthStore UserBearerAuthStore
	digestServer    *diago.DigestAuthServer
	rateLimiterInc  *DialogRateLimiterIncoming
	rateLimiterOut  *DialogRateLimiterOutgoing
	flowRPC         *FlowRPC
}

func NewPBXMemory(envConf EnvConfig, conf Config) *PBX {
	return NewPBX(envConf, conf)
}

func (pbx *PBX) Serve(ctx context.Context) error {
	tu := pbx.tu

	log.Info("Starting pbx...")
	log.Debug("PBX serve", "conf", pbx.env)
	err := tu.Serve(ctx, pbx.handler)

	// Terminate gracefully all dialogs
	log.Info("Terminating gracefully all dialogs... please wait")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tu.DialogCacheClient().DialogRange(ctx, func(id string, d *diago.DialogClientSession) bool {
		log.Info("Terminating client session", "id", id)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.Hangup(ctx)
		return true
	})

	tu.DialogCacheServer().DialogRange(ctx, func(id string, d *diago.DialogServerSession) bool {
		log.Info("Terminating server session", "id", id)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.Hangup(ctx)
		return true
	})

	return err
}

func (pbx *PBX) ServeBackground(ctx context.Context) error {
	tu := pbx.tu

	log.Info("PBX serve background", "conf", pbx.env)
	err := tu.ServeBackground(ctx, pbx.handler)
	return err
}

func (pbx *PBX) handler(inDialog *diago.DialogServerSession) {
	defer func() {
		// Try recovering if possible
		if p := recover(); p != nil {
			fmt.Printf("stacktrace from panic:\n%v\n\n%s\n", p, string(debug.Stack()))
		}
	}()

	callID := inDialog.InviteRequest.CallID().Value()

	// This is expensive for zerolog. We need to find better
	log := log.With("call_id", callID)
	log.Info("New dialog request", "from", inDialog.FromUser(), "to", inDialog.ToUser())
	defer log.Info("Dialog finished")

	// Check our limiter
	if rl := pbx.rateLimiterInc; rl != nil {
		if !rl.DialogRPSLimit() {
			log.Warn("Rate limit reached. Closing dialog", "max", rl.DialogRPS)
			return
		}

		if !rl.DialogActiveLimit() {
			log.Warn("Active dialogs limit reached. Closing dialog", "max", rl.DialogMax)
			return
		}
		defer rl.DialogActiveDec()
	}

	metricDialogsStarted.Inc()
	metricDialogsActive.Inc()
	defer func() {
		metricDialogsActive.Dec()
		metricDialogsEnded.Inc()
	}()

	pbx.registerOnStateDialog(inDialog)

	// Prevent loops
	if inDialog.InviteRequest.Source() == inDialog.InviteRequest.Destination() {
		log.Error("Requests are in loop")
		return
	}

	cx := CallContext{
		PBX: pbx,
		log: log,
	}

	disposition := "NOANSWER"
	inDialog.OnState(func(s sip.DialogState) {
		if s == sip.DialogStateConfirmed {
			metricDialogsAnswered.Inc()
			disposition = "ANSWER"
		}
	})

	defer func(start time.Time) {
		if !pbx.env.CDREnable {
			return
		}
		cdrStore := pbx.cdrStore
		callID := inDialog.InviteRequest.CallID().Value()
		cdr := CDR{
			Direction:        DirectionIn,
			CallID:           callID,
			OriginatorCallID: callID,
			StartTime:        start,
			Duration:         time.Since(start),
			CallerID:         inDialog.FromUser(),
			CalleeID:         inDialog.ToUser(),
			Disposition:      disposition,
			MediaStats:       cx.inDialogStats,
			RecordingID:      cx.recordingID,
			// For visual
			StartTimeFormated: start.Format(time.DateTime),
		}

		if cdr.Disposition == "" {
			cdr.Disposition = disposition
		}

		cdr.MES = cdr.MediaStats.calcMES(disposition)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		log.Info("Writing incoming cdr")
		if err := cdrStore.CDRWrite(ctx, cdr); err != nil {
			log.Error("Failed to store CDR", "error", err)
		}
	}(time.Now())

	defer func() {
		log.Info("Hanguping incoming call")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := inDialog.Hangup(ctx); err != nil {
			log.Error("Failed to hangup incoming call", "error", err)
		}
	}()
	if pbx.env.TestMode {
		// Just expose this in test mode
		switch inDialog.ToUser() {
		case "playback", "playbackmemory":
			cx.Playback(inDialog)
			return
		case "playback_webrtc":
			cx.PlaybackWebrtc(inDialog)
			return
		case "bridge":

		case "webrtcbridge":
			if err := pbx.WebrtcBridge(inDialog); err != nil {
				log.Error("Failed to handle webrtc", "error", err)
				return
			}
			return
		case "answerwebrtc":
			cx.AnswerWebrtc(inDialog)
			return
		}
	}

	cx.BridgeCall(inDialog)
	return
}

func (pbx *PBX) invite(ctx context.Context, recipient sip.Uri, bridge *diago.Bridge, opts diago.InviteOptions) (*diago.DialogClientSession, *diago.DialogMedia, error) {
	var dialog *diago.DialogClientSession
	var med *diago.DialogMedia
	var err error

	if bridge == nil {
		dialog, med, err = pbx.tu.Invite(ctx, recipient, opts)
	} else {
		dialog, med, err = pbx.tu.InviteBridge(ctx, recipient, bridge, opts)
	}

	if err != nil {
		return nil, nil, err
	}

	pbx.registerOnStateDialog(dialog)

	return dialog, med, err
}

// invite bridge is wrapper where we could, based on dialed endpoint do some routing to right carrier
func (pbx *PBX) inviteBridge(ctx context.Context, user string, bridge *diago.Bridge, opts diago.InviteOptions) (*diago.DialogClientSession, *diago.DialogMedia, error) {
	recipient := sip.Uri{}
	if err := sip.ParseUri(pbx.env.OutboundDialUri, &recipient); err != nil {
		return nil, nil, err
	}
	recipient.User = user
	return pbx.invite(ctx, recipient, bridge, opts)
}

// TODO: not fully reliable, needs explicit closing of dialog
func (pbx *PBX) registerOnStateDialog(d diago.DialogSession) {
	cache := pbx.cache
	dialogSip := d.DialogSIP()

	if cache == nil {
		return
	}

	var direction uint8
	if _, ok := d.(*diago.DialogClientSession); ok {
		direction = 1
	}

	handleStateChange := func(s sip.DialogState) {
		log.Info("Dialog State Change", "id", dialogSip.ID, "state", s.String())
		if cache == nil {
			return
		}

		// Cache dialog to external only when answered.
		// We should not block here too much!!!
		if s == sip.DialogStateConfirmed {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			data := NewDialogData(dialogSip, direction)
			if err := cache.StoreDialog(ctx, data); err != nil {
				log.Error("Failed to store in external cache", "error", err)
			}
		}

		if s == sip.DialogStateEnded {
			cache.DeleteDialog(context.TODO(), d.Id())
		}
	}
	// onState change
	dialogSip.OnState(handleStateChange)
	handleStateChange(dialogSip.LoadState())
}

func (pbx *PBX) ReInviteDialogs(ctx context.Context, dialogs []DialogData, bridges []BridgeData) error {
	tu := pbx.tu
	doDialplan := func(dialog *diago.DialogClientSession, med *diago.DialogMedia) {
		defer dialog.Close()
		defer med.Close()
		// Returned to same dialplan

		playfile, _ := testdata.OpenFile("demo-instruct.wav")
		log.Info("Playing a file", "file", "demo-instruct.wav")

		pb, err := med.PlaybackCreate()
		if err != nil {
			log.Error("Failed to create playback", "error", err)
			return
		}

		if _, err := pb.Play(playfile, "audio/wav"); err != nil {
			log.Error("Playing failed", "error", err)
		}

		// select {
		// case <-dialog.Context().Done():
		// }
	}

	reinvite := func(ctx context.Context, tu *diago.Diago, d DialogData) error {
		// We stil do not have INVITE Server transaction
		inviteReqMsg, err := sip.ParseMessage([]byte(d.InviteRequestData))
		if err != nil {
			return err
		}
		inviteReq := inviteReqMsg.(*sip.Request)

		if d.Direction == 1 {
			fromHDR := inviteReq.From()
			toHDR := inviteReq.To()
			callid := inviteReq.CallID()
			cseq := inviteReq.CSeq()

			dialog, med, err := pbx.invite(ctx, inviteReq.Recipient, nil, diago.InviteOptions{
				Headers: []sip.Header{
					fromHDR,
					toHDR,
					callid,
					cseq,
				},
			})
			if err != nil {
				return err
			}

			go doDialplan(dialog, med)
			return nil
		}

		// TODO: Check dialog state, only reinvite for successful calls?
		cont := inviteReq.Contact()
		fromHDR := inviteReq.To().AsFrom()
		toHDR := inviteReq.From().AsTo()
		callid := inviteReq.CallID()
		cseq := inviteReq.CSeq()

		// If client than just try build same invite request

		// This is now reinvite
		// Force transport
		cont.Params.Add("transport", inviteReq.Transport())
		dialog, med, err := pbx.invite(ctx, cont.Address, nil, diago.InviteOptions{
			Headers: []sip.Header{
				&fromHDR,
				&toHDR,
				callid,
				cseq,
			},
		})
		if err != nil {
			return err
		}

		go doDialplan(dialog, med)

		return nil
	}

	// For now support non bridged dialogs
	var errs error
	for _, d := range dialogs {
		err := reinvite(ctx, tu, d)
		if err != nil {
			return err
		}
		if err := pbx.cache.DeleteDialogNode(ctx, d.ID, d.NodeID); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

type DialogMediaStats struct {
	RTTMax            time.Duration `json:"rttMax"`
	RTTMin            time.Duration `json:"rttMin"`
	PacketsReadCount  uint64        `json:"packetsReadCount"`
	PacketsWriteCount uint64        `json:"packetsWriteCount"`
	PacketsReadLost   uint32        `json:"packetsReadLost"`
	PacketsWriteLost  uint32        `json:"packetsWriteLost"`
	MaxJitter         time.Duration `json:"maxJitter"`
}

func (mediaStats *DialogMediaStats) calcMES(disposition string) float64 {
	mes := 0.0
	if disposition == "ANSWER" || disposition == "BRIDGED" {
		mes = 0.9
	}

	if mediaStats.RTTMax > 20*time.Millisecond {
		mes -= float64(20*time.Millisecond) / float64(mediaStats.RTTMax)
	}
	if mediaStats.MaxJitter > 10*time.Millisecond {
		mes -= float64(10*time.Millisecond) / float64(mediaStats.MaxJitter)
	}

	return mes
}

type CallContext struct {
	*PBX

	inDialogStats  DialogMediaStats
	outDialogStats DialogMediaStats
	bridged        bool
	recordingID    string
	log            *slog.Logger
}

func (cx *CallContext) BridgeCall(inDialog *diago.DialogServerSession) {
	pbx := cx.PBX
	log := cx.log

	// Lets match our endpoint
	fromEndpoint := ConfigEndpoint{}
	log.Info("Matching incoming call", "from", inDialog.FromUser(), "source", inDialog.InviteRequest.Source())
	if !pbx.router.MatchEndpoint(inDialog.InviteRequest, &fromEndpoint) {
		inDialog.Respond(sip.StatusForbidden, "Forbidden", nil)
		return
	}

	if !pbx.authorizeInboundDialog(inDialog, fromEndpoint, log) {
		return
	}

	log.Info("Matched endpoint", "endpoint", fromEndpoint.Name, "media", fromEndpoint.Media.Type)
	if err := inDialog.Trying(); err != nil {
		log.Error("Trying sending failed", "error", err)
		return
	} // Progress -> 100 Trying

	// Where now to dial?
	// find outbound URI
	routeConf := ConfigRoute{}
	if !pbx.router.MatchIncomingRoute(inDialog.InviteRequest, fromEndpoint.Route, &routeConf) {
		log.Info("No incoming route found")
		inDialog.Respond(sip.StatusRequestTerminated, "RequestTerminated", nil)
		return
	}
	log.Info("Matched route", "route", fromEndpoint.Route, "id", routeConf.ID)

	if code := routeConf.Hangup.Code; code > 0 {
		reason := routeConf.Hangup.Reason
		if reason == "" {
			reason = SIPResponseReason(code)
		}
		inDialog.Respond(code, reason, nil)
		return
	}

	toEndpoint := ConfigEndpoint{}
	outboundURIStr := cx.endpointLoadOutboundURI(inDialog, &routeConf, &toEndpoint)
	if outboundURIStr == "" {
		log.Info("No outbound URI found")
		inDialog.Respond(sip.StatusRequestTerminated, "RequestTerminated", nil)
		return
	}

	log.Info("Matched outbound uri", "endpoint", toEndpoint.Name, "uri", outboundURIStr)
	if toEndpoint.Match.Type == "flow" {
		// Doing Agent RPC
		redirect := ConfigEndpoint{}
		err := cx.flowRPC.NewServerDialog(inDialog.Context(), toEndpoint.Name, inDialog, &redirect)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// Mostly call was hanguped
				log.Debug("Agent dialog handling finished with error", "error", err)
			} else {
				log.Error("Agent dialog handling finished with error", "error", err)
			}
			return
		}

		// Are we having next endpoint?
		if redirect.Name == "" {
			return
		}

		toEndpoint = redirect
		outboundURIStr = cx.endpointResolveOutboundURI(&toEndpoint)
	}

	outboundURI := sip.Uri{}
	if err := sip.ParseUri(outboundURIStr, &outboundURI); err != nil {
		log.Error("Failed to parse outbound uri", "error", err)
		inDialog.Respond(sip.StatusForbidden, "Forbidden", nil)
		return
	}

	// Overwrite user that is targeted
	outboundURI.User = inDialog.ToUser()
	if pref := routeConf.StripPrefix; pref != "" {
		outboundURI.User = strings.TrimPrefix(outboundURI.User, pref)
	}

	// Before answer check are we rate limited and block
	if rl := pbx.rateLimiterOut; rl != nil {
		ctx, cancel := context.WithTimeout(inDialog.Context(), 10*time.Second)
		defer cancel()
		if err := rl.Block(log, ctx); err != nil {
			if !errors.Is(err, context.DeadlineExceeded) {
				log.Error("Rate limit absorb failed", "error", err)
				return
			}
			log.Warn("Outgoing request rate limited. Hanguping...", "error", err)

			inDialog.Respond(429, "Rate limited", nil)
			return
		}
	}

	if err := inDialog.Ringing(); err != nil {
		log.Error("Ringing sending failed", "error", err)
		return
	}

	cx.bridgeAnswer(inDialog, outboundURI, &routeConf, &fromEndpoint, &toEndpoint)
	return
}

func (pbx *PBX) authorizeInboundDialog(inDialog *diago.DialogServerSession, endpoint ConfigEndpoint, log *slog.Logger) bool {
	switch endpoint.Auth.AuthType() {
	case "":
		return true
	case ConfigAuthTypeBearer:
		return pbx.authorizeInboundDialogBearer(inDialog, endpoint, log)
	case ConfigAuthTypeDigest:
		return pbx.authorizeInboundDialogDigest(inDialog, endpoint, log)
	default:
		log.Error("unsupported auth type", "auth_type", endpoint.Auth.Type)
		inDialog.Respond(sip.StatusForbidden, "Forbidden", nil)
		return false
	}
}

func (pbx *PBX) authorizeInboundDialogBearer(inDialog *diago.DialogServerSession, endpoint ConfigEndpoint, log *slog.Logger) bool {
	authHeader := inDialog.InviteRequest.GetHeader("Authorization")
	if authHeader == nil {
		inDialog.Respond(sip.StatusUnauthorized, "Unauthorized", nil)
		return false
	}
	token, ok := bearerToken(strings.TrimSpace(authHeader.Value()))
	if !ok {
		inDialog.Respond(sip.StatusUnauthorized, "Unauthorized", nil)
		return false
	}

	err := authenticateBearer(inDialog.Context(), pbx.bearerAuthStore, UserBearerAuth{
		User:   endpointBearerIdentity(inDialog, endpoint),
		Token:  token,
		Method: sip.INVITE.String(),
	})
	if err != nil {
		if errors.Is(err, ErrBearerAuthUnavailable) {
			log.Error("bearer auth unavailable", "error", err)
			inDialog.Respond(sip.StatusServiceUnavailable, "Service Unavailable", nil)
			return false
		}
		log.Info("bearer auth failed", "error", err)
		inDialog.Respond(sip.StatusUnauthorized, "Unauthorized", nil)
		return false
	}
	return true
}

func (pbx *PBX) authorizeInboundDialogDigest(inDialog *diago.DialogServerSession, endpoint ConfigEndpoint, log *slog.Logger) bool {
	err := pbx.digestServer.AuthorizeDialog(inDialog, diago.DigestAuth{
		Username: endpointDigestIdentity(endpoint),
		Password: endpoint.Auth.Password,
		Realm:    "diagox",
	})
	if err != nil {
		log.Info("Not authorized", "error", err)
		return false
	}
	return true
}

func endpointBearerIdentity(inDialog *diago.DialogServerSession, endpoint ConfigEndpoint) string {
	if endpoint.Match.Type == "user_dynamic" && endpoint.Auth.Username == "" {
		return inDialog.FromUser()
	}
	if endpoint.Auth.Username != "" {
		return endpoint.Auth.Username
	}
	return endpoint.Name
}

func endpointDigestIdentity(endpoint ConfigEndpoint) string {
	if endpoint.Auth.Username != "" {
		return endpoint.Auth.Username
	}
	return endpoint.Name
}

func (cx *CallContext) bridgeAnswer(
	inDialog *diago.DialogServerSession,
	outboundURI sip.Uri,
	routeConf *ConfigRoute,
	fromEndpoint *ConfigEndpoint,
	toEndpoint *ConfigEndpoint) {

	inCtx := inDialog.Context()
	xLinkedId := sip.NewHeader("X-Orig-Call-ID", inDialog.InviteRequest.CallID().Value())
	var sipHeaders []sip.Header
	if headers := routeConf.SipHeaders; headers != nil {
		sipHeaders = make([]sip.Header, len(headers)+1)
		sipHeaders[0] = xLinkedId
		i := 1
		for k, v := range headers {
			sipHeaders[i] = sip.NewHeader(k, v)
			i++
		}
	} else {
		sipHeaders = []sip.Header{
			xLinkedId,
		}
	}

	// Add additional sip headers
	if headers := routeConf.SipHeadersPass; headers != nil {
		for _, name := range headers {
			h := inDialog.InviteRequest.GetHeader(name)
			if h != nil {
				sipHeaders = append(sipHeaders, sip.HeaderClone(h))
			}
		}
	}

	bridge := diago.NewBridge()
	bridge.DTMFpass = true // Allow passing DTMF

	bridgeId := uuid.New().String()
	BridgeCache.Store(bridgeId, &bridge)
	defer BridgeCache.Delete(bridgeId)

	// Prepare invite options and do dialing
	inviteOpts := diago.InviteClientOptions{
		Originator: inDialog,
		Headers:    sipHeaders,
		OnResponse: func(res *sip.Response) error {
			return nil
		},
		OnMediaUpdate: func(d *diago.DialogMedia) {
			cx.OnOutboundRTPSession(d.RTPSession())
		},
		Username:         toEndpoint.Auth.Username,
		Password:         toEndpoint.Auth.Password,
		EarlyMediaDetect: true,
	}

	ctx, cancel := context.WithTimeout(inCtx, 32*time.Second)
	defer cancel()
	log.Info("Invite", "uri", outboundURI.String(), "tran", toEndpoint.TransportID)

	outDialog, err := cx.PBX.tu.NewDialog(outboundURI, diago.NewDialogOptions{
		TransportID: toEndpoint.TransportID,
	})
	if err != nil {
		log.Error("Error received", "error", err)
		return
	}
	defer outDialog.Close()
	defer outDialog.Hangup(outDialog.Context())

	// as requested
	// https://github.com/emiago/diagox/issues/2
	if toEndpoint.contactHDRParsed != nil {
		// We are not doing here full clone, but as value is read only it should not create race
		outDialog.UA.ContactHDR = *toEndpoint.contactHDRParsed
	}

	disposition := "NOANSWER"
	defer func(start time.Time) {
		if !cx.env.CDREnable {
			return
		}

		callIDHeader := outDialog.InviteRequest.CallID()
		if callIDHeader == nil {
			// No call made
			return
		}

		cdr := CDR{
			Direction:        DirectionOut,
			CallID:           callIDHeader.Value(),
			OriginatorCallID: xLinkedId.Value(),
			StartTime:        start,
			Duration:         time.Since(start),
			CallerID:         outDialog.FromUser(),
			CalleeID:         outDialog.ToUser(),
			Disposition:      disposition,
			MediaStats:       cx.outDialogStats,
			RecordingID:      cx.recordingID,
			Bridged:          cx.bridged,
			// For visual
			StartTimeFormated: start.Format(time.DateTime),
		}

		cdr.MES = cdr.MediaStats.calcMES(disposition)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		log.Info("Writing outgoing cdr")
		cdrStore := cx.cdrStore
		if err := cdrStore.CDRWrite(ctx, cdr); err != nil {
			log.Error("Failed to store outgoing CDR", "error", err)
		}
	}(time.Now())

	var outMedia *diago.DialogMedia
	var earlyMedia *diago.DialogMedia
	var outWebrtc *diago.DialogWebrtc
	var earlyWebrtc *diago.DialogWebrtc
	invited := func() (invited bool) {
		if toEndpoint.Media.Type == "webrtc" {
			// HANDLE WEBRTC
			outWebrtc, err = outDialog.InviteWebrtc(ctx, diago.InviteWebrtcOptions{
				WebrtcConfig: webrtc.Configuration{},
			})
			if err != nil {
				log.Error("Webrtc invite failed", "error", err)
				return
			}
			return true
		}

		// outDialog.Media().RTPSession().Sess.LocalSDP()
		outMedia, err = outDialog.Invite(ctx, inviteOpts)
		if err != nil {
			invErr := err
			if errors.Is(err, diago.ErrClientEarlyMedia) {
				// How to deal with this?
				// We need to proxy this 183
				// Asterisk keeps order like it would answer this channel.
				earlyMedia, err = inDialog.ProgressMedia(diago.ProgressMediaOptions{})
				if err != nil {
					log.Error("Failed to proxy Progress", "error", err)
					return
				}

				// Start early earlyBridge, but be explicit in calling proxy media
				earlyBridge := diago.NewBridge()
				earlyBridge.Init(log)

				if err := earlyBridge.AddDialogMedia(earlyMedia); err != nil {
					log.Error("Failed to add into early bridge", "error", err)
					return
				}

				if err := earlyBridge.AddDialogMedia(outMedia); err != nil {
					log.Error("Failed to add outdialog into bridge", "error", err)
					return
				}

				invErr = outDialog.WaitAnswer(ctx, outMedia, sipgo.AnswerOptions{
					OnResponse: inviteOpts.OnResponse,
					Username:   inviteOpts.Username,
					Password:   inviteOpts.Password,
				})
			}

			// In case early media this err would be nil
			if err := invErr; err != nil {
				// Do we support fallback
				if routeConf.Fallback.Enabled && isInviteErrFallback(err, routeConf.Fallback) {
					if outMedia != nil {
						defer outMedia.Close()
						outMedia = nil
					}
					outDialog, outMedia, err = cx.inviteFallback(inCtx, inDialog, routeConf, inviteOpts)
					if err != nil {
						log.Error("Fallback error", "error", err)
						return
					}
					defer outDialog.Close()
				} else {
					log.Error("Failed to dial", "error", err, "caller_hng", inCtx.Err(), "timeout", ctx.Err())
					return
				}
			}
		}
		return true
	}()

	if !invited {
		return
	}
	if outMedia != nil {
		defer outMedia.Close()
	}
	if outWebrtc != nil {
		defer outWebrtc.Close()
	}

	var inMedia *diago.DialogMedia
	var inWebrtc *diago.DialogWebrtc
	answered := func() (answered bool) {
		if fromEndpoint.Media.Type == "webrtc" {

			if earlyWebrtc != nil {
				log.Error("Early webrtc media can not be bridged yet")
				return
			}
			// Now handle webrtc
			// TODO: How to handle now RTP Session metrics
			log.Info("Answering webrtc")
			inWebrtc, err = inDialog.AnswerWebrtc(diago.AnswerWebrtcOptions{
				Codecs: outWebrtc.MediaSession().CommonCodecs(),
			})
			if err != nil {
				log.Error("Failed to answer webrtc", "error", err)
				return
			}
			return true
		}

		// Now get answered SDP from outgoing and pass same formats to avoid transcoding
		answerOpts := diago.AnswerOptions{
			OnMediaUpdate: func(d *diago.DialogMedia) {
				cx.OnInboundRTPSession(d.RTPSession())
			},
			// Getting CommonCodecs is safe here as media session is established
			Codecs: outMedia.MediaSession().CommonCodecs(),
		}

		if earlyMedia != nil {
			if err := inDialog.AnswerEarlyMedia(earlyMedia, answerOpts); err != nil {
				log.Error("Failed to answer", "error", err)
				return
			}
			inMedia = earlyMedia
		} else {
			m, err := inDialog.Answer(answerOpts)
			if err != nil {
				log.Error("Failed to answer", "error", err)
				return
			}
			inMedia = m
		}

		// if err := inDialog.AnswerOptions(answerOpts); err != nil {
		// 	log.Error("Failed to answer", "error", err)
		// 	return
		// } //
		cx.OnInboundRTPSession(inMedia.RTPSession())
		return true
	}()

	if !answered {
		return
	}
	if inMedia != nil {
		defer inMedia.Close()
	}
	if inWebrtc != nil {
		defer inWebrtc.Close()
	}
	disposition = "ANSWER"

	audioMediaIn := func() AudioMediaStack {
		if inWebrtc != nil {
			return inWebrtc
		}
		return inMedia
	}()

	audioMediaOut := func() AudioMediaStack {
		if outWebrtc != nil {
			return outWebrtc
		}
		return outMedia
	}()

	// If we are recording this run then monitor
	if routeConf.Recording && audioMediaIn != nil {
		// mPropsR := diago.MediaProps{}
		// ar, err := inDialog.AudioReader(diago.WithAudioReaderMediaProps(&mPropsR))
		// if err != nil {
		// 	log.Error("Failed to create monitor reader", "error", err)
		// 	return
		// }

		// mPropsW := diago.MediaProps{}
		// aw, err := inDialog.AudioWriter(diago.WithAudioWriterMediaProps(&mPropsW))
		// if err != nil {
		// 	log.Error("Failed to create monitor writer", "error", err)
		// 	return
		// }

		// if mPropsR.Codec != mPropsW.Codec {
		// 	// We can not use stereo monitor. We would need some additional tools
		// 	if mPropsR.Codec.SampleDur != mPropsW.Codec.SampleDur &&
		// 		mPropsR.Codec.SampleRate != mPropsW.Codec.SampleRate {
		// 		log.Error("Failed to create monitor writer due to different codecs match")
		// 		return
		// 	}
		// }

		// Create wav file to store recording
		filename := path.Join(cx.env.RecordingsPath, inDialog.ID+".wav")
		recordFile, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0755)
		if err != nil {
			log.Error("Failed to create recording file", "error", err)
			return
		}
		defer recordFile.Close()
		// Now create WavWriter to have Wav Container written
		// wawWriter := audio.NewWavWriter(recordFile)
		// defer wawWriter.Close() // Must be called for header update

		mon, err := audioMediaIn.AudioStereoRecordingCreate(recordFile)
		if err != nil {
			log.Error("Failed to create stereo recording", "error", err)
			return
		}

		defer func() {
			if err := mon.Close(); err != nil {
				log.Error("Closing audio stereo recording returned error", "error", err)
			}
		}()
		// Create nom monitor stereo that will monitor and send PCM to wawContainer
		// mon := diagomod.AudioMonitorPCMStereo{}
		// mon.Init(wawWriter, mPropsR.Codec, ar, aw)
		// defer mon.Close()

		// Make it now default for bridge
		audioMediaIn.SetAudioReader(mon.AudioReader())
		audioMediaIn.SetAudioWriter(mon.AudioWriter())

		log.Info("Starting monitor", "file", recordFile.Name())
		cx.recordingID = inDialog.ID
	}

	log.Debug("Adding media IN")
	if err := bridge.AddMedia(audioMediaIn); err != nil {
		log.Error("Failed to add into bridge", "error", err)
		return
	}

	log.Debug("Adding media out")
	if err := bridge.AddMedia(audioMediaOut); err != nil {
		log.Error("Failed to add outdialog into bridge", "error", err)
		return
	}

	if err := outDialog.Ack(ctx); err != nil {
		log.Error("Failed to ack", "error", err)
		return
	}

	cx.bridged = true
	// RTP Session is not yet SUPPORTED BY WEBRTC so we need todo like this
	if rtpSess := outMedia.RTPSession(); rtpSess != nil {
		cx.OnOutboundRTPSession(rtpSess)
	}

	cx.waitDialogTermination(inCtx, outDialog)
}

type AudioMediaStack interface {
	AudioStereoRecordingCreate(*os.File) (diago.AudioStereoRecordingWav, error)
	SetAudioReader(io.Reader)
	SetAudioWriter(io.Writer)
}

func (cx *CallContext) waitDialogTermination(inCtx context.Context, outDialog *diago.DialogClientSession) {
	outCtx := outDialog.Context()
	log := cx.log
	// This is beauty, as you can even easily detect who hangups
	log.Info("Waiting call termination")
	select {
	case <-inCtx.Done():
		hctx, hcancel := context.WithTimeout(outCtx, 10*time.Second)
		defer hcancel()
		if err := outDialog.Hangup(hctx); err != nil {
			log.Error("Failed to hangup out dialog", "error", err)
		}
	case <-outCtx.Done():
	case <-time.After(6 * time.Hour):
		log.Error("Bridged call Timeout (6 Hours)")
	}
}

func isInviteErrFallback(err error, fallbackConf ConfigRouteFallback) bool {
	var resErr *sipgo.ErrDialogResponse
	if errors.As(err, &resErr) {
		fcodes := fallbackConf.FallbacksCodes
		for _, code := range fcodes {
			if code == int(resErr.Res.StatusCode) {
				return true
			}
		}
	}

	// If this is timeout
	return errors.Is(err, context.DeadlineExceeded) && fallbackConf.FallbacksTimeout
}

func (cx *CallContext) inviteFallback(inCtx context.Context, inDialog *diago.DialogServerSession, routeConf *ConfigRoute, inviteOpts diago.InviteClientOptions) (*diago.DialogClientSession, *diago.DialogMedia, error) {
	pbx := cx.PBX
	log := cx.log
	// Threat this dialing with fallback
	for _, endName := range routeConf.Fallback.Endpoints {
		end, exists := pbx.router.conf.Endpoints[endName]
		if !exists {
			return nil, nil, fmt.Errorf("endpoint %q does not exists", end.Name)
		}

		outboundURI := sip.Uri{}
		if err := sip.ParseUri(end.URI, &outboundURI); err != nil {
			return nil, nil, err
		}

		outDialog, err := pbx.tu.NewDialog(outboundURI, diago.NewDialogOptions{
			TransportID: end.TransportID,
		})
		if err != nil {
			return nil, nil, err
		}
		owned := true
		defer func() {
			if owned {
				outDialog.Close()
			}
		}()

		ctx, cancel := context.WithTimeout(inCtx, 32*time.Second)
		defer cancel()

		log.Info("Dialing fallback endpoint", "uri", end.URI)
		outMedia, err := outDialog.Invite(ctx, inviteOpts)
		if err != nil {
			if isInviteErrFallback(err, routeConf.Fallback) {
				// log.Info().Str("uri", end.URI).Msg("Dialing fallback endpoint")
				continue
			}
			return nil, nil, err
		}

		owned = false
		return outDialog, outMedia, nil
	}
	return nil, nil, fmt.Errorf("no fallback endpoint found")
}

func (cx *CallContext) endpointLoadOutboundURI(inDialog *diago.DialogServerSession, route *ConfigRoute, toEndpoint *ConfigEndpoint) string {
	pbx := cx.PBX
	if !pbx.router.MatchOutgoingRouteEndpoint(inDialog.InviteRequest, route, toEndpoint) {
		log.Info("No route endpoint found, using enviroment OUTBOUND_DIAL_URI")
		return pbx.env.OutboundDialUri
	}

	log.Debug("Matched route endpoint", "endpoint", toEndpoint)
	return cx.endpointResolveOutboundURI(toEndpoint)
}
func (cx *CallContext) endpointResolveOutboundURI(toEndpoint *ConfigEndpoint) string {
	pbx := cx.PBX
	if toEndpoint.useRegistry {
		return cx.endpointResolveRegisteredURI(toEndpoint)
	}

	// Normally user type do not have uri, instead they register which is after check
	// Still user can hardcode this uri even as user
	if toEndpoint.URI != "" {
		return toEndpoint.URI
	}

	// if fromEndpoint.Match.Type == "user" && toEndpoint.Match.Type == "user" {
	// Routing will control should some endpoint read registry or not
	if toEndpoint.Match.Type == "user" {
		return cx.endpointResolveRegisteredURI(toEndpoint)
	}

	if toEndpoint.Match.Type == "flow" {
		return "flow"
	}

	// if end.AOR.URI != "" {
	// 	return end.AOR.URI
	// }
	return pbx.env.OutboundDialUri
}

func (cx *CallContext) endpointResolveRegisteredURI(toEndpoint *ConfigEndpoint) string {
	pbx := cx.PBX
	regId := createRegId(toEndpoint.Name, cx.env.SIPHostname)
	binding, err := pbx.registry.RegisterBindingGet(regId)
	if err != nil {
		log.Info("Failed to get binding for user", "error", err)
		return ""
	}

	if len(binding.Contacts) == 0 {
		log.Error("binding contacts is zero")
		return ""
	}

	// We could here match and dial multicontactss
	contUri := binding.Contacts[0]
	cont := binding.Contacts[0].String()
	if cont == "" {
		log.Error("Binding contact is zero")
		return ""
	}

	// Handle WEBRTC or non resolvable contacts
	if strings.HasSuffix(contUri.Host, ".invalid") || toEndpoint.Media.Type == "webrtc" {
		cont = "sip:" + binding.SourceAddr + ";transport=" + binding.Transport
	}

	// TODO avoid reparsing
	// TODO handle multi contact dial
	return cont
}

func (cx *CallContext) OnInboundRTPSession(rtpSess *media.RTPSession) {
	rtpSess.OnReadRTCP(onReadRTCP(&cx.inDialogStats))
	rtpSess.OnWriteRTCP(onWriteRTCP(&cx.inDialogStats))
}

func (cx *CallContext) OnOutboundRTPSession(rtpSess *media.RTPSession) {
	rtpSess.OnReadRTCP(onReadRTCP(&cx.outDialogStats))
	rtpSess.OnWriteRTCP(onWriteRTCP(&cx.outDialogStats))
}

func (cx *CallContext) ListenRTPSession(rtpSess *media.RTPSession, stats *DialogMediaStats) {
	rtpSess.OnReadRTCP(onReadRTCP(stats))
	rtpSess.OnWriteRTCP(onWriteRTCP(stats))
}

func (cx *CallContext) answer(inDialog *diago.DialogServerSession) (*diago.DialogMedia, error) {
	med, err := inDialog.Answer(diago.AnswerOptions{
		OnMediaUpdate: func(d *diago.DialogMedia) {
			cx.OnInboundRTPSession(d.RTPSession())
		},
	})
	if err != nil {
		return nil, err
	}
	cx.OnInboundRTPSession(med.RTPSession())
	return med, nil
}

func (cx *CallContext) AnswerWebrtc(inDialog *diago.DialogServerSession) {
	log := cx.log
	inDialog.Trying()

	codecs, err := cx.PBX.env.mediaCodecs()
	if err != nil {
		log.Error("Failed to load media codecs", "error", err)
		return
	}
	med, err := inDialog.AnswerWebrtc(diago.AnswerWebrtcOptions{
		Codecs: codecs,
	})
	if err != nil {
		log.Error("Failed to answer", "error", err)
		return
	}
	defer med.Close()

	pb, err := med.PlaybackCreate()
	if err != nil {
		log.Error("Failed to create playback", "error", err)
		return
	}

	f, err := testdata.OpenFile("demo-thanks.wav")
	if err != nil {
		log.Error("Failed to open file", "error", err)
		return
	}
	pb.Play(f, "audio/wav")
}

func onReadRTCP(stats *DialogMediaStats) func(pkt rtcp.Packet, rtpStats media.RTPReadStats) {
	return func(pkt rtcp.Packet, rtpStats media.RTPReadStats) {
		if rtpStats.RTT > 0 {
			stats.RTTMax = max(stats.RTTMax, rtpStats.RTT)
			if stats.RTTMin == 0 {
				stats.RTTMin = rtpStats.RTT
			}
			stats.RTTMin = min(stats.RTTMin, rtpStats.RTT)
		}
		stats.PacketsReadCount = rtpStats.PacketsCount

		if sr, ok := pkt.(*rtcp.SenderReport); ok {
			metricRTPPacketsRead.Add(float64(sr.PacketCount))
			if len(sr.Reports) > 0 {
				rr := sr.Reports[0]
				stats.PacketsReadLost = rr.TotalLost
				sampleRate := rtpStats.SampleRate
				if sampleRate == 0 {
					sampleRate = 8000
				}
				jitterDuration := time.Duration(float64(rr.Jitter) / float64(sampleRate) * float64(time.Second))
				stats.MaxJitter = max(stats.MaxJitter, jitterDuration)

				jitterSeconds := float64(rr.Jitter) / float64(sampleRate)
				metricRTPJitter.Observe(jitterSeconds)
				metricRTPJitterLast.Set(jitterSeconds)
				metricRTPPacketLossRatio.Set(float64(rr.FractionLost) / 255)
			}
		}
		if rtpStats.RTT > 0 {
			rttSeconds := rtpStats.RTT.Seconds()
			metricRTPRTT.Observe(rttSeconds)
			metricRTPRTTLast.Set(rttSeconds)
		}
	}
}

func onWriteRTCP(stats *DialogMediaStats) func(pkt rtcp.Packet, rtpStats media.RTPWriteStats) {
	return func(pkt rtcp.Packet, rtpStats media.RTPWriteStats) {
		if sr, ok := pkt.(*rtcp.SenderReport); ok {
			metricRTPPacketsWritten.Add(float64(sr.PacketCount))
			if len(sr.Reports) > 0 {
				rr := sr.Reports[0]

				// expect mono 8000 rate
				// jittDur := time.Duration(float64(rr.Jitter) / float64(rtpStats.SampleRate) * float64(time.Second))
				// stats.MaxJitter = max(stats.MaxJitter, jittDur)

				stats.PacketsWriteLost = rr.TotalLost
			}
		}
		stats.PacketsWriteCount = rtpStats.PacketsCount

	}
}
