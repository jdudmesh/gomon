package webui

// gomon is a simple command line tool that watches your files and automatically restarts the application when it detects any changes in the working directory.
// Copyright (C) 2023 John Dudmesh

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/a-h/templ"
	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/console"
	notif "github.com/jdudmesh/gomon/internal/notification"
	"github.com/jdudmesh/gomon/internal/process"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/r3labs/sse/v2"
	log "github.com/sirupsen/logrus"
)

type KiloEvent struct {
	Target string `json:"x-kilo-target"`
	Swap   string `json:"x-kilo-swap"`
	Markup string `json:"x-kilo-markup"`
	Action string `json:"x-kilo-action"`
}

type server struct {
	isEnabled           bool
	port                int
	httpServer          *http.Server
	sseServer           *sse.Server
	db                  *sqlx.DB
	globalSystemControl notif.NotificationChannel
	eventSink           chan notif.Notification
	isClosed            atomic.Bool
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		w.Header().Set("Access-Control-Allow-Origin", origin)
		next.ServeHTTP(w, r)
	})
}

func New(cfg config.Config, gsc notif.NotificationChannel, db *sqlx.DB) (*server, error) {
	srv := &server{
		isEnabled:           cfg.UI.Enabled,
		port:                cfg.UI.Port,
		db:                  db,
		globalSystemControl: gsc,
		eventSink:           make(chan notif.Notification),
		isClosed:            atomic.Bool{},
	}

	if !srv.isEnabled {
		return srv, nil
	}

	if srv.port == 0 {
		srv.port = 4001
	}

	srv.sseServer = sse.New()
	srv.sseServer.AutoReplay = false
	srv.sseServer.Headers["Access-Control-Allow-Origin"] = "*"
	srv.sseServer.CreateStream("events")

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.indexPageHandler)
	mux.HandleFunc("/dist/main.js", srv.clientBundleScriptHandler)
	mux.HandleFunc("/dist/main.css", srv.clientBundleStylesheetHandler)
	mux.Handle("/actions/restart", withCORS(http.HandlerFunc(srv.restartActionHandler)))
	mux.Handle("/actions/exit", withCORS(http.HandlerFunc(srv.exitActionHandler)))
	mux.Handle("/actions/search", withCORS(http.HandlerFunc(srv.searchActionHandler)))
	mux.Handle("/components/search-select", withCORS(http.HandlerFunc(srv.searchSelectComponentHandler)))
	mux.Handle("/sse", srv.sseServer)

	srv.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", srv.port),
		Handler: mux,
	}

	return srv, nil
}

func (c *server) Start() error {
	if !c.isEnabled {
		return nil
	}

	log.Infof("Starting UI server on http://localhost:%d", c.port)
	go func() {
		err := c.httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.globalSystemControl <- notif.Notification{
				Type:     notif.NotificationTypeSystemError,
				Message:  fmt.Sprintf("ui server shut down unexpectedly: %v", err),
				Metadata: err,
			}
		}
	}()

	go func() {
		err := (error)(nil)
		for ev := range c.eventSink {
			switch ev.Type {
			case notif.NotificationTypeStartup:
				err = c.sendRunEvent(ev.Metadata.(*console.LogRun))
			case notif.NotificationTypeLogEvent:
				err = c.sendLogEvent(ev.Metadata.(*console.LogEvent))
			}
			if err != nil {
				log.Errorf("sending log event: %v", err)
			}
		}
		log.Info("closing event monitor")
	}()

	return nil
}

func (c *server) Close() error {
	if c.isClosed.Load() {
		return nil
	}
	log.Info("closing UI server")
	c.isClosed.Store(true)

	close(c.eventSink)

	if c.sseServer != nil {
		c.sseServer.Close()
	}

	if c.httpServer != nil {
		err := c.httpServer.Close()
		if err != nil {
			return fmt.Errorf("closing http server: %w", err)
		}
	}

	return nil
}

func (c *server) Notify(n notif.Notification) {
	if c.isClosed.Load() {
		return
	}
	c.eventSink <- n
}

func (c *server) sendLogEvent(ev *console.LogEvent) error {
	buffer := bytes.Buffer{}
	err := Event(ev).Render(context.Background(), &buffer)
	if err != nil {
		return fmt.Errorf("rendering event: %w", err)
	}

	msg := KiloEvent{
		Target: "#run-" + strconv.Itoa(ev.RunID),
		Swap:   "beforeend scroll:lastchild",
		Markup: buffer.String(),
	}
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshalling event: %w", err)
	}

	c.sseServer.Publish("events", &sse.Event{
		Data: msgBytes,
	})

	return nil
}

