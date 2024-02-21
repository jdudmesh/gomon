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
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/notification"
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
	excludePaths  []string
	watcher       *fsnotify.Watcher
}

func New(cfg config.Config, opts ...HotReloaderOption) (*filesystemWatcher, error) {
	reloader := &filesystemWatcher{
		rootDirectory: cfg.RootDirectory,
		hardReload:    cfg.HardReload,
		softReload:    cfg.SoftReload,
		envFiles:      cfg.EnvFiles,
		generated:     cfg.Generated,
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

func (w *filesystemWatcher) Watch(callbackFn notification.NotificationCallback) error {
	var err error
	log.Infof("starting gomon with root directory: %s", w.rootDirectory)

	w.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: %+v", err)
	}

	err = w.init()
	if err != nil {
		return fmt.Errorf("adding watcher for root path: %w", err)
	}

	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				break
			}
			if event.Has(fsnotify.Write) {
				w.processFileChange(event, callbackFn)
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				break
			}
			log.Errorf("watcher: %+v", err)
		}
	}
}

func (w *filesystemWatcher) processFileChange(event fsnotify.Event, callbackFn notification.NotificationCallback) {
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
			callbackFn(notification.Notification{
				ID:              notification.NextID(),
				ChildProccessID: "",
				Date:            time.Now(),
				Type:            notification.NotificationTypeHardRestartRequested,
				Message:         relPath,
			})
			return
		}
	}

	for _, soft := range w.softReload {
		if match, _ := filepath.Match(soft, filepath.Base(filePath)); match {
			callbackFn(notification.Notification{
				ID:              notification.NextID(),
				ChildProccessID: "",
				Date:            time.Now(),
				Type:            notification.NotificationTypeSoftRestartRequested,
				Message:         relPath,
			})
			return
		}
	}

	for patt, generated := range w.generated {
		if match, _ := filepath.Match(patt, filepath.Base(filePath)); match {
			log.Infof("generated file source: %s", relPath)
			for _, task := range generated {
				switch task {
				case process.ForceHardRestart:
					callbackFn(notification.Notification{
						ID:              notification.NextID(),
						ChildProccessID: "",
						Date:            time.Now(),
						Type:            notification.NotificationTypeHardRestartRequested,
						Message:         relPath,
					})
				case process.ForceSoftRestart:
					callbackFn(notification.Notification{
						ID:              notification.NextID(),
						ChildProccessID: "",
						Date:            time.Now(),
						Type:            notification.NotificationTypeSoftRestartRequested,
						Message:         relPath,
					})
				default:
					callbackFn(notification.Notification{
						ID:              notification.NextID(),
						ChildProccessID: "",
						Date:            time.Now(),
						Type:            notification.NotificationTypeOOBTaskRequested,
						Message:         task,
					})
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
				callbackFn(notification.Notification{
					ID:              notification.NextID(),
					ChildProccessID: "",
					Date:            time.Now(),
					Type:            notification.NotificationTypeHardRestartRequested,
					Message:         relPath,
				})
				return
			}
		}
	}

	log.Infof("unhandled modified file: %s", relPath)
}

func (w *filesystemWatcher) init() error {
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
