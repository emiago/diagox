// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/emiago/sipgo"
)

var (
	BridgeCache = sync.Map{}
)

type DialogData struct {
	ID                string
	NodeID            string
	Direction         uint8 // 0: Server, 1:Client
	InviteRequestData string
	Dialplan          string
	// InviteRequest     *sip.Request `json:"-"`
}

func NewDialogData(v *sipgo.Dialog, direction uint8) DialogData {
	d := DialogData{
		ID: v.ID,
		// InviteRequest:     v.InviteRequest,
		Direction:         direction,
		NodeID:            nodeID,
		Dialplan:          "playback",
		InviteRequestData: v.InviteRequest.String(),
	}

	// Apply last cseq number
	// d.InviteRequest.CSeq().SeqNo = v.CSEQ()
	return d
}

type DialogExternalCache interface {
	StoreDialog(ctx context.Context, data DialogData) error
	LoadDialog(ctx context.Context, id string) (DialogData, error)
	DeleteDialog(ctx context.Context, id string) error
	DeleteDialogNode(ctx context.Context, id string, nodeId string) error
	LoadDialogs(ctx context.Context, nodeID string) ([]DialogData, error)

	// StoreBridge(ctx context.Context, data BridgeData) error
	// LoadBridges(ctx context.Context, nodeID string) ([]BridgeData, error)
}

func CacheDumpBridges(f io.Writer) error {
	bridges := []BridgeData{}
	BridgeCache.Range(func(key, value any) bool {
		log.Info("Dumping bridge", "key", key)

		b := BridgeData{
			ID: key.(string),
		}

		bridges = append(bridges, b)

		return true
	})

	data, err := json.Marshal(bridges)
	if err != nil {
		return err
	}

	for nn := 0; nn < len(data); {
		n, err := f.Write(data)
		if err != nil {
			return err
		}
		nn += n
	}
	return err
}

// func CacheLoadDialogs(f io.Reader, failedNodeID string) ([]DialogData, error) {
// 	data, err := io.ReadAll(f)
// 	if err != nil {
// 		return nil, err
// 	}

// 	dialogs := []DialogData{}
// 	err = json.Unmarshal(data, &dialogs)
// 	if err != nil {
// 		log.Error().Err(err).Msg("failed to unmarshal")
// 		return nil, err
// 	}

// 	filtered := []DialogData{}
// 	for _, d := range dialogs {
// 		if d.NodeID == nodeID {
// 			log.Info().Str("id", d.ID).Msg("Skipping as it is our dialog")
// 			continue
// 		}

// 		if failedNodeID != "" {
// 			if failedNodeID != d.NodeID {
// 				log.Info().Str("id", d.ID).Str("failedNode", failedNodeID).Str("dialogNode", d.NodeID).Msg("Skipping as it is not dialog from failed node")
// 				continue
// 			}
// 		}

// 		req, err := sip.ParseMessage([]byte(d.InviteRequestData))
// 		if err != nil {
// 			return nil, err
// 		}
// 		if req == nil {
// 			panic("parsing returned nil request")
// 		}
// 		d.InviteRequest = req.(*sip.Request)
// 		filtered = append(filtered, d)
// 	}

// 	return filtered, nil
// }
