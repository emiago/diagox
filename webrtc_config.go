// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/emiago/diago/media"
	"github.com/pion/webrtc/v3"
)

var defaultMediaCodecs = []media.Codec{
	media.CodecAudioAlaw,
	media.CodecAudioUlaw,
	media.CodecTelephoneEvent8000,
}

func parseMediaCodecs(raw string) ([]media.Codec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return append([]media.Codec(nil), defaultMediaCodecs...), nil
	}

	codecNames := strings.Split(raw, ",")
	codecs := make([]media.Codec, 0, len(codecNames))
	for _, codecName := range codecNames {
		codecName = strings.TrimSpace(codecName)
		if codecName == "" {
			return nil, fmt.Errorf("MEDIA_CODECS contains an empty codec name")
		}

		codec, ok := mapMediaCodec(codecName)
		if !ok {
			return nil, fmt.Errorf("MEDIA_CODECS contains unsupported codec %q", codecName)
		}
		codecs = append(codecs, codec)
	}

	return codecs, nil
}

func mapMediaCodec(name string) (media.Codec, bool) {
	for _, codec := range defaultMediaCodecs {
		if codec.Name == name {
			return codec, true
		}
	}
	return media.Codec{}, false
}

func (e EnvConfig) mediaCodecs() ([]media.Codec, error) {
	return parseMediaCodecs(e.MediaCodecs)
}

func parseWebrtcICEServers(raw string) ([]webrtc.ICEServer, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var iceServers []webrtc.ICEServer
	if err := json.Unmarshal([]byte(raw), &iceServers); err != nil {
		return nil, fmt.Errorf("failed to parse WEBRTC_ICE_SERVERS: %w", err)
	}
	for i, server := range iceServers {
		if len(server.URLs) == 0 {
			return nil, fmt.Errorf("WEBRTC_ICE_SERVERS[%d] must include at least one URL", i)
		}
	}
	return iceServers, nil
}

func (e EnvConfig) webrtcConfiguration() (webrtc.Configuration, error) {
	iceServers, err := parseWebrtcICEServers(e.WebrtcICEServers)
	if err != nil {
		return webrtc.Configuration{}, err
	}
	if len(iceServers) == 0 {
		return webrtc.Configuration{}, nil
	}
	return webrtc.Configuration{
		ICEServers:           iceServers,
		ICETransportPolicy:   webrtc.ICETransportPolicyAll,
		BundlePolicy:         webrtc.BundlePolicyMaxBundle,
		SDPSemantics:         webrtc.SDPSemanticsUnifiedPlanWithFallback,
		ICECandidatePoolSize: 5,
	}, nil
}
