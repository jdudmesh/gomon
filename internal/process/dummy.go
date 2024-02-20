package process

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
	"sync"

	"github.com/jdudmesh/gomon/internal/notification"
)

type dummyProcess struct {
	childOuterRunWait sync.WaitGroup
}

func NewDummy() ChildProcess {
	return &dummyProcess{
		childOuterRunWait: sync.WaitGroup{},
	}
}

func (d *dummyProcess) AddEventConsumer(sink notification.EventConsumer) {}

func (d *dummyProcess) Start() error {
	d.childOuterRunWait.Add(1)
	d.childOuterRunWait.Wait()
	return nil
}

func (d *dummyProcess) Close() error {
	d.childOuterRunWait.Done()
	return nil
}

func (d *dummyProcess) HardRestart(string) error {
	return nil
}

func (d *dummyProcess) SoftRestart(string) error {
	return nil
}

func (d *dummyProcess) RunOutOfBandTask(string) error {
	return nil
}
