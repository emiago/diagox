// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/mediawebrtc"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
)

var (
	nodeID string
	log    *slog.Logger
)

func Bootstrap(ctx context.Context, conf Config, envConf EnvConfig) {
	log = slog.Default()
	pbx := NewPBX(envConf, conf)
	if err := pbx.Init(ctx, conf); err != nil {
		log.Error("Failed to init PBX", "error", err)
		return
	}
	if err := pbx.Run(ctx); err != nil {
		log.Error("PBX finished with error", "error", err)
	}
}

func NewPBX(envConf EnvConfig, conf Config) *PBX {
	log = slog.Default()

	defaultProviders := NewDefaultProviders(envConf)
	return &PBX{
		cdrStore:       defaultProviders.CDRStore,
		sipTracer:      defaultProviders.SIPTracer,
		cache:          defaultProviders.Cache,
		env:            envConf,
		registry:       defaultProviders.Registry,
		rateLimiterInc: defaultProviders.RateIn,
		rateLimiterOut: defaultProviders.RateOut,
		router: Router{
			conf: conf,
		},
		flowRPC: NewFlowRPC(conf),
	}
}

func (pbx *PBX) Init(ctx context.Context, conf Config) error {
	if err := startEmbeddedWebrtcSTUN(ctx, pbx.env); err != nil {
		return err
	}

	if pbx.env.SIPCDRTrace {
		sip.SIPDebug = true
		sipTrace = pbx.env.SIPDebug
	}

	if err := pbx.setupDiago(conf); err != nil {
		return err
	}
	// Start agent RPC
	pbx.flowRPC.ServeBackground(pbx.env.FlowRPCAddr)

	_, isMemory := pbx.cdrStore.(*CDRMemoryStorage)
	if pbx.env.FrontendDevMode && isMemory {
		pbx.cdrStore.CDRWrite(context.TODO(), CDR{StartTime: time.Now().Add(-48 * time.Hour), Direction: DirectionIn, CallerID: "491234", CalleeID: "499876", Duration: 3 * time.Second, CallID: uuid.NewString(), Disposition: "NOANSWER"})
		pbx.cdrStore.CDRWrite(context.TODO(),
			CDR{StartTime: time.Now().Add(-12 * time.Hour), Direction: DirectionOut, CallerID: "sipgo", CalleeID: "echotest", Duration: 5 * time.Minute, CallID: uuid.NewString(), Disposition: "ANSWER", MES: 0.85,
				MediaStats: DialogMediaStats{
					RTTMax:           10 * time.Millisecond,
					PacketsReadLost:  3,
					PacketsWriteLost: 4,
					MaxJitter:        20 * time.Millisecond,
				},
			},
		)
		pbx.cdrStore.CDRWrite(context.TODO(), CDR{StartTime: time.Now().Add(-1 * time.Hour), Direction: DirectionIn, CallerID: "123", CalleeID: "876", Duration: 340 * time.Second, CallID: uuid.NewString(), Disposition: "ANSWER", MES: 0.9})

		// Add CDR with recording
		os.WriteFile(pbx.env.RecordingsPath+"/demo-thanks.wav", []byte("RIFF"), 0755)
		pbx.cdrStore.CDRWrite(context.TODO(), CDR{StartTime: time.Now().Add(-1*time.Hour - 5*time.Minute), Direction: DirectionIn, CallerID: "123", CalleeID: "876", Duration: 340 * time.Second, CallID: uuid.NewString(), Disposition: "ANSWER", MES: 0.9, RecordingID: "demo-thanks"})
	}

	sip.SIPDebugTracer(pbx.SIPTracer())

	return nil
}

func (pbx *PBX) Run(ctx context.Context) error {
	go httpServer(ctx, pbx.env.HTTPAddr, &Handler{env: pbx.env, CdrStore: pbx.cdrStore, D: pbx.tu, SIPtracer: pbx.SIPTracer()})
	return pbx.Serve(ctx)
}

