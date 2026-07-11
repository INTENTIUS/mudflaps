// Package machine drives a machine through its lifecycle. It is an asynchronous
// advancer: a mutating request sets the machine's transient state immediately
// and schedules the move to the resting state on the injected clock. In
// production the delays are short real durations; in tests a fake clock fires
// them on demand so transitions are instant and deterministic.
package machine

import (
	"log/slog"
	"time"

	"github.com/intentius/mudflaps/internal/clock"
	"github.com/intentius/mudflaps/internal/flaps"
	"github.com/intentius/mudflaps/internal/store"
)

// Delays controls how long each transient state lasts before the machine
// settles. Keep these short; they exist only to make transient states
// observable.
type Delays struct {
	Create  time.Duration
	Start   time.Duration
	Stop    time.Duration
	Restart time.Duration
	Destroy time.Duration
	Update  time.Duration
	Suspend time.Duration
}

// DefaultDelays are modest real-time delays used by the running server.
func DefaultDelays() Delays {
	return Delays{
		Create:  75 * time.Millisecond,
		Start:   75 * time.Millisecond,
		Stop:    75 * time.Millisecond,
		Restart: 75 * time.Millisecond,
		Destroy: 75 * time.Millisecond,
		Update:  75 * time.Millisecond,
		Suspend: 75 * time.Millisecond,
	}
}

// Advancer schedules lifecycle transitions against a store.
type Advancer struct {
	store  *store.Store
	clk    clock.Clock
	delays Delays
	log    *slog.Logger
}

// NewAdvancer returns an Advancer. A nil logger is replaced with a discarding
// default.
func NewAdvancer(s *store.Store, clk clock.Clock, delays Delays, log *slog.Logger) *Advancer {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Advancer{store: s, clk: clk, delays: delays, log: log}
}

// Create moves a freshly created machine creating -> starting -> started.
func (a *Advancer) Create(app, machineID string) {
	a.clk.AfterFunc(a.delays.Create, func() {
		a.set(app, machineID, flaps.StateStarting)
		a.clk.AfterFunc(a.delays.Start, func() {
			a.set(app, machineID, flaps.StateStarted)
		})
	})
}

// Start moves a stopped machine starting -> started. The transient state is set
// by the caller (the server) before scheduling.
func (a *Advancer) Start(app, machineID string) {
	a.clk.AfterFunc(a.delays.Start, func() {
		a.set(app, machineID, flaps.StateStarted)
	})
}

// Stop moves a machine stopping -> stopped.
func (a *Advancer) Stop(app, machineID string) {
	a.clk.AfterFunc(a.delays.Stop, func() {
		a.set(app, machineID, flaps.StateStopped)
	})
}

// Restart moves a machine restarting -> started.
func (a *Advancer) Restart(app, machineID string) {
	a.clk.AfterFunc(a.delays.Restart, func() {
		a.set(app, machineID, flaps.StateStarted)
	})
}

// Suspend moves a machine suspending -> suspended. Resume is a normal Start.
func (a *Advancer) Suspend(app, machineID string) {
	a.clk.AfterFunc(a.delays.Suspend, func() {
		a.set(app, machineID, flaps.StateSuspended)
	})
}

// Destroy moves a machine destroying -> destroyed.
func (a *Advancer) Destroy(app, machineID string) {
	a.clk.AfterFunc(a.delays.Destroy, func() {
		a.set(app, machineID, flaps.StateDestroyed)
	})
}

// Update settles a machine that is being replaced. The server performs the
// version churn synchronously (minting the new instance_id and marking the prior
// version replaced) as part of the update response; the advancer only boots the
// new version replacing -> started.
func (a *Advancer) Update(app, machineID string) {
	a.clk.AfterFunc(a.delays.Update, func() {
		a.set(app, machineID, flaps.StateStarted)
	})
}

// set writes a resting or terminal state onto a machine, keeping the current
// version entry's state in step.
func (a *Advancer) set(app, machineID string, state flaps.MachineState) {
	_, err := a.store.UpdateMachine(app, machineID, func(m *flaps.Machine) error {
		m.State = state
		if n := len(m.Versions); n > 0 {
			m.Versions[n-1].State = state
		}
		return nil
	})
	if err != nil {
		// A machine can be deleted out from under a pending transition; that is
		// expected, not an error worth surfacing.
		a.log.Debug("state advance skipped", "app", app, "machine", machineID, "state", state, "err", err)
	}
}
