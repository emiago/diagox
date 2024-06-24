// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"text/template"
	"time"

	"gitlab.com/emiagox/diagox/testdata"

	"github.com/emiago/diago"
	"github.com/emiago/diago/audio"
	"github.com/emiago/diago/diagotest"
	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		lvl = slog.LevelInfo
	}
	slog.SetLogLoggerLevel(lvl)
	m.Run()
}

func testPBXRun(t testing.TB, conf Config, envConf EnvConfig) *PBX {
	pbx := NewPBXMemory(envConf, conf)
	require.NoError(t, pbx.setupDiago(conf))

	err := pbx.ServeBackground(context.TODO())
	require.NoError(t, err)
	return pbx
}

func testUASRun(t testing.TB, uri *sip.Uri, cb func(d *diago.DialogServerSession)) {
	testUASRunOpts(t, uri, "udp4", cb)
}

func testUASRunTCP(t testing.TB, uri *sip.Uri, cb func(d *diago.DialogServerSession)) {
	testUASRunOpts(t, uri, "tcp4", cb)
}

func testUASRunOpts(t testing.TB, uri *sip.Uri, tran string, cb func(d *diago.DialogServerSession)) {
	uas, _ := sipgo.NewUA()
	t.Cleanup(func() {
		uas.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	host := "127.0.0.1"
	port := 11000 + rand.IntN(999)

	if uri != nil {
		if uri.Host != "" {
			host = uri.Host
		}
		if uri.Port > 0 {
			port = uri.Port
		}

		*uri = sip.Uri{
			User: "uas",
			Host: host,
			Port: port,
		}
	}

	dg := diago.NewDiago(uas,
		diago.WithTransport(diago.Transport{
			Transport: tran,
			BindHost:  host,
			BindPort:  port,
		}),
	)
	err := dg.ServeBackground(ctx, cb)
	require.NoError(t, err)

}

func testUACRun(t testing.TB, uri *sip.Uri) *diago.Diago {
	ua, _ := sipgo.NewUA()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		ua.Close()
		cancel()
	})
	port := 22000 + rand.IntN(100)
	host := "127.0.0.1"

	if uri.Host != "" {
		host = uri.Host
	}
	if uri.Port > 0 {
		port = uri.Port
	}

	dg := diago.NewDiago(ua,
		diago.WithTransport(diago.Transport{
			Transport: "udp4",
			BindHost:  host,
			BindPort:  port,
		}),
	)
	uri.Host = host
	uri.Port = port

	err := dg.ServeBackground(ctx, func(d *diago.DialogServerSession) {
		// Neded for handling bye
		<-d.Context().Done()
	})
	require.NoError(t, err)
	return dg
}

type TestConfData struct {
	UDPPort         int
	EndpointDialUri string
}

func testLoadDefaultConf(t testing.TB, c *Config, d TestConfData) {
	f, err := testdata.OpenConfigFile("gopbx_test_default.yaml")
	require.NoError(t, err)
	defer f.Close()
	data, _ := io.ReadAll(f)

	temp, err := template.New("parse").Parse(string(data))
	require.NoError(t, err)

	bufYaml := bytes.NewBuffer([]byte{})
	temp.Execute(bufYaml, d)

	err = ConfigLoad(bufYaml, c)
	require.NoError(t, err)
}

func testDefaultConfig(t testing.TB, c *Config, uri *sip.Uri, endpointDialUri string) {
	port := 15000 + rand.IntN(999)
	uri.Host = "127.0.0.1"
	uri.Port = port
	testLoadDefaultConf(t, c, TestConfData{
		UDPPort:         port,
		EndpointDialUri: endpointDialUri,
	})
}

