// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRateLimiterIncoming(t *testing.T) {
	limiter := RateLimiterAtomic{
		Max: 100,
		RPS: 10,
	}

	// Make 10 allowed requests
	for i := 0; i < 10; i++ {
		require.True(t, limiter.DialogRPSLimit(), "request %d within limit should have been allowed", i+1)
	}

	require.False(t, limiter.DialogRPSLimit(), "request exceeding limit should have been denied")

	// Make rate limiter older
	limiter.rateTimestamp = time.Now().Unix() - 1
	// After the second changes, requests should again be allowed
	require.True(t, limiter.DialogRPSLimit(), "request after timestamp change should have been allowed")
}

func TestRateLimiterOutgoing(t *testing.T) {
	limiter := RateLimiterAtomic{
		RPS: 10,
	}

	// Make 10 allowed requests
	for i := 0; i < 10; i++ {
		require.NoError(t, limiter.Block(log, context.TODO()), "request %d within limit should have been allowed", i+1)
	}

	require.False(t, limiter.DialogRPSLimit(), "request exceeding limit should have been denied")

	// Request will timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	limiter.rateTimestamp = time.Now().Unix()
	require.Error(t, limiter.Block(log, ctx))
}
