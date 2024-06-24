// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"strings"
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testParseConfig(t testing.TB, y string) Config {
	y = strings.TrimSpace(y)
	c := Config{}
	err := ConfigLoad(strings.NewReader(y), &c)
	require.NoError(t, err)
	return c
}
func TestRoutingUser(t *testing.T) {
	y := `
endpoints:
  alice: # -> alice@tenant1.domain.com
    did: 4912321314 # Whatever is assigned. When saving this will be checked does exists
    match: 
      type: "user" 
    auth:
      username: "carrier" 
      password: "t3st123"
  123456:
    name: "1234567"
    did: 4912321314 # Whatever is assigned. When saving this will be checked does exists
    match: 
      type: "user" 
    auth:
      username: "carrier" 
      password: "t3st123"`

	conf := testParseConfig(t, y)
	t.Log(conf)

	r := Router{conf}
	_, exists := r.findUserEndpoint("alice")
	assert.True(t, exists)
	_, exists = r.findUserEndpoint("1234567")
	assert.True(t, exists)

}

func TestRoutingMatchEndpoint(t *testing.T) {
	y := `
endpoints:
  alice: # -> alice@tenant1.domain.com
    did: 4912321314 # Whatever is assigned. When saving this will be checked does exists
    match: 
      type: "user" 
    auth:
      username: "carrier" 
      password: "t3st123"
  carrier_internal:
    match: 
      type: "ip"
      values: ["192.168.10.10/16", "10.5.1.1"]
    auth:
      username: "carrier" 
      password: "t3st123"
`

	conf := testParseConfig(t, y)
	r := Router{conf}

	t.Run("user", func(t *testing.T) {
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Alice <sip:alice@localhost>"))
		req.SetSource("192.168.100.1:48132")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)
		require.True(t, exists)
		assert.Equal(t, "alice", end.Name)
		assert.Equal(t, "user", end.Match.Type)
	})

	t.Run("ip", func(t *testing.T) {
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Unknown <sip:unknown@localhost>"))
		req.SetSource("192.168.100.1:48132")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)
		require.True(t, exists)
		assert.Equal(t, "carrier_internal", end.Name)
		assert.Equal(t, "ip", end.Match.Type)
	})

}

func TestRoutingMatchEndpointIPIndex(t *testing.T) {
	t.Run("exactIP", func(t *testing.T) {
		conf := testParseConfig(t, `
endpoints:
  carrier:
    match:
      type: "ip"
      values: ["10.5.1.1"]
`)
		r := Router{conf}
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Unknown <sip:unknown@localhost>"))
		req.SetSource("10.5.1.1:48132")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)

		require.True(t, exists)
		assert.Equal(t, "carrier", end.Name)
	})

	t.Run("cidrOrderWins", func(t *testing.T) {
		conf := testParseConfig(t, `
endpoints:
  broad:
    match:
      type: "ip"
      values: ["10.0.0.0/8"]
  exact:
    match:
      type: "ip"
      values: ["10.5.1.1"]
`)
		r := Router{conf}
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Unknown <sip:unknown@localhost>"))
		req.SetSource("10.5.1.1:48132")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)

		require.True(t, exists)
		assert.Equal(t, "broad", end.Name)
	})

	t.Run("invalidIPValue", func(t *testing.T) {
		var conf Config
		err := ConfigLoad(strings.NewReader(`
endpoints:
  carrier:
    match:
      type: "ip"
      values: ["not-an-ip"]
`), &conf)

		require.Error(t, err)
	})
}

func TestRoutingMatchEndpointTransport(t *testing.T) {
	t.Run("ipTransport", func(t *testing.T) {
		conf := testParseConfig(t, `
endpoints:
  udp_carrier:
    match:
      type: "ip"
      transport: "udp"
      values: ["10.5.1.1"]
  tcp_carrier:
    match:
      type: "ip"
      transport: "tcp"
      values: ["10.5.1.1"]
`)
		r := Router{conf}
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Unknown <sip:unknown@localhost>"))
		req.SetSource("10.5.1.1:48132")
		req.SetTransport("TCP")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)

		require.True(t, exists)
		assert.Equal(t, "tcp_carrier", end.Name)
	})

	t.Run("userTransport", func(t *testing.T) {
		conf := testParseConfig(t, `
endpoints:
  alice_udp:
    name: "alice"
    match:
      type: "user"
      transport: "udp"
  alice_tcp:
    name: "alice"
    match:
      type: "user"
      transport: "tcp"
`)
		r := Router{conf}
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Alice <sip:alice@localhost>"))
		req.SetSource("10.5.1.1:48132")
		req.SetTransport("TCP")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)

		require.True(t, exists)
		assert.Equal(t, "alice", end.Name)
		assert.Equal(t, "tcp", end.Match.Transport)
	})

	t.Run("transportMismatch", func(t *testing.T) {
		conf := testParseConfig(t, `
endpoints:
  udp_carrier:
    match:
      type: "ip"
      transport: "udp"
      values: ["10.5.1.1"]
`)
		r := Router{conf}
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Unknown <sip:unknown@localhost>"))
		req.SetSource("10.5.1.1:48132")
		req.SetTransport("TCP")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)

		require.False(t, exists)
		assert.Empty(t, end.Name)
	})
}

