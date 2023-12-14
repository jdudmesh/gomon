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

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const DefaultConfigFileName = "gomon.config.yml"

type Config struct {
	RootDirectory  string              `yaml:"rootDirectory"`
	Command        []string            `yaml:"command"`
	Entrypoint     string              `yaml:"entrypoint"`
	EntrypointArgs []string            `yaml:"entrypointArgs"`
	EnvFiles       []string            `yaml:"envFiles"`
	ExcludePaths   []string            `yaml:"excludePaths"`
	HardReload     []string            `yaml:"hardReload"`
	SoftReload     []string            `yaml:"softReload"`
	Generated      map[string][]string `yaml:"generated"`
	Prestart       []string            `yaml:"prestart"`
	Proxy          struct {
		Enabled    bool `yaml:"enabled"`
		Port       int  `yaml:"port"`
		Downstream struct {
			Host    string `yaml:"host"`
			Timeout int    `yaml:"timeout"`
		} `yaml:"downstream"`
	} `yaml:"proxy"`
	UI struct {
		Enabled bool `yaml:"enabled"`
		Port    int  `yaml:"port"`
	} `yaml:"ui"`
}

var defaultConfig = Config{
	HardReload:   []string{"*.go", "go.mod", "go.sum"},
	SoftReload:   []string{"*.html", "*.css", "*.js"},
	ExcludePaths: []string{".gomon", "vendor"},
}

func New(configPath string) (Config, error) {
	if configPath == "" {
		return defaultConfig, nil
	}

	cfg := Config{}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Warn("could not find valid config file")
		return cfg, nil
	} else if err != nil {
		return defaultConfig, fmt.Errorf("checking for config file: %w", err)
	}

	log.Infof("loading config from %s", configPath)
	f, err := os.Open(configPath)
	if err != nil {
		return defaultConfig, fmt.Errorf("opening config file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return defaultConfig, fmt.Errorf("reading config file: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return defaultConfig, fmt.Errorf("unmarhsalling config: %w", err)
	}

	if findIndex(cfg.ExcludePaths, ".gomon") < 0 {
		cfg.ExcludePaths = append(cfg.ExcludePaths, ".gomon")
	}

	return cfg, nil
}

func findIndex(array []string, target string) int {
	for i, value := range array {
		if value == target {
			return i
		}
	}
	return -1
}
