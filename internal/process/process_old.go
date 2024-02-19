package process

import (
	"io"
	"time"

	notif "github.com/jdudmesh/gomon/internal/notification"
)

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

// import (
// 	"bufio"
// 	"bytes"
// 	"errors"
// 	"fmt"
// 	"io"
// 	"os"
// 	"os/exec"
// 	"strings"
// 	"sync"
// 	"sync/atomic"
// 	"syscall"
// 	"time"

// 	"github.com/google/uuid"
// 	ipc "github.com/james-barrow/golang-ipc"
// 	gomonclient "github.com/jdudmesh/gomon-client"
// 	"github.com/jdudmesh/gomon/internal/config"
// 	"github.com/jdudmesh/gomon/internal/console"
// 	notif "github.com/jdudmesh/gomon/internal/notification"
// 	"github.com/jmoiron/sqlx"
// 	log "github.com/sirupsen/logrus"
// )

type ChildProcess interface {
	HardRestart(string) error
	SoftRestart(string) error
	RunOutOfBandTask(string) error
	Start() error
	Close() error
	AddEventConsumer(sink notif.EventConsumer)
}

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

// type childProcess struct {
// 	rootDirectory     string
// 	command           []string
// 	entrypoint        string
// 	envVars           []string
// 	entrypointArgs    []string
// 	prestart          []string
// 	db                *sqlx.DB
// 	currentRunID      atomic.Int64
// 	childCmd          atomic.Value
// 	childInnerRunWait sync.WaitGroup
// 	childOuterRunWait sync.WaitGroup
// 	state             atomic.Int64
// 	backoff           time.Duration
// 	childCmdClosed    chan bool
// 	consoleOutput     ConsoleOutput
// 	ipcServer         *ipc.Server
// 	ipcServerLock     sync.Mutex
// 	eventConsumers    []notif.EventConsumer
// 	killTimeout       time.Duration // TODO: make configurable
// }

// type ChildProcessOption func(*childProcess) error

// func WithConsoleOutput(c ConsoleOutput) ChildProcessOption {
// 	return func(r *childProcess) error {
// 		r.consoleOutput = c
// 		return nil
// 	}
// }

// func WithEventConsumer(consumer notif.EventConsumer) ChildProcessOption {
// 	return func(r *childProcess) error {
// 		r.eventConsumers = append(r.eventConsumers, consumer)
// 		return nil
// 	}
// }

// func New(cfg config.Config, db *sqlx.DB, opts ...ChildProcessOption) (*childProcess, error) {
// 	proc := &childProcess{
// 		rootDirectory:     cfg.RootDirectory,
// 		command:           cfg.Command,
// 		entrypoint:        cfg.Entrypoint,
// 		envVars:           os.Environ(),
// 		entrypointArgs:    cfg.EntrypointArgs,
// 		prestart:          cfg.Prestart,
// 		db:                db,
// 		currentRunID:      atomic.Int64{},
// 		childInnerRunWait: sync.WaitGroup{},
// 		childOuterRunWait: sync.WaitGroup{},
// 		ipcServerLock:     sync.Mutex{},
// 		childCmdClosed:    make(chan bool, 1),
// 		eventConsumers:    []notif.EventConsumer{},
// 		killTimeout:       5 * time.Second,
// 	}

// 	for _, opt := range opts {
// 		err := opt(proc)
// 		if err != nil {
// 			return nil, err
// 		}
// 	}

// 	if len(proc.command) == 0 {
// 		proc.command = []string{"go", "run"}
// 	}

// 	if proc.entrypoint == "" {
// 		return nil, errors.New("an entrypoint is required")
// 	}

// 	for _, file := range cfg.EnvFiles {
// 		err := proc.loadEnvFile(file)
// 		if err != nil {
// 			return nil, fmt.Errorf("loading env file: %w", err)
// 		}
// 	}

// 	return proc, nil
// }

// func (c *childProcess) Start() error {
// 	log.Debug("starting child process")

// 	c.backoff = initialBackoff
// 	c.childOuterRunWait.Add(1)

// 	c.startChild()

// 	c.childOuterRunWait.Wait()
// 	c.childCmdClosed <- true

// 	c.setState(processStateClosed)

// 	return nil
// }

// func (c *childProcess) Close() error {
// 	if c.getState() == processStateClosed {
// 		return nil
// 	}

