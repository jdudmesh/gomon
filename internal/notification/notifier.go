package notification

import (
	"context"
	"errors"
	"fmt"
	"time"

	ipc "github.com/jdudmesh/gomon-ipc"
)

type Notifier struct {
	ipcServer ipc.Connection
}

func NewNotifier() *Notifier {
	return &Notifier{
		ipcServer: ipc.NewConnection(ipc.ServerConnection),
	}
}

func (n *Notifier) Start(callbackFn NotificationCallback) error {
	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()

	err := n.ipcServer.ListenAndServe(ctx, func(state ipc.ConnectionState) error {
		switch state {
		case ipc.Connected:
			callbackFn(Notification{
				Type:    NotificationTypeIPC,
				Message: "child process connected",
			})
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("starting IPC server: %w", err)
	}

	return err
}

func (n *Notifier) Close() error {
	return n.ipcServer.Close()
}

func (n *Notifier) Notify(msg string) error {
	if !n.ipcServer.IsConnected() {
		return errors.New("IPC server is not connected")
	}

	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second)
	defer cancelFn()

	err := n.ipcServer.Write(ctx, []byte(msg))
	if err != nil {
		return fmt.Errorf("writing to IPC server: %w", err)
	}
	return nil
}
