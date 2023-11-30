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
	"bytes"
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

	"github.com/google/uuid"
	ipc "github.com/james-barrow/golang-ipc"
	gomonclient "github.com/jdudmesh/gomon-client"
	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/console"
	notif "github.com/jdudmesh/gomon/internal/notification"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
)

const (
	ForceHardRestart = "__hard_reload"
	ForceSoftRestart = "__soft_reload"
)

type consoleOutput interface {
	Stdout() io.Writer
	Stderr() io.Writer
}

const ipcStatusDisconnected = "Disconnected"
const initialBackoff = 50 * time.Millisecond
const maxBackoff = 5 * time.Second

type childProcess struct {
	rootDirectory       string
	entrypoint          string
	envVars             []string
	entrypointArgs      []string
	prestart            []string
	db                  *sqlx.DB
	currentRunID        atomic.Int64
	childCmd            atomic.Value
	childInnerRunWait   sync.WaitGroup
	childOuterRunWait   sync.WaitGroup
	isStarting          atomic.Bool
	isStarted           atomic.Bool
	isExpectingShutdown atomic.Bool
	isClosed            atomic.Bool
	backoff             time.Duration
	childCmdClosed      chan bool
	consoleOutput       consoleOutput
	ipcServer           *ipc.Server
	eventSinks          []notif.NotificationSink
	killTimeout         time.Duration // TODO: make configurable
}

type ChildProcessOption func(*childProcess) error

func WithConsoleOutput(c consoleOutput) ChildProcessOption {
	return func(r *childProcess) error {
		r.consoleOutput = c
		return nil
	}
}

func WithEventSink(sink notif.NotificationSink) ChildProcessOption {
	return func(r *childProcess) error {
		r.eventSinks = append(r.eventSinks, sink)
		return nil
	}
}

func New(cfg *config.Config, db *sqlx.DB, opts ...ChildProcessOption) (*childProcess, error) {
	proc := &childProcess{
		rootDirectory:       cfg.RootDirectory,
		entrypoint:          cfg.Entrypoint,
		envVars:             os.Environ(),
		entrypointArgs:      cfg.EntrypointArgs,
		prestart:            cfg.Prestart,
		db:                  db,
		currentRunID:        atomic.Int64{},
		childInnerRunWait:   sync.WaitGroup{},
		childOuterRunWait:   sync.WaitGroup{},
		isStarting:          atomic.Bool{},
		isStarted:           atomic.Bool{},
		isExpectingShutdown: atomic.Bool{},
		isClosed:            atomic.Bool{},
		childCmdClosed:      make(chan bool, 1),
		eventSinks:          []notif.NotificationSink{},
		killTimeout:         5 * time.Second,
	}

	for _, opt := range opts {
		err := opt(proc)
		if err != nil {
			return nil, err
		}
	}

	if proc.entrypoint == "" {
		return nil, errors.New("an entrypoint is required")
	}

	for _, file := range cfg.EnvFiles {
		err := proc.loadEnvFile(file)
		if err != nil {
			return nil, fmt.Errorf("loading env file: %w", err)
		}
	}

	return proc, nil
}

func (r *childProcess) Start() error {
	log.Debug("starting child process")
	r.backoff = initialBackoff
	r.childOuterRunWait.Add(1)
	r.startChild()
	r.childOuterRunWait.Wait()
	r.childCmdClosed <- true
	return nil
}

func (r *childProcess) Close() error {
	if r.isClosed.Load() {
		return nil
	}

	if r.ipcServer != nil {
		err := r.ipcServer.Write(gomonclient.MsgTypeShutdown, nil)
		if err != nil {
			log.Errorf("ipc write: %+v", err)
		}
		log.Info("closing IPC server")
		r.ipcServer.Close()
	}

	err := r.closeChild()
	if err != nil {
		return fmt.Errorf("terminating child process: %w", err)
	}

	return nil
}

func (r *childProcess) AddEventSink(sink notif.NotificationSink) {
	r.eventSinks = append(r.eventSinks, sink)
}

