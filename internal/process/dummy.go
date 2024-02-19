package process

import (
	"sync"

	notif "github.com/jdudmesh/gomon/internal/notification"
)

type dummyProcess struct {
	childOuterRunWait sync.WaitGroup
}

func NewDummy() ChildProcess {
	return &dummyProcess{
		childOuterRunWait: sync.WaitGroup{},
	}
}

func (d *dummyProcess) AddEventConsumer(sink notif.EventConsumer) {}

func (d *dummyProcess) Start() error {
	d.childOuterRunWait.Add(1)
	d.childOuterRunWait.Wait()
	return nil
}

func (d *dummyProcess) Close() error {
	d.childOuterRunWait.Done()
	return nil
}

func (d *dummyProcess) HardRestart(string) error {
	return nil
}

func (d *dummyProcess) SoftRestart(string) error {
	return nil
}

func (d *dummyProcess) RunOutOfBandTask(string) error {
	return nil
}
