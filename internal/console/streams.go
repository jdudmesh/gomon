package console

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
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
)

type streams struct {
	enabled          bool
	db               *sqlx.DB
	stdoutWriter     chan string
	stderrWriter     chan string
	currentRunID     atomic.Int64
	consoleListeners []chan interface{}
	closed           atomic.Bool
}

type streamWriter struct {
	eventSink chan string
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

func New(cfg *config.Config, db *sqlx.DB) (*streams, error) {
	stm := &streams{
		enabled:          cfg.UI.Enabled,
		db:               db,
		stdoutWriter:     make(chan string),
		stderrWriter:     make(chan string),
		consoleListeners: []chan interface{}{},
		closed:           atomic.Bool{},
	}

	return stm, nil
}

func (s *streams) AddEventSink(sink chan interface{}) {
	s.consoleListeners = append(s.consoleListeners, sink)
}

func (s *streams) RemoveEventSink(sink chan interface{}) {
	for i, listener := range s.consoleListeners {
		if listener == sink {
			s.consoleListeners = append(s.consoleListeners[:i], s.consoleListeners[i+1:]...)
			break
		}
	}
}

func (s *streams) Start() error {
	go func() {
		for {
			select {
			case line := <-s.stdoutWriter:
				if !s.enabled {
					os.Stdout.WriteString(line)
					continue
				}
				err := s.write("stdout", line)
				if err != nil {
					log.Errorf("writing stdout: %v", err)
				}
			case line := <-s.stderrWriter:
				if !s.enabled {
					os.Stderr.WriteString(line)
					continue
				}
				err := s.write("stderr", line)
				if err != nil {
					log.Errorf("writing stderr: %v", err)
				}
			}
		}
	}()

	return nil
}

func (s *streams) Close() error {
	log.Info("closing console streams")
	s.closed.Store(true)
	close(s.stdoutWriter)
	close(s.stderrWriter)
	return nil
}

func (s *streams) Stdout() io.Writer {
	return &streamWriter{eventSink: s.stdoutWriter}
}

func (s *streams) Stderr() io.Writer {
	return &streamWriter{eventSink: s.stderrWriter}
}

func (s *streams) write(logType, logData string) error {
	if s.closed.Load() {
		if len(logData) > 0 {
			log.Warnf("console streams closed, dropping log data: %s", logData)
		}
		return nil
	}

	runID := s.currentRunID.Load()
	eventDate := time.Now()

	for _, line := range strings.Split(logData, "\n") {
		res, err := s.db.Exec(`
				INSERT INTO events (run_id, event_type, event_data, created_at)
				VALUES ($1, $2, $3, $4)`, runID, logType, line, eventDate)
		if err != nil {
			return fmt.Errorf("inserting event: %w", err)
		}

		eventID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("getting last insert id: %w", err)
		}

		event := &LogEvent{
			ID:        int(eventID),
			RunID:     int(runID),
			EventType: logType,
			EventData: line,
			CreatedAt: eventDate,
		}

		s.notifyEventListeners(event)
	}

	return nil
}

func (s *streams) OnHardRestart() {
	if s == nil {
		return
	}

	runDate := time.Now()
	res, err := s.db.Exec("INSERT INTO runs (created_at) VALUES ($1)", runDate)
	if err != nil {
		log.Errorf("inserting run: %v", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		log.Errorf("getting last insert id: %v", err)
	}
	s.currentRunID.Store(runID)

	run := &LogRun{
		ID:        int(runID),
		CreatedAt: runDate,
	}
	s.notifyEventListeners(run)
}

func (s *streams) notifyEventListeners(event interface{}) {
	for _, listener := range s.consoleListeners {
		listener <- event
	}
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.eventSink <- string(p)
	return len(p), nil
}