func (r *childProcess) HardRestart(path string) error {
	r.notifyEventSinks(notif.Notification{
		Type:    notif.NotificationTypeLogEvent,
		Message: "***hard restart requested***",
		Metadata: &console.LogEvent{
			RunID:     int(r.currentRunID.Load()),
			EventType: ForceHardRestart,
			EventData: fmt.Sprintf("***hard restart requested (%s)***", path),
			CreatedAt: time.Now(),
		},
	})

	isStarting := r.isStarting.Load()
	if isStarting {
		return nil
	}

	log.Infof("hard restart: %s", path)

	r.childOuterRunWait.Add(1)

	err := r.closeChild()
	if err != nil {
		return fmt.Errorf("terminating child process: %w", err)
	}

	r.backoff = initialBackoff
	r.isExpectingShutdown.Store(false)
	r.startChild()

	return nil
}

func (r *childProcess) SoftRestart(path string) error {
	r.notifyEventSinks(notif.Notification{
		Type:    notif.NotificationTypeLogEvent,
		Message: "***soft restart requested***",
		Metadata: &console.LogEvent{
			RunID:     int(r.currentRunID.Load()),
			EventType: ForceSoftRestart,
			EventData: "***soft restart requested***",
			CreatedAt: time.Now(),
		},
	})

	isStarting := r.isStarting.Load()
	if isStarting {
		return nil
	}

	log.Infof("soft restart: %s", path)

	err := r.ipcServer.Write(gomonclient.MsgTypeReload, []byte(path))
	if err != nil {
		log.Errorf("ipc write: %+v", err)
	}

	return nil
}

func (r *childProcess) RunOutOfBandTask(task string) error {
	log.Infof("running task: %s", task)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	args := strings.Split(task, " ")
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = r.rootDirectory
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = r.envVars

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("starting task: %w", err)
	}

	err = cmd.Wait()
	if err != nil {
		log.Error("oob task failed")
		log.Warnf("stdout:\n%s", stdoutBuf.String())
		log.Warnf("stderr:\n%s", stderrBuf.String())
		return fmt.Errorf("oob task failed: %w", err)
	}

	_, _ = stdoutBuf.WriteTo(r.consoleOutput.Stdout())
	_, _ = stderrBuf.WriteTo(r.consoleOutput.Stderr())

	return nil
}

func (r *childProcess) startChild() {
	if r.isExpectingShutdown.Load() {
		log.Warn("child process already shutting down")
		return
	}
	r.isStarting.Store(true)
	r.isStarted.Store(false)
	r.childInnerRunWait.Add(1)
	go func() {
		defer r.childInnerRunWait.Done()

		r.dispatchStartupEvent()

		log.Info("running prestart tasks")
		err := r.executePrestart()
		if err != nil {
			log.Errorf("running prestart: %+v", err)
			return
		}

		ipcChannel := "gomon-" + uuid.New().String()
		err = r.startIPCServer(ipcChannel)
		if err != nil {
			log.Errorf("starting ipc server: %+v", err)
			return
		}

		if r.getChildCmd() != nil {
			log.Warn("child process already running")
			return
		}

		log.Infof("spawning 'go run %s'", r.entrypoint)

		args := []string{"run", r.entrypoint}
		if len(r.entrypointArgs) > 0 {
			args = append(args, r.entrypointArgs...)
		}

		envVars := []string{fmt.Sprintf("GOMON_IPC_CHANNEL=%s", ipcChannel)}
		envVars = append(envVars, r.envVars...)

		cmd := exec.Command("go", args...)
		cmd.Dir = r.rootDirectory
		cmd.Stdout = r.consoleOutput.Stdout()
		cmd.Stderr = r.consoleOutput.Stderr()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = envVars

		r.setChildCmd(cmd)

		err = cmd.Start()
		if err != nil {
			log.Errorf("spawning child process: %+v", err)
			return
		}

		r.isStarting.Store(false)

		err = cmd.Wait()
		if err != nil && !(err.Error() != "signal: terminated" || err.Error() != "signal: killed") {
			log.Warnf("child process exited abnormally: %+v", err)
		}

		exitStatus := cmd.ProcessState.ExitCode()
		if exitStatus > 0 {
			log.Warnf("child process exited with non-zero status: %d", cmd.ProcessState.ExitCode())
		}

		r.notifyEventSinks(notif.Notification{Type: notif.NotificationTypeShutdown})
		r.setChildCmd(nil)

		if !r.isExpectingShutdown.Load() {
			log.Warn("child process exited unexpectedly, restarting")
			if r.backoff > maxBackoff {
				log.Warn("child process restarted too many times, max backoff reached")
				r.backoff = maxBackoff
			}
			time.Sleep(r.backoff)
			r.startChild()
			r.backoff *= 2
		} else {
			r.childOuterRunWait.Done()
		}
	}()
}

