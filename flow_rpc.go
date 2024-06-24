// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/audio"
	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo/sip"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/google/uuid"
)

type FlowRequest struct {
	ID   string          `json:"id"`
	DID  string          `json:"did"`
	OP   string          `json:"op"`
	Data json.RawMessage `json:"data"`

	ctx    context.Context
	cancel context.CancelFunc
}

type FlowResponse struct {
	ID     string          `json:"id"`
	DID    string          `json:"did"` // dialog ID
	Code   int             `json:"code"`
	Reason string          `json:"reason"`
	Data   json.RawMessage `json:"data"`
}

type FlowConnection struct {
	conn               net.Conn
	requestDialog      sync.Map
	clientTransactions sync.Map
}

type FlowDialogChan struct {
	requests            chan FlowRequest
	lastBufferedRequest FlowRequest
}

func (c *FlowConnection) writeResponse(req FlowRequest, code int, err string) error {
	conn := c.conn
	r := FlowResponse{ID: req.ID, DID: req.DID, Code: code, Reason: err, Data: nil}
	data, _ := json.Marshal(r)
	return wsutil.WriteServerText(conn, data)
}

type flowConnections struct {
	conns map[string][]*FlowConnection
	mu    sync.Mutex
}

func (ac *flowConnections) Delete(endpointID string, agentConn *FlowConnection) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	conns := ac.conns[endpointID]
	for i, c := range conns {
		if c == agentConn {
			conns = append(conns[:i], conns[i+1:]...)
			ac.conns[endpointID] = conns
		}
	}
}

func (ac *flowConnections) Load(endpointID string) (*FlowConnection, bool) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	conns, exists := ac.conns[endpointID]
	if len(conns) == 0 {
		return nil, false
	}
	if len(conns) == 1 {
		return conns[0], exists
	}

	ind := rand.IntN(len(conns))
	return conns[ind], exists
}

type FlowRPC struct {
	Config Config
	log    *slog.Logger

	connections *flowConnections
	playbackDir string

	recordCache sync.Map
}

func NewFlowRPC(conf Config) *FlowRPC {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	return &FlowRPC{
		Config: conf,
		log:    slog.Default().With("caller", "flow"),
		connections: &flowConnections{
			conns: map[string][]*FlowConnection{},
		},
		playbackDir: wd,
	}
}

func (rpc *FlowRPC) ServeBackground(addr string) {
	go func() {
		err := rpc.Serve(addr)
		rpc.log.Info("Agent server closed", "error", err)
	}()
}

