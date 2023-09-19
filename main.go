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
	"strings"
	"syscall"

	"github.com/jdudmesh/gomon/internal/watcher"
	log "github.com/sirupsen/logrus"
)

func main() {
	var rootDirectory string
	var entrypoint string
	var entrypointArgs []string
	var templatePathGlob string
	var envFiles string

	quit := make(chan bool)

	curDir, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	fs := flag.NewFlagSet("gomon flags", flag.ExitOnError)
	fs.StringVar(&rootDirectory, "root", curDir, "The root directory to watch")
	fs.StringVar(&templatePathGlob, "template", "", "The template path to watch. Should be a glob pattern")
	fs.StringVar(&envFiles, "env", "", "A comma separated list of env files to load")
	err = fs.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("parsing flags: %v", err)
	}

	err = os.Chdir(rootDirectory)
	if err != nil {
		log.Fatalf("Cannot set working directory: %v", err)
	}

	args := strings.Split(fs.Arg(0), " ")
	entrypoint = args[0]
	entrypointArgs = args[1:]

	w, err := watcher.New(
		watcher.WithEntrypoint(entrypoint),
		watcher.WithEntrypointArgs(entrypointArgs),
		watcher.WithTemplatePathGlob(templatePathGlob),
		watcher.WithEnvFiles(envFiles),
		watcher.WithCloseFunc(func(w *watcher.HotReloader) {
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
