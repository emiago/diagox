// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigAuthType(t *testing.T) {
	t.Run("explicitBearer", func(t *testing.T) {
		conf := testParseConfig(t, `
endpoints:
  webrtc:
    match:
      type: "user_dynamic"
    auth:
      type: bearer
`)

		require.NoError(t, conf.Validate())
		assert.Equal(t, ConfigAuthTypeBearer, conf.Endpoints["webrtc"].Auth.AuthType())
	})

	t.Run("legacyDigest", func(t *testing.T) {
		conf := testParseConfig(t, `
endpoints:
  alice:
    match:
      type: "user"
    auth:
      username: alice
      password: secret
`)

		require.NoError(t, conf.Validate())
		assert.Equal(t, ConfigAuthTypeDigest, conf.Endpoints["alice"].Auth.AuthType())
	})

	t.Run("invalid", func(t *testing.T) {
		var conf Config
		err := ConfigLoad(strings.NewReader(`
endpoints:
  alice:
    match:
      type: "user"
    auth:
      type: oauth
`), &conf)
		require.NoError(t, err)
		require.Error(t, conf.Validate())
	})
}

func TestConfigDynamicUserEndpointValidation(t *testing.T) {
	var conf Config
	err := ConfigLoad(strings.NewReader(`
endpoints:
  webrtc_one:
    match:
      type: "user_dynamic"
  webrtc_two:
    match:
      type: "user_dynamic"
`), &conf)
	require.NoError(t, err)
	require.Error(t, conf.Validate())
}
