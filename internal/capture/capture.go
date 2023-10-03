package capture

import (
	"bytes"
	"context"
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

type consoleCapture struct {
	port         int
	httpServer   *http.Server
	sseServer    *sse.Server
	dataPath     string
	db           *sqlx.DB
	currentRunID atomic.Int64
}

type LogRun struct {
	ID        int64     `db:"id" json:"id"`
	CreatedAt time.Time `db:"created_at" json:"createdAt"`
}

type LogEvent struct {
	ID        int64     `db:"id" json:"id"`
	RunID     int64     `db:"run_id" json:"runId"`
	EventType string    `db:"event_type" json:"eventType"`
	EventData string    `db:"event_data" json:"eventData"`
	CreatedAt time.Time `db:"created_at" json:"createdAt"`
}

type stdoutWriter struct {
	captureMgr *consoleCapture
}

type stderrWriter struct {
	captureMgr *consoleCapture
}

func New(config config.Config) *consoleCapture {
	if !config.UI.Enabled {
		return nil
	}
	cap := &consoleCapture{
		port: config.UI.Port,
	}
	if cap.port == 0 {
		cap.port = 4001
	}

	cap.sseServer = sse.New()
	cap.sseServer.AutoReplay = false
	cap.sseServer.CreateStream("logs")

	mux := http.NewServeMux()
	mux.HandleFunc("/", cap.handleIndex)
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
				log.Fatalf("creating .gomon directory: %v", err)
			}
		} else {
			log.Fatalf("checking for .gomon directory: %v", err)
		}
	}

	cap.db, err = sqlx.Connect("sqlite3", path.Join(cap.dataPath, "./gomon.db"))
	if err != nil {
		log.Fatalf("connecting to sqlite: %v", err)
	}

	_, err = cap.db.Exec(schema)
	if err != nil {
		log.Fatalf("creating console capture db schema: %v", err)
	}

	return cap
}

func (c *consoleCapture) Start() error {
	log.Infof("UI server running on http://localhost:%d", c.port)
	go func() {
		err := c.httpServer.ListenAndServe()
		log.Infof("shutting down UI server: %v", err)
	}()

	return nil
}

func (c *consoleCapture) Stop() error {
	if c.sseServer != nil {
		c.sseServer.Close()
	}
	if c.httpServer != nil {
		return c.httpServer.Close()
	}
	return nil
}

func (c *consoleCapture) Stdout() io.Writer {
	return &stdoutWriter{captureMgr: c}
}

func (c *consoleCapture) Stderr() io.Writer {
	return &stderrWriter{captureMgr: c}
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
		err = Event(event).Render(context.Background(), &buffer)
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
	res, err := c.db.Exec("INSERT INTO runs (created_at) VALUES ($1)", time.Now())
	if err != nil {
		log.Errorf("inserting run: %v", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		log.Errorf("getting last insert id: %v", err)
	}
	c.currentRunID.Store(runID)
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

	page := Index(run, runs, events)
	err = page.Render(r.Context(), w)
	if err != nil {
		log.Errorf("rendering index: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

}

func (w *stdoutWriter) Write(p []byte) (int, error) {
	if w.captureMgr == nil {
		return os.Stdout.Write(p)
	}
	return w.captureMgr.write("stdout", string(p))
}

func (w *stderrWriter) Write(p []byte) (int, error) {
	if w.captureMgr == nil {
		return os.Stderr.Write(p)
	}
	return w.captureMgr.write("stderr", string(p))
}
