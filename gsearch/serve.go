// serve.go - the local web UI for the Google SERP scraper.
//
// runServe starts the engine (one shared Chromium session) and serves:
//
//	GET /          the search UI
//	GET /api/search?q=..&num=..&max=..
//	GET /api/status
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scraper/gsearch"
)

//go:embed index.html
var assets embed.FS

func runServe() {
	newFS("serve")
	addr := flagStr("addr", ":8080", "listen address")
	display := flagStr("display", ":99", "Xvfb display")
	tabs := flagInt("tabs", 1, "browser tabs / concurrency (lower = less memory)")
	interval := flagDur("interval", 2*time.Second, "min delay between requests")
	timeout := flagDur("timeout", 20*time.Second, "per-page load timeout")
	retries := flagInt("retries", 3, "retries per search on CAPTCHA/timeout")
	parseFlags()

	eng, err := gsearch.New(gsearch.Config{
		Display:     *display,
		Concurrency: *tabs,
		Interval:    *interval,
		Timeout:     *timeout,
		Retries:     *retries,
	})
	if err != nil {
		log.Fatalf("engine: %v", err)
	}
	defer eng.Close()
	log.Printf("browser ready with %d tabs", *tabs)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing q"})
			return
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("num"))
		if perPage <= 0 {
			perPage = 20
		}
		maxR, _ := strconv.Atoi(r.URL.Query().Get("max"))
		if maxR <= 0 {
			maxR = 100
		}
		start := time.Now()
		res, err := eng.Search(q, perPage, maxR)
		resp := map[string]any{
			"query":       q,
			"count":       len(res),
			"duration_ms": time.Since(start).Milliseconds(),
			"results":     res,
		}
		if err != nil {
			resp["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		tabsN, _ := eng.Status()
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "tabs": tabsN})
	})

	// /api/search/stream streams live progress over SSE, then sends a final
	// "done" event carrying the full result payload.
	mux.HandleFunc("/api/search/stream", func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			http.Error(w, `{"error":"missing q"}`, http.StatusBadRequest)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		perPage, _ := strconv.Atoi(r.URL.Query().Get("num"))
		if perPage <= 0 {
			perPage = 20
		}
		maxR, _ := strconv.Atoi(r.URL.Query().Get("max"))
		if maxR <= 0 {
			maxR = 100
		}

		send := func(kind, msg string, count int, extra map[string]any) {
			if r.Context().Err() != nil { // client went away
				return
			}
			ev := map[string]any{"kind": kind, "message": msg, "count": count}
			for k, v := range extra {
				ev[k] = v
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}

		start := time.Now()
		res, err := eng.SearchWithProgress(q, perPage, maxR, func(p gsearch.Progress) {
			send(p.Kind, p.Message, p.Count, nil)
		})

		out := map[string]any{
			"query":       q,
			"count":       len(res),
			"duration_ms": time.Since(start).Milliseconds(),
			"results":     res,
		}
		if err != nil {
			out["error"] = err.Error()
			send("done", "Search failed: "+err.Error(), len(res), out)
			return
		}
		send("done", fmt.Sprintf("Found %d results", len(res)), len(res), out)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, _ := assets.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	log.Printf("web UI on http://127.0.0.1%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
