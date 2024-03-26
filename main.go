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
	"path/filepath"
	"strings"

	"github.com/jdudmesh/gomon/internal/app"
	"github.com/jdudmesh/gomon/internal/config"
	log "github.com/sirupsen/logrus"
)

const (
	red    = 31
	yellow = 33
	blue   = 36
	gray   = 37
)

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

	// create a context that can be used to cancel all the other components
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	// create the app, this orchestrates all the other components
	app, err := app.New(cfg)
	if err != nil {
		log.Fatalf("creating app: %v", err)
	}
	defer app.Close()

	// run the web proxy
	go func() {
		err = app.RunProxy()
		if err != nil {
			log.Errorf("starting proxy: %v", err)
			ctxCancel()
		}
	}()

	// run the user interface
	go func() {
		err := app.RunWebUI()
		if err != nil {
			log.Errorf("starting web UI: %v", err)
			ctxCancel()
		}
	}()

	// start the console
	go func() {
		err := app.RunConsole()
		if err != nil {
			log.Errorf("starting console: %v", err)
			ctxCancel()
		}
	}()

	// start the IPC server
	go func() {
		err := app.RunNotifer()
		if err != nil {
			log.Errorf("starting IPC server: %v", err)
			ctxCancel()
		}
	}()

	// start listening for file changes
	go func() {
		err := app.MonitorFileChanges(ctx)
		if err != nil {
			ctxCancel()
		}
	}()

	// monitor and handle signals
	go func() {
		err := app.ProcessSignals()
		if err != nil {
			ctxCancel()
		}
	}()

	// monitor and handle restart events
	go app.ProcessRestartEvents(ctx)

	// all components should be up and running by now
	pid := os.Getpid()
	log.Infof("gomon started with pid %d", pid)

	// this is the main process loop, just keep restarting the child process until the main context is cancelled or an error occurs
	go func() {
		// keep restarting the child process until the main context is cancelled (terminated by the user or an error occurs)
		for ctx.Err() == nil {
			err := app.RunChildProcess(cfg)
			if err != nil {
				ctxCancel()
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
