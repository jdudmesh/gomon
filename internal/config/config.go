package config

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
	"io"
	"os"
	"path"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const defaultConfigFileName = "gomon.config.yml"

type Config struct {
	RootDirectory     string   `yaml:"rootDirectory"`
	Entrypoint        string   `yaml:"entrypoint"`
	EntrypointArgs    []string `yaml:"entrypointArgs"`
	TemplatePathGlob  string   `yaml:"templatePathGlob"`
	EnvFiles          []string `yaml:"envFiles"`
	ReloadOnUnhandled bool     `yaml:"reloadOnUnhandled"`
	Proxy             struct {
		Enabled    bool `yaml:"enabled"`
		Port       int  `yaml:"port"`
		Downstream struct {
			Host    string `yaml:"host"`
			Timeout int    `yaml:"timeout"`
		} `yaml:"downstream"`
		FrontendDevServer struct {
			Host    string `yaml:"host"`
			Timeout int    `yaml:"timeout"`
			Route   string `yaml:"route"`
			Inject  string `yaml:"inject"`
		} `yaml:"frontendDevServer"`
	} `yaml:"proxy"`
}

func New(configPath, rootDirectory string) (*Config, error) {
	config := &Config{}

	filename := configPath
	if len(filename) == 0 {
		filename = path.Join(rootDirectory, "./"+defaultConfigFileName)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		log.Warn("could not find valid config file")
		return config, nil
	}

	log.Infof("loading config from %s", filename)
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening config file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("unmarhsalling config: %w", err)
	}

	// override config with flags if set
	if config.RootDirectory == "" {
		config.RootDirectory = rootDirectory
	}

	return config, nil
}
