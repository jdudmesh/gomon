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
	"fmt"
	"net/http"
	"strconv"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/console"
	"github.com/jdudmesh/gomon/internal/process"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/r3labs/sse/v2"
	log "github.com/sirupsen/logrus"
)

type ChildProcess interface {
	HardRestart(string) error
	SoftRestart(string) error
}

type server struct {
	enabled      bool
	port         int
	httpServer   *http.Server
	sseServer    *sse.Server
	db           *sqlx.DB
	childProcess ChildProcess
	eventSink    chan interface{}
}

func New(cfg *config.Config, childProcess ChildProcess, db *sqlx.DB) (*server, error) {
	srv := &server{
		enabled:      cfg.UI.Enabled,
		port:         cfg.UI.Port,
		db:           db,
		childProcess: childProcess,
		eventSink:    make(chan interface{}),
	}

	if !srv.enabled {
		return srv, nil
	}

	if srv.port == 0 {
		srv.port = 4001
	}

	srv.sseServer = sse.New()
	srv.sseServer.AutoReplay = false
	srv.sseServer.CreateStream("logs")
	srv.sseServer.CreateStream("runs")

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/restart", srv.handleRestart)
	mux.HandleFunc("/search", srv.handlSearch)
	mux.HandleFunc("/__gomon__/events", srv.sseServer.ServeHTTP)
	srv.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", srv.port),
		Handler: mux,
	}

	return srv, nil
}

func (c *server) EventSink() chan interface{} {
	return c.eventSink
}

func (c *server) Start() error {
	if !c.enabled {
		return nil
	}

	log.Infof("UI server running on http://localhost:%d", c.port)

	go func() {
		for ev := range c.eventSink {
			if logEvent, ok := ev.(*console.LogEvent); ok {
				buffer := bytes.Buffer{}
				err := Event(logEvent).Render(context.Background(), &buffer)
				if err != nil {
					log.Errorf("rendering event: %v", err)
					continue
				}
				c.sseServer.Publish("logs", &sse.Event{
					Data: buffer.Bytes(),
				})

			} else if logRun, ok := ev.(*console.LogRun); ok {
				runDataBytes, err := json.Marshal(logRun)
				if err != nil {
					log.Errorf("marshalling run data: %v", err)
					return
				}
				c.sseServer.Publish("runs", &sse.Event{
					Data: runDataBytes,
				})
			}
		}
	}()

	go func() {
		err := c.httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Infof("shutting down UI server: %v", err)
		}
	}()

	return nil
}

func (c *server) Close() error {
	log.Info("closing UI server")
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

func (c *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	var err error

	runs := []*console.LogRun{}
	err = c.db.Select(&runs, "SELECT * FROM runs ORDER BY created_at DESC")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	run := int(runs[0].ID)
	runParam := r.URL.Query().Get("run")
	if !(runParam == "" || runParam == "current") {
		run, err = strconv.Atoi(runParam)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	events := []*console.LogEvent{}
	err = c.db.Select(&events, "SELECT * FROM events WHERE run_id = ? ORDER BY created_at ASC", run)
	if err != nil {
		log.Errorf("getting event: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	page := Console(run, runs, events)
	err = page.Render(r.Context(), w)
	if err != nil {
		log.Errorf("rendering index: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (c *server) handleRestart(w http.ResponseWriter, r *http.Request) {
	err := c.childProcess.HardRestart(process.ForceHardRestart)
	if err != nil {
		log.Errorf("hard restart: %+v", err)
	}
	w.WriteHeader(http.StatusOK)
}

func (c *server) handlSearch(w http.ResponseWriter, r *http.Request) {
	var err error
	run := r.URL.Query().Get("run")
	stm := r.URL.Query().Get("stm")
	filter := r.URL.Query().Get("filter")
	events := []*console.LogEvent{}

	params := map[string]interface{}{"run_id": run}
	sql := "SELECT * FROM events WHERE run_id = :run_id "
	if !(stm == "" || stm == "all") {
		sql += " AND event_type = :event_type "
		params["event_type"] = stm
	}
	if filter != "" {
		sql += " AND event_data LIKE :event_data "
		params["event_data"] = "%" + filter + "%"
	}
	sql += " ORDER BY created_at ASC;"

	res, err := c.db.NamedQuery(sql, params)
	if err != nil {
		log.Errorf("getting event: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer res.Close()
	for res.Next() {
		ev := new(console.LogEvent)
		err = res.StructScan(ev)
		if err != nil {
			log.Errorf("scanning event: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		events = append(events, ev)
	}

	if len(events) == 0 {
		_, err = w.Write([]byte("<div class=\"text-2xl text-bold\">no events found</div>"))
		if err != nil {
			log.Errorf("writing response: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	markup := EventList(events)
	err = markup.Render(r.Context(), w)
	if err != nil {
		log.Errorf("rendering index: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}
