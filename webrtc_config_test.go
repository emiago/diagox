// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"testing"

	"github.com/emiago/diago/media"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMediaCodecs(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		codecs, err := parseMediaCodecs("")
		require.NoError(t, err)
		assert.Equal(t, defaultMediaCodecs, codecs)
	})

	t.Run("ordered", func(t *testing.T) {
		codecs, err := parseMediaCodecs("PCMU,PCMA,telephone-event")
		require.NoError(t, err)
		assert.Equal(t, []media.Codec{
			media.CodecAudioUlaw,
			media.CodecAudioAlaw,
			media.CodecTelephoneEvent8000,
		}, codecs)
	})

	t.Run("opus", func(t *testing.T) {
		codecs, err := parseMediaCodecs("opus")
		require.NoError(t, err)
		assert.Equal(t, []media.Codec{media.CodecAudioOpus}, codecs)
	})

	t.Run("unknown", func(t *testing.T) {
		_, err := parseMediaCodecs("PCMU,G722")
		require.Error(t, err)
	})

	t.Run("alias", func(t *testing.T) {
		_, err := parseMediaCodecs("ulaw")
		require.Error(t, err)
	})

	t.Run("caseSensitive", func(t *testing.T) {
		_, err := parseMediaCodecs("pcmu")
		require.Error(t, err)
	})

	t.Run("emptyName", func(t *testing.T) {
		_, err := parseMediaCodecs("PCMU,,PCMA")
		require.Error(t, err)
	})
}

func TestParseWebrtcICEServers(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		servers, err := parseWebrtcICEServers("")
		require.NoError(t, err)
		assert.Empty(t, servers)
	})

	t.Run("valid", func(t *testing.T) {
		servers, err := parseWebrtcICEServers(`[{"urls":["stun:turn.example.com:3478"]},{"urls":["turn:turn.example.com:3478?transport=udp"],"username":"user","credential":"pass"}]`)
		require.NoError(t, err)
		require.Len(t, servers, 2)
		assert.Equal(t, []string{"stun:turn.example.com:3478"}, servers[0].URLs)
		assert.Equal(t, "user", servers[1].Username)
		assert.Equal(t, "pass", servers[1].Credential)
	})

	t.Run("invalidJSON", func(t *testing.T) {
		_, err := parseWebrtcICEServers(`not-json`)
		require.Error(t, err)
	})

	t.Run("missingURL", func(t *testing.T) {
		_, err := parseWebrtcICEServers(`[{"username":"user"}]`)
		require.Error(t, err)
	})
}

func TestWebrtcConfiguration(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		conf, err := EnvConfig{}.webrtcConfiguration()
		require.NoError(t, err)
		assert.Empty(t, conf.ICEServers)
	})

	t.Run("configured", func(t *testing.T) {
		conf, err := EnvConfig{
			WebrtcICEServers: `[{"urls":["stun:turn.example.com:3478"]}]`,
		}.webrtcConfiguration()
		require.NoError(t, err)
		require.Len(t, conf.ICEServers, 1)
		assert.Equal(t, []string{"stun:turn.example.com:3478"}, conf.ICEServers[0].URLs)
		assert.Equal(t, "all", conf.ICETransportPolicy.String())
	})
}
