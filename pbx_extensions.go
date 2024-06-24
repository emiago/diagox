// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"gitlab.com/emiagox/diagox/testdata"

	"github.com/emiago/diago"
	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo/sip"
)

func (cx *CallContext) Playback(inDialog *diago.DialogServerSession) {
	inDialog.Trying()  // Progress -> 100 Trying
	inDialog.Ringing() // Ringing -> 180 Response

	med, err := cx.answer(inDialog)
	if err != nil {
		log.Error("Answering failed", "error", err)
		return
	} // Answqer -> 200 Response
	defer med.Close()

	playfile, _ := testdata.OpenFile("demo-instruct.wav")
	log.Info("Playing a file", "file", "demo-instruct.wav")

	pb, err := med.PlaybackCreate()
	if err != nil {
		log.Error("Failed to create playback", "error", err)
		return
	}

	if _, err := pb.Play(playfile, "audio/wav"); err != nil {
		if errors.Is(err, net.ErrClosed) {
			return
		}
		log.Error("Playing failed", "error", err)
	}
}

func (cx *CallContext) PlaybackWebrtc(inDialog *diago.DialogServerSession) {
	inDialog.Trying()  // Progress -> 100 Trying
	inDialog.Ringing() // Ringing -> 180 Response

	med, err := inDialog.AnswerWebrtc(diago.AnswerWebrtcOptions{})
	if err != nil {
		log.Error("Answering failed", "error", err)
		return
	} // Answqer -> 200 Response
	defer med.Close()

	playfile, _ := testdata.OpenFile("demo-instruct.wav")
	log.Info("Playing a file", "file", "demo-instruct.wav")

	pb, err := med.PlaybackCreate()
	if err != nil {
		log.Error("Failed to create playback", "error", err)
		return
	}

	if _, err := pb.Play(playfile, "audio/wav"); err != nil {
		if errors.Is(err, net.ErrClosed) {
			return
		}
		log.Error("Playing failed", "error", err)
	}
}

func (pbx *PBX) ExternalMedia(inDialog *diago.DialogServerSession) {
	inDialog.Progress()                                // Progress -> 100 Trying
	inDialog.Ringing()                                 // Ringing -> 180 Response
	med, err := inDialog.Answer(diago.AnswerOptions{}) // Answer -> 200 Response
	if err != nil {
		log.Error("Answering failed", "error", err)
		return
	}
	defer med.Close()

	lastPrint := time.Now()
	pktsCount := 0
	buf := make([]byte, media.RTPBufSize)
	for {
		_, err := med.RTPPacketReader.Read(buf)
		if err != nil {
			return
		}
		pkt := med.RTPPacketReader.PacketHeader

		if time.Since(lastPrint) > 3*time.Second {
			lastPrint = time.Now()
			log.Info("Received packets", "PayloadType", pkt.PayloadType, "pkts", pktsCount)
		}
		pktsCount++
	}
}

func (pbx *PBX) WebrtcBridge(inDialog *diago.DialogServerSession) error {
	ctx := inDialog.Context()

	inDialog.Progress()
	inDialog.Ringing()

	bridge := diago.NewBridge()

	var inWebrtc *diago.DialogWebrtc
	outDialog, outMedia, err := pbx.inviteBridge(ctx, inDialog.ToUser(), &bridge, diago.InviteOptions{
		OnResponse: func(res *sip.Response) error {
			if res.StatusCode != 200 {
				return nil
			}
			var err error
			inWebrtc, err = inDialog.AnswerWebrtc(diago.AnswerWebrtcOptions{})
			if err != nil {
				return err
			}
			return bridge.AddDialogWebrtc(inWebrtc)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to dial: %w", err)
	}
	defer outDialog.Close()
	if outMedia != nil {
		defer outMedia.Close()
	}
	if inWebrtc != nil {
		defer inWebrtc.Close()
	}

	outCtx := outDialog.Context()
	defer func() {
		hctx, hcancel := context.WithTimeout(outCtx, 5*time.Second)
		defer hcancel()
		if err := outDialog.Hangup(hctx); err != nil {
			log.Error("Failed to hangup", "error", err)
		}
	}()

	// This is beauty, as you can even easily detect who hangups
	inCtx := inDialog.Context()
	select {
	case <-inCtx.Done():
	case <-outCtx.Done():
	}

	return nil
}
