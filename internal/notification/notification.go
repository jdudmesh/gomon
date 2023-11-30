package notification

type NotificationType int
type NotificationChannel chan Notification

const (
	NotificationTypeSystemError NotificationType = iota
	NotificationTypeSystemShutdown
	NotificationTypeStartup
	NotificationTypeHardRestart
	NotificationTypeSoftRestart
	NotificationTypeShutdown
	NotificationTypeLogEvent
)

type Notification struct {
	Type     NotificationType
	Message  string
	Metadata interface{}
}

type NotificationSink interface {
	Notify(n Notification)
}

func NewChannel() NotificationChannel {
	return make(chan Notification)
}
