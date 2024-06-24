// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"sync/atomic"

	"github.com/arl/statsviz"
)

var metricNumberOfCallsActive = atomic.Int64{}

var metricMediaRoundTripTime = atomic.Int64{}
var metricMediaRoundTripTimeGauge = atomic.Int64{}
var metricMediaReadRTPPackets = atomic.Int64{}
var metricMediaWriteRTPPackets = atomic.Int64{}
var metricMediaJitter = atomic.Int64{}
var metricMediaPacketLoss = atomic.Uint64{}
var metricMediaCount = atomic.Int64{}

func plotNumberOfCalls() statsviz.TimeSeriesPlot {
	// Describe the 'sine' time series.
	sine := statsviz.TimeSeries{
		Name:    "number of calls",
		Unitfmt: "%{y:.4s}B",
		GetValue: func() float64 {
			return float64(metricNumberOfCallsActive.Load())
		},
	}

	// Build a new plot, showing our sine time series
	plot, err := statsviz.TimeSeriesPlotConfig{
		Name:       "number of calls",
		Title:      "Number of calls",
		Type:       statsviz.Scatter,
		InfoText:   `<b>RTT</b> for round trip time. <i>Media quality</i>`,
		YAxisTitle: "y number",
		Series:     []statsviz.TimeSeries{sine},
	}.Build()
	if err != nil {
		panic(err)
	}

	return plot
}

func plotRoundTripTime() statsviz.TimeSeriesPlot {
	// Describe the 'avg' time series.
	avg := statsviz.TimeSeries{
		Name:    "RTT AVG",
		Unitfmt: ".4f",
		GetValue: func() float64 {
			dur := metricMediaRoundTripTime.Load()
			total := max(1, metricMediaCount.Load())
			return float64(dur) / float64(total)
		},
	}

	last := statsviz.TimeSeries{
		Name:    "RTT LAST VAL",
		Unitfmt: ".4f",
		GetValue: func() float64 {
			dur := metricMediaRoundTripTimeGauge.Load()
			return float64(dur)
		},
	}

	// Build a new plot, showing our sine time series
	plot, err := statsviz.TimeSeriesPlotConfig{
		Name:       "round trip time",
		Title:      "RTP Round Trip Time",
		Type:       statsviz.Scatter,
		InfoText:   `<b>RTT</b> for round trip time. <i>Media quality</i>`,
		YAxisTitle: "y ms",
		Series:     []statsviz.TimeSeries{avg, last},
	}.Build()
	if err != nil {
		panic(err)
	}

	return plot
}

func plotJitter() statsviz.TimeSeriesPlot {
	// Describe the 'sine' time series.
	sine := statsviz.TimeSeries{
		Name:    "jitter",
		Unitfmt: ".4f",
		GetValue: func() float64 {
			dur := metricMediaJitter.Load()
			total := max(1, metricMediaCount.Load())
			return float64(dur) / float64(total)
		},
	}

	// Build a new plot, showing our sine time series
	plot, err := statsviz.TimeSeriesPlotConfig{
		Name:       "jitter",
		Title:      "RTP Jitter",
		Type:       statsviz.Scatter,
		InfoText:   `<b>RTT</b> for round trip time. <i>Media quality</i>`,
		YAxisTitle: "y ms",
		Series:     []statsviz.TimeSeries{sine},
	}.Build()
	if err != nil {
		panic(err)
	}

	return plot
}

func plotFractionLoss() statsviz.TimeSeriesPlot {
	// Describe the 'sine' time series.
	sine := statsviz.TimeSeries{
		Name:    "fraction lost",
		Unitfmt: ".4f",
		GetValue: func() float64 {
			dur := metricMediaPacketLoss.Load()
			total := max(1, metricMediaCount.Load())
			return float64(dur) / float64(total)
		},
	}

	// Build a new plot, showing our sine time series
	plot, err := statsviz.TimeSeriesPlotConfig{
		Name:       "fraction lost",
		Title:      "RTP fraction lost",
		Type:       statsviz.Scatter,
		InfoText:   `<b>RTT</b> for round trip time. <i>Media quality</i>`,
		YAxisTitle: "y ms",
		Series:     []statsviz.TimeSeries{sine},
	}.Build()
	if err != nil {
		panic(err)
	}

	return plot
}

func plotPacketsCount() statsviz.TimeSeriesPlot {
	// Describe the 'sine' time series.
	recvSerie := statsviz.TimeSeries{
		Name:    "rtp recv packets",
		Unitfmt: "%{y:.4s}B",
		GetValue: func() float64 {
			return float64(metricMediaReadRTPPackets.Load())
		},
	}

	sendSerie := statsviz.TimeSeries{
		Name:    "rtp send packets",
		Unitfmt: "%{y:.4s}B",
		GetValue: func() float64 {
			return float64(metricMediaReadRTPPackets.Load())
		},
	}

	// Build a new plot, showing our sine time series
	plot, err := statsviz.TimeSeriesPlotConfig{
		Name:       "rtp send/recv packets",
		Title:      "RTP SEND RECV",
		Type:       statsviz.Scatter,
		InfoText:   `RTP sent and received packets`,
		YAxisTitle: "y number",
		Series:     []statsviz.TimeSeries{recvSerie, sendSerie},
	}.Build()
	if err != nil {
		panic(err)
	}

	return plot
}