// 	c.setState(processStateClosing)

// 	err := c.closeChild()
// 	if err != nil {
// 		return fmt.Errorf("terminating child process: %w", err)
// 	}

// 	return nil
// }

// func (c *childProcess) AddEventConsumer(sink notif.EventConsumer) {
// 	c.eventConsumers = append(c.eventConsumers, sink)
// }

// func (c *childProcess) HardRestart(path string) error {
// 	c.notifyEventConsumers(notif.Notification{
// 		Type:    notif.NotificationTypeLogEvent,
// 		Message: "***hard restart requested***",
// 		Metadata: &console.LogEvent{
// 			RunID:     int(c.currentRunID.Load()),
// 			EventType: ForceHardRestart,
// 			EventData: fmt.Sprintf("***hard restart requested (%s)***", path),
// 			CreatedAt: time.Now(),
// 		},
// 	})

// 	state := c.getState()
// 	if !(state == processStateStarted || state == processStateStopped) {
// 		return nil
// 	}

// 	log.Infof("hard restart: %s", path)

// 	c.childOuterRunWait.Add(1)

// 	err := c.closeChild()
// 	if err != nil {
// 		return fmt.Errorf("terminating child process: %w", err)
// 	}

// 	c.backoff = initialBackoff
// 	c.startChild()

// 	return nil
// }

// func (c *childProcess) SoftRestart(path string) error {
// 	c.ipcServerLock.Lock()
// 	defer c.ipcServerLock.Unlock()

// 	c.notifyEventConsumers(notif.Notification{
// 		Type:    notif.NotificationTypeLogEvent,
// 		Message: "***soft restart requested***",
// 		Metadata: &console.LogEvent{
// 			RunID:     int(c.currentRunID.Load()),
// 			EventType: ForceSoftRestart,
// 			EventData: "***soft restart requested***",
// 			CreatedAt: time.Now(),
// 		},
// 	})

// 	if c.getState() != processStateStarted {
// 		return nil
// 	}

// 	log.Infof("soft restart: %s", path)

// 	if c.ipcServer == nil {
// 		return nil
// 	}

// 	if c.ipcServer.StatusCode() != ipc.Connected {
// 		log.Warn("ipc server not connected, cannot perform soft restart")
// 		return nil
// 	}

// 	err := c.ipcServer.Write(gomonclient.MsgTypeReload, []byte(path))
// 	if err != nil {
// 		log.Errorf("ipc write: %+v", err)
// 	}

// 	return nil
// }

// func (c *childProcess) RunOutOfBandTask(task string) error {
// 	log.Infof("running task: %s", task)

// 	stdoutBuf := &bytes.Buffer{}
// 	stderrBuf := &bytes.Buffer{}

// 	args := strings.Split(task, " ")
// 	cmd := exec.Command(args[0], args[1:]...)
// 	cmd.Dir = c.rootDirectory
// 	cmd.Stdout = stdoutBuf
// 	cmd.Stderr = stderrBuf
// 	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
// 	cmd.Env = c.envVars

// 	err := cmd.Start()
// 	if err != nil {
// 		return fmt.Errorf("starting task: %w", err)
// 	}

// 	err = cmd.Wait()
// 	if err != nil {
// 		log.Error("oob task failed")
// 		log.Warnf("stdout:\n%s", stdoutBuf.String())
// 		log.Warnf("stderr:\n%s", stderrBuf.String())
// 		return fmt.Errorf("oob task failed: %w", err)
// 	}

// 	_, _ = stdoutBuf.WriteTo(c.consoleOutput.Stdout())
// 	_, _ = stderrBuf.WriteTo(c.consoleOutput.Stderr())

// 	return nil
// }

// func (c *childProcess) startChild() {
// 	if c.getState() != processStateStopped {
// 		return
// 	}
// 	c.setState(processStateStarting)
// 	c.childInnerRunWait.Add(1)
// 	go func() {
// 		defer c.childInnerRunWait.Done()

// 		c.logAndDispatchStartupEvent()

// 		log.Info("running prestart tasks")
// 		err := c.executePrestart()
// 		if err != nil {
// 			log.Errorf("running prestart: %+v", err)
// 			return
// 		}

// 		if c.getChildCmd() != nil {
// 			log.Warn("child process already running")
// 			return
// 		}

