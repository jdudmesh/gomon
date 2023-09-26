package watcher

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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
	ipc "github.com/james-barrow/golang-ipc"
	"github.com/jdudmesh/gomon/internal/config"
	gomonclient "github.com/jdudmesh/gomon/pkg/client"
	log "github.com/sirupsen/logrus"
)

const ipcStatusDisconnected = "Disconnected"

type BrowserNotifier interface {
	Notify(string)
}

type HotReloaderOption func(*HotReloader) error
type HotReloaderCloseFunc func(*HotReloader)

type HotReloader struct {
	config.Config
	envVars         []string
	excludePaths    []string
	watcher         *fsnotify.Watcher
	childCmd        atomic.Value
	childCmdClosed  chan bool
	childLock       sync.Mutex
	closeLock       sync.Mutex
	closeFunc       HotReloaderCloseFunc
	killTimeout     time.Duration // TODO: make configurable
	isRespawning    atomic.Bool
	ipcServer       *ipc.Server
	ipcChannel      string
	browserNotifier BrowserNotifier
}

func WithCloseFunc(fn HotReloaderCloseFunc) HotReloaderOption {
	return func(r *HotReloader) error {
		r.closeFunc = fn
		return nil
	}
}

func WithBrowserNotifier(n BrowserNotifier) HotReloaderOption {
	return func(r *HotReloader) error {
		r.browserNotifier = n
		return nil
	}
}

func New(config config.Config, closeFn HotReloaderCloseFunc, opts ...HotReloaderOption) (*HotReloader, error) {
	reloader := &HotReloader{
		Config:         config,
		closeFunc:      closeFn,
		excludePaths:   []string{".git"},
		envVars:        os.Environ(),
		childCmdClosed: make(chan bool, 1),
		childLock:      sync.Mutex{},
		closeLock:      sync.Mutex{},
		killTimeout:    5 * time.Second,
		isRespawning:   atomic.Bool{},
		ipcChannel:     "gomon-" + uuid.New().String(),
	}

	reloader.excludePaths = append(reloader.excludePaths, config.ExludePaths...)

	for _, opt := range opts {
		err := opt(reloader)
		if err != nil {
			return nil, err
		}
	}

	if reloader.Config.Entrypoint == "" {
		return nil, fmt.Errorf("An entrypoint is required")
	}

	for _, file := range reloader.Config.EnvFiles {
		err := reloader.loadEnvFile(file)
		if err != nil {
			return nil, fmt.Errorf("loading env file: %w", err)
		}
	}

	reloader.envVars = append(reloader.envVars, fmt.Sprintf("GOMON_IPC_CHANNEL=%s", reloader.ipcChannel))

	return reloader, nil
}

func (r *HotReloader) Run() error {
	var err error
	log.Infof("starting gomon with root directory: %s", r.Config.RootDirectory)

	r.isRespawning.Store(false)

	err = r.runIPCServer()
	if err != nil {
		return err
	}

	err = r.watch()
	if err != nil {
		return err
	}

	r.spawnChild()

	return nil
}

func (r *HotReloader) Close() error {
	if r.watcher != nil {
		log.Info("terminating file watcher")
		err := r.watcher.Close()
		if err != nil {
			return fmt.Errorf("closing watcher: %w", err)
		}
	}

	log.Info("closing IPC server")
	r.ipcServer.Close()

	err := r.closeChild()
	if err != nil {
		return fmt.Errorf("terminating child process: %w", err)
	}

	return nil
}

func (r *HotReloader) runIPCServer() error {
	var err error
	r.ipcServer, err = ipc.StartServer(r.ipcChannel, nil)
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
				fallthrough
			case gomonclient.MsgTypePing:
				if r.browserNotifier != nil {
					data := string(msg.Data)
					r.browserNotifier.Notify(data)
				}
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

func (r *HotReloader) watch() error {
	var err error

	r.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: %+v", err)
	}

	go func() {
		for {
			select {
			case event, ok := <-r.watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) {
					r.processFileChange(event)
				}
			case err, ok := <-r.watcher.Errors:
				log.Errorf("watcher: %+v", err)
				if !ok {
					return
				}
			}
		}
	}()

	err = r.watchTree()
	if err != nil {
		return fmt.Errorf("adding watcher for root path: %w", err)
	}

	return nil
}

