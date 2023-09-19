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
	log "github.com/sirupsen/logrus"
)

type HotReloaderOption func(*HotReloader) error
type HotReloaderCloseFunc func(*HotReloader)

type HotReloader struct {
	rootDir            string
	entrypoint         string
	entrypointArgs     []string
	envFiles           []string
	envVars            []string
	templatePathGlob   string
	respawnOnUnhandled bool
	watcher            *fsnotify.Watcher
	childCmd           *exec.Cmd
	childCmdClosed     chan bool
	childLock          sync.Mutex
	closeFunc          HotReloaderCloseFunc
	killTimeout        time.Duration // TODO: make configurable
	isRespawn          atomic.Bool
}

func WithEntrypoint(path string) HotReloaderOption {
	return func(r *HotReloader) error {
		r.entrypoint = path
		return nil
	}
}

func WithEntrypointArgs(args []string) HotReloaderOption {
	return func(r *HotReloader) error {
		r.entrypointArgs = args
		return nil
	}
}

func WithEnvFiles(files string) HotReloaderOption {
	return func(r *HotReloader) error {
		fileList := strings.Split(files, ",")
		for _, file := range fileList {
			if file != "" {
				r.envFiles = append(r.envFiles, filepath.Join(file))
			}
		}
		return nil
	}
}

func WithTemplatePathGlob(path string) HotReloaderOption {
	return func(r *HotReloader) error {
		r.templatePathGlob = path
		return nil
	}
}

func WithCloseFunc(fn HotReloaderCloseFunc) HotReloaderOption {
	return func(r *HotReloader) error {
		r.closeFunc = fn
		return nil
	}
}

func New(opts ...HotReloaderOption) (*HotReloader, error) {
	var err error

	reloader := &HotReloader{
		envFiles:       []string{},
		envVars:        os.Environ(),
		childCmdClosed: make(chan bool, 1),
		childLock:      sync.Mutex{},
		killTimeout:    5 * time.Second,
		isRespawn:      atomic.Bool{},
	}

	for _, opt := range opts {
		err := opt(reloader)
		if err != nil {
			return nil, err
		}
	}

	if reloader.entrypoint == "" {
		return nil, fmt.Errorf("An entrypoint is required")
	}

	reloader.rootDir, err = os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot establish root directory: %w", err)
	}

	for _, file := range reloader.envFiles {
		err := reloader.loadEnvFile(file)
		if err != nil {
			return nil, fmt.Errorf("loading env file: %w", err)
		}
	}

	return reloader, nil
}

func (r *HotReloader) Run() error {
	err := r.watch()
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

	err := r.closeChild()
	if err != nil {
		return fmt.Errorf("terminating child process: %w", err)
	}

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

	if strings.HasSuffix(filePath, "go") { // TODO: make configurable
		log.Infof("modified Go file: %s", filePath)
		r.respawnChild()
		return
	}

	if r.templatePathGlob != "" {
		if match, _ := filepath.Match(filepath.Join(r.rootDir, r.templatePathGlob), filePath); match {
			log.Infof("modified template: %s", filePath)
			err := syscall.Kill(-r.childCmd.Process.Pid, syscall.SIGUSR1)
			if err != nil {
				log.Errorf("sending SIGUSR1 to child process: %+v", err)
			}
			return
		}
	}

	if r.envFiles != nil {
		f := filepath.Base(filePath)
		for _, envFile := range r.envFiles {
			if f == envFile {
				log.Infof("modified env file: %s", filePath)
				r.respawnChild()
				return
			}
		}
	}

	if r.respawnOnUnhandled {
		log.Infof("modified file: %s", filePath)
		r.respawnChild()
		return
	}

	log.Infof("unhandled modified file: %s", filePath)
}

func (r *HotReloader) watchTree() error {
	return filepath.Walk(r.rootDir, func(srcPath string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f.IsDir() {
			return r.watcher.Add(srcPath)
		}
		return nil
	})
}

func (r *HotReloader) spawnChild(isRespawn ...bool) {
	go func() {
		log.Infof("spawning 'go run %s'", r.entrypoint)
		args := []string{"run", r.entrypoint}
		if len(r.entrypointArgs) > 0 {
			args = append(args, r.entrypointArgs...)
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = r.rootDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = r.envVars
		r.setChild(cmd)

		defer func() {
			r.setChild(nil)

			r.childCmdClosed <- true
			if !r.isRespawn.Load() && r.closeFunc != nil {
				r.closeFunc(r)
			}
		}()

		err := cmd.Start()
		if err != nil {
			log.Errorf("spawning child process: %+v", err)
			return
		}

		log.Infof("child process started (pid: %d)", r.childCmd.Process.Pid)
		r.isRespawn.Store(false)
		err = cmd.Wait()
		if err != nil {
			log.Warnf("child process exited: %+v", err)
		}
	}()
}

func (r *HotReloader) respawnChild() {
	r.isRespawn.Store(true)
	err := r.closeChild()
	if err != nil {
		log.Errorf("closing child process: %+v", err)
		return
	}
	r.spawnChild()
}

func (r *HotReloader) closeChild() error {
	if r.getChild() != nil {
		log.Info("terminating child process")
		err := syscall.Kill(-r.childCmd.Process.Pid, syscall.SIGTERM)
		if err != nil {
			return err
		}
		select {
		case <-r.childCmdClosed:
			log.Info("child process closed")
		case <-time.After(r.killTimeout):
			cmd := r.getChild()
			if cmd != nil {
				log.Warn("child process did not shut down gracefully, killing it")
				err = cmd.Process.Kill()
				if err != nil {
					return fmt.Errorf("killing child process: %w", err)
				}
			}
		}
	}
	return nil
}

func (r *HotReloader) getChild() *exec.Cmd {
	r.childLock.Lock()
	defer r.childLock.Unlock()

	return r.childCmd
}

func (r *HotReloader) setChild(cmd *exec.Cmd) {
	r.childLock.Lock()
	defer r.childLock.Unlock()

	r.childCmd = cmd
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