/*
	 func testLoadConfig(t, c *Config, uri *sip.Uri, endpointDialUri string) {
		port := 15000 + rand.IntN(999)

		c.Transports = map[string]ConfigTransport{
			"udp": {
				Transport: "udp",
				Bind:      "127.0.0.1",
				Port:      port,
			},
		}
		uri.Host = "127.0.0.1"
		uri.Port = port

		c.Endpoints = map[string]ConfigEndpoint{
			"dialer": {
				Name: "dialer",
				URI:  endpointDialUri,
				Match: ConfigMatch{
					Type: "ip",
					Values: []string{
						"127.0.0.1/32",
					},
				},
				Route: "default",
			},
		}

		c.Routes = map[string][]ConfigRoute{
			"default": []ConfigRoute{
				{ID: "recording", EndpointName: "dialer", Recording: true},
				{ID: "123", EndpointName: "dialer", Recording: false},
				{ID: "hangup404", EndpointName: "dialer", Hangup: ConfigRouteHangup{
					Code: 404,
				}},
			},
		}
	}
*/
func TestLoadCalls(t *testing.T) {
	{
		uas, _ := sipgo.NewUA()
		defer uas.Close()
		dg := diago.NewDiago(uas,
			diago.WithTransport(diago.Transport{
				Transport: "udp",
				BindHost:  "127.0.0.1",
				BindPort:  11111,
			}),
		)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := dg.ServeBackground(ctx, func(d *diago.DialogServerSession) {
			d.Progress()
			med, err := d.Answer(diago.AnswerOptions{})
			if err != nil {
				t.Log("Failed to answer", err)
				return
			}
			defer med.Close()

		})
		require.NoError(t, err)
	}

	{
		conf := Config{
			Transports: map[string]ConfigTransport{
				"udp": {
					Transport: "udp",
					Bind:      "127.0.0.1",
					Port:      15060,
				},
			},
			Endpoints: map[string]ConfigEndpoint{
				"dialer": {
					Name: "dialer",
					Match: ConfigMatch{
						Type: "ip",
						Values: []string{
							"127.0.0.1/16",
						},
					},
				},
			},
		}

		envConf := EnvConfig{
			OutboundDialUri: "sip:127.0.0.1:11111",
		}

		pbx := NewPBXMemory(envConf, conf)
		require.NoError(t, pbx.setupDiago(conf))

		err := pbx.ServeBackground(context.TODO())
		require.NoError(t, err)
	}

	// Now lets have dialer
	{
		ua, _ := sipgo.NewUA()
		defer ua.Close()
		dg := diago.NewDiago(ua,
			diago.WithTransport(diago.Transport{
				Transport: "udp",
				BindHost:  "127.0.0.1",
				BindPort:  22222,
			}),
		)
		err := dg.ServeBackground(context.TODO(), func(d *diago.DialogServerSession) {
			// Neded for handling bye
			<-d.Context().Done()
		})
		require.NoError(t, err)

		wg := sync.WaitGroup{}
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				dialog, med, err := dg.Invite(context.Background(), sip.Uri{User: "test", Host: "127.0.0.1", Port: 15060}, diago.InviteOptions{})
				if err != nil {
					t.Log("Invite failed", err)
					return
				}
				defer dialog.Close()
				defer med.Close()

				// To have succesful bridge wait UAS to hangup
				<-dialog.Context().Done()

				// if err := dialog.Hangup(context.TODO()); err != nil {
				// 	t.Log("Failed to hangup", err)
				// }
			}()
		}
		wg.Wait()
	}

}

func TestIntegrationPBXBridge(t *testing.T) {
	uasUri := sip.Uri{}
	testUASRun(t, &uasUri, func(d *diago.DialogServerSession) {
		// Do echo
		med, _ := d.Answer(diago.AnswerOptions{})
		defer med.Close()
		audioR, _ := med.AudioReader()
		audioW, _ := med.AudioWriter()
		go func() {
			media.Copy(audioR, audioW)
		}()
		<-d.Context().Done()
	})

	conf := Config{}
	pbxUri := sip.Uri{User: "123"}
	testDefaultConfig(t, &conf, &pbxUri, uasUri.String())
	_ = testPBXRun(t, conf, EnvConfig{})

	dialer := testUACRun(t, &sip.Uri{})
	d, med, err := dialer.Invite(context.TODO(), pbxUri, diago.InviteOptions{})
	require.NoError(t, err)
	defer d.Close()
	defer med.Close()

	// Send RTP until echo
	audioR, _ := med.AudioReader()
	audioW, _ := med.AudioWriter()
	go func() {
		for {
			_, err := audioW.Write(bytes.Repeat([]byte{0}, 160))
			if err != nil {
				return
			}
		}
	}()
	_, err = audioR.Read(make([]byte, media.RTPBufSize))
	require.NoError(t, err)

	err = d.Hangup(context.TODO())
	require.NoError(t, err)
}