func (c *server) sendRunEvent(ev *console.LogRun) error {
	buffer := bytes.Buffer{}
	err := EmptyRun(ev.ID).Render(context.Background(), &buffer)
	if err != nil {
		return fmt.Errorf("rendering event: %w", err)
	}

	msg := KiloEvent{
		Target: "#log-output-inner",
		Swap:   "beforeend scroll:lastchild",
		Markup: buffer.String(),
	}
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshalling event: %w", err)
	}
	c.sseServer.Publish("events", &sse.Event{
		Data: msgBytes,
	})

	buffer = bytes.Buffer{}
	err = c.searchSelectComponent(&buffer)
	if err != nil {
		log.Errorf("rendering: %v", err)
		return fmt.Errorf("rendering event: %w", err)
	}
	msg = KiloEvent{
		Target: "#search-select",
		Swap:   "outerHTML",
		Markup: buffer.String(),
	}
	msgBytes, err = json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshalling event: %w", err)
	}
	c.sseServer.Publish("events", &sse.Event{
		Data: msgBytes,
	})

	return nil
}

func (c *server) restartActionHandler(w http.ResponseWriter, r *http.Request) {
	c.globalSystemControl <- notif.Notification{
		Type:    notif.NotificationTypeHardRestartRequested,
		Message: process.ForceHardRestart,
	}
	w.WriteHeader(http.StatusOK)
}

func (c *server) exitActionHandler(w http.ResponseWriter, r *http.Request) {
	c.globalSystemControl <- notif.Notification{
		Type:    notif.NotificationTypeSystemShutdown,
		Message: process.ForceHardRestart,
	}
	w.WriteHeader(http.StatusOK)
}

func (c *server) searchActionHandler(w http.ResponseWriter, r *http.Request) {
	var err error
	runID := r.URL.Query().Get("r")
	stm := r.URL.Query().Get("stm")
	filter := r.URL.Query().Get("q")
	events := [][]*console.LogEvent{}

	if runID == "" {
		err = c.db.Get(&runID, "SELECT id FROM runs ORDER BY created_at DESC LIMIT 1;")
		if err != nil {
			log.Errorf("getting run id: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	if runID != "" {
		params := map[string]interface{}{}
		sql := "SELECT * FROM events WHERE "
		if runID == "all" {
			sql += "1 = 1 " // dummy clause
		} else {
			params["run_id"] = runID
			sql += "run_id = :run_id "
		}
		if !(stm == "" || stm == "all") {
			sql += " AND event_type = :event_type "
			params["event_type"] = stm
		}
		if filter != "" {
			sql += " AND event_data LIKE :event_data "
			params["event_data"] = "%" + filter + "%"
		}
		sql += " ORDER BY run_id ASC, created_at ASC limit 1000;"

		res, err := c.db.NamedQuery(sql, params)
		if err != nil {
			log.Errorf("getting event: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer res.Close()

		var lastRunID int = -1
		for res.Next() {
			ev := new(console.LogEvent)
			err = res.StructScan(ev)
			if err != nil {
				log.Errorf("scanning event: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if lastRunID != ev.RunID {
				events = append(events, []*console.LogEvent{})
				lastRunID = int(ev.RunID)
			}
			events[len(events)-1] = append(events[len(events)-1], ev)
		}
	}

	markup := (templ.Component)(nil)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(events) == 0 {
		markup = SearchNoResults()
	} else {
		markup = EventList(events)
	}

	err = markup.Render(r.Context(), w)
	if err != nil {
		log.Errorf("rendering index: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (c *server) searchSelectComponentHandler(w http.ResponseWriter, r *http.Request) {
	buf := bytes.Buffer{}
	err := c.searchSelectComponent(&buf)
	if err != nil {
		log.Errorf("rendering: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

func (c *server) searchSelectComponent(w io.Writer) error {
	var err error

	runs := []*console.LogRun{}
	err = c.db.Select(&runs, "SELECT * FROM runs ORDER BY created_at DESC limit 50")
	if err != nil {
		return fmt.Errorf("getting runs: %w", err)
	}

	currentRun := 0
	if len(runs) > 0 {
		currentRun = int(runs[0].ID)
	}

	markup := SearchSelect(runs, currentRun)
	err = markup.Render(context.Background(), w)
	if err != nil {
		return fmt.Errorf("rendering data: %w", err)
	}

	return nil
}

func (c *server) indexPageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(index)
}

func (c *server) clientBundleScriptHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Write(script)
}

func (c *server) clientBundleStylesheetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(stylesheet)
}
