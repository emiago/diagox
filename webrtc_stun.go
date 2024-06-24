// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/pion/turn/v2"
)

type WebrtcSTUNConfig struct {
	Enabled  bool   `env:"WEBRTC_STUN_ENABLED" envDefault:"false"`
	BindIP   string `env:"WEBRTC_STUN_BIND_IP" envDefault:"0.0.0.0"`
	Port     int    `env:"WEBRTC_STUN_PORT" envDefault:"3478"`
	PublicIP string `env:"WEBRTC_STUN_PUBLIC_IP" envDefault:""`
	Realm    string `env:"WEBRTC_STUN_REALM" envDefault:"diagox"`
	Username string `env:"WEBRTC_STUN_USERNAME" envDefault:""`
	Password string `env:"WEBRTC_STUN_PASSWORD" envDefault:""`
}

func startEmbeddedWebrtcSTUN(ctx context.Context, env EnvConfig) error {
	conf := env.WebrtcSTUN
	if !conf.Enabled {
		return nil
	}
	if conf.Port <= 0 || conf.Port > 65535 {
		return fmt.Errorf("WEBRTC_STUN_PORT must be between 1 and 65535")
	}

	publicIP := conf.PublicIP
	if publicIP == "" {
		publicIP = env.SIPExternalMediaIP
	}
	turnEnabled := conf.Username != "" && conf.Password != ""
	if (conf.Username == "") != (conf.Password == "") {
		return fmt.Errorf("WEBRTC_STUN_USERNAME and WEBRTC_STUN_PASSWORD must be configured together")
	}
	if turnEnabled && publicIP == "" {
		bindIP := net.ParseIP(conf.BindIP)
		if bindIP == nil || bindIP.IsUnspecified() {
			return fmt.Errorf("WEBRTC_STUN_PUBLIC_IP or SIP_EXTERNAL_MEDIA_IP is required when embedded TURN is enabled")
		}
	}

	packetConn, err := net.ListenPacket("udp", net.JoinHostPort(conf.BindIP, strconv.Itoa(conf.Port)))
	if err != nil {
		return fmt.Errorf("listen embedded WebRTC STUN/TURN: %w", err)
	}

	relayAddressGenerator, err := embeddedWebrtcRelayAddressGenerator(conf.BindIP, publicIP)
	if err != nil {
		_ = packetConn.Close()
		return err
	}

	authHandler := func(username, realm string, srcAddr net.Addr) ([]byte, bool) {
		if conf.Username == "" || conf.Password == "" || username != conf.Username {
			return nil, false
		}
		return turn.GenerateAuthKey(username, realm, conf.Password), true
	}

	server, err := turn.NewServer(turn.ServerConfig{
		Realm:       conf.Realm,
		AuthHandler: authHandler,
		PacketConnConfigs: []turn.PacketConnConfig{
			{
				PacketConn:            packetConn,
				RelayAddressGenerator: relayAddressGenerator,
			},
		},
	})
	if err != nil {
		_ = packetConn.Close()
		return fmt.Errorf("start embedded WebRTC STUN/TURN: %w", err)
	}

	log.Info("Started embedded WebRTC STUN/TURN",
		"bind", packetConn.LocalAddr().String(),
		"public_ip", publicIP,
		"realm", conf.Realm,
		"turn_enabled", turnEnabled,
	)
	if !turnEnabled {
		log.Warn("Embedded WebRTC TURN authentication is disabled; configure WEBRTC_STUN_USERNAME and WEBRTC_STUN_PASSWORD to allow TURN allocations")
	}

	go func() {
		<-ctx.Done()
		if err := server.Close(); err != nil {
			log.Error("Failed to stop embedded WebRTC STUN/TURN", "error", err)
		}
	}()
	return nil
}

func embeddedWebrtcRelayAddressGenerator(bindIP, publicIP string) (turn.RelayAddressGenerator, error) {
	if publicIP == "" {
		return &turn.RelayAddressGeneratorNone{Address: bindIP}, nil
	}

	relayIP := net.ParseIP(publicIP)
	if relayIP == nil {
		return nil, fmt.Errorf("WEBRTC_STUN_PUBLIC_IP must be an IP address")
	}
	return &turn.RelayAddressGeneratorStatic{
		RelayAddress: relayIP,
		Address:      bindIP,
	}, nil
}