func (rpc *FlowRPC) Serve(addr string) error {
	httpRespond := func(w http.ResponseWriter, status int, msg string) {
		w.WriteHeader(status)
		w.Write([]byte(msg))
	}

	httpError := func(w http.ResponseWriter, status int, msg string, err error) {
		rpc.log.Debug("Responding error", "error", err)
		w.WriteHeader(status)
		w.Write([]byte(msg))
	}

	rpc.log.Info("Starting Agent Server on :6000")

	return http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpointId := r.URL.Query().Get("endpoint")
		if endpointId == "" {
			httpRespond(w, 400, "Endpoint parameter required")
			return
		}
		endpoints := rpc.Config.Endpoints

		_, exists := endpoints[endpointId]
		if !exists {
			httpRespond(w, 404, "No endpoint found")
			return
		}

		flowConn := func() (c *FlowConnection) {
			rpc.connections.mu.Lock()
			defer rpc.connections.mu.Unlock()
			connectionBuf := func() []*FlowConnection {
				if connectionBuf, exists := rpc.connections.conns[endpointId]; exists {
					return connectionBuf
				}

				return make([]*FlowConnection, 0, 2)
			}()

			if len(connectionBuf) == cap(connectionBuf) {
				httpError(w, 400, "Maximum connection of agent reached", nil)
				return
			}

			conn, _, _, err := ws.UpgradeHTTP(r, w)
			if err != nil {
				// handle error
				httpError(w, 500, "Failed to upgrade websocket", err)
				return
			}

			agentConn := &FlowConnection{
				conn:          conn,
				requestDialog: sync.Map{},
			}

			connectionBuf = append(connectionBuf, agentConn)
			rpc.connections.conns[endpointId] = connectionBuf
			return agentConn
		}()
		if flowConn == nil {
			return
		}

		log := rpc.log.With("endpoint", endpointId)
		cliContentType := r.Header.Get("X-Request-Type") == "cli"
		go func() {
			conn := flowConn.conn
			defer conn.Close()
			defer rpc.connections.Delete(endpointId, flowConn)

			connCtx, cancel := context.WithCancel(context.Background())
			defer cancel()

			defer func() {
				if p := recover(); p != nil {
					log.Info("Flow reading ws recoverd", "msg", p)
					buf := make([]byte, 8182)
					n := runtime.Stack(buf, false)
					fmt.Println(string(buf[:n]))
				}
			}()

			log.Info("Reading request started")
			for {
				buf, op, err := wsutil.ReadClientData(conn)
				if err != nil {
					// handle error
					log.Error("Reading client endup in error", "error", err)
					return
				}

				if op != ws.OpText {
					log.Error("Not received textual data", "op", op)
					continue
				}

				// Determine is it request or response

				msg := struct {
					OP   string
					DID  string
					Code int
				}{}

				var req FlowRequest
				var res FlowResponse
				err = func() error {
					var err error
					if cliContentType {
						// Args are send like cli flags
						args := strings.Split(string(buf), " ")
						if code, err := strconv.Atoi(args[0]); err == nil {
							// This is response
							res.Code = code
							if len(args) < 2 {
								return fmt.Errorf("ID is required for response")
							}
							res.ID = args[1]
							// res.DID = "last"
							// if len(args) > 2 {
							// 	res.DID = args[2]
							// }
							return nil
						}

						data := map[string]any{}
						for _, a := range args[1:] {
							kv := strings.Split(a, "=")
							if len(kv) < 2 {
								continue
							}
							key := strings.TrimLeft(kv[0], "-")
							data[key] = strings.TrimSpace(kv[1])
						}
						req.ID = "cli." + uuid.NewString()
						req.DID = "last" // use last call unless specified
						req.OP = args[0]
						if len(args) > 1 {
							req.ID = args[1]
						}

						if len(args) > 2 {
							req.DID = args[2]
						}

						req.Data, err = json.Marshal(data)
						return err

					}
					if err := json.Unmarshal(buf, &msg); err != nil {
						return err
					}

					if msg.Code > 0 {
						// This is response
						return json.Unmarshal(buf, &res)
					}
					return json.Unmarshal(buf, &req)
				}()
				if err != nil {
					log.Error("failed to parse request", "error", err)
					continue
				}

				if res.ID != "" && res.Code > 0 {
					// This is response
					fn, exists := flowConn.clientTransactions.Load(res.ID)
					if !exists {
						log.Error("Response received for non existing transaction", "res", res)
						continue
					}
					// TODO remove this uglyness
					log.Debug("Calling response callback", "id", res.ID)
					cb, ok := fn.(func(res FlowResponse))
					if !ok {
						panic("wrong type of res func")
					}
					cb(res)
					continue
				}

				// For Incoming requests are routed based on Dialog ID
				// For Outgoing requests we will only follow transactions
				if req.DID == "" {
					// This is outgoing request?
					log.Error("Outgoing requests are not yet supported, please provide dialog id with `did` value")
					continue
				}

				dialogID := req.DID
				// For inbound dialog must exist, but other are outbount requests
				requestsChan, exists := flowConn.requestDialog.Load(dialogID)
				if !exists {
					// log.Error("")
					if err := flowConn.writeResponse(req, 404, "Dialog Does Not Exist"); err != nil {
						log.Error("Failed to write response 404", "error", err)
						return
					}
					continue
				}
				dialogRequestCh := requestsChan.(FlowDialogChan)

				if req.OP == ":requestCancel" {
					// If this is last request then cancel
					// Buffered request are not possible to cancel
					if dialogRequestCh.lastBufferedRequest.ID == req.ID {
						dialogRequestCh.lastBufferedRequest.cancel()
					}
					return
				}

				// Add context cancelation
				req.ctx, req.cancel = context.WithCancel(connCtx)
				// if err := agentConn.writeResponse(req, 100, "Processing"); err != nil {
				// 	log.Error("Failed to write response 429", "error", err)
				// 	return
				// }
				select {
				case <-time.After(10 * time.Second):
					if err := flowConn.writeResponse(req, 429, "Too Many Requests"); err != nil {
						log.Error("Failed to write response 429", "error", err)
						return
					}
					log.Error("Buffer is full. Timeout on flow for consuming request")
					return
				case dialogRequestCh.requests <- req:
					dialogRequestCh.lastBufferedRequest = req
					log.Info("Buffering new request", "req.id", req.ID, "req.op", string(req.OP))
				}

			}
		}()

	}))
}

