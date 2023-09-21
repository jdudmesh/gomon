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

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/proxy"
	"github.com/jdudmesh/gomon/internal/watcher"
	log "github.com/sirupsen/logrus"
)

func main() {
	var configPath string
	var rootDirectory string
	var entrypoint string
	var entrypointArgs []string
	var templatePathGlob string
	var envFiles string

	quit := make(chan bool)

	fs := flag.NewFlagSet("gomon flags", flag.ExitOnError)
	fs.StringVar(&configPath, "config", "", "Path to a config file (gomon.config.yml))")
	fs.StringVar(&rootDirectory, "root", "", "The root directory to watch")
	fs.StringVar(&templatePathGlob, "template", "", "The template path to watch. Should be a glob pattern")
	fs.StringVar(&envFiles, "env", "", "A comma separated list of env files to load")
	err := fs.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("parsing flags: %v", err)
	}

	args := strings.Split(fs.Arg(0), " ")
	entrypoint = args[0]
	entrypointArgs = args[1:]

	if rootDirectory == "" {
		// if no root directory is specified, use the directory of the config file or fallback to the current directory
		if configPath != "" {
			rootDirectory = filepath.Base(configPath)
		} else {
			curDir, err := os.Getwd()
			if err != nil {
				panic(err)
			}
			rootDirectory = curDir
		}
	}

	config, err := config.New(configPath, rootDirectory)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	if entrypoint != "" {
		config.Entrypoint = entrypoint
	}

	if len(entrypointArgs) > 0 {
		config.EntrypointArgs = entrypointArgs
	}

	if len(config.HardReload) == 0 {
		config.HardReload = []string{"*.go", "go.mod", "go.sum"}
	}

	if len(config.ExludePaths) == 0 {
		config.ExludePaths = []string{"vendor"}
	}

	if envFiles != "" {
		config.EnvFiles = strings.Split(envFiles, ",")
	}

	err = os.Chdir(config.RootDirectory)
	if err != nil {
		log.Fatalf("Cannot set working directory: %v", err)
	}

	proxy, err := proxy.New(*config)
	if err != nil {
		log.Fatalf("creating proxy: %v", err)
	}
	defer func() {
		err := proxy.Stop()
		if err != nil {
			log.Fatalf("stopping proxy: %v", err)
		}
	}()

	err = proxy.Start()
	if err != nil {
		log.Fatalf("starting proxy: %v", err)
	}

	w, err := watcher.New(*config,
		func(w *watcher.HotReloader) {
			quit <- true
		},
		watcher.WithBrowserNotifier(proxy),
	)

	if err != nil {
		log.Fatalf("creating monitor: %v", err)
	}
	defer w.Close()

	err = w.Run()
	if err != nil {
		log.Fatalf("running monitor: %v", err)
	}

	pid := os.Getpid()
	log.Infof("gomon started with pid %d", pid)

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigint:
		log.Info("received signal, exiting")
	case <-quit:
		log.Info("received quit, exiting")
	}
}