// 		ipcChannel, err := c.startIPCServer()
// 		if err != nil {
// 			log.Errorf("starting ipc server: %v", err)
// 		}

// 		log.Infof("spawning '%s %s'", strings.Join(c.command, " "), c.entrypoint)

// 		args := c.command[1:]
// 		args = append(args, c.entrypoint)
// 		if len(c.entrypointArgs) > 0 {
// 			args = append(args, c.entrypointArgs...)
// 		}

// 		envVars := []string{fmt.Sprintf("GOMON_IPC_CHANNEL=%s", ipcChannel)}
// 		envVars = append(envVars, c.envVars...)

// 		cmd := exec.Command(c.command[0], args...)
// 		cmd.Dir = c.rootDirectory
// 		cmd.Stdout = c.consoleOutput.Stdout()
// 		cmd.Stderr = c.consoleOutput.Stderr()
// 		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
// 		cmd.Env = envVars

// 		c.setChildCmd(cmd)

// 		err = cmd.Start()
// 		if err != nil {
// 			log.Errorf("spawning child process: %+v", err)
// 			return
// 		}

// 		c.setState(processStateStarted)

// 		err = cmd.Wait()
// 		if err != nil && !(err.Error() != "signal: terminated" || err.Error() != "signal: killed") {
// 			log.Warnf("child process exited abnormally: %+v", err)
// 		}

// 		exitStatus := cmd.ProcessState.ExitCode()
// 		if exitStatus > 0 {
// 			log.Warnf("child process exited with non-zero status: %d", cmd.ProcessState.ExitCode())
// 		}

// 		c.notifyEventConsumers(notif.Notification{Type: notif.NotificationTypeShutdown})
// 		c.setChildCmd(nil)

// 		s := c.getState()
// 		if !(s == processStateStopping || s == processStateClosing) {
// 			c.setState(processStateStopped)
// 			log.Warn("child process exited unexpectedly, restarting")
// 			if c.backoff > maxBackoff {
// 				log.Warn("child process restarted too many times, max backoff reached")
// 				c.backoff = maxBackoff
// 			}
// 			time.Sleep(c.backoff)
// 			c.startChild()
// 			c.backoff *= 2
// 		} else {
// 			c.setState(processStateStopped)
// 			c.childOuterRunWait.Done()
// 		}
// 	}()
// }

// func (c *childProcess) logAndDispatchStartupEvent() {
// 	runDate := time.Now()
// 	res, err := c.db.Exec("INSERT INTO runs (created_at) VALUES ($1)", runDate)
// 	if err != nil {
// 		log.Errorf("inserting run: %v", err)
// 	}
// 	runID, err := res.LastInsertId()
// 	if err != nil {
// 		log.Errorf("getting last insert id: %v", err)
// 	}
// 	c.currentRunID.Store(runID)

// 	run := &console.LogRun{
// 		ID:        int(runID),
// 		CreatedAt: runDate,
// 	}

// 	c.notifyEventConsumers(notif.Notification{
// 		Type:     notif.NotificationTypeStartup,
// 		Metadata: run,
// 	})
// }

// func (c *childProcess) closeChild() error {
// 	if c.getState() == processStateStopped {
// 		return nil
// 	}

// 	c.setState(processStateStopping)

// 	cmd := c.getChildCmd()
// 	if cmd == nil {
// 		c.childOuterRunWait.Done()
// 		c.setState(processStateStopped)
// 		return nil
// 	}

// 	if cmd.Process == nil {
// 		c.childOuterRunWait.Done()
// 		c.setState(processStateStopped)
// 		return nil
// 	}

// 	log.Info("terminating child process")
// 	c.closeIPCServer()

// 	// calling syscall.Kill with a negative pid sends the signal to the entire process group
// 	// including the child process and any of its children
// 	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
// 	if err != nil {
// 		return err
// 	}

// 	select {
// 	case <-c.childCmdClosed:
// 		log.Info("child process closed")
// 	case <-time.After(c.killTimeout):
// 		cmd = c.getChildCmd()
// 		if cmd != nil {
// 			log.Warn("child process did not shut down gracefully, killing it")
// 			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
// 			if err != nil && err.Error() != "os: process already finished" {
// 				return fmt.Errorf("killing child process: %w", err)
// 			}
// 		}
// 	}