func (rpc *FlowRPC) NewServerDialog(ctx context.Context, toEndpointId string, d *diago.DialogServerSession, next *ConfigEndpoint) error {
	log := rpc.log.With("flow", toEndpointId)
	flowConn, exists := rpc.connections.Load(toEndpointId)
	if !exists {
		return fmt.Errorf("flow=%s does not exists", toEndpointId)
	}
	dialogID := d.ID

	defer func() {
		if p := recover(); p != nil {
			log.Info("Agent panic recoverd")
			buf := make([]byte, 8182)
			n := runtime.Stack(buf, false)
			fmt.Println(p)
			fmt.Println(string(buf[:n]))
		}
	}()

	// Now create dialog listener channel and add into connection dialogs pool
	dialogsChan := FlowDialogChan{
		requests: make(chan FlowRequest, 10),
	}
	flowConn.requestDialog.Store(d.ID, dialogsChan)
	defer flowConn.requestDialog.Delete(d.ID)

	// Add also for fast referencing
	flowConn.requestDialog.Store("last", dialogsChan)
	defer flowConn.requestDialog.Delete("last")

	defer log.Info("Agent dialog exit", "id", d.ID)

	doRequest := func(ctx context.Context, event string, timeout time.Duration, data any) (*FlowResponse, error) {
		response := &FlowResponse{}
		err := func() error {
			conn := flowConn.conn
			var jsonData []byte = nil
			var err error
			if data != nil {
				jsonData, err = json.Marshal(data)
				if err != nil {
					return err
				}
			}
			ev := FlowRequest{
				ID:   uuid.NewString(),
				OP:   event,
				DID:  dialogID,
				Data: jsonData,
			}
			data, err := json.Marshal(ev)
			if err != nil {
				return err
			}

			resChan := make(chan FlowResponse, 1) // Reusing chan as we do not need many chan. Making it a bit buffer to avoid blockers on read routine
			flowConn.clientTransactions.Store(ev.ID, func(res FlowResponse) {
				if res.Code < 200 {
					return
				}
				select {
				case <-ctx.Done():
					return
				case resChan <- res:
				}
			})
			defer flowConn.clientTransactions.Delete(ev.ID)

			log.Info("Flow Send Request", "req.op", ev.OP, "req.id", ev.ID)
			if err := wsutil.WriteServerText(conn, data); err != nil {
				return err
			}

			// Now wait response
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			select {
			case r := <-resChan:
				*response = r
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}()

		return response, err
	}

	writeResponse := func(req FlowRequest, code int, errMsg string, data any) error {
		conn := flowConn.conn

		var jsonPayload []byte = nil
		if data != nil {
			var err error
			jsonPayload, err = json.Marshal(data)
			if err != nil {
				return err
			}
		}

		ev := FlowResponse{
			ID:     req.ID,
			DID:    dialogID,
			Code:   code,
			Reason: errMsg,
			Data:   jsonPayload,
		}

		rawData, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		return wsutil.WriteServerMessage(conn, ws.OpText, rawData)
	}

	waitRequest := func(ctx context.Context) (FlowRequest, error) {
		// TODO: if agent is lost, this call would hang here
		select {
		case req := <-dialogsChan.requests:
			return req, nil
		case <-ctx.Done():
			return FlowRequest{}, ctx.Err()
		}
	}

	// Lets Do Invite and wait Agent to accept it or reject
	// Now if there are multiple Agent peers we can send this to all of them and first who accepts
	// NOTE: WE NEED TO HAVE DIALOG CHAN READY Before doing this
	{
		inviteData := struct {
			CallID string `json:"callID"`
			From   string `json:"from"`
			To     string `json:"to"`
		}{
			CallID: d.InviteRequest.CallID().Value(),
			From:   d.FromUser(),
			To:     d.ToUser(),
		}

		res, err := doRequest(ctx, "invite", 60*time.Second, inviteData)
		if err != nil {
			return err
		}

		if res.Code != 200 {
			return fmt.Errorf("dialog not accepted code=%d reason=%s", res.Code, res.Reason)
		}
	}

	defer func() {
		if d.LoadState() != sip.DialogStateEnded {
			return
		}
		// Doing hangup
		if _, err := doRequest(context.Background(), "bye", 10*time.Second, nil); err != nil {
			log.Error("Request bye not handled", "error", err)
		}
	}()

	stopListen := func() error {
		return nil
	}
	var dialogMedia *diago.DialogMedia
	startListen := func() error {
		var err error
		if dialogMedia == nil {
			return fmt.Errorf("call not answered")
		}
		stopListen, err = dialogMedia.ListenBackground()
		return err
	}

	for {
		request, err := waitRequest(ctx)
		if err != nil {
			return err
		}
		log.Info("Agent New Request", "req.op", request.OP, "req.id", request.ID)

		data, err := func() (any, error) {
			switch request.OP {
			case "ring":
				return nil, d.Ringing()
			case "answer":
				med, err := d.Answer(diago.AnswerOptions{})
				if err != nil {
					return nil, err
				}
				dialogMedia = med
				defer dialogMedia.Close()

				// We want to have listening in background, to prevent bad streaming
				if err := startListen(); err != nil {
					return nil, err
				}

				return nil, nil
			case "hangup":
				if err := d.Hangup(ctx); err != nil {
					return nil, err
				}
				return nil, &errAgentExit{}

			case "read_dtmf":
				if d.LoadState() != sip.DialogStateConfirmed {
					return nil, &errResponse{400, "call not answered"}
				}

				args := struct {
					Termination     string `json:"termination"`
					DurationSeconds int    `json:"duration_sec,omitempty"`
				}{
					DurationSeconds: 10,
				}
				if err := json.Unmarshal(request.Data, &args); err != nil {
					return nil, err
				}

				r, err := dialogMedia.AudioReaderDTMF()
				if err != nil {
					return nil, err
				}

				if err := writeResponse(request, 183, "Read DTMF In Progress", nil); err != nil {
					return nil, err
				}

				// Stop Listen active stream
				if err := stopListen(); err != nil {
					return nil, err
				}

				result := strings.Builder{}
				errTermination := errors.New("termination")
				{
					err := r.Listen(func(dtmf rune) error {
						log.Debug("Writing dtmf", "dtmf", string(dtmf))
						result.WriteRune(dtmf)
						// If is termination exit
						if string(dtmf) == args.Termination {
							return errTermination
						}
						return nil
					}, time.Duration(args.DurationSeconds)*time.Second)
					if err != nil && !errors.Is(err, errTermination) {
						return nil, err
					}
				}

				// Keep listening
				if err := startListen(); err != nil {
					return nil, err
				}

				return struct {
					Dtmf string `json:"dtmf"`
				}{
					Dtmf: result.String(),
				}, nil

			case "listen":
				// Short recordings
				// - timeout
				// - silence detection

				args := struct {
					DurationSeconds int    `json:"duration_sec,omitempty"`
					AudioFormat     string `json:"audio_format"`
				}{
					DurationSeconds: 10,
				}
				if err := json.Unmarshal(request.Data, &args); err != nil {
					return nil, err
				}

				// 30 sec ~= 480KB for alaw/ulaw.
				if args.DurationSeconds > 30 {
					return nil, &errResponse{code: 400, reason: "Bad Request - Duration Seconds above Max"}
				}

				// records call
				if err := stopListen(); err != nil {
					return nil, err
				}

				props := diago.MediaProps{}
				ar, _ := dialogMedia.AudioReader(diago.WithAudioReaderMediaProps(&props))
				// aw, _ := d.AudioWriter()

				transcoder := &audio.PCMDecoderReader{}
				if err := transcoder.Init(props.Codec, ar); err != nil {
					return nil, err
				}

				recordID := []byte(request.ID)
				// Type of Data, Len of ID, request ID, Audio Codecs
				memoryBuf := make([]byte, 1+4+len(recordID)+props.Codec.Samples16()*50*10) // 10 second buf of PCM of 8000h
				memoryBuf[0] = 1                                                           // Recording/Audio
				binary.BigEndian.PutUint32(memoryBuf[1:5], uint32(len(recordID)))
				n := copy(memoryBuf[5:], recordID)
				metadataSize := n + 5
				// Record
				recordBuf := &recordBuffer{
					memoryBuf: memoryBuf,
					n:         metadataSize, // This makes sure we start writing from offset
				}

				// Send that Recording is started
				if err := writeResponse(request, 183, "Recording In Progress", nil); err != nil {
					return nil, err
				}

				_, err := media.Copy(transcoder, recordBuf)
				if err != nil {
					if !errors.Is(err, io.ErrClosedPipe) {
						return nil, err
					}
				}

				if err := writeResponse(request, 190, "Recording Write", nil); err != nil {
					return nil, err
				}

				// Make sure metadata is present
				recording := recordBuf.Bytes()

				// Buffer must be correct
				if len(recording) < metadataSize {
					panic("buffered is lower than metadata size")
				}
				if binary.BigEndian.Uint32(recording[1:5]) != uint32(len(recordID)) {
					panic("recording lengths is not set correctly")
				}

				if testRecID := string(recording[5 : 5+len(recordID)]); testRecID != request.ID {
					panic("recording ID is not set correctly testrecid=" + testRecID)
				}

				if args.AudioFormat == "wav" {
					wavBuf := bytes.NewBuffer(make([]byte, 0, 44+len(recording)))
					_, err := audio.WavWrite(wavBuf, recording[metadataSize:], audio.WavWriteOpts{
						SampleRate:  int(props.Codec.SampleRate),
						BitDepth:    16,
						NumChans:    props.Codec.NumChannels,
						AudioFormat: 1,
					})
					if err != nil {
						return nil, err
					}
					recording = append(recording[:metadataSize], wavBuf.Bytes()...)
				}

				if err := wsutil.WriteServerBinary(flowConn.conn, recording); err != nil {
					return nil, err
				}

				// Keep listening
				if err := startListen(); err != nil {
					return nil, err
				}

				// How now to read recording
				return struct {
					Size        int     `json:"size"`
					DurationSec float64 `json:"duration_sec"`
				}{
					Size:        recordBuf.n,
					DurationSec: float64(recordBuf.n) / float64(props.Codec.Samples16()) * props.Codec.SampleDur.Seconds(),
				}, nil

			case "play":
				if d.LoadState() != sip.DialogStateConfirmed {
					return nil, &errResponse{400, "call not answered"}
				}

				err := func() error {
					args := struct {
						Uri string `json:"uri"`
					}{}
					if err := json.Unmarshal(request.Data, &args); err != nil {
						return err
					}

					playback, err := dialogMedia.PlaybackCreate()
					if err != nil {
						return err
					}

					if err := writeResponse(request, 183, "Playback In Progress", nil); err != nil {
						return err
					}

					log.Info("Playing uri", "uri", args.Uri)
					if strings.HasPrefix(args.Uri, "file://") {
						filename := strings.TrimPrefix(args.Uri, "file://")
						file := path.Join(rpc.playbackDir, filename)
						file = path.Clean(file)
						_, err = playback.PlayFile(file)
						return err
					}

					if strings.HasPrefix(args.Uri, "http") {
						_, err = playback.PlayURL(args.Uri)
						return err
					}

					return &errResponse{400, "unknown playback type uri=" + args.Uri}
				}()
				return nil, err

			case "echo":
				if d.LoadState() != sip.DialogStateConfirmed {
					return nil, &errResponse{400, "Call Not Answered"}
				}

				if err := writeResponse(request, 183, "Echo In Progress", nil); err != nil {
					return nil, err
				}
				return nil, dialogMedia.Echo()

			case "redirect":
				args := struct {
					Endpoint string `json:"endpoint"`
				}{}
				if err := json.Unmarshal(request.Data, &args); err != nil {
					return nil, err
				}
				end, exists := rpc.Config.Endpoints[args.Endpoint]
				if !exists {
					return nil, &errResponse{400, "Endpoint Does Not Exist"}
				}
				if end.Match.Type == "flow" {
					return nil, &errResponse{400, "Endpoint Can Not Be Flow API"}
				}

				return nil, &errAgentExit{
					Redirect: true,
					Endpoint: end,
				}

			default:
				return nil, fmt.Errorf("unknown action")
			}
			return nil, nil
		}()

		if err != nil {
			if errors.Is(err, context.Canceled) && request.ctx.Err() != nil {
				writeResponse(request, 499, "Client Closed Request", data)
				return err
			}

			if (errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed)) && d.Context().Err() != nil {
				writeResponse(request, 487, "Request terminated", data)
				return err
			}

			var e *errResponse
			if errors.As(err, &e) {
				writeResponse(request, e.code, e.reason, data)
				continue
			}

			// Are we exiting. In this case we answer OK and exit
			var eExit *errAgentExit
			if errors.As(err, &eExit) {
				writeResponse(request, 200, "OK", data)
				// Are we redirecting
				if eExit.Redirect {
					*next = eExit.Endpoint
				}
				return nil
			}

			writeResponse(request, 500, "Internal Server Error", data)
			return err
		}
		// This may not work like this in case we need to return data
		writeResponse(request, 200, "OK", data)
	}
}

type errResponse struct {
	code   int
	reason string
}

func (e *errResponse) Error() string {
	return e.reason
}

type errAgentExit struct {
	Redirect bool
	Endpoint ConfigEndpoint
}

func (e *errAgentExit) Error() string {
	return "Agent redirected to endpoint=" + e.Endpoint.Name
}

type recordBuffer struct {
	memoryBuf []byte
	n         int
	// onBuffered func([]byte) error
}

func (buf *recordBuffer) Write(b []byte) (int, error) {
	// Alignment of buf size with num samples
	if avail := cap(buf.memoryBuf) - buf.n; avail <= len(b) {
		if buf.n == 0 {
			return 0, io.ErrUnexpectedEOF
		}

		// Can this last frame fit?
		if avail == len(b) {
			buf.n += copy(buf.memoryBuf[buf.n:], b)
		}

		// Buffer is full
		return 0, io.ErrClosedPipe
	}

	buf.n += copy(buf.memoryBuf[buf.n:], b)
	return len(b), nil
}

func (buf *recordBuffer) Bytes() []byte {
	return buf.memoryBuf[:buf.n]
}
