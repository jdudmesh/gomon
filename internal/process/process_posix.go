//go:build linux || darwin
// +build linux darwin

package process

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
	close(c.termChild)

	isChildClosed := make(chan struct{})
	go func() {
		c.childLock.Lock()
		defer c.childLock.Unlock()
		close(isChildClosed)
	}()

	select {
	case <-isChildClosed:
		log.Info("child process closed")
	case <-time.After(c.killTimeout):
		close(c.killChild)
	}

	return nil
}