func TestRoutingMatchEndpointDynamicUser(t *testing.T) {
	y := `
endpoints:
  alice:
    match:
      type: "user"
  internal:
    match:
      type: "ip"
      values: ["192.168.10.10/16"]
  webrtc_users:
    match:
      type: "user_dynamic"
  fallback:
    route: default
`

	conf := testParseConfig(t, y)
	r := Router{conf}

	t.Run("exactUserWins", func(t *testing.T) {
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Alice <sip:alice@localhost>"))
		req.SetSource("10.0.0.1:48132")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)
		require.True(t, exists)
		assert.Equal(t, "alice", end.Name)
	})

	t.Run("ipWins", func(t *testing.T) {
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Carol <sip:carol@localhost>"))
		req.SetSource("192.168.100.1:48132")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)
		require.True(t, exists)
		assert.Equal(t, "internal", end.Name)
	})

	t.Run("dynamicUserWins", func(t *testing.T) {
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Carol <sip:carol@localhost>"))
		req.SetSource("10.0.0.1:48132")

		end := ConfigEndpoint{}
		exists := r.MatchEndpoint(req, &end)
		require.True(t, exists)
		assert.Equal(t, "webrtc_users", end.Name)
		assert.Equal(t, "user_dynamic", end.Match.Type)
	})
}

func TestRoutingMatchDID(t *testing.T) {
	y := `
endpoints:
  alice: # -> alice@tenant1.domain.com
    did: 4912321314 # Whatever is assigned. When saving this will be checked does exists
    match: 
      type: "user" 
    auth:
      username: "carrier" 
      password: "t3st123"
  carrier:
    match: 
      type: "ip"
      values: ["192.168.10.10", "10.5.1.1"]
    auth:
      username: "carrier" 
      password: "t3st123"

routes:
  incoming:
    - id: 49123456789
      endpoint: carrier
    - id: alice
      endpoint: alice
    - id: "affix"
      match: any	
      endpoint: carrier
    - id: ""
      match: any	
      endpoint: carrier
`

	conf := testParseConfig(t, y)
	r := Router{conf}

	t.Run("MatchE164", func(t *testing.T) {
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "49123456789", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Alice <sip:alice@localhost>"))
		req.AppendHeader(sip.NewHeader("To", "Alice <sip:49123456789@localhost>"))

		end := ConfigEndpoint{}
		route := ConfigRoute{}
		exists := r.MatchIncomingRoute(req, "incoming", &route)
		require.True(t, exists)
		exists = r.MatchOutgoingRouteEndpoint(req, &route, &end)
		require.True(t, exists)

		assert.Equal(t, "carrier", end.Name)
		assert.Equal(t, "49123456789", route.ID)
	})

	t.Run("MatchAsterisk", func(t *testing.T) {
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "112233", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Alice <sip:alice@localhost>"))
		req.AppendHeader(sip.NewHeader("To", "Alice <sip:112233@localhost>"))

		end := ConfigEndpoint{}
		route := ConfigRoute{}
		exists := r.MatchIncomingRoute(req, "incoming", &route)
		require.True(t, exists)

		exists = r.MatchOutgoingRouteEndpoint(req, &route, &end)
		require.True(t, exists)

		assert.Equal(t, "carrier", end.Name)
		assert.Equal(t, "", route.ID)
	})

	t.Run("MatchAffix", func(t *testing.T) {
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "11affix123", Host: "localhost"})
		req.AppendHeader(sip.NewHeader("From", "Alice <sip:alice@localhost>"))
		req.AppendHeader(sip.NewHeader("To", "Affix <sip:11affix123@localhost>"))

		end := ConfigEndpoint{}
		route := ConfigRoute{}
		exists := r.MatchIncomingRoute(req, "incoming", &route)
		require.True(t, exists)

		exists = r.MatchOutgoingRouteEndpoint(req, &route, &end)
		require.True(t, exists)

		assert.Equal(t, "carrier", end.Name)
		assert.Equal(t, "affix", route.ID)
	})

}

func TestRoutingMatchDynamicRegistryEndpoint(t *testing.T) {
	y := `
endpoints:
  webrtc_users:
    match:
      type: "user_dynamic"
    media:
      type: "webrtc"
routes:
  incoming:
    - id: ""
      match: "any"
      use_registry: true
      endpoint: webrtc_users
`

	conf := testParseConfig(t, y)
	r := Router{conf}
	req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: "localhost"})
	req.AppendHeader(sip.NewHeader("To", "Alice <sip:alice@localhost>"))

	route := ConfigRoute{UseRegistry: true, EndpointName: "webrtc_users"}
	end := ConfigEndpoint{}
	exists := r.MatchOutgoingRouteEndpoint(req, &route, &end)

	require.True(t, exists)
	assert.Equal(t, "alice", end.Name)
	assert.Equal(t, "user_dynamic", end.Match.Type)
	assert.Equal(t, "webrtc", end.Media.Type)
}
