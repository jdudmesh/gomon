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
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/notification"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
)

type streams struct {
	enabled               bool
	stdoutWriter          chan string
	stderrWriter          chan string
	currentRunID          atomic.Int64
	currentChildProcessID string
	callbackFn            notification.NotificationCallback
}

type streamWriter struct {
	streamConsumer chan string
}

func New(cfg config.Config, callbackFn notification.NotificationCallback) (*streams, error) {
	stm := &streams{
		enabled:      cfg.UI.Enabled,
		stdoutWriter: make(chan string),
		stderrWriter: make(chan string),
		callbackFn:   callbackFn,
	}

	return stm, nil
}

func (s *streams) Start() error {
	for {
		select {
		case line := <-s.stdoutWriter:
			if !s.enabled {
				os.Stdout.WriteString(line)
				continue
			}
			err := s.write(notification.NotificationTypeStdOut, line, s.callbackFn)
			if err != nil {
				log.Errorf("writing stdout: %v", err)
			}
		case line := <-s.stderrWriter:
			if !s.enabled {
				os.Stderr.WriteString(line)
				continue
			}
			err := s.write(notification.NotificationTypeStdErr, line, s.callbackFn)
			if err != nil {
				log.Errorf("writing stderr: %v", err)
			}
		}
	}
}

func (s *streams) Close() error {
	log.Info("closing console streams")
	close(s.stdoutWriter)
	close(s.stderrWriter)
	return nil
}

func (s *streams) Stdout() io.Writer {
	return &streamWriter{streamConsumer: s.stdoutWriter}
}

func (s *streams) Stderr() io.Writer {
	return &streamWriter{streamConsumer: s.stderrWriter}
}

func (s *streams) write(logType notification.NotificationType, logData string, callbackFn notification.NotificationCallback) error {
	eventDate := time.Now()

	for _, line := range strings.Split(logData, "\n") {
		callbackFn(notification.Notification{
			ID:              notification.NextID(),
			Date:            eventDate,
			ChildProccessID: s.currentChildProcessID,
			Type:            logType,
			Message:         line,
		})
	}

	return nil
}

func (s *streams) Notify(n notification.Notification) error {
	if n.Type == notification.NotificationTypeStartup {
		s.currentChildProcessID = n.ChildProccessID
	}
	return nil
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.streamConsumer <- string(p)
	return len(p), nil
}