// 	return nil
// }

// func (c *childProcess) executePrestart() error {
// 	for _, task := range c.prestart {
// 		err := c.RunOutOfBandTask(task)
// 		if err != nil {
// 			return fmt.Errorf("running prestart task: %w", err)
// 		}
// 	}
// 	time.Sleep(time.Second)
// 	return nil
// }

// func (c *childProcess) notifyEventConsumers(n notif.Notification) {
// 	for _, consumer := range c.eventConsumers {
// 		consumer.Notify(n)
// 	}
// }

// func (c *childProcess) startIPCServer() (string, error) {
// 	err := (error)(nil)

// 	c.ipcServerLock.Lock()
// 	defer c.ipcServerLock.Unlock()

// 	ipcChannel := "gomon-" + uuid.New().String()

// 	c.ipcServer, err = ipc.StartServer(ipcChannel, nil)
// 	if err != nil {
// 		return "", fmt.Errorf("ipc server: %w", err)
// 	}

// 	go func() {
// 		for {
// 			if c.ipcServer.StatusCode() == ipc.Closed {
// 				break
// 			}

// 			msg, err := c.ipcServer.Read()
// 			if err != nil {
// 				log.Errorf("ipc read: %+v", err)
// 				continue
// 			}

// 			switch msg.MsgType {
// 			case gomonclient.MsgTypeStartup:
// 				log.Info("Received startup message from downstream process")
// 				c.notifyEventConsumers(notif.Notification{Type: notif.NotificationTypeHardRestart, Message: "startup"})

// 			case gomonclient.MsgTypeReloaded:
// 				log.Info("Received reload message from downstream process")
// 				c.notifyEventConsumers(notif.Notification{Type: notif.NotificationTypeSoftRestart, Message: string(msg.Data)})

// 			case gomonclient.MsgTypePing:
// 				err := c.ipcServer.Write(gomonclient.MsgTypePong, nil)
// 				if err != nil {
// 					log.Errorf("ipc write: %+v", err)
// 				}

// 			case gomonclient.MsgTypeInternal:
// 				log.Debugf("Internal message received: %+v", msg)
// 				if msg.Status == ipcStatusDisconnected {
// 					log.Info("IPC server closed")
// 					return
// 				}

// 			default:
// 				log.Warnf("unhandled ipc message type: %d", msg.MsgType)
// 			}
// 		}
// 	}()

// 	return ipcChannel, nil
// }

// func (c *childProcess) closeIPCServer() {
// 	c.ipcServerLock.Lock()
// 	defer c.ipcServerLock.Unlock()

// 	err := c.ipcServer.Write(gomonclient.MsgTypeShutdown, nil)
// 	if err != nil {
// 		log.Errorf("ipc write: %+v", err)
// 	}

// 	if c.ipcServer != nil {
// 		c.ipcServer.Close()
// 		c.ipcServer = nil
// 	}
// }

// func (c *childProcess) loadEnvFile(filename string) error {
// 	if _, err := os.Stat(filename); os.IsNotExist(err) {
// 		log.Warnf("env file %s does not exist", filename)
// 		return nil
// 	}

// 	file, err := os.Open(filename)
// 	if err != nil {
// 		return err
// 	}
// 	defer file.Close()

// 	scanner := bufio.NewScanner(file)
// 	for scanner.Scan() {
// 		line := strings.TrimSpace(scanner.Text())
// 		if strings.HasPrefix(line, "#") || len(line) == 0 {
// 			continue
// 		}
// 		c.envVars = append(c.envVars, line)
// 	}

// 	if err := scanner.Err(); err != nil {
// 		return err
// 	}

// 	return nil
// }

// func (c *childProcess) getChildCmd() *exec.Cmd {
// 	if cmd, ok := c.childCmd.Load().(*exec.Cmd); !ok {
// 		return nil
// 	} else {
// 		return cmd
// 	}
// }

// func (c *childProcess) setChildCmd(cmd *exec.Cmd) {
// 	c.childCmd.Store(cmd)
// }

// func (c *childProcess) setState(state processState) {
// 	if processState(c.state.Load()) == processStateClosing {
// 		if state != processStateClosed {
// 			return
// 		}
// 	}
// 	c.state.Store(int64(state))
// }

// func (c *childProcess) getState() processState {
// 	return processState(c.state.Load())
// }
