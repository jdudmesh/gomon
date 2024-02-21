package main

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
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/console"
	"github.com/jdudmesh/gomon/internal/notification"
	"github.com/jdudmesh/gomon/internal/process"
	"github.com/jdudmesh/gomon/internal/proxy"
	"github.com/jdudmesh/gomon/internal/utils"
	"github.com/jdudmesh/gomon/internal/watcher"
	"github.com/jdudmesh/gomon/internal/webui"
	log "github.com/sirupsen/logrus"
	"gopkg.in/cenkalti/backoff.v1"
)

const (
	red    = 31
	yellow = 33
	blue   = 36
	gray   = 37
)

type logFormatter struct {
}

func (l *logFormatter) Format(entry *log.Entry) ([]byte, error) {
	var levelColor int
	switch entry.Level {
	case log.DebugLevel, log.TraceLevel:
		levelColor = gray
	case log.WarnLevel:
		levelColor = yellow
	case log.ErrorLevel, log.FatalLevel, log.PanicLevel:
		levelColor = red
	case log.InfoLevel:
		levelColor = blue
	default:
		levelColor = blue
	}

	entry.Message = strings.TrimSuffix(entry.Message, "\n")

	b := &bytes.Buffer{}
	fmt.Fprintf(b, "\x1b[%dm%s", levelColor, entry.Message)

	b.WriteByte('\n')
	return b.Bytes(), nil

}
func main() {
	formatter := new(logFormatter)
	log.SetFormatter(formatter)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	if cfg.Entrypoint == "" {
		log.Fatalf("entrypoint is required")
	}

	err = os.Chdir(cfg.RootDirectory)
	if err != nil {
		log.Fatalf("Cannot set working directory: %v", err)
	}

	db, err := utils.NewDatabase(cfg)
	if err != nil {
		log.Fatalf("creating database: %v", err)
	}
	defer func() {
		log.Info("closing database")
		db.Close()
	}()

	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	hardRestart := make(chan string)
	softRestart := make(chan string)
	oobTask := make(chan string)

	// init the web proxy
	proxy, err := proxy.New(cfg)
	if err != nil {
		log.Fatalf("creating proxy: %v", err)
	}

	defer func() {
		err := proxy.Close()
		if err != nil {
			log.Fatalf("stopping proxy: %v", err)
		}
	}()

	if proxy.Enabled() {
		go func() {
			err = proxy.Start()
			if err != nil {
				log.Errorf("starting proxy: %v", err)
				ctxCancel()
			}
		}()
	}

	webui, err := webui.New(cfg, db, func(n notification.Notification) {
		db.LogNotification(n)
		switch n.Type {
		case notification.NotificationTypeHardRestartRequested:
			hardRestart <- "webui"
		case notification.NotificationTypeSoftRestartRequested:
			softRestart <- "webui"
		case notification.NotificationTypeShutdownRequested:
			ctxCancel()
		}
	})

	if err != nil {
		log.Fatalf("creating web UI: %v", err)
	}
	defer func() {
		webui.Close()
	}()
	if webui.Enabled() {
		go func() {
			err := webui.Start()
			if err != nil {
				log.Errorf("starting web UI: %v", err)
				ctxCancel()
			}
		}()
	}

	// create the console redirector
	consoleWriter, err := console.New(cfg)
	if err != nil {
		log.Fatalf("creating console: %v", err)
	}

	defer func() {
		err := consoleWriter.Close()
		if err != nil {
			log.Errorf("closing console: %v", err)
		}
	}()

	go func() {
		err := consoleWriter.Serve(func(n notification.Notification) {
			db.LogNotification(n)
			webui.Notify(n)
		})
		if err != nil {
			log.Errorf("starting console: %v", err)
			ctxCancel()
		}
	}()

	// start the IPC server
	notifier, err := notification.NewNotifier(func(n notification.Notification) {
		db.LogNotification(n)
		proxy.Notify(n)
		webui.Notify(n)
	})
	if err != nil {
		log.Fatalf("creating notifier: %v", err)
	}
	defer notifier.Close()

	go func() {
		err := notifier.Start()
		if err != nil {
			log.Errorf("starting IPC server: %v", err)
			ctxCancel()
		}
	}()

	// init the file system watcher/process spawner
	watcher, err := watcher.New(cfg)
	if err != nil {
		log.Fatalf("creating monitor: %v", err)
	}
	defer watcher.Close()

	// start listening for file changes
	go func() {
		err = watcher.Watch(func(n notification.Notification) {
			db.LogNotification(n)
			switch n.Type {
			case notification.NotificationTypeHardRestartRequested:
				hardRestart <- n.Message
			case notification.NotificationTypeSoftRestartRequested:
				softRestart <- n.Message
			case notification.NotificationTypeOOBTaskRequested:
				oobTask <- n.Message
			}
			proxy.Notify(n)
		})
		if err != nil {
			log.Errorf("running monitor: %v", err)
			ctxCancel()
		}
	}()

	// listen for quit signal
	sigint := make(chan os.Signal, 1)
	go func() {
		signal.Notify(sigint, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGUSR1)
		for s := range sigint {
			switch s {
			case syscall.SIGHUP:
				log.Info("received signal, restarting")
				softRestart <- "sighup"
			case syscall.SIGUSR1:
				log.Info("received signal, hard restarting")
				hardRestart <- "sigusr1"
			case syscall.SIGINT, syscall.SIGTERM:
				log.Info("received term signal, exiting")
				ctxCancel()
			}
		}
	}()

	// all components should be up and running by now
	pid := os.Getpid()
	log.Infof("gomon started with pid %d", pid)

	// this is the main process loop
	childProcess := process.AtomicChildProcess{}
	childProcess.Store(nil)
	go func() {
		// keep restarting the child process until the main context is cancelled (terminated by the user or an error occurs)
		for ctx.Err() == nil {
			proc, err := process.NewChildProcess(cfg)
			if err != nil {
				log.Fatalf("creating child process: %v", err)
			}

			childProcess.Store(proc)

			backoffPolicy := backoff.NewExponentialBackOff()
			backoffPolicy.InitialInterval = 500 * time.Millisecond
			backoffPolicy.MaxInterval = 5000 * time.Millisecond
			backoffPolicy.MaxElapsedTime = 60 * time.Second

			err = backoff.Retry(func() error {
				// TODO: is ok to pass the main context here?
				return proc.Start(ctx, consoleWriter, func(n notification.Notification) {
					db.LogNotification(n)
					consoleWriter.Notify(n)
					proxy.Notify(n)
					webui.Notify(n)
				})
			}, backoffPolicy)

			if err != nil {
				log.Errorf("failed retrying child process: %v", err)
				ctxCancel()
				return
			}
		}
	}()

	// handle restart events
	go func() {
		for {
			select {
			case hint := <-hardRestart:
				log.Info("hard restart: " + hint)
				proc := childProcess.Load()
				if proc != nil {
					proc.Stop()
				}
			case hint := <-softRestart:
				log.Info("soft restart: " + hint)
				err := notifier.Notify(hint)
				if err != nil {
					log.Warnf("notifying child process: %v", err)
				}
			case task := <-oobTask:
				log.Info("out of band task: " + task)
				proc := childProcess.Load()
				if proc != nil {
					proc.ExecuteOOBTask(task, func(n notification.Notification) {
						db.LogNotification(n)
						consoleWriter.Notify(n)
					})
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()
}

func loadConfig() (config.Config, error) {
	var configPath string
	var rootDirectory string
	var entrypoint string
	var entrypointArgs []string
	var envFiles string

	fs := flag.NewFlagSet("gomon flags", flag.ExitOnError)
	fs.StringVar(&configPath, "conf", "", "Path to a config file (gomon.config.yml))")
	fs.StringVar(&rootDirectory, "dir", "", "The directory to watch")
	fs.StringVar(&envFiles, "env", "", "A comma separated list of env files to load")
	maybeProxyOnly := fs.Bool("proxy-only", false, "Only start the proxy, do not start the child process")
	err := fs.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("parsing flags: %v", err)
	}

	args := strings.Split(fs.Arg(0), " ")
	entrypoint = args[0]
	entrypointArgs = args[1:]

	if rootDirectory == "" {
		curDir, err := os.Getwd()
		if err != nil {
			log.Fatalf("getting current directory: %v", err)
		}
		rootDirectory = curDir
	}

	if configPath == "" {
		nextConfigPath := filepath.Join(rootDirectory, config.DefaultConfigFileName)
		if _, err := os.Stat(nextConfigPath); err == nil {
			configPath = nextConfigPath
		} else if !os.IsNotExist(err) {
			log.Fatalf("checking for default config file: %v", err)
		}
	}

	cfg, err := config.New(configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	if cfg.RootDirectory == "" {
		cfg.RootDirectory = rootDirectory
	}

	if entrypoint != "" {
		cfg.Entrypoint = entrypoint
	}

	if len(entrypointArgs) > 0 {
		cfg.EntrypointArgs = entrypointArgs
	}

	if envFiles != "" {
		cfg.EnvFiles = strings.Split(envFiles, ",")
	}

	if maybeProxyOnly != nil {
		cfg.ProxyOnly = *maybeProxyOnly
	}

	return cfg, nil
}
