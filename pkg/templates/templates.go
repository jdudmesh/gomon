package templates

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
	"html/template"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/labstack/echo"
)

type CloseFunc func()

type templateManagerEcho struct {
	templates *template.Template
}

func (t *templateManagerEcho) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

func NewEcho(pathGlob string) (*templateManagerEcho, CloseFunc) {
	t := &templateManagerEcho{
		templates: template.Must(template.ParseGlob(pathGlob)),
	}

	quit := make(chan bool)
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, syscall.SIGUSR1)
		for {
			select {
			case <-sigint:
				t.templates = template.Must(template.ParseGlob(pathGlob))
			case <-quit:
				return
			}
		}
	}()

	return t, func() {
		quit <- true
	}
}
