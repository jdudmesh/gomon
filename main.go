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
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jdudmesh/gomon/internal/capture"
	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/proxy"
	"github.com/jdudmesh/gomon/internal/watcher"
	log "github.com/sirupsen/logrus"
)

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	if config.Entrypoint == "" {
		log.Fatalf("entrypoint is required")
	}

	respawn := make(chan bool)
	defer close(respawn)

	quit := make(chan bool)
	defer close(quit)

	err = os.Chdir(config.RootDirectory)
	if err != nil {
		log.Fatalf("Cannot set working directory: %v", err)
	}

	// init the web proxy
	proxy, err := proxy.New(*config)
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

	// init the console output capture
	capture, err := capture.New(*config, capture.WithRespawn(respawn))
	if err != nil {
		log.Fatalf("creating capture: %v", err)
	}

	defer func() {
		err := capture.Close()
		if err != nil {
			log.Errorf("closing capture: %v", err)
		}
	}()

	err = capture.Start()
	if err != nil {
		log.Fatalf("starting capture: %v", err)
	}

	// init the file system watcher/process spawner
	w, err := watcher.New(*config,
		watcher.WithNotifier(proxy),
		watcher.WithConsoleCapture(capture),
		watcher.WithCloseFunc(func() {
			quit <- true
		}),
	)

	if err != nil {
		log.Fatalf("creating monitor: %v", err)
	}
	defer w.Close()

	err = w.Run()
	if err != nil {
		log.Fatalf("running monitor: %v", err)
	}

	// all components should be up and running by now
	pid := os.Getpid()
	log.Infof("gomon started with pid %d", pid)

	// listen for respawn events from other components e.g. the web UI
	go func() {
		for range respawn {
			w.Respawn()
		}
	}()

	// wait for quit signal
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigint:
		log.Info("received signal, exiting")
	case <-quit:
		log.Info("received quit, exiting")
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

	config, err := config.New(configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	if config.RootDirectory == "" {
		config.RootDirectory = rootDirectory
	}

	if entrypoint != "" {
		config.Entrypoint = entrypoint
	}

	if len(entrypointArgs) > 0 {
		config.EntrypointArgs = entrypointArgs
	}

	if envFiles != "" {
		config.EnvFiles = strings.Split(envFiles, ",")
	}

	return config, nil
}
