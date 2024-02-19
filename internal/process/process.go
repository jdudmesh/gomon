package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/notification"
	"github.com/jdudmesh/gomon/internal/utils"
	log "github.com/sirupsen/logrus"
)

type AtomicChildProcess struct {
	value atomic.Value
}

func (a *AtomicChildProcess) Load() *childProcess {
	return a.value.Load().(*childProcess)
}

func (a *AtomicChildProcess) Store(p *childProcess) {
	a.value.Store(p)
}

type ProcessState int

const (
	ProcessStateStopped ProcessState = iota
	ProcessStateStarting
	ProcessStateStarted
	ProcessStateStopping
)

type childProcess struct {
	rootDirectory  string
	command        []string
	entrypoint     string
	envVars        []string
	entrypointArgs []string
	prestart       []string
	state          *utils.State[ProcessState]
	childLock      sync.Mutex
	closeLock      sync.Mutex
	termChild      chan struct{}
	killChild      chan struct{}
	killTimeout    time.Duration
}

func NewChildProcess(cfg config.Config) (*childProcess, error) {
	proc := &childProcess{
		rootDirectory:  cfg.RootDirectory,
		command:        cfg.Command,
		entrypoint:     cfg.Entrypoint,
		envVars:        os.Environ(),
		entrypointArgs: cfg.EntrypointArgs,
		prestart:       cfg.Prestart,
		state:          utils.NewState[ProcessState](ProcessStateStopped),
		childLock:      sync.Mutex{},
		closeLock:      sync.Mutex{},
		termChild:      make(chan struct{}),
		killChild:      make(chan struct{}),
		killTimeout:    5 * time.Second,
	}

	if len(proc.command) == 0 {
		proc.command = []string{"go", "run"}
		if proc.entrypoint == "" {
			return nil, errors.New("an entrypoint is required")
		}
	}

	for _, file := range cfg.EnvFiles {
		err := proc.loadEnvFile(file)
		if err != nil {
			return nil, fmt.Errorf("loading env file: %w", err)
		}
	}

	return proc, nil
}

func (c *childProcess) Start(ctx context.Context, console ConsoleOutput, callbackFn notification.NotificationCallback) error {
	c.childLock.Lock()
	defer c.childLock.Unlock()

	if c.state.Get() != ProcessStateStopped {
		return errors.New("process is already running")
	}

	childProcessID := notification.NextID()

	for _, task := range c.prestart {
		oobTask := NewOutOfBandTask(c.rootDirectory, task, c.envVars)
		err := oobTask.Run(childProcessID, callbackFn)
		if err != nil {
			return fmt.Errorf("running prestart task: %w", err)
		}
	}

	c.state.Set(ProcessStateStarting)

	childContext, cancelChildContext := context.WithCancel(ctx)
	defer cancelChildContext()

	args := c.command[1:]
	if len(c.entrypoint) > 0 {
		args = append(args, c.entrypoint)
		if len(c.entrypointArgs) > 0 {
			args = append(args, c.entrypointArgs...)
		}
	}

	cmd := exec.CommandContext(childContext, c.command[0], args...)
	cmd.Dir = c.rootDirectory
	cmd.Stdout = console.Stdout()
	cmd.Stderr = console.Stderr()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = c.envVars

	err := cmd.Start()
	if err != nil {
		log.Errorf("spawning child process: %+v", err)
		return err
	}

	c.state.Set(ProcessStateStarted)

	callbackFn(notification.Notification{
		ID:              notification.NextID(),
		ChildProccessID: childProcessID,
		Date:            time.Now(),
		Type:            notification.NotificationTypeStartup,
		Message:         "process started",
	})

	exitStatus := make(chan int)
	go func() {
		err = cmd.Wait()
		if err != nil && !(err.Error() != "signal: terminated" || err.Error() != "signal: killed") {
			log.Warnf("child process exited abnormally: %+v", err)
		}

		s := cmd.ProcessState.ExitCode()
		if s > 0 {
			log.Warnf("child process exited with non-zero status: %d", cmd.ProcessState.ExitCode())
		}
		exitStatus <- s
	}()

	select {
	case <-c.termChild:
		log.Info("stopping child process: terminate requested")
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if err != nil {
			return err
		}
	case <-c.killChild:
		log.Info("stopping child process: close requested")
		cancelChildContext()
	case <-childContext.Done():
		log.Info("child process closed: context cancelled")
	}

	s := <-exitStatus
	log.Infof("child process exited with status: %d", s)

	c.state.Set(ProcessStateStopped)

	callbackFn(notification.Notification{
		ID:              notification.NextID(),
		ChildProccessID: childProcessID,
		Date:            time.Now(),
		Type:            notification.NotificationTypeShutdown,
		Message:         "process stopped",
	})

	return nil
}

func (c *childProcess) loadEnvFile(filename string) error {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		log.Warnf("env file %s does not exist", filename)
		return nil
	}

	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || len(line) == 0 {
			continue
		}
		c.envVars = append(c.envVars, line)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}
