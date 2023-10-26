package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/r3labs/sse/v2"
	log "github.com/sirupsen/logrus"
)

var schema = `
CREATE TABLE IF NOT EXISTS runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_runs_created_at ON runs(created_at);

CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL,
	event_type TEXT NOT NULL,
	event_data TEXT NOT NULL,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_events_event_type ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_created_at ON events(created_at);
`

type ConsoleCaptureOption func(*consoleCapture) error

func WithRespawn(respawn chan bool) ConsoleCaptureOption {
	return func(c *consoleCapture) error {
		c.respawn = respawn
		return nil
	}
}

type consoleCapture struct {
	enabled      bool
	port         int
	httpServer   *http.Server
	sseServer    *sse.Server
	dataPath     string
	db           *sqlx.DB
	currentRunID atomic.Int64
	respawn      chan bool
	stdoutWriter chan string
	stderrWriter chan string
}

type LogRun struct {
	ID        int       `db:"id" json:"id"`
	CreatedAt time.Time `db:"created_at" json:"createdAt"`
}

type LogEvent struct {
	ID        int       `db:"id" json:"id"`
	RunID     int       `db:"run_id" json:"runId"`
	EventType string    `db:"event_type" json:"eventType"`
	EventData string    `db:"event_data" json:"eventData"`
	CreatedAt time.Time `db:"created_at" json:"createdAt"`
}

type streamWriter struct {
	eventSink chan string
}

func New(config config.Config, opts ...ConsoleCaptureOption) (*consoleCapture, error) {
	cap := &consoleCapture{
		enabled:      config.UI.Enabled,
		port:         config.UI.Port,
		stdoutWriter: make(chan string),
		stderrWriter: make(chan string),
	}

	if !cap.enabled {
		return cap, nil
	}

	if cap.port == 0 {
		cap.port = 4001
	}

	for _, opt := range opts {
		err := opt(cap)
		if err != nil {
			return nil, fmt.Errorf("applying option: %w", err)
		}
	}

	cap.sseServer = sse.New()
	cap.sseServer.AutoReplay = false
	cap.sseServer.CreateStream("logs")
	cap.sseServer.CreateStream("runs")

	mux := http.NewServeMux()
	mux.HandleFunc("/", cap.handleIndex)
	mux.HandleFunc("/restart", cap.handleRestart)
	mux.HandleFunc("/search", cap.handlSearch)
	mux.HandleFunc("/__gomon__/events", cap.sseServer.ServeHTTP)
	cap.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", cap.port),
		Handler: mux,
	}

	cap.dataPath = path.Join(config.RootDirectory, "./.gomon")
	_, err := os.Stat(cap.dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.Mkdir(cap.dataPath, 0755)
			if err != nil {
				return nil, fmt.Errorf("creating .gomon directory: %w", err)
			}
		} else {
			return nil, fmt.Errorf("checking for .gomon directory: %w", err)
		}
	}

	cap.db, err = sqlx.Connect("sqlite3", path.Join(cap.dataPath, "./gomon.db"))
	if err != nil {
		return nil, fmt.Errorf("connecting to sqlite: %w", err)
	}

	_, err = cap.db.Exec(schema)
	if err != nil {
		return nil, fmt.Errorf("creating db schema: %w", err)
	}

	return cap, nil
}

func (c *consoleCapture) Start() error {
	if !c.enabled {
		return nil
	}

	log.Infof("UI server running on http://localhost:%d", c.port)

	go func() {
		err := c.httpServer.ListenAndServe()
		log.Infof("shutting down UI server: %v", err)
	}()

	go func() {
		for {
			select {
			case line := <-c.stdoutWriter:
				if !c.enabled {
					os.Stdout.WriteString(line)
					continue
				}
				_, err := c.write("stdout", line)
				if err != nil {
					log.Errorf("writing stdout: %v", err)
				}
			case line := <-c.stderrWriter:
				if !c.enabled {
					os.Stderr.WriteString(line)
					continue
				}
				_, err := c.write("stderr", line)
				if err != nil {
					log.Errorf("writing stderr: %v", err)
				}
			}
		}
	}()

	return nil
}

func (c *consoleCapture) Close() error {
	close(c.stdoutWriter)
	close(c.stderrWriter)

	if c.sseServer != nil {
		c.sseServer.Close()
	}

	if c.httpServer != nil {
		return c.httpServer.Close()
	}

	return nil
}

func (c *consoleCapture) Stdout() io.Writer {
	return &streamWriter{eventSink: c.stdoutWriter}
}

func (c *consoleCapture) Stderr() io.Writer {
	return &streamWriter{eventSink: c.stderrWriter}
}

func (c *consoleCapture) write(logType, logData string) (int, error) {
	for _, line := range strings.Split(logData, "\n") {
		res, err := c.db.Exec("INSERT INTO events (run_id, event_type, event_data, created_at) VALUES ($1, $2, $3, $4)", c.currentRunID.Load(), logType, line, time.Now())
		if err != nil {
			return 0, fmt.Errorf("inserting event: %w", err)
		}

		eventID, err := res.LastInsertId()
		if err != nil {
			log.Errorf("getting last insert id: %v", err)
			continue
		}

		event := &LogEvent{}
		err = c.db.Get(event, "SELECT * FROM events WHERE id = $1", eventID)
		if err != nil {
			log.Errorf("getting event: %v", err)
			continue
		}

		buffer := bytes.Buffer{}
		err = Event(*event).Render(context.Background(), &buffer)
		if err != nil {
			log.Errorf("rendering event: %v", err)
			continue
		}
		c.sseServer.Publish("logs", &sse.Event{
			Data: buffer.Bytes(),
		})
	}

	return len(logData), nil
}

func (c *consoleCapture) Respawning() {
	if c == nil {
		return
	}
	runDate := time.Now()
	res, err := c.db.Exec("INSERT INTO runs (created_at) VALUES ($1)", runDate)
	if err != nil {
		log.Errorf("inserting run: %v", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		log.Errorf("getting last insert id: %v", err)
	}
	c.currentRunID.Store(runID)

	runData := struct {
		ID   int64     `json:"id"`
		Date time.Time `json:"date"`
	}{
		ID:   runID,
		Date: runDate,
	}
	runDataBytes, err := json.Marshal(runData)
	if err != nil {
		log.Errorf("marshalling run data: %v", err)
		return
	}
	c.sseServer.Publish("runs", &sse.Event{
		Data: runDataBytes,
	})
}

func (c *consoleCapture) handleIndex(w http.ResponseWriter, r *http.Request) {
	var err error

	runs := []LogRun{}
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

	events := []LogEvent{}
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

func (c *consoleCapture) handleRestart(w http.ResponseWriter, r *http.Request) {
	if c.respawn == nil {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	c.respawn <- true
	w.WriteHeader(http.StatusOK)
}

func (c *consoleCapture) handlSearch(w http.ResponseWriter, r *http.Request) {
	var err error
	run := r.URL.Query().Get("run")
	stm := r.URL.Query().Get("stm")
	filter := r.URL.Query().Get("filter")
	events := []LogEvent{}

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
		var ev LogEvent
		err = res.StructScan(&ev)
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

func (w *streamWriter) Write(p []byte) (int, error) {
	w.eventSink <- string(p)
	return len(p), nil
}
