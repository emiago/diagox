// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"
)

var (
	DirectionIn  = "IN"
	DirectionOut = "OUT"
)

type CDR struct {
	Direction        string           `json:"direction"`
	CallID           string           `json:"callId"`
	OriginatorCallID string           `json:"originatorCallId"`
	StartTime        time.Time        `json:"startTime"`
	Duration         time.Duration    `json:"duration"`
	CallerID         string           `json:"callerId"`
	CalleeID         string           `json:"calleeId"`
	Bill             float64          `json:"bill"`
	Disposition      string           `json:"disposition"`
	MediaStats       DialogMediaStats `json:"mediaStats"`
	MES              float64          `json:"mes"` // Media Experience Score
	RecordingID      string           `json:"recordingId"`
	Bridged          bool             `json:"bridged"`

	StartTimeFormated string `json:"startTimeFormatted"`
}

type CDRReadOptions struct {
	Offset           int
	FromTime         time.Time
	ToTime           time.Time
	From             string
	To               string
	CallID           string
	OriginatorCallID string
	Mes              float64
}

type CDRStorage interface {
	CDRWrite(ctx context.Context, cdr CDR) error
	CDRRead(ctx context.Context, buf []CDR, opts CDRReadOptions) (n int, err error)

	// CDRUpdate(ctx context.Context, t time.Time) error
}

type CDRMemoryStorage struct {
	mu   sync.Mutex
	data []CDR
	ind  int
	end  int
}

func NewCDRMemoryStorage() *CDRMemoryStorage {
	return &CDRMemoryStorage{
		data: make([]CDR, 10000),
	}
}

func (s *CDRMemoryStorage) CDRWrite(ctx context.Context, cdr CDR) error {
	s.mu.Lock()
	if s.ind == len(s.data) {
		s.ind = 0
	}

	if cdr.StartTimeFormated == "" {
		cdr.StartTimeFormated = cdr.StartTime.Format(time.RFC3339)
	}

	s.data[s.ind] = cdr
	s.end = s.ind
	s.ind++
	s.mu.Unlock()
	return nil
}

func (s *CDRMemoryStorage) CDRRead(ctx context.Context, buf []CDR, opts CDRReadOptions) (n int, err error) {
	// we need reverse reading
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := s.end; i >= 0 && n < len(buf); {
		d := s.data[i]
		i--
		if d.StartTime.IsZero() {
			// Consider not set
			break
		}

		if !opts.FromTime.IsZero() && !opts.ToTime.IsZero() {
			if d.StartTime.Compare(opts.FromTime) < 0 {
				continue
			}

			if d.StartTime.Compare(opts.ToTime) > 0 {
				continue
			}
		}

		if opts.From != "" && !strings.Contains(d.CallerID, opts.From) {
			continue
		}

		if opts.To != "" && !strings.Contains(d.CalleeID, opts.To) {
			continue
		}

		if opts.CallID != "" && !strings.Contains(d.CallID, opts.CallID) {
			continue
		}

		if opts.OriginatorCallID != "" && !strings.Contains(d.CallID, opts.OriginatorCallID) {
			continue
		}

		buf[n] = d
		n++
	}

	return n, nil
}

type CDRPiper struct {
	CDRStorage
	Pipe chan CDR
}

func NewCDRPiper() *CDRPiper {
	return &CDRPiper{Pipe: make(chan CDR)}
}

func (s *CDRPiper) CDRWrite(ctx context.Context, cdr CDR) error {
	select {
	case s.Pipe <- cdr:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type SIPTracer interface {
	sip.SIPTracer
	SIPTraceFind(callID string, f func(m SIPTrace))
}

type SIPTrace struct {
	Created          time.Time
	RW               int // 0 read 1 write
	Transport        string
	Laddr            string
	Raddr            string
	CallID           string
	OriginatorCallID string
	Msg              string
}

type SipTracerMemoryStorage struct {
	mu   sync.Mutex
	data []SIPTrace
	ind  int
	end  int
	log  *slog.Logger
}

func NewSipTracerMemoryStorage() *SipTracerMemoryStorage {
	return &SipTracerMemoryStorage{
		data: make([]SIPTrace, 10000), // Store max 100000
		log:  slog.Default().With("caller", "siptracer"),
	}
}

func parseSIPTrace(trace *SIPTrace, transport string, laddr string, raddr string, sipmsg []byte) {

	getHeaderVal := func(headerName string) string {
		ind := bytes.Index(sipmsg, []byte(headerName+":"))
		if ind < 0 {
			slog.Debug("no header in sip message", "header", headerName)
			return ""
		}

		end := bytes.Index(sipmsg[ind:], []byte("\r"))

		callid := string(sipmsg[ind : ind+end])
		callid = strings.TrimLeft(callid, "Call-ID:")
		callid = strings.TrimSpace(callid)
		return callid
	}

	callId := getHeaderVal("Call-ID")
	if callId == "" {
		slog.Error("No callid in sip message")
		return
	}
	linkedID := getHeaderVal("X-Orig-Call-ID")

	// log.Debug("Storing trace", "callid", callid)
	*trace = SIPTrace{
		Created:          time.Now(),
		RW:               trace.RW,
		Transport:        transport,
		Laddr:            laddr,
		Raddr:            raddr,
		CallID:           callId,
		OriginatorCallID: linkedID,
		Msg:              string(sipmsg),
	}
}

func ParseSIPTrace(trace *SIPTrace, transport string, laddr string, raddr string, sipmsg []byte) {
	parseSIPTrace(trace, transport, laddr, raddr, sipmsg)
}

func SIPTraceDebug(transport string, laddr string, raddr string, sipmsg []byte, rw int) {
	sipTraceDebug(transport, laddr, raddr, sipmsg, rw)
}

func (s *SipTracerMemoryStorage) SIPTraceRead(transport string, laddr string, raddr string, sipmsg []byte) {
	sipTraceDebug(transport, laddr, raddr, sipmsg, 0)
	trace := SIPTrace{
		RW: 0,
	}
	parseSIPTrace(&trace, transport, laddr, raddr, sipmsg)
	s.writeTrace(trace)
}

func (s *SipTracerMemoryStorage) SIPTraceWrite(transport string, laddr string, raddr string, sipmsg []byte) {
	sipTraceDebug(transport, laddr, raddr, sipmsg, 1)

	trace := SIPTrace{
		RW: 1,
	}
	parseSIPTrace(&trace, transport, laddr, raddr, sipmsg)
	s.writeTrace(trace)
}

func (s *SipTracerMemoryStorage) writeTrace(trace SIPTrace) {
	s.mu.Lock()
	if s.ind == len(s.data) {
		s.ind = 0
	}

	s.data[s.ind] = trace
	s.end = s.ind
	s.ind++
	s.mu.Unlock()
}

func (s *SipTracerMemoryStorage) SIPTraceFind(callID string, f func(m SIPTrace)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, v := range s.data {
		if v.CallID == callID || v.OriginatorCallID == callID {
			s.mu.Unlock()
			f(v)
			s.mu.Lock()
		}
	}
}

var sipTrace bool

func sipTraceDebug(transport string, laddr string, raddr string, sipmsg []byte, rw int) {
	if !sipTrace {
		return
	}

	if rw > 0 {
		fmt.Printf("%s: %s write to -> %s\n%s", transport, laddr, raddr, sipmsg)
		return
	}
	fmt.Printf("%s: %s read from <- %s\n%s", transport, laddr, raddr, sipmsg)
}