func TestIntegrationPBXRecording(t *testing.T) {
	uasUri := sip.Uri{}
	testUASRun(t, &uasUri, func(d *diago.DialogServerSession) {
		// Do echo
		t.Log("UAS answering")
		med, _ := d.Answer(diago.AnswerOptions{})
		defer med.Close()
		audioR, _ := med.AudioReader()
		audioW, _ := med.AudioWriter()
		go func() {
			media.Copy(audioR, audioW)
		}()
		<-d.Context().Done()
	})

	conf := Config{}
	pbxUri := sip.Uri{User: "recording"}
	testDefaultConfig(t, &conf, &pbxUri, uasUri.String())
	envConf := EnvConfig{
		RecordingsPath: "recordings",
	}
	pbx := testPBXRun(t, conf, envConf)

	dialer := testUACRun(t, &sip.Uri{})
	dialog, med, err := dialer.Invite(context.TODO(), pbxUri, diago.InviteOptions{})
	require.NoError(t, err)
	defer dialog.Close()
	defer med.Close()
	defer dialog.Hangup(dialog.Context())

	fh, err := testdata.OpenFile("demo-thanks.wav")
	require.NoError(t, err)

	pb, _ := med.PlaybackCreate()
	_, err = pb.Play(fh, "audio/wav")
	require.NoError(t, err)

	err = dialog.Hangup(dialog.Context())
	require.NoError(t, err)
	t.Log("Wait that dialog terminates")
	assert.Eventually(t, func() bool {
		dialogs := 0
		pbx.tu.DialogCacheServer().DialogRange(context.Background(), func(id string, d *diago.DialogServerSession) bool {
			dialogs++
			return false
		})
		return dialogs == 0
	}, 10*time.Second, 200*time.Millisecond)

	recordingFile, err := os.Open("recordings/" + dialog.ID + ".wav")
	require.NoError(t, err)
	wavReader := audio.NewWavReader(recordingFile)
	require.NoError(t, wavReader.ReadHeaders())
	assert.EqualValues(t, 8000, wavReader.SampleRate)
	assert.EqualValues(t, 16, wavReader.BitsPerSample)
}

func TestPBXHandler(t *testing.T) {
	t.Skip("For now it is hard to unit test when we also need to bridge and we have no way to control that")
	conf := Config{}
	pbxUri := sip.Uri{User: "123"}
	testDefaultConfig(t, &conf, &pbxUri, "")

	pbx := NewPBXMemory(EnvConfig{}, conf)
	require.NoError(t, pbx.setupDiago(conf))

	req, err := diagotest.NewRequest(sip.INVITE, sip.Uri{User: "123", Host: "127.0.0.1", Port: 15060})
	require.NoError(t, err)

	dialog, _, err := diagotest.NewDialogServerSession(req)
	require.NoError(t, err)
	dialog.OnState(func(s sip.DialogState) {
		t.Log("State", s)
		if s == sip.DialogStateConfirmed {
			dialog.Hangup(context.TODO())
		}
	})

	pbx.handler(dialog)
}

func TestIntegrationPBXHangupCauseCode(t *testing.T) {
	conf := Config{}
	pbxUri := sip.Uri{User: "hangup404"}
	testDefaultConfig(t, &conf, &pbxUri, "")
	testPBXRun(t, conf, EnvConfig{})

	dialer := testUACRun(t, &sip.Uri{})
	_, _, err := dialer.Invite(context.TODO(), pbxUri, diago.InviteOptions{})
	var resErr *sipgo.ErrDialogResponse
	require.ErrorAs(t, err, &resErr)
	assert.EqualValues(t, resErr.Res.StatusCode, 404)
}

