// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import "github.com/prometheus/client_golang/prometheus"

var (
	metricDialogsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "diagox",
		Name:      "dialogs_active",
		Help:      "Current number of inbound dialogs being handled by Diagox.",
	})
	metricDialogsStarted = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "diagox",
		Name:      "dialogs_started_total",
		Help:      "Total number of inbound dialogs accepted by Diagox.",
	})
	metricDialogsAnswered = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "diagox",
		Name:      "dialogs_answered_total",
		Help:      "Total number of inbound dialogs that reached the confirmed state.",
	})
	metricDialogsEnded = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "diagox",
		Name:      "dialogs_ended_total",
		Help:      "Total number of inbound dialog handlers that finished.",
	})
	metricRTPPacketsRead = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "diagox",
		Name:      "rtp_packets_read_total",
		Help:      "Total RTP packets reported as received by RTCP.",
	})
	metricRTPPacketsWritten = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "diagox",
		Name:      "rtp_packets_written_total",
		Help:      "Total RTP packets reported as sent by RTCP.",
	})
	metricRTPRTT = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "diagox",
		Name:      "rtp_round_trip_time_seconds",
		Help:      "Observed RTP round-trip times in seconds.",
		Buckets:   prometheus.DefBuckets,
	})
	metricRTPRTTLast = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "diagox",
		Name:      "rtp_round_trip_time_last_seconds",
		Help:      "Most recently observed RTP round-trip time in seconds.",
	})
	metricRTPJitter = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "diagox",
		Name:      "rtp_jitter_seconds",
		Help:      "Observed RTP jitter values in seconds.",
		Buckets:   prometheus.DefBuckets,
	})
	metricRTPJitterLast = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "diagox",
		Name:      "rtp_jitter_last_seconds",
		Help:      "Most recently observed RTP jitter in seconds.",
	})
	metricRTPPacketLossRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "diagox",
		Name:      "rtp_packet_loss_ratio",
		Help:      "Most recently observed RTP packet-loss fraction, from zero to one.",
	})
)

func init() {
	prometheus.MustRegister(
		metricDialogsActive,
		metricDialogsStarted,
		metricDialogsAnswered,
		metricDialogsEnded,
		metricRTPPacketsRead,
		metricRTPPacketsWritten,
		metricRTPRTT,
		metricRTPRTTLast,
		metricRTPJitter,
		metricRTPJitterLast,
		metricRTPPacketLossRatio,
	)
}
