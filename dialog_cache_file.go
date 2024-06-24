// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/emiago/sipgo"
)

func dialogsFileName(nodeID string) string {
	return fmt.Sprintf("dialogs_%s.json", nodeID)
}

type DialogCacheFile struct {
	file   *os.File
	NodeID string

	mu      sync.Mutex
	dialogs map[string]DialogData
}

func NewDialogCacheFile(nodeID string) *DialogCacheFile {
	return &DialogCacheFile{
		NodeID:  nodeID,
		dialogs: map[string]DialogData{},
	}
}

func (c *DialogCacheFile) Init() error {
	cacheFile, err := os.OpenFile(dialogsFileName(nodeID), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	c.file = cacheFile
	return nil
}

func (c *DialogCacheFile) Close() {
	c.file.Close()
}

func (c *DialogCacheFile) StoreDialog(ctx context.Context, data DialogData) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dialogs[data.ID] = data
	return c.dumpDialogs()
}

func (c *DialogCacheFile) DeleteDialog(ctx context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.dialogs, id)
	return c.dumpDialogs()
}

// TODO
func (c *DialogCacheFile) DeleteDialogNode(ctx context.Context, id string, nodeID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.dialogs, id)
	return c.dumpDialogs()
}

func (c *DialogCacheFile) LoadDialog(ctx context.Context, id string) (DialogData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, exists := c.dialogs[id]
	if !exists {
		return d, sipgo.ErrDialogDoesNotExists
	}
	return d, nil
}

func (c *DialogCacheFile) LoadDialogs(ctx context.Context, nodeID string) ([]DialogData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Println("Loaading dialogs for", dialogsFileName(nodeID))
	cacheFile, err := os.OpenFile(dialogsFileName(nodeID), os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return nil, err
	}
	defer cacheFile.Close()

	defer cacheFile.Truncate(0)

	v := []DialogData{}
	return v, json.NewDecoder(cacheFile).Decode(&v)
	// res := []DialogData{}
	// for _, d := range c.dialogs {
	// 	if d.NodeID == nodeID {
	// 		res = append(res, d)
	// 	}
	// }
}

func (c *DialogCacheFile) dumpDialogs() error {
	// raw, err := json.Marshal(c.dialogs)
	// if err != nil {
	// 	return err
	// }

	// Truncate and store
	if err := c.file.Truncate(0); err != nil {
		return err
	}

	if _, err := c.file.Seek(0, 0); err != nil {
		return err
	}

	enc := json.NewEncoder(c.file)
	enc.SetEscapeHTML(false)

	dgs := make([]DialogData, len(c.dialogs))
	i := 0
	for _, v := range c.dialogs {
		dgs[i] = v
		i++
	}

	return enc.Encode(dgs)
	// _, err = c.File.Write(raw)
	// return err

}

// func CacheDumpDialogs(f *os.File) error {
// 	dialogs := []DialogData{}
// 	diago.DialogsServerCache.DialogRange(context.TODO(), func(id string, v *diago.DialogServerSession) bool {
// 		log.Info().Any("key", id).Msg("Dumping dialog")

// 		d := DialogData{
// 			ID:                v.ID,
// 			InviteRequest:     v.InviteRequest,
// 			NodeID:            nodeID,
// 			Dialplan:          "playback",
// 			InviteRequestData: v.InviteRequest.String(),
// 		}

// 		// Apply last cseq number
// 		d.InviteRequest.CSeq().SeqNo = v.CSEQ()
// 		dialogs = append(dialogs, d)
// 		return true
// 	})

// 	diago.DialogsClientCache.DialogRange(context.TODO(), func(id string, v *diago.DialogClientSession) bool {
// 		log.Info().Any("key", id).Msg("Dumping dialog")

// 		d := DialogData{
// 			ID:                v.ID,
// 			InviteRequest:     v.InviteRequest,
// 			NodeID:            nodeID,
// 			Dialplan:          "playback",
// 			Direction:         1,
// 			InviteRequestData: v.InviteRequest.String(),
// 		}

// 		// Apply last cseq number
// 		d.InviteRequest.CSeq().SeqNo = v.CSEQ()
// 		dialogs = append(dialogs, d)
// 		return true
// 	})

// 	data, err := json.Marshal(dialogs)
// 	if err != nil {
// 		return err
// 	}

// 	f.Truncate(0)
// 	f.Seek(0, 0)

// 	for nn := 0; nn < len(data); {
// 		n, err := f.Write(data[nn:])
// 		if err != nil {
// 			return err
// 		}
// 		nn += n
// 	}
// 	return nil
// }

type BridgeData struct {
	ID      string
	NodeID  string
	Dialogs []string
}
