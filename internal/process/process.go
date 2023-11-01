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
	log "github.com/sirupsen/logrus"
)

const (
	ForceHardRestart = "__hard_reload"
	ForceSoftRestart = "__soft_reload"
)

type ConsoleOutput interface {
	OnHardRestart()
	Stdout() io.Writer
	Stderr() io.Writer
}

const ipcStatusDisconnected = "Disconnected"

type childProcess struct {
	rootDirectory  string
	entrypoint     string
	envVars        []string
	entrypointArgs []string
	prestart       []string
	childCmd       atomic.Value
	childLock      sync.Mutex
	childRunWait   sync.WaitGroup
	isStarting     atomic.Bool
	childCmdClosed chan bool
	consoleOutput  ConsoleOutput
	ipcServer      *ipc.Server
	hmrListeners   []chan string
	killTimeout    time.Duration // TODO: make configurable
}

type ChildProcessOption func(*childProcess) error

func WithConsoleOutput(c ConsoleOutput) ChildProcessOption {
	return func(r *childProcess) error {
		r.consoleOutput = c
		return nil
	}
}

func WithHMRListener(listener chan string) ChildProcessOption {
	return func(r *childProcess) error {
		r.hmrListeners = append(r.hmrListeners, listener)
		return nil
	}
}

func New(cfg *config.Config, opts ...ChildProcessOption) (*childProcess, error) {
	proc := &childProcess{
		rootDirectory:  cfg.RootDirectory,
		entrypoint:     cfg.Entrypoint,
		envVars:        os.Environ(),
		entrypointArgs: cfg.EntrypointArgs,
		prestart:       cfg.Prestart,
		childLock:      sync.Mutex{},
		childRunWait:   sync.WaitGroup{},
		isStarting:     atomic.Bool{},
		childCmdClosed: make(chan bool, 1),
		hmrListeners:   []chan string{},
		killTimeout:    5 * time.Second,
	}

	for _, opt := range opts {
		err := opt(proc)
		if err != nil {
			return nil, err
		}
	}

	if proc.entrypoint == "" {
		return nil, fmt.Errorf("An entrypoint is required")
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
	r.childRunWait.Add(1)
	r.startChild()
	r.childRunWait.Wait()
	return nil
}

func (r *childProcess) Close() error {
	log.Info("closing IPC server")
	r.ipcServer.Close()

	err := r.closeChild()
	if err != nil {
		return fmt.Errorf("terminating child process: %w", err)
	}

	return nil
}

func (r *childProcess) HardRestart(path string) error {
	isStarting := r.isStarting.Load()
	if isStarting {
		return nil
	}

	log.Infof("hard restart: %s", path)

	r.childRunWait.Add(1)

	err := r.closeChild()
	if err != nil {
		return fmt.Errorf("terminating child process: %w", err)
	}

	r.startChild()

	return nil
}

func (r *childProcess) SoftRestart(path string) error {
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

	args := strings.Split(task, " ")
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = r.rootDirectory
	cmd.Stdout = r.consoleOutput.Stdout()
	cmd.Stderr = r.consoleOutput.Stderr()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = r.envVars

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("starting task: %w", err)
	}

	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("waiting for task: %w", err)
	}

	return nil
}

func (r *childProcess) startChild() {
	r.childLock.Lock()
	r.isStarting.Store(true)
	go func() {
		defer func() {
			r.isStarting.Store(false)
			r.setChildCmd(nil)
			r.childLock.Unlock()
			r.childRunWait.Done()
			r.childCmdClosed <- true
		}()

		r.consoleOutput.OnHardRestart()

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
	}()
}

func (r *childProcess) closeChild() error {
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

func (r *childProcess) notifyEventListeners(msg string) {
	for _, listener := range r.hmrListeners {
		listener <- msg
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
				r.notifyEventListeners(string(msg.Data))

			case gomonclient.MsgTypePing:
				err := r.ipcServer.Write(gomonclient.MsgTypePong, nil)
				if err != nil {
					log.Errorf("ipc write: %+v", err)
				}

			case gomonclient.MsgTypeStartup:
				r.notifyEventListeners("#gomon:startup#")

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
