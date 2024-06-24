// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustNewUA() *sipgo.UserAgent {
	ua, err := sipgo.NewUA()
	if err != nil {
		panic(err)
	}
	return ua
}

func TestClusterE2E(t *testing.T) {
	// This requires running docker compose first
	if os.Getenv("TEST_E2E") != "true" {
		t.Skip()
	}

	pbxDomain := "gopbx.coredns.local"
	// mysqlHost := "localhost:3306"

	t.Run("bridge", func(t *testing.T) {
		testUASRun(t, &sip.Uri{Host: "0.0.0.0", Port: 5080}, func(d *diago.DialogServerSession) {
			med, _ := d.Answer(diago.AnswerOptions{})
			defer med.Close()
			<-d.Context().Done()
		})

		uac := testUACRun(t, &sip.Uri{Host: "0.0.0.0"})
		dialog, med, err := uac.Invite(context.TODO(), sip.Uri{User: "1234", Host: pbxDomain, Port: 5060}, diago.InviteOptions{
			Username: "test",
			Password: "test123",
		})
		require.NoError(t, err)
		defer dialog.Close()
		defer med.Close()

		time.Sleep(1 * time.Second)
		err = dialog.Hangup(context.Background())
		assert.NoError(t, err)
	})

	t.Run("bridgeWithHeader", func(t *testing.T) {
		headerExist := false
		testUASRun(t, &sip.Uri{Host: "0.0.0.0", Port: 5080}, func(d *diago.DialogServerSession) {
			h := d.InviteRequest.GetHeader("X-MyCustom-Header")
			headerExist = h != nil && h.Value() == "gopbx"
			med, _ := d.Answer(diago.AnswerOptions{})
			defer med.Close()
			<-d.Context().Done()
		})

		uac := testUACRun(t, &sip.Uri{Host: "0.0.0.0"})
		dialog, med, err := uac.Invite(context.TODO(), sip.Uri{User: "custom_header", Host: pbxDomain, Port: 5060}, diago.InviteOptions{
			Username: "test",
			Password: "test123",
		})
		require.NoError(t, err)
		defer dialog.Close()
		defer med.Close()

		time.Sleep(1 * time.Second)
		err = dialog.Hangup(context.Background())
		assert.NoError(t, err)
		assert.True(t, headerExist)
	})

	t.Run("bridgeWithHeader", func(t *testing.T) {
		headerExist := false
		headerPassExists := false
		testUASRun(t, &sip.Uri{Host: "0.0.0.0", Port: 5080}, func(d *diago.DialogServerSession) {
			h := d.InviteRequest.GetHeader("X-MyCustom-Header")
			headerExist = h != nil && h.Value() == "gopbx"

			h1 := d.InviteRequest.GetHeader("X-AccountID")
			h2 := d.InviteRequest.GetHeader("X-PassMe")
			headerPassExists = h1 != nil && h2 != nil && h1.Value() == "123" && h2.Value() == "I am passed"
			med, _ := d.Answer(diago.AnswerOptions{})
			defer med.Close()
			<-d.Context().Done()
		})

		uac := testUACRun(t, &sip.Uri{Host: "0.0.0.0"})
		dialog, med, err := uac.Invite(context.TODO(), sip.Uri{User: "custom_header", Host: pbxDomain, Port: 5060}, diago.InviteOptions{
			Username: "test",
			Password: "test123",
			Headers: []sip.Header{
				sip.NewHeader("X-AccountID", "123"),
				sip.NewHeader("X-PassMe", "I am passed"),
			},
		})
		require.NoError(t, err)
		defer dialog.Close()
		defer med.Close()

		time.Sleep(1 * time.Second)
		err = dialog.Hangup(context.Background())
		assert.NoError(t, err)
		assert.True(t, headerExist)
		assert.True(t, headerPassExists)

		// TODO find CDR records
	})
}

// `func TestE2E(t *testing.T) {
// 	pbxReady := make(chan struct{})
// 	goerr := make(chan error)
// 	ctx, cancel := context.WithCancel(context.Background())
// 	defer cancel()
// 	ctx = context.WithValue(ctx, sipgo.ListenReadyCtxKey, sipgo.ListenReadyCtxValue(pbxReady))

// 	cdrStore := NewCDRPiper()
// 	go func() {
// 		goerr <- startPBXDefault(ctx, "127.0.0.1:5060", cdrStore)
// 	}()

// 	select {
// 	case e := <-goerr:
// 		t.Fatal(e)
// 	case <-pbxReady:
// 	}

// 	uac := sipgox.NewPhone(mustNewUA())
// 	dialog, err := uac.Dial(ctx, sip.Uri{Host: "127.0.0.1", Port: 5060, User: "answer"}, sipgox.DialOptions{})
// 	require.NoError(t, err)

// 	assert.Equal(t, sip.DialogStateEstablished, <-dialog.State())
// 	assert.Equal(t, sip.DialogStateConfirmed, <-dialog.State())

// 	err = dialog.Hangup(ctx)
// 	require.NoError(t, err)

// 	assert.Equal(t, sip.DialogStateEnded, <-dialog.State())

// 	// Now Check CDR
// 	cdr := <-cdrStore.Pipe
// 	assert.Equal(t, dialog.InviteRequest.From().Address.User, cdr.CallerID)
// 	assert.Equal(t, dialog.InviteRequest.To().Address.User, cdr.CalleeID)
// 	assert.NotEmpty(t, cdr.StartTime)
// 	assert.NotEmpty(t, cdr.Duration)
// }`

type funcVsInterface struct {
	f func(data []byte) (n int, err error)
}

// go:noinline
func (i *funcVsInterface) Write(data []byte) (n int, err error) {
	return 0, nil
}

func write(data []byte) (n int, err error) {
	return 0, nil
}

func BenchmarkFuncVsInterface(b *testing.B) {
	s := &funcVsInterface{
		f: write,
	}
	var writer io.Writer = s

	b.Run("func", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := s.f([]byte{})
			if err != nil {
				b.Error(err)
			}
		}
	})

	b.Run("interface", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := writer.Write([]byte{})
			if err != nil {
				b.Error(err)
			}
		}
	})

}
