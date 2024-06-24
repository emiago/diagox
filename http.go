// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"net/http/httputil"
	_ "net/http/pprof"

	"github.com/arl/statsviz"
	"github.com/emiago/diago"
)

//go:embed frontend
var frontendFS embed.FS

type Handler struct {
	env      EnvConfig
	CdrStore CDRStorage

	D         *diago.Diago
	tmpls     *template.Template
	SIPtracer SIPTracer
}

func httpError(w http.ResponseWriter, msg string, code int, err error) {
	if err != nil {
		slog.Error("http response due to error", "error", err)
	}
	http.Error(w, msg, code)
}

func httpAPIError(r *http.Request, w http.ResponseWriter, msg string, code int, err error) {
	if err != nil {
		slog.Error("http response due to error", "error", err)
	}

	if isJsonRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"error":"%s"}`, msg)
		return
	}

	http.Error(w, msg, code)
}

func httpSlog(next http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			slog.Info("New http request", "method", r.Method, "path", r.URL.Path)
			next.ServeHTTP(w, r)
		},
	)
}

func httpLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, _ := httputil.DumpRequest(req, true)
		fmt.Printf("--- HTTP Request ---\n%s---\n", string(data))
		next.ServeHTTP(w, req)
	})
}

func isJsonRequest(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return ct == "application/json" || strings.HasPrefix(r.URL.Path, "/api")
}

func httpServer(_ context.Context, addr string, h *Handler) {
	mux := http.DefaultServeMux

	if err := statsviz.Register(mux,
		statsviz.TimeseriesPlot(plotNumberOfCalls()),
		statsviz.TimeseriesPlot(plotRoundTripTime()),
		statsviz.TimeseriesPlot(plotJitter()),
		statsviz.TimeseriesPlot(plotFractionLoss()),
		statsviz.TimeseriesPlot(plotPacketsCount()),
		statsviz.SendFrequency(5*time.Second),
	); err != nil {
		log.Error("failed statsviz", "error", err)
		return
	}

	// Connect to a server
	// nc := func() *nats.Conn {
	// 	for {
	// 		nc, err := nats.Connect(nats.DefaultURL)
	// 		if err == nil {
	// 			return nc
	// 		}
	// 		log.Error().Err(err).Msg("Failed to connect nats")
	// 		time.Sleep(1 * time.Second)
	// 	}
	// }()

	// nc.Subscribe("request", func(m *nats.Msg) {
	// 	payload := string(m.Data)

	// 	log.Info().Str("msg", payload).Msg("New request")

	// 	switch {
	// 	case strings.HasPrefix(payload, "/hangup/"):
	// 		id := strings.TrimPrefix(payload, "/hangup/")
	// 		dialog, exists := dcache.Load(id)
	// 		if exists {
	// 			dialogSess := dialog.(diago.DialogSession)
	// 			dialogSess.Hangup(context.TODO())
	// 			nc.Publish(m.Reply, []byte("Hanguped over nats"))
	// 		}

	// 	}
	// 	// nc.Publish(m.Reply, []byte("I can help!"))
	// })

	// http.HandleFunc("/hangup", func(w http.ResponseWriter, r *http.Request) {
	// 	id := r.URL.Query().Get("id")
	// 	dialog, exists := dcache.Load(id)
	// 	if !exists {
	// 		// Publish hangup to all other instances
	// 		// Requests
	// 		msg, err := nc.Request("request", []byte("/hangup/"+id), 1000*time.Millisecond)
	// 		if err != nil {
	// 			w.WriteHeader(http.StatusInternalServerError)
	// 			w.Write([]byte(err.Error()))
	// 			return
	// 		}

	// 		// Replies
	// 		w.Write(msg.Data)
	// 		return
	// 	}
	// 	dialogSess := dialog.(diago.DialogSession)
	// 	dialogSess.Hangup(r.Context())

	// 	w.Write([]byte("hangup succesfully"))
	// 	return
	// })
	// ADD HTTP as well
	mux.HandleFunc("GET /api/v1/history", h.pageHistory)

	if !h.env.FrontendEnable {
		log.Info("Start http server", "addr", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Error("Failed to start http server", "error", err)
		}
		return
	}

	// http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	staticFs, err := fs.Sub(frontendFS, "frontend/static")
	if err != nil {
		log.Error("Failed to load static files", "error", err)
		return
	}
	mux.Handle("/static/", httpSlog(http.StripPrefix("/static/", http.FileServer(http.FS(staticFs)))))

	// h.tmpls = template.Must(template.ParseGlob("./frontend/*.html"))
	h.tmpls = template.Must(template.ParseFS(frontendFS, "frontend/*.html"))

	mux.HandleFunc("/", h.pageHistory)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFs, "static/favicon.ico")
	})
	// http.HandleFunc("/dashboard", h.pageIndex)
	mux.HandleFunc("GET /history", h.pageHistory)
	mux.HandleFunc("GET /history/siptrace", h.pageHistorySipTrace)
	mux.HandleFunc("GET /history/recording", h.pageHistoryRecording)
	mux.HandleFunc("GET /recording", h.recordingRead)
	mux.HandleFunc("/configuration", h.pageConfiguration)
	mux.HandleFunc("/configuration/yaml", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.OpenFile(h.env.ConfFile, os.O_RDONLY, 0777)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()

		info, _ := f.Stat()
		http.ServeContent(w, r, h.env.ConfFile, info.ModTime(), f)
	})

	mux.HandleFunc("GET /webrtcphone", h.pageWebrtcPhone)
	mux.HandleFunc("GET /webrtc_phone", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, frontendFS, "frontend/webrtc_phone/index.html")
	})
	assetsFs, err := fs.Sub(frontendFS, "frontend/webrtc_phone/assets")
	mux.Handle("/webrtc_phone/assets/", httpSlog(http.StripPrefix("/webrtc_phone/assets/", http.FileServer(http.FS(assetsFs)))))

	log.Info("Start http server", "addr", addr)

	handler := addCORS(mux)
	if h.env.HTTPDebug {
		handler = httpLog(handler)
	}

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Error("Failed to start http server", "error", err)
	}
}

func addCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Cross-Origin-Embedder-Policy", "unsafe-none")
		w.Header().Set("Cross-Origin-Opener-Policy", "cross-origin")
		// w.Header().Set("Content-Security-Policy", "connect-src ws://localhost:9000")
		w.Header().Set("Content-Security-Policy", "default-src * 'unsafe-inline' 'unsafe-eval' data: blob:")
		next.ServeHTTP(w, req)
	})
}

func (h *Handler) templates() *template.Template {
	if !h.env.FrontendDevMode {
		// NO debug. This loads and parses all templates onse, but bellow is better for debugging
		return h.tmpls
	}

	return template.Must(template.ParseGlob("./frontend/*.html"))
}

func (h *Handler) renderIndex(w http.ResponseWriter, r *http.Request, content string) {
	data := struct {
		Title   string
		Content string
		Sidebar bool
	}{
		Title:   "Diagox",
		Content: content,
		Sidebar: r.Header.Get("Hx-Request") != "true", // htmx navigation
	}

	h.templates().ExecuteTemplate(w, "index.html", data)
}

func (h *Handler) pageIndex(w http.ResponseWriter, r *http.Request) {
	parsedTemplate := h.templates().Lookup("index.html")
	// content := templates.Lookup(page + ".html")
	if parsedTemplate == nil {
		httpError(w, "Page not found", http.StatusNotFound, nil)
		return
	}

	content := strings.Builder{}
	if err := parsedTemplate.Execute(&content, nil); err != nil {
		httpError(w, "Template execution failed", http.StatusInternalServerError, err)
		return
	}

	h.renderIndex(w, r, content.String())
}

func (h *Handler) pageHistory(w http.ResponseWriter, r *http.Request) {

	query := r.URL.Query()
	dateFrom := query.Get("date_from")
	dateTo := query.Get("date_to")
	filters := query.Get("query")

	opts := CDRReadOptions{}
	err := func() error {
		var err error
		if dateFrom != "" && dateTo != "" {
			opts.FromTime, err = time.Parse("2006-01-02T15:04", dateFrom)
			if err != nil {
				return fmt.Errorf("failed to parse date_from: %w", err)
			}
			opts.ToTime, err = time.Parse("2006-01-02T15:04", dateTo)
			if err != nil {
				return fmt.Errorf("failed to parse date_to: %w", err)
			}
		}

		if filters != "" {
			hasFilter := false
			for _, kv := range strings.Split(filters, "|") {
				f := strings.TrimSpace(kv)
				if f == "" {
					continue
				}
				args := strings.SplitN(f, ":", 2)
				if len(args) != 2 {
					continue
				}
				key := strings.TrimSpace(args[0])
				value := strings.TrimSpace(args[1])
				set := true
				switch key {
				case "from":
					opts.From = value
				case "to":
					opts.To = value
				case "call_id":
					opts.CallID = value
				case "orig_call_id":
					opts.OriginatorCallID = value
				case "mes":
					opts.Mes, err = strconv.ParseFloat(value, 64)
					if err != nil {
						return fmt.Errorf("failed to parse mes filter: %w", err)
					}
				default:
					set = false
				}
				hasFilter = set || hasFilter
			}

			// Fallback to From if nothing provided
			if !hasFilter {
				opts.From = filters
				filters = "from: " + opts.From
			}

		}

		return err
	}()

	if err != nil {
		httpAPIError(r, w, "Bad query parameters", http.StatusBadRequest, err)
		return
	}
	// Implement paging
	buf := make([]CDR, 100)
	n, err := h.CdrStore.CDRRead(r.Context(), buf, opts)
	if err != nil {
		httpAPIError(r, w, "Failed to load cdrs", http.StatusInternalServerError, err)
		return
	}
	cdrs := buf[:n]

	slog.Debug("Loaded CDR records", "n", n, "filters", filters)

	// DO API
	if isJsonRequest(r) {
		// Return data as json
		if err := json.NewEncoder(w).Encode(cdrs); err != nil {
			httpAPIError(r, w, "Failed to parse cdrs", http.StatusInternalServerError, err)
			return
		}
		return
	}

	now := time.Now()
	if dateFrom == "" {
		dateFrom = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).Format("2006-01-02T15:04")
	}

	if dateTo == "" {
		dateTo = time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local).Format("2006-01-02T15:04")
	}

	historyData := struct {
		DateFrom string
		DateTo   string
		Query    string
		CDRS     []CDR
	}{
		DateFrom: dateFrom,
		DateTo:   dateTo,
		Query:    filters,
		CDRS:     cdrs,
	}

	parsedTemplate := h.templates().Lookup("history.html")
	// content := templates.Lookup(page + ".html")
	if parsedTemplate == nil {
		httpError(w, "Page not found", http.StatusNotFound, nil)
		return
	}

	content := strings.Builder{}
	if err := parsedTemplate.Execute(&content, historyData); err != nil {
		httpError(w, "Template execution failed", http.StatusInternalServerError, err)
		return
	}

	h.renderIndex(w, r, content.String())
}

func (h *Handler) pageHistorySipTrace(w http.ResponseWriter, r *http.Request) {
	// parsedTemplate := h.templates().Lookup("history_siptrace.html")
	// // content := templates.Lookup(page + ".html")
	// if parsedTemplate == nil {
	// 	httpError(w, "Page not found", http.StatusNotFound)
	// 	return
	// }

	// parsedTemplate.ExecuteTemplate()
	if h.SIPtracer == nil {
		httpError(w, "SIP tracer is not enabled", http.StatusNotFound, nil)
		return
	}

	callid := r.URL.Query().Get("callid")
	if callid == "" {
		httpError(w, "missing callid parameter", http.StatusBadRequest, nil)
		return
	}

	w.WriteHeader(200)
	builder := strings.Builder{}
	h.SIPtracer.SIPTraceFind(callid, func(m SIPTrace) {
		dir := " <-- "
		if m.RW == 1 {
			dir = " --> "
		}

		builder.WriteString("===> " + m.Created.Format(time.RFC3339) + " " + m.Laddr + dir + m.Raddr + " <===\n")
		builder.WriteString(m.Msg)
		builder.WriteString("\n")

		w.Write([]byte(builder.String()))
		builder.Reset()
	})
	// builder.WriteString("</pre>")
	w.Write([]byte(builder.String()))
}

func (h *Handler) pageHistoryRecording(w http.ResponseWriter, r *http.Request) {
	recordingID := r.URL.Query().Get("recording_id")
	historyData := struct {
		Title            string
		RecordingURL     string
		DownloadFileName string
	}{
		Title:            "Recording",
		RecordingURL:     "/recording?recording_id=" + recordingID,
		DownloadFileName: recordingID + ".wav",
	}
	if err := h.templates().ExecuteTemplate(w, "history_recording.html", historyData); err != nil {
		httpError(w, "Page not found", http.StatusNotFound, err)
		return
	}
}

func (h *Handler) recordingRead(w http.ResponseWriter, r *http.Request) {
	recordingID := r.URL.Query().Get("recording_id")
	filename := h.env.recordingPath(recordingID)
	file, err := os.Open(filename)
	if err != nil {
		httpError(w, "no such a file", http.StatusBadRequest, err)
		return
	}
	stat, err := file.Stat()
	if err != nil {
		httpError(w, "failed to read file properties", http.StatusBadRequest, err)
		return
	}

	http.ServeContent(w, r, stat.Name(), stat.ModTime(), file)
}

func (h *Handler) pageConfiguration(w http.ResponseWriter, r *http.Request) {
	parsedTemplate := h.templates().Lookup("configuration.html")
	// content := templates.Lookup(page + ".html")
	if parsedTemplate == nil {
		httpError(w, "Page not found", http.StatusNotFound, nil)
		return
	}

	content := strings.Builder{}
	if err := parsedTemplate.Execute(&content, nil); err != nil {
		httpError(w, "Template execution failed", http.StatusInternalServerError, err)
		return
	}

	h.renderIndex(w, r, content.String())
}

func (h *Handler) pageWebrtcPhone(w http.ResponseWriter, r *http.Request) {
	parsedTemplate := h.templates().Lookup("webrtcphone.html")
	// content := templates.Lookup(page + ".html")
	if parsedTemplate == nil {
		httpError(w, "Page not found", http.StatusNotFound, nil)
		return
	}

	content := strings.Builder{}
	if err := parsedTemplate.Execute(&content, nil); err != nil {
		httpError(w, "Template execution failed", http.StatusInternalServerError, err)
		return
	}

	h.renderIndex(w, r, content.String())
}

// renderTemplate dynamically loads the appropriate template from the embedded filesystem
func renderTemplate(w http.ResponseWriter, filename string, data any) {
	tmplPath := filepath.Join("frontend", filename)

	// For non restart needs. use local dir instead embed
	parsedTemplate, err := template.ParseFiles(tmplPath)
	// parsedTemplate, err := template.ParseFS(frontendFS, tmplPath)
	if err != nil {
		httpError(w, "Page not found", http.StatusNotFound, nil)
		return
	}
	if err := parsedTemplate.Execute(w, data); err != nil {
		httpError(w, "Template execution failed", http.StatusInternalServerError, err)
	}
}
