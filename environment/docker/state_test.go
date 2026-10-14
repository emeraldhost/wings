package docker

import (
	"testing"

	"github.com/Rene-Roscher/wings/environment"
	"github.com/Rene-Roscher/wings/events"
	"github.com/Rene-Roscher/wings/system"
)

// Fork: the backup/restore queue states must be accepted by SetState, otherwise
// a queued backup or restore panics in the router goroutine.
func TestSetStateAcceptsQueuedStates(t *testing.T) {
	states := []string{
		environment.ProcessOfflineState,
		environment.ProcessStartingState,
		environment.ProcessRunningState,
		environment.ProcessStoppingState,
		environment.ProcessBackupState,
		environment.ProcessRestoringState,
		environment.ProcessBackupQueuedState,
		environment.ProcessRestoreQueuedState,
	}
	for _, st := range states {
		e := &Environment{emitter: events.NewBus(), st: system.NewAtomicString(environment.ProcessOfflineState)}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("SetState(%q) panicked: %v", st, r)
				}
			}()
			e.SetState(st)
		}()
		if got := e.State(); got != st {
			t.Fatalf("SetState(%q): got state %q", st, got)
		}
	}
}

func TestSetStateRejectsUnknownState(t *testing.T) {
	e := &Environment{emitter: events.NewBus(), st: system.NewAtomicString(environment.ProcessOfflineState)}
	defer func() {
		if recover() == nil {
			t.Fatal("expected SetState to panic on an unknown state")
		}
	}()
	e.SetState("bogus")
}