func TestIntegrationPBXFallbackEndpoints(t *testing.T) {
	uas404Uri := sip.Uri{}
	testUASRun(t, &uas404Uri, func(d *diago.DialogServerSession) {
		d.Respond(404, "Not Found", nil)
	})

	uas200Uri := sip.Uri{}
	testUASRun(t, &uas200Uri, func(d *diago.DialogServerSession) {
		med, _ := d.Answer(diago.AnswerOptions{})
		defer med.Close()
		med.Echo()
	})

	conf := Config{}
	pbxUri := sip.Uri{User: "hangup404"}
	testDefaultConfig(t, &conf, &pbxUri, "")

	// Setup carrier that will return 404
	conf.Endpoints["carrier_404"] = ConfigEndpoint{
		Name: "carrier_404",
		URI:  uas404Uri.String(),
	}

	// Carrier that will return 200
	conf.Endpoints["carrier_200"] = ConfigEndpoint{
		Name: "carrier_200",
		URI:  uas200Uri.String(),
	}

	// Setup default route with fallback
	conf.Routes["default"] = []ConfigRoute{
		{
			ID:           "",
			Match:        "any",
			EndpointName: "carrier_404",
			Fallback: ConfigRouteFallback{
				Enabled:          true,
				FallbacksCodes:   []int{404},
				FallbacksTimeout: true,
				Endpoints:        []string{"carrier_404", "carrier_200"},
			},
		},
	}
	testPBXRun(t, conf, EnvConfig{})

	uac := testUACRun(t, &sip.Uri{})
	d, med, err := uac.Invite(context.TODO(), pbxUri, diago.InviteOptions{})
	require.NoError(t, err)
	defer d.Close()
	defer med.Close()

	// Send RTP until echo
	audioR, _ := med.AudioReader()
	audioW, _ := med.AudioWriter()
	go func() {
		for {
			_, err := audioW.Write(bytes.Repeat([]byte{0}, 160))
			if err != nil {
				return
			}
		}
	}()
	_, err = audioR.Read(make([]byte, media.RTPBufSize))
	require.NoError(t, err)
}

func TestIntegrationPBXTransportID(t *testing.T) {
	uasTCPUri := sip.Uri{}
	testUASRunTCP(t, &uasTCPUri, func(d *diago.DialogServerSession) {
		med, _ := d.Answer(diago.AnswerOptions{})
		defer med.Close()
		med.Echo()
	})

	conf := Config{}
	pbxUri := sip.Uri{User: "123"}
	testDefaultConfig(t, &conf, &pbxUri, "")
	// Create TCP transport and point this as carrier tcp
	conf.Transports["tcpID"] = ConfigTransport{
		Transport: "tcp",
		Bind:      "127.0.0.1",
		Port:      16000 + rand.IntN(999),
	}
	conf.Endpoints["carrier_tcp"] = ConfigEndpoint{
		Name:        "carrier_tcp",
		URI:         uasTCPUri.String(),
		TransportID: "tcpID", // Mark this to use transport tcp
	}
	conf.Routes["default"] = []ConfigRoute{
		{
			ID:           "",
			Match:        "any",
			EndpointName: "carrier_tcp",
		},
	}
	testPBXRun(t, conf, EnvConfig{})

	uac := testUACRun(t, &sip.Uri{})
	d, med, err := uac.Invite(context.TODO(), pbxUri, diago.InviteOptions{})
	require.NoError(t, err)
	defer d.Close()
	defer med.Close()

	// Send RTP until echo
	audioR, _ := med.AudioReader()
	audioW, _ := med.AudioWriter()
	go func() {
		for {
			_, err := audioW.Write(bytes.Repeat([]byte{0}, 160))
			if err != nil {
				return
			}
		}
	}()
	_, err = audioR.Read(make([]byte, media.RTPBufSize))
	require.NoError(t, err)
}

func BenchmarkIntegrationCalls(t *testing.B) {
	uasUri := sip.Uri{}
	writeBytes := bytes.Repeat([]byte{0}, 160*50) // 1 second
	testUASRun(t, &uasUri, func(d *diago.DialogServerSession) {
		// Do echo
		med, _ := d.Answer(diago.AnswerOptions{})
		defer med.Close()

		audioW, _ := med.AudioWriter()
		_, err := media.WriteAll(audioW, writeBytes, 160)
		if err != nil {
			t.Log("writing failed with error", err)
		}
		return
	})

	conf := Config{}
	pbxUri := sip.Uri{User: "123"}
	testDefaultConfig(t, &conf, &pbxUri, uasUri.String())

	envConf := EnvConfig{}
	_ = testPBXRun(t, conf, envConf)

	dialer := testUACRun(t, &sip.Uri{})

	t.ResetTimer()
	t.Run("stress", func(t *testing.B) {
		for i := 0; i < t.N; i++ {
			func() {
				dialog, med, err := dialer.Invite(context.TODO(), sip.Uri{User: "123", Host: "127.0.0.1", Port: 15060}, diago.InviteOptions{})
				require.NoError(t, err)
				defer dialog.Close()
				defer med.Close()
				// We need to wait to get bridged
				if err := med.Echo(); err != nil && !errors.Is(err, io.EOF) {
					t.Log("Echo finished with error", err)
				}
				// <-dialog.Context().Done()
			}()
		}
	})
}
