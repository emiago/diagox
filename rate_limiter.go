// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

type RateLimiterIn interface {
	DialogActiveLimit() bool
	DialogActiveDec()
	DialogRPSLimit() bool
}

type RateLimiterOut interface {
	Block(log *slog.Logger, ctx context.Context) error
	DialogRPSLimit() bool
}

type DialogRateLimiterIncoming struct {
	DialogMax int64
	DialogRPS int64

	RateLimiterIn
}

type DialogRateLimiterOutgoing struct {
	DialogRPS int64

	RateLimiterOut
}

func NewDialogRateLimiterIncoming(conf RateLimiterIncomingConfig) *DialogRateLimiterIncoming {
	if !conf.Enabled {
		return nil
	}

	return &DialogRateLimiterIncoming{
		DialogMax: conf.DialogMax,
		DialogRPS: conf.DialogRPS,
		RateLimiterIn: &RateLimiterAtomic{
			Max: conf.DialogMax,
			RPS: conf.DialogRPS,
		},
	}
}

func NewDialogRateLimiterOutgoing(conf RateLimiterOutgoingConfig) *DialogRateLimiterOutgoing {
	if !conf.Enabled {
		return nil
	}

	return &DialogRateLimiterOutgoing{
		DialogRPS: conf.DialogRPS,
		RateLimiterOut: &RateLimiterAtomic{
			RPS: conf.DialogRPS,
		},
	}
}

type RateLimiterAtomic struct {
	Max int64
	RPS int64

	rateCount     int64
	rateTimestamp int64
	activeCount   int64
}

// Checks Dialog RPS
func (r *RateLimiterAtomic) DialogActiveLimit() bool {
	if r.Max == 0 {
		return true
	}
	current := atomic.AddInt64(&r.activeCount, 1)
	if current < r.Max {
		return true
	}
	r.DialogActiveDec()
	return false
}

func (r *RateLimiterAtomic) DialogActiveDec() {
	if r.Max == 0 {
		return
	}
	atomic.AddInt64(&r.activeCount, -1)
}

// Checks Dialog RPS
func (r *RateLimiterAtomic) DialogRPSLimit() bool {
	if r.RPS == 0 {
		return true
	}

	currentSecond := time.Now().Unix()
	timestamp := &r.rateTimestamp
	counter := &r.rateCount

	// Atomically load the last processed timestamp
	lastSecond := atomic.LoadInt64(timestamp)

	if lastSecond != currentSecond {
		// This only make sure that we reset once, but relies on timestamp incremental
		if atomic.CompareAndSwapInt64(timestamp, lastSecond, currentSecond) {
			atomic.StoreInt64(counter, 0)
		}
	}

	return atomic.AddInt64(counter, 1) <= r.RPS // Request allowed
}

// Block based on current RPS limit
// It absorbs (blocks) until RPS drops down
func (r *RateLimiterAtomic) Block(log *slog.Logger, ctx context.Context) error {
	for {
		if r.DialogRPSLimit() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
			log.Debug("Blocking outgoing 1s due to high RPS")
		}
	}

}
