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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/notification"
	"github.com/jdudmesh/gomon/internal/utils"
	log "github.com/sirupsen/logrus"
)

// type ChildProcess interface {
// 	HardRestart(string) error
// 	SoftRestart(string) error
// 	ExecuteOOBTask(string) error
// 	Start() error
// 	Close() error
// 	AddEventConsumer(sink notification.EventConsumer)
// }

const (
	ForceHardRestart = "__hard_reload"
	ForceSoftRestart = "__soft_reload"
)

type ConsoleOutput interface {
	Stdout() io.Writer
	Stderr() io.Writer
}

type processState int64

const (
	processStateStopped processState = iota
	processStateStarting
	processStateStarted
	processStateStopping
	processStateClosing
	processStateClosed
)

const ipcStatusDisconnected = "Disconnected"
const initialBackoff = 50 * time.Millisecond
const maxBackoff = 5 * time.Second

type AtomicChildProcess struct {
	value atomic.Value
}

func (a *AtomicChildProcess) Load() *childProcess {
	return a.value.Load().(*childProcess)
}

func (a *AtomicChildProcess) Store(p *childProcess) {
	a.value.Store(p)
}

type ProcessState int

const (
	ProcessStateStopped ProcessState = iota
	ProcessStateStarting
	ProcessStateStarted
	ProcessStateStopping
)

type childProcess struct {
	rootDirectory  string
	command        []string
	entrypoint     string
	envVars        []string
	entrypointArgs []string
	prestart       []string
	state          *utils.State[ProcessState]
	childLock      sync.Mutex
	closeLock      sync.Mutex
	termChild      chan struct{}
	killChild      chan struct{}
	killTimeout    time.Duration
	childProcessID string
}

func NewChildProcess(cfg config.Config) (*childProcess, error) {
	proc := &childProcess{
		rootDirectory:  cfg.RootDirectory,
		command:        cfg.Command,
		entrypoint:     cfg.Entrypoint,
		envVars:        os.Environ(),
		entrypointArgs: cfg.EntrypointArgs,
		prestart:       cfg.Prestart,
		state:          utils.NewState[ProcessState](ProcessStateStopped),
		childLock:      sync.Mutex{},
		closeLock:      sync.Mutex{},
		termChild:      make(chan struct{}),
		killChild:      make(chan struct{}),
		killTimeout:    5 * time.Second,
	}

	if len(proc.command) == 0 {
		proc.command = []string{"go", "run"}
		if proc.entrypoint == "" {
			return nil, errors.New("an entrypoint is required")
		}
	}

	for _, file := range cfg.EnvFiles {
		err := proc.loadEnvFile(file)
		if err != nil {
			return nil, fmt.Errorf("loading env file: %w", err)
		}
	}

	return proc, nil
}

func (c *childProcess) Start(console ConsoleOutput, callbackFn notification.NotificationCallback) error {
	c.childLock.Lock()
	defer c.childLock.Unlock()

	if c.state.Get() != ProcessStateStopped {
		return errors.New("process is already running")
	}

	c.childProcessID = notification.NextID()

	// run prestart tasks
	for _, task := range c.prestart {
		err := c.ExecuteOOBTask(task, callbackFn)
		if err != nil {
			return fmt.Errorf("running prestart task: %w", err)
		}
	}

	c.state.Set(ProcessStateStarting)

	childCtx, cancelChildCtx := context.WithCancel(context.Background())
	defer cancelChildCtx()

	args := c.command[1:]
	if len(c.entrypoint) > 0 {
		args = append(args, c.entrypoint)
		if len(c.entrypointArgs) > 0 {
			args = append(args, c.entrypointArgs...)
		}
	}

	// create and start the child process
	cmd := exec.CommandContext(childCtx, c.command[0], args...)
	cmd.Dir = c.rootDirectory
	cmd.Stdout = console.Stdout()
	cmd.Stderr = console.Stderr()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = c.envVars

	err := cmd.Start()
	if err != nil {
		log.Errorf("spawning child process: %+v", err)
		return err
	}

	c.state.Set(ProcessStateStarted)

	callbackFn(notification.Notification{
		ID:              notification.NextID(),
		ChildProccessID: c.childProcessID,
		Date:            time.Now(),
		Type:            notification.NotificationTypeStartup,
		Message:         "process started",
	})

	// wait for the child process to exit, putting the exit code into the exitWait channel
	// allows us to wait for multiple triggers (signals or process exit)
	exitWait := make(chan int)
	go func() {
		err = cmd.Wait()
		if err != nil && !(err.Error() != "signal: terminated" || err.Error() != "signal: killed") {
			log.Warnf("child process exited abnormally: %+v", err)
		}

		s := cmd.ProcessState.ExitCode()
		if s > 0 {
			log.Warnf("child process exited with non-zero status: %d", cmd.ProcessState.ExitCode())
		}
		exitWait <- s
	}()

	// this loop waits for the child process to exit (using the exitWait channel), or for a signal to stop it
	var exitCode int
event_loop:
	for {
		select {
		case <-c.termChild:
			// graceful shutdown (Windows (non-Posix) clients will not receive this signal)
			log.Info("stopping child process: terminate requested")
			// confusingly, the syscall.Kill function sends a TERMINATE signal
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			if err != nil {
				return err
			}
		case <-c.killChild:
			// hard shutdown
			log.Info("stopping child process: close requested")
			cancelChildCtx()
		case exitCode = <-exitWait:
			log.Infof("child process exited with status: %d", exitCode)
			break event_loop
		}
	}

	c.state.Set(ProcessStateStopped)

	callbackFn(notification.Notification{
		ID:              notification.NextID(),
		ChildProccessID: c.childProcessID,
		Date:            time.Now(),
		Type:            notification.NotificationTypeShutdown,
		Message:         fmt.Sprintf("process stopped: exit code %d", exitCode),
	})

	if exitCode > 0 {
		return fmt.Errorf("child process exited with status: %d", exitCode)
	}

	return nil
}

func (c *childProcess) loadEnvFile(filename string) error {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		log.Warnf("env file %s does not exist", filename)
		return nil
	}

	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || len(line) == 0 {
			continue
		}
		c.envVars = append(c.envVars, line)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func (c *childProcess) ExecuteOOBTask(task string, callbackFn notification.NotificationCallback) error {
	oobTask := NewOutOfBandTask(c.rootDirectory, task, c.envVars)
	err := oobTask.Run(c.childProcessID, callbackFn)
	return err
}
