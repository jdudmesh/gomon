//go:build linux || darwin
// +build linux darwin

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
	"errors"
	"time"

	log "github.com/sirupsen/logrus"
)

func (c *childProcess) Stop() error {
	c.closeLock.Lock()
	defer c.closeLock.Unlock()

	if c.state.Get() != ProcessStateStarted {
		return errors.New("process is not running")
	}

	c.state.Set(ProcessStateStopping)

	isChildClosed := make(chan struct{})
	go func() {
		// wait for the child process to close by trying capture lock, will have been locked in the Start method
		c.childLock.Lock()
		defer c.childLock.Unlock()
		// signal that the child process has closed
		isChildClosed <- struct{}{}
	}()

	// send the signal to the child process to close
	c.termChild <- struct{}{}

	// wait for the child process to close or timeout
	select {
	case <-isChildClosed:
		log.Info("child process closed")
	case <-time.After(c.killTimeout):
		c.killChild <- struct{}{}
	}

	return nil
}
