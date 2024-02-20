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
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jdudmesh/gomon/internal/notification"
	log "github.com/sirupsen/logrus"
)

type outOfBandTask struct {
	rootDirectory string
	task          string
	envVars       []string
}

func NewOutOfBandTask(rootDirectory string, task string, envVars []string) *outOfBandTask {
	return &outOfBandTask{
		rootDirectory: rootDirectory,
		task:          task,
		envVars:       envVars,
	}
}

func (o *outOfBandTask) Run(childProcessID string, callbackFn notification.NotificationCallback) error {
	log.Infof("running task: %s", o.task)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	callbackFn(notification.Notification{
		ID:              notification.NextID(),
		ChildProccessID: childProcessID,
		Date:            time.Now(),
		Type:            notification.NotificationTypeOOBTaskStartup,
		Message:         "running task: " + o.task,
	})

	args := strings.Split(o.task, " ")
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = o.rootDirectory
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = o.envVars

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("starting task: %w", err)
	}

	err = cmd.Wait()

	if stdoutBuf.Len() > 0 {
		callbackFn(notification.Notification{
			ID:              notification.NextID(),
			ChildProccessID: childProcessID,
			Date:            time.Now(),
			Type:            notification.NotificationTypeOOBTaskStdOut,
			Message:         string(stdoutBuf.Bytes()),
		})
	}

	if stderrBuf.Len() > 0 {
		callbackFn(notification.Notification{
			ID:              notification.NextID(),
			ChildProccessID: childProcessID,
			Date:            time.Now(),
			Type:            notification.NotificationTypeOOBTaskStdErr,
			Message:         string(stderrBuf.Bytes()),
		})
	}

	if err != nil {
		return fmt.Errorf("running oob task: %w", err)
	}

	return nil
}
