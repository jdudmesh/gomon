package process

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
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/notification"
)

type testConsole struct{}

func (t *testConsole) Stdout() io.Writer {
	return os.Stdout
}

func (t *testConsole) Stderr() io.Writer {
	return os.Stderr
}

func TestChildProcess(t *testing.T) {
	proc, err := NewChildProcess(config.Config{
		RootDirectory: "/bin",
		Command:       []string{"sleep", "300"},
	})

	if err != nil {
		t.Fatalf("error creating child process: %v", err)
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	var stopError error
	go func() {
		<-time.After(5 * time.Second)
		t.Log("stopping child process")
		stopError = proc.Stop()
	}()

	err = proc.Start(ctx, &testConsole{}, func(n notification.Notification) error {
		t.Logf("notification: %v", n)
		return nil
	})

	if err != nil {
		t.Fatalf("error starting child process: %v", err)
	}

	if stopError != nil {
		t.Fatalf("error stopping child process: %v", err)
	}

}
