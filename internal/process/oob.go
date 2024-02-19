package process

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jdudmesh/gomon/internal/notification"
	log "github.com/sirupsen/logrus"
)

type outOfBandTask struct {
	rootDirectory string
	task          string
	envVars       []string
}

func NewOutOfBandTask(rootDirectory string, task string, envVars []string) *outOfBandTask {
	return &outOfBandTask{
		rootDirectory: rootDirectory,
		task:          task,
		envVars:       envVars,
	}
}

func (o *outOfBandTask) Run(childProcessID string, callbackFn notification.NotificationCallback) error {
	log.Infof("running task: %s", o.task)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	callbackFn(notification.Notification{
		ID:              notification.NextID(),
		ChildProccessID: childProcessID,
		Date:            time.Now(),
		Type:            notification.NotificationTypeOOBTaskStartup,
		Message:         "running task: " + o.task,
	})

	args := strings.Split(o.task, " ")
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = o.rootDirectory
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = o.envVars

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("starting task: %w", err)
	}

	err = cmd.Wait()

	if stdoutBuf.Len() > 0 {
		callbackFn(notification.Notification{
			ID:              notification.NextID(),
			ChildProccessID: childProcessID,
			Date:            time.Now(),
			Type:            notification.NotificationTypeOOBTaskStdOut,
			Message:         string(stdoutBuf.Bytes()),
		})
	}

	if stderrBuf.Len() > 0 {
		callbackFn(notification.Notification{
			ID:              notification.NextID(),
			ChildProccessID: childProcessID,
			Date:            time.Now(),
			Type:            notification.NotificationTypeOOBTaskStdErr,
			Message:         string(stderrBuf.Bytes()),
		})
	}

	if err != nil {
		return fmt.Errorf("running oob task: %w", err)
	}

	return nil
}
