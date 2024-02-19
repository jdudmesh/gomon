//go:build windows
// +build windows

package process

import (
	"errors"
)

func (c *childProcess2) Stop() error {
	c.closeLock.Lock()
	defer c.closeLock.Unlock()

	if c.state.Get() != ProcessStateStarted {
		return errors.New("process is not running")
	}

	c.state.Set(ProcessStateStopping)
	close(c.killChild)

	c.childLock.Lock()
	defer c.childLock.Unlock()

	return nil
}
