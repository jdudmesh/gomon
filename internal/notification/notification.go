package notification

import (
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
)

type NotificationCallback func(n Notification)

type NotificationType int

const (
	NotificationTypeSystemError NotificationType = iota
	NotificationTypeSoftRestartRequested
	NotificationTypeHardRestartRequested
	NotificationTypeOOBTaskRequested
	NotificationTypeShutdownRequested
	NotificationTypeSystemShutdown
	NotificationTypeStartup
	NotificationTypeHardRestart
	NotificationTypeSoftRestart
	NotificationTypeShutdown
	NotificationTypeLogEvent
	NotificationTypeStdOut
	NotificationTypeStdErr
	NotificationTypeOOBTaskStartup
	NotificationTypeOOBTaskStdOut
	NotificationTypeOOBTaskStdErr
	NotificationTypeIPC
)

type Notification struct {
	ID              string           `json:"id" db:"id"` // snowflake
	Date            time.Time        `json:"createdAt" db:"created_at"`
	ChildProccessID string           `json:"childProcessId" db:"child_process_id"` // snowflake
	Type            NotificationType `json:"type" db:"event_type"`
	Message         string           `json:"message" db:"event_data"`
}

type EventConsumer interface {
	Notify(n Notification)
}

var (
	generator           *snowflake.Node
	createGeneratorOnce = sync.Once{}
)

func NextID() string {
	createGeneratorOnce.Do(func() {
		generator, _ = snowflake.NewNode(1)
	})
	return generator.Generate().Base32()
}
