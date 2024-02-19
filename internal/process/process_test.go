package process

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/notification"
)

type testConsole struct{}

func (t *testConsole) Stdout() io.Writer {
	return os.Stdout
}

func (t *testConsole) Stderr() io.Writer {
	return os.Stderr
}

func TestChildProcess(t *testing.T) {
	proc, err := NewChildProcess(config.Config{
		RootDirectory: "/bin",
		Command:       []string{"sleep", "300"},
	})

	if err != nil {
		t.Fatalf("error creating child process: %v", err)
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	var stopError error
	go func() {
		<-time.After(5 * time.Second)
		t.Log("stopping child process")
		stopError = proc.Stop()
	}()

	err = proc.Start(ctx, &testConsole{}, func(n notification.Notification) {
		t.Logf("notification: %v", n)
	})
	if err != nil {
		t.Fatalf("error starting child process: %v", err)
	}

	if stopError != nil {
		t.Fatalf("error stopping child process: %v", err)
	}

}
