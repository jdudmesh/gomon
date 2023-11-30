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
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/console"
	"github.com/jdudmesh/gomon/internal/notification"
	"github.com/jdudmesh/gomon/internal/process"
	"github.com/jdudmesh/gomon/internal/proxy"
	"github.com/jdudmesh/gomon/internal/watcher"
	"github.com/jdudmesh/gomon/internal/webui"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
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

	globalSystemControl := notification.NewChannel()

	// init the web proxy
	proxy, err := proxy.New(cfg, globalSystemControl)
	if err != nil {
		log.Fatalf("creating proxy: %v", err)
	}
	defer func() {
		err := proxy.Close()
		if err != nil {
			log.Fatalf("stopping proxy: %v", err)
		}
	}()

	err = proxy.Start()
	if err != nil {
		log.Fatalf("starting proxy: %v", err)
	}

	// TODO split console enabled from web UI enabled
	// open the database (if enabled)
	var db *sqlx.DB
	if cfg.UI.Enabled {
		db, err = createDatabase(cfg)
		if err != nil {
			log.Fatalf("creating database: %v", err)
		}
		defer func() {
			log.Info("closing database")
			db.Close()
		}()
	}

	// create the console redirector
	console, err := console.New(cfg, globalSystemControl, db)
	if err != nil {
		log.Fatalf("creating console: %v", err)
	}

	defer func() {
		err := console.Close()
		if err != nil {
			log.Errorf("closing console: %v", err)
		}
	}()

	err = console.Start()
	if err != nil {
		log.Fatalf("starting console: %v", err)
	}

	// init the child process
	childProcess, err := process.New(cfg, db,
		process.WithConsoleOutput(console),
		process.WithEventSink(console),
		process.WithEventSink(proxy))
	if err != nil {
		log.Fatalf("creating child process: %v", err)
	}
	defer childProcess.Close()

	// init the file system watcher/process spawner
	watcher, err := watcher.New(cfg, childProcess)
	if err != nil {
		log.Fatalf("creating monitor: %v", err)
	}
	defer watcher.Close()

	// init the web UI
	if cfg.UI.Enabled {
		ui, err := webui.New(cfg, globalSystemControl, db)
		if err != nil {
			log.Fatalf("creating web UI: %v", err)
		}
		defer func() {
			ui.Close()
		}()

		childProcess.AddEventSink(ui)
		console.AddEventSink(ui)

		err = ui.Start()
		if err != nil {
			log.Fatalf("starting web UI: %v", err)
		}
		defer ui.Close()
	}

	// all components should be up and running by now
	pid := os.Getpid()
	log.Infof("gomon started with pid %d", pid)

	// start listening for file changes
	err = watcher.Run()
	if err != nil {
		log.Fatalf("running monitor: %v", err)
	}

	// listen for system notifications
	go func() {
		for n := range globalSystemControl {
			switch n.Type {
			case notification.NotificationTypeSystemError:
				log.Error(n.Metadata.(error))
				fallthrough
			case notification.NotificationTypeSystemShutdown:
				childProcess.Close()
			case notification.NotificationTypeHardRestart:
				err = childProcess.HardRestart(n.Message)
				if err != nil {
					log.Fatalf("hard restarting child process: %v", err)
				}
			case notification.NotificationTypeSoftRestart:
				err = childProcess.SoftRestart(n.Message)
				if err != nil {
					log.Fatalf("soft restarting child process: %v", err)
				}
			}
		}
	}()

	// listen for quit signal
	sigint := make(chan os.Signal, 1)
	defer close(sigint)
	go func() {
		signal.Notify(sigint, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		for s := range sigint {
			switch s {
			case syscall.SIGHUP:
				log.Info("received signal, restarting")
				globalSystemControl <- notification.Notification{
					Type:    notification.NotificationTypeHardRestart,
					Message: "SIGHUP",
				}
			case os.Interrupt, syscall.SIGTERM:
				log.Info("received signal, exiting")
				globalSystemControl <- notification.Notification{
					Type:    notification.NotificationTypeSystemShutdown,
					Message: "SIGTERM",
				}
			}
		}
	}()

	err = childProcess.Start()
	if err != nil {
		log.Fatalf("starting child process: %v", err)
	}
}

func loadConfig() (*config.Config, error) {
	var configPath string
	var rootDirectory string
	var entrypoint string
	var entrypointArgs []string
	var envFiles string

	fs := flag.NewFlagSet("gomon flags", flag.ExitOnError)
	fs.StringVar(&configPath, "conf", "", "Path to a config file (gomon.config.yml))")
	fs.StringVar(&rootDirectory, "dir", "", "The directory to watch")
	fs.StringVar(&envFiles, "env", "", "A comma separated list of env files to load")
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

	return cfg, nil
}

func createDatabase(config *config.Config) (*sqlx.DB, error) {
	dataPath := path.Join(config.RootDirectory, "./.gomon")
	_, err := os.Stat(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.Mkdir(dataPath, 0755)
			if err != nil {
				return nil, fmt.Errorf("creating .gomon directory: %w", err)
			}
		} else {
			return nil, fmt.Errorf("checking for .gomon directory: %w", err)
		}
	}

	db, err := sqlx.Connect("sqlite3", path.Join(dataPath, "./gomon.db"))
	if err != nil {
		return nil, fmt.Errorf("connecting to sqlite: %w", err)
	}

	_, err = db.Exec(schema)
	if err != nil {
		return nil, fmt.Errorf("creating db schema: %w", err)
	}

	return db, nil
}

var schema = `
CREATE TABLE IF NOT EXISTS runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_runs_created_at ON runs(created_at);

CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL,
	event_type TEXT NOT NULL,
	event_data TEXT NOT NULL,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_events_event_type ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_created_at ON events(created_at);
`
