package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/console"
	"github.com/jdudmesh/gomon/internal/notification"
	"github.com/jdudmesh/gomon/internal/process"
	"github.com/jdudmesh/gomon/internal/proxy"
	"github.com/jdudmesh/gomon/internal/utils"
	"github.com/jdudmesh/gomon/internal/watcher"
	"github.com/jdudmesh/gomon/internal/webui"
	log "github.com/sirupsen/logrus"
	"gopkg.in/cenkalti/backoff.v1"
)

type App struct {
	sigint        chan os.Signal
	hardRestart   chan string
	softRestart   chan string
	oobTask       chan string
	childProcess  process.AtomicChildProcess
	db            Database
	watcher       Watcher
	proxy         WebProxy
	notifier      Notifier
	consoleWriter Console
	webui         UI
}

type Closeable interface {
	Close() error
}

type Startable interface {
	Start() error
}

type Database interface {
	Closeable
	notification.EventConsumer
	webui.Database
}

type Watcher interface {
	Closeable
	Watch(notification.NotificationCallback) error
}

type WebProxy interface {
	Closeable
	Startable
	notification.EventConsumer
	Enabled() bool
}

type Notifier interface {
	Closeable
	Startable
	notification.EventConsumer
	SendSoftRestart(hint string) error
}

type Console interface {
	Closeable
	Startable
	notification.EventConsumer
	process.ConsoleOutput
}

type UI interface {
	Closeable
	Startable
	notification.EventConsumer
	Enabled() bool
}

func New(cfg config.Config) (*App, error) {
	var err error

	app := &App{
		sigint:       make(chan os.Signal, 1),
		hardRestart:  make(chan string),
		softRestart:  make(chan string),
		oobTask:      make(chan string),
		childProcess: process.AtomicChildProcess{},
	}

	app.db, err = utils.NewDatabase(cfg)
	if err != nil {
		log.Fatalf("creating database: %v", err)
	}

	app.proxy, err = proxy.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating proxy: %v", err)
	}

	app.watcher, err = watcher.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating monitor: %w", err)
	}

	app.notifier, err = notification.NewNotifier(app.Notify)
	if err != nil {
		return nil, fmt.Errorf("creating notifier: %v", err)
	}

	app.consoleWriter, err = console.New(cfg, app.Notify)
	if err != nil {
		return nil, fmt.Errorf("creating console: %v", err)
	}

	app.webui, err = webui.New(cfg, app.db, func(n notification.Notification) error {
		switch n.Type {
		case notification.NotificationTypeHardRestartRequested:
			app.hardRestart <- "webui"
		case notification.NotificationTypeSoftRestartRequested:
			app.softRestart <- "webui"
		case notification.NotificationTypeShutdownRequested:
			app.sigint <- syscall.SIGTERM
		}
		return app.Notify(n)
	})
	if err != nil {
		return nil, fmt.Errorf("creating console: %v", err)
	}

	return app, nil
}

func (a *App) Close() {
	proc := a.childProcess.Load()
	if proc != nil {
		proc.Stop()
	}

	if a.db != nil {
		a.db.Close()
	}
	if a.proxy != nil {
		a.proxy.Close()
	}
	if a.watcher != nil {
		a.watcher.Close()
	}
	if a.notifier != nil {
		a.notifier.Close()
	}
	if a.consoleWriter != nil {
		a.consoleWriter.Close()
	}
	if a.webui != nil {
		a.webui.Close()
	}
}

func (a *App) MonitorFileChanges(ctx context.Context) error {
	err := a.watcher.Watch(func(n notification.Notification) error {
		switch n.Type {
		case notification.NotificationTypeHardRestartRequested:
			a.hardRestart <- n.Message
		case notification.NotificationTypeSoftRestartRequested:
			a.softRestart <- n.Message
		case notification.NotificationTypeOOBTaskRequested:
			a.oobTask <- n.Message
		}
		return a.Notify(n)
	})

	if err != nil {
		log.Errorf("running monitor: %v", err)
	}

	return err
}

func (a *App) RunProxy() error {
	if a.proxy.Enabled() {
		return a.proxy.Start()
	}
	return nil
}

func (a *App) RunNotifer() error {
	return a.notifier.Start()
}

func (a *App) RunConsole() error {
	return a.consoleWriter.Start()
}

func (a *App) RunWebUI() error {
	if a.webui.Enabled() {
		return a.webui.Start()
	}
	return nil
}

func (a *App) RunChildProcess(cfg config.Config) error {
	proc, err := process.NewChildProcess(cfg)
	if err != nil {
		log.Fatalf("creating child process: %v", err)
	}

	a.childProcess.Store(proc)

	backoffPolicy := backoff.NewExponentialBackOff()
	backoffPolicy.InitialInterval = 500 * time.Millisecond
	backoffPolicy.MaxInterval = 5000 * time.Millisecond
	backoffPolicy.MaxElapsedTime = 60 * time.Second

	err = backoff.Retry(func() error {
		return proc.Start(a.consoleWriter, a.Notify)
	}, backoffPolicy)

	if err != nil {
		log.Errorf("failed retrying child process: %v", err)
		return err
	}

	return nil
}

func (a *App) ProcessRestartEvents(ctx context.Context) error {
	for {
		select {
		case hint := <-a.hardRestart:
			log.Info("hard restart: " + hint)
			proc := a.childProcess.Load()
			if proc != nil {
				proc.Stop()
			}
		case hint := <-a.softRestart:
			log.Info("soft restart: " + hint)
			err := a.notifier.SendSoftRestart(hint)
			if err != nil {
				log.Warnf("notifying child process: %v", err)
			}
		case task := <-a.oobTask:
			log.Info("out of band task: " + task)
			proc := a.childProcess.Load()
			if proc != nil {
				proc.ExecuteOOBTask(task, a.Notify)
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (a *App) ProcessSignals() error {
	signal.Notify(a.sigint, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGUSR1)
	for s := range a.sigint {
		switch s {
		case syscall.SIGHUP:
			log.Info("received signal, restarting")
			a.softRestart <- "sighup"
		case syscall.SIGUSR1:
			log.Info("received signal, hard restarting")
			a.hardRestart <- "sigusr1"
		case syscall.SIGINT, syscall.SIGTERM:
			log.Info("received term signal, exiting")
			return errors.New("shutdown requested")
		}
	}
	return nil
}

func (a *App) Notify(n notification.Notification) error {
	a.db.Notify(n)
	a.consoleWriter.Notify(n)
	a.proxy.Notify(n)
	a.webui.Notify(n)
	a.notifier.Notify(n)
	return nil
}
