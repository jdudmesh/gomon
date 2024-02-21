package notification

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
	"context"
	"errors"
	"fmt"
	"time"

	ipc "github.com/jdudmesh/gomon-ipc"
)

const SoftRestartMessage = "__soft_reload"

type Notifier struct {
	ipcServer  ipc.Connection
	callbackFn NotificationCallback
}

func NewNotifier(callbackFn NotificationCallback) (*Notifier, error) {
	n := &Notifier{
		callbackFn: callbackFn,
	}
	ipcServer, err := ipc.NewConnection(ipc.ServerConnection, ipc.WithReadHandler(n.handleInboundMessage))
	if err != nil {
		return nil, fmt.Errorf("creating IPC server: %w", err)
	}
	n.ipcServer = ipcServer
	return n, nil
}

func (n *Notifier) Start() error {
	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()

	err := n.ipcServer.ListenAndServe(ctx, func(state ipc.ConnectionState) error {
		switch state {
		case ipc.Connected:
			n.callbackFn(Notification{
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

func (n *Notifier) handleInboundMessage(data []byte) error {
	msg := string(data)
	if len(msg) == 0 {
		return nil
	}
	switch msg {
	case SoftRestartMessage:
		n.callbackFn(Notification{
			Type:    NotificationTypeSoftRestart,
			Message: "soft restart completed",
		})

	}

	return nil
}