func (r *childProcess) dispatchStartupEvent() {
	runDate := time.Now()
	res, err := r.db.Exec("INSERT INTO runs (created_at) VALUES ($1)", runDate)
	if err != nil {
		log.Errorf("inserting run: %v", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		log.Errorf("getting last insert id: %v", err)
	}
	r.currentRunID.Store(runID)

	run := &console.LogRun{
		ID:        int(runID),
		CreatedAt: runDate,
	}

	r.notifyEventSinks(notif.Notification{
		Type:     notif.NotificationTypeStartup,
		Metadata: run,
	})
}

func (r *childProcess) closeChild() error {
	r.isExpectingShutdown.Store(true)

	cmd := r.getChildCmd()
	if cmd == nil {
		return nil
	}

	if cmd.Process == nil {
		return nil
	}
	log.Info("terminating child process")

	// calling syscall.Kill with a negative pid sends the signal to the entire process group
	// including the child process and any of its children
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if err != nil {
		return err
	}

	select {
	case <-r.childCmdClosed:
		log.Info("child process closed")
	case <-time.After(r.killTimeout):
		cmd = r.getChildCmd()
		if cmd != nil {
			log.Warn("child process did not shut down gracefully, killing it")
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if err != nil && err.Error() != "os: process already finished" {
				return fmt.Errorf("killing child process: %w", err)
			}
		}
	}

	return nil
}

func (r *childProcess) executePrestart() error {
	for _, task := range r.prestart {
		err := r.RunOutOfBandTask(task)
		if err != nil {
			return fmt.Errorf("running prestart task: %w", err)
		}
	}
	time.Sleep(time.Second)
	return nil
}

func (r *childProcess) notifyEventSinks(n notif.Notification) {
	for _, listener := range r.eventSinks {
		listener.Notify(n)
	}
}

func (r *childProcess) startIPCServer(ipcChannel string) error {
	var err error
	r.ipcServer, err = ipc.StartServer(ipcChannel, nil)
	if err != nil {
		return fmt.Errorf("ipc server: %w", err)
	}

	go func() {
		for {
			msg, err := r.ipcServer.Read()
			if err != nil {
				log.Errorf("ipc read: %+v", err)
				continue
			}

			switch msg.MsgType {
			case gomonclient.MsgTypeReloaded:
				log.Info("Received reload message from downstream process")
				r.notifyEventSinks(notif.Notification{Type: notif.NotificationTypeSoftRestart, Message: string(msg.Data)})

			case gomonclient.MsgTypePing:
				err := r.ipcServer.Write(gomonclient.MsgTypePong, nil)
				if err != nil {
					log.Errorf("ipc write: %+v", err)
				}

			case gomonclient.MsgTypeStartup:
				log.Info("Received startup message from downstream process")
				r.isStarted.Store(true)
				r.notifyEventSinks(notif.Notification{Type: notif.NotificationTypeHardRestart})

			case gomonclient.MsgTypeInternal:
				log.Debugf("Internal message received: %+v", msg)
				if msg.Status == ipcStatusDisconnected {
					log.Info("IPC server closed")
					return
				}

			default:
				log.Warnf("unhandled ipc message type: %d", msg.MsgType)
			}
		}
	}()

	return nil
}

func (r *childProcess) loadEnvFile(filename string) error {
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
		r.envVars = append(r.envVars, line)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func (r *childProcess) getChildCmd() *exec.Cmd {
	if cmd, ok := r.childCmd.Load().(*exec.Cmd); !ok {
		return nil
	} else {
		return cmd
	}
}

func (r *childProcess) setChildCmd(cmd *exec.Cmd) {
	r.childCmd.Store(cmd)
}
