// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

type testRateLimiterIn struct{}

func (testRateLimiterIn) DialogActiveLimit() bool {
	return true
}

func (testRateLimiterIn) DialogActiveDec() {}

func (testRateLimiterIn) DialogRPSLimit() bool {
	return true
}

type testRateLimiterOut struct{}

func (testRateLimiterOut) Block(*slog.Logger, context.Context) error {
	return nil
}

func (testRateLimiterOut) DialogRPSLimit() bool {
	return true
}

func TestNewDefaultProvidersRateLimiters(t *testing.T) {
	providers := NewDefaultProviders(EnvConfig{
		RateLimiterIn: RateLimiterIncomingConfig{
			Enabled:   true,
			DialogRPS: 10,
			DialogMax: 20,
		},
		RateLimiterOut: RateLimiterOutgoingConfig{
			Enabled:   true,
			DialogRPS: 30,
		},
	})

	require.NotNil(t, providers.RateIn)
	require.Equal(t, int64(10), providers.RateIn.DialogRPS)
	require.Equal(t, int64(20), providers.RateIn.DialogMax)
	require.NotNil(t, providers.RateIn.RateLimiterIn)
	require.NotNil(t, providers.RateOut)
	require.Equal(t, int64(30), providers.RateOut.DialogRPS)
	require.NotNil(t, providers.RateOut.RateLimiterOut)
}

func TestNewPBXInitializesRateLimitersFromDefaultProviders(t *testing.T) {
	pbx := NewPBX(EnvConfig{
		RateLimiterIn: RateLimiterIncomingConfig{
			Enabled:   true,
			DialogRPS: 10,
			DialogMax: 20,
		},
		RateLimiterOut: RateLimiterOutgoingConfig{
			Enabled:   true,
			DialogRPS: 30,
		},
	}, Config{})

	require.NotNil(t, pbx.rateLimiterInc)
	require.Equal(t, int64(10), pbx.rateLimiterInc.DialogRPS)
	require.Equal(t, int64(20), pbx.rateLimiterInc.DialogMax)
	require.NotNil(t, pbx.rateLimiterInc.RateLimiterIn)
	require.NotNil(t, pbx.rateLimiterOut)
	require.Equal(t, int64(30), pbx.rateLimiterOut.DialogRPS)
	require.NotNil(t, pbx.rateLimiterOut.RateLimiterOut)
}

func TestApplyProvidersRateLimiters(t *testing.T) {
	pbx := NewPBX(EnvConfig{}, Config{})
	rateIn := &DialogRateLimiterIncoming{
		RateLimiterIn: testRateLimiterIn{},
	}
	rateOut := &DialogRateLimiterOutgoing{
		RateLimiterOut: testRateLimiterOut{},
	}

	pbx.ApplyProviders(Providers{
		RateIn:  rateIn,
		RateOut: rateOut,
	})

	require.Same(t, rateIn, pbx.rateLimiterInc)
	require.Same(t, rateOut, pbx.rateLimiterOut)
}