func (r *HotReloader) processFileChange(event fsnotify.Event) {
	filePath, _ := filepath.Abs(event.Name)
	relPath, err := filepath.Rel(r.Config.RootDirectory, filePath)
	if err != nil {
		log.Errorf("failed to get relative path for %s: %+v", filePath, err)
		relPath = filePath
	}

	for _, exclude := range r.excludePaths {
		if strings.HasPrefix(relPath, exclude) {
			log.Infof("excluded file: %s", relPath)
			return
		}
	}

	for _, hard := range r.Config.HardReload {
		if match, _ := filepath.Match(hard, filepath.Base(filePath)); match {
			log.Infof("hard reload: %s", relPath)
			r.respawnChild()
			return
		}
	}

	for _, soft := range r.Config.SoftReload {
		if match, _ := filepath.Match(soft, filepath.Base(filePath)); match {
			log.Infof("soft reload: %s", relPath)
			err := r.ipcServer.Write(gomonclient.MsgTypeReload, []byte(relPath))
			if err != nil {
				log.Errorf("ipc write: %+v", err)
			}
			return
		}
	}

	if r.Config.EnvFiles != nil {
		f := filepath.Base(filePath)
		for _, envFile := range r.Config.EnvFiles {
			if f == envFile {
				log.Infof("modified env file: %s", relPath)
				r.respawnChild()
				return
			}
		}
	}

	log.Infof("unhandled modified file: %s", relPath)
}

func (r *HotReloader) watchTree() error {
	return filepath.Walk(r.Config.RootDirectory, func(srcPath string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f.IsDir() {
			return r.watcher.Add(srcPath)
		}
		return nil
	})
}

func (r *HotReloader) spawnChild() {
	go func() {
		r.childLock.Lock()
		defer r.childLock.Unlock()

		if r.getChildCmd() != nil {
			log.Warn("child process already running")
			return
		}

		log.Infof("spawning 'go run %s'", r.Config.Entrypoint)

		args := []string{"run", r.Config.Entrypoint}
		if len(r.Config.EntrypointArgs) > 0 {
			args = append(args, r.Config.EntrypointArgs...)
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = r.Config.RootDirectory
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = r.envVars

		r.setChildCmd(cmd)

		err := cmd.Start()
		if err != nil {
			log.Errorf("spawning child process: %+v", err)
			return
		}

		err = cmd.Wait()
		if err != nil && !(err.Error() != "signal: terminated" || err.Error() != "signal: killed") {
			log.Warnf("child process exited abnormally: %+v", err)
		}
		r.setChildCmd(nil)
		r.childCmdClosed <- true

		exitStatus := cmd.ProcessState.ExitCode()
		if exitStatus > 0 && r.closeFunc != nil {
			r.closeFunc(r)
		}

		r.isRespawning.Store(false)
	}()
}

func (r *HotReloader) respawnChild() {
	r.isRespawning.Store(true)

	err := r.closeChild()
	if err != nil {
		log.Errorf("closing child process: %+v", err)
		return
	}
	r.spawnChild()
}

func (r *HotReloader) closeChild() error {
	r.closeLock.Lock()
	defer r.closeLock.Unlock()

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

func (r *HotReloader) loadEnvFile(filename string) error {
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

func (r *HotReloader) getChildCmd() *exec.Cmd {
	if cmd, ok := r.childCmd.Load().(*exec.Cmd); !ok {
		return nil
	} else {
		return cmd
	}
}

func (r *HotReloader) setChildCmd(cmd *exec.Cmd) {
	r.childCmd.Store(cmd)
}
