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
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/process"
	log "github.com/sirupsen/logrus"
)

type HotReloaderOption func(*filesystemWatcher) error

type filesystemWatcher struct {
	rootDirectory string
	hardReload    []string
	softReload    []string
	envFiles      []string
	generated     map[string][]string
	childProcess  process.ChildProcess
	excludePaths  []string
	watcher       *fsnotify.Watcher
}

func New(cfg config.Config, childProcess process.ChildProcess, opts ...HotReloaderOption) (*filesystemWatcher, error) {
	reloader := &filesystemWatcher{
		rootDirectory: cfg.RootDirectory,
		hardReload:    cfg.HardReload,
		softReload:    cfg.SoftReload,
		envFiles:      cfg.EnvFiles,
		generated:     cfg.Generated,
		childProcess:  childProcess,
		excludePaths:  []string{".git", ".vscode", ".idea"},
	}

	reloader.excludePaths = append(reloader.excludePaths, cfg.ExcludePaths...)

	for _, opt := range opts {
		err := opt(reloader)
		if err != nil {
			return nil, err
		}
	}

	return reloader, nil
}

func (w *filesystemWatcher) Run() error {
	var err error
	log.Infof("starting gomon with root directory: %s", w.rootDirectory)

	err = w.watch()
	if err != nil {
		return err
	}

	return nil
}

func (w *filesystemWatcher) Close() error {
	if w.watcher != nil {
		log.Info("terminating file watcher")
		err := w.watcher.Close()
		if err != nil {
			return fmt.Errorf("closing watcher: %w", err)
		}
	}
	return nil
}

func (w *filesystemWatcher) watch() error {
	var err error

	w.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: %+v", err)
	}

	go func() {
		for {
			select {
			case event, ok := <-w.watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) {
					w.processFileChange(event)
				}
			case err, ok := <-w.watcher.Errors:
				log.Errorf("watcher: %+v", err)
				if !ok {
					return
				}
			}
		}
	}()

	err = w.watchTree()
	if err != nil {
		return fmt.Errorf("adding watcher for root path: %w", err)
	}

	return nil
}

func (w *filesystemWatcher) processFileChange(event fsnotify.Event) {
	filePath, _ := filepath.Abs(event.Name)
	relPath, err := filepath.Rel(w.rootDirectory, filePath)
	if err != nil {
		log.Errorf("failed to get relative path for %s: %+v", filePath, err)
		relPath = filePath
	}

	for _, exclude := range w.excludePaths {
		if strings.HasPrefix(relPath, exclude) {
			log.Debugf("excluded file: %s", relPath)
			return
		}
	}

	for _, hard := range w.hardReload {
		if match, _ := filepath.Match(hard, filepath.Base(filePath)); match {
			err := w.childProcess.HardRestart(relPath)
			if err != nil {
				log.Errorf("hard restart: %+v", err)
			}
			return
		}
	}

	for _, soft := range w.softReload {
		if match, _ := filepath.Match(soft, filepath.Base(filePath)); match {
			err := w.childProcess.SoftRestart(relPath)
			if err != nil {
				log.Errorf("soft restart: %+v", err)
			}
			return
		}
	}

	for patt, generated := range w.generated {
		if match, _ := filepath.Match(patt, filepath.Base(filePath)); match {
			log.Infof("generated file source: %s", relPath)
			for _, task := range generated {
				if task == process.ForceHardRestart {
					log.Infof("hard restart: %s", relPath)
					err := w.childProcess.HardRestart(relPath)
					if err != nil {
						log.Errorf("hard restart: %+v", err)
					}
					continue
				}
				if task == process.ForceSoftRestart {
					log.Infof("soft restart: %s", relPath)
					err = w.childProcess.SoftRestart(relPath)
					if err != nil {
						log.Errorf("hard restart: %+v", err)
					}
					continue
				}
				err := w.childProcess.RunOutOfBandTask(task)
				if err != nil {
					log.Errorf("running generated task: %+v", err)
				}
			}
		}
		return
	}

	if w.envFiles != nil {
		f := filepath.Base(filePath)
		for _, envFile := range w.envFiles {
			if f == envFile {
				log.Infof("modified env file: %s", relPath)
				err := w.childProcess.HardRestart(relPath)
				if err != nil {
					log.Errorf("hard restart: %+v", err)
				}
				return
			}
		}
	}

	log.Infof("unhandled modified file: %s", relPath)
}

func (w *filesystemWatcher) watchTree() error {
	return filepath.Walk(w.rootDirectory, func(srcPath string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f.IsDir() {
			isExcluded := false
			for _, exclude := range w.excludePaths {
				p := path.Join(w.rootDirectory, exclude)
				if srcPath == p {
					isExcluded = true
					break
				}
			}
			if isExcluded {
				return filepath.SkipDir
			}
			return w.watcher.Add(srcPath)
		}
		return nil
	})
}