func (pbx *PBX) setupDiago(conf Config) error {
	// Setup our main transaction user
	ua, _ := sipgo.NewUA()
	envConf := pbx.env

	diagoOpts := []diago.DiagoOption{}
	diagoTransports := []diago.Transport{}
	mediaBindIps := []net.IP{}
	for tid, t := range conf.Transports {
		if t.Bind == "" {
			t.Bind = pbx.env.SIPBindIP
		}

		if t.ExternalHost == "" {
			t.ExternalHost = pbx.env.SIPExternalHost
		}

		if t.ExternalMediaIP == "" {
			t.ExternalMediaIP = pbx.env.SIPExternalMediaIP
		}

		tran := diago.Transport{
			ID:              tid,
			Transport:       t.Transport,
			BindHost:        t.Bind,
			BindPort:        t.Port,
			ExternalHost:    t.ExternalHost,
			ExternalPort:    t.ExternalPort,
			MediaExternalIP: net.ParseIP(t.ExternalMediaIP),
			RewriteContact:  t.RewriteContact,
		}

		if t.Transport == "tls" || t.Transport == "wss" {
			if envConf.ServerTLSKey != "" {
				// Support loading base64 key and tls
				var key, crt []byte
				var err error
				if key, err = base64.StdEncoding.DecodeString(envConf.ServerTLSKey); err != nil {
					return err
				}
				if crt, err = base64.StdEncoding.DecodeString(envConf.ServerTLSCrt); err != nil {
					return err
				}

				cert, err := tls.X509KeyPair(crt, key)
				if err != nil {
					return err
				}
				serverTLSConf := &tls.Config{
					Certificates: []tls.Certificate{cert},
					ServerName:   envConf.SIPHostname,
				}
				tran.TLSConf = serverTLSConf
				log.Info("TLS Certificate loaded", "dns_names", cert.Leaf.DNSNames, "ip_addreses", cert.Leaf.IPAddresses)
			} else if envConf.ServerTLSKeyPath != "" {
				cert, err := tls.LoadX509KeyPair(envConf.ServerTLSCrtPath, envConf.ServerTLSKeyPath)
				if err != nil {
					return err
				}

				serverTLSConf := &tls.Config{
					Certificates: []tls.Certificate{cert},
					ServerName:   envConf.SIPHostname,
				}
				tran.TLSConf = serverTLSConf
				log.Info("TLS Certificate loaded", "dns_names", cert.Leaf.DNSNames, "ip_addreses", cert.Leaf.IPAddresses)
			} else {
				log.Warn("No Server TLS Certificate configured for tran=%w. Consider setting SERVER_TLS_* enviroment variables", "tran", tran.Transport)
			}
		}

		diagoTransports = append(diagoTransports, tran)
		diagoOpts = append(diagoOpts, diago.WithTransport(tran))
		mediaBindIps = append(mediaBindIps, net.ParseIP(t.Bind))
	}

	// cli, err := sipgo.NewClient(ua,
	// 	sipgo.WithClientNAT(),
	// 	sipgo.WithClientHostname("127.0.0.1"),
	// )
	// if err != nil {
	// 	return err
	// }

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		return err
	}
	codecs, err := envConf.mediaCodecs()
	if err != nil {
		return err
	}

	diagoOpts = append(diagoOpts,
		// diago.WithClient(cli),
		diago.WithServer(srv),
		diago.WithMediaConfig(diago.MediaConfig{
			Codecs: codecs,
		}),
	)

	pbx.tu = diago.NewDiago(ua, diagoOpts...)
	// pbx.tuWebrtc = &diagomod.DiagoWebrtc{}
	// if err := pbx.tuWebrtc.Init(mediaBindIps); err != nil {
	// 	return err
	// }
	// Add our registrar
	userauths := NewUserAuthStoreMemory()
	for _, end := range conf.Endpoints {
		if end.Match.Type != "user" {
			continue
		}
		// TODO: DO we need this?
		userauths.UserAuthAdd(end.Name, diago.DigestAuth{
			Username: end.Auth.Username,
			Password: end.Auth.Password,
		})
	}

	var bearerAuthStore UserBearerAuthStore
	if pbx.env.SIPRegisterBearerAuth.URL != "" {
		bearerAuthStore = NewUserAuthStoreBearerHTTP(pbx.env.SIPRegisterBearerAuth)
	}
	pbx.bearerAuthStore = bearerAuthStore

	registrar := NewRegistrar(pbx.env.SIPHostname, pbx.registry, userauths, bearerAuthStore)
	srv.OnRegister(registrar.registerHandler)

	pbx.digestServer = diago.NewDigestServer()

	if err := pbx.setupDiagoWebrtc(mediaBindIps); err != nil {
		return err
	}
	return nil
}

func (pbx *PBX) setupDiagoWebrtc(iceIPs []net.IP) error {
	webrtcConfig, err := pbx.env.webrtcConfiguration()
	if err != nil {
		return err
	}

	apiConfig := diago.WebrtcAPIConfig{
		Config: webrtcConfig,
		ICEIPs: iceIPs,
	}
	api, err := diago.NewWebrtcAPIFromConfig(apiConfig)
	if err != nil {
		return err
	}
	diago.SetWebrtcAPI(api)

	mediaAPI, err := mediawebrtc.NewWebrtcAPIFromConfig(mediawebrtc.WebrtcAPIConfig{
		Config: webrtcConfig,
		ICEIPs: iceIPs,
	})
	if err != nil {
		return err
	}
	mediawebrtc.SetWebrtcAPI(mediaAPI)
	return nil
}
