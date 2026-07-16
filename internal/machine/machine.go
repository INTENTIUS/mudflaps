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

// Create moves a freshly created machine creating -> starting -> started (or,
// for a machine whose image can't be pulled, creating -> starting -> failed;
// see settle).
func (a *Advancer) Create(app, machineID string) {
	a.clk.AfterFunc(a.delays.Create, func() {
		a.set(app, machineID, flaps.StateStarting)
		a.clk.AfterFunc(a.delays.Start, func() {
			a.settle(app, machineID)
		})
	})
}

// Start moves a stopped machine starting -> started (or -> failed for an
// unpullable image). The transient state is set by the caller (the server)
// before scheduling.
func (a *Advancer) Start(app, machineID string) {
	a.clk.AfterFunc(a.delays.Start, func() {
		a.settle(app, machineID)
	})
}

// Stop moves a machine stopping -> stopped.
func (a *Advancer) Stop(app, machineID string) {
	a.clk.AfterFunc(a.delays.Stop, func() {
		a.set(app, machineID, flaps.StateStopped)
	})
}

// Restart moves a machine restarting -> started (or -> failed for an unpullable
// image).
func (a *Advancer) Restart(app, machineID string) {
	a.clk.AfterFunc(a.delays.Restart, func() {
		a.settle(app, machineID)
	})
}

// Suspend moves a machine suspending -> suspended. Resume is a normal Start.
func (a *Advancer) Suspend(app, machineID string) {
	a.clk.AfterFunc(a.delays.Suspend, func() {
		a.set(app, machineID, flaps.StateSuspended)
	})
}

// Destroy reaps the machine: once the destroy delay elapses it is removed from
// the store (real flaps does not keep a destroyed machine around to be
// operated on). A `wait` for `destroyed` is satisfied by the machine being
// gone; see the server's wait handler.
func (a *Advancer) Destroy(app, machineID string) {
	a.clk.AfterFunc(a.delays.Destroy, func() {
		if err := a.store.DeleteMachine(app, machineID); err != nil {
			a.log.Debug("destroy reap skipped", "app", app, "machine", machineID, "err", err)
		}
	})
}

// Update settles a machine that is being replaced. The server performs the
// version churn synchronously (minting the new instance_id and marking the prior
// version replaced) as part of the update response; the advancer only boots the
// new version replacing -> started.
func (a *Advancer) Update(app, machineID string) {
	a.clk.AfterFunc(a.delays.Update, func() {
		a.settle(app, machineID)
	})
}

// settle drives a booting machine to its resting state: StateStarted normally,
// or StateFailed when its config marks it unpullable (see
// flaps.MachineConfig.FailsToBoot). This is the one place a machine can end in
// `failed` — modeling a boot-time image-pull failure so a client that waits for
// `started` observes a deploy that never comes up (issue #61). Reading the
// config inside the same mutation keeps the decision consistent with the very
// latest update (an update to a good image before this fires still boots).
func (a *Advancer) settle(app, machineID string) {
	_, err := a.store.UpdateMachine(app, machineID, func(m *flaps.Machine) error {
		state := flaps.StateStarted
		if m.Config.FailsToBoot() {
			state = flaps.StateFailed
		}
		m.State = state
		if n := len(m.Versions); n > 0 {
			m.Versions[n-1].State = state
		}
		return nil
	})
	if err != nil {
		a.log.Debug("settle skipped", "app", app, "machine", machineID, "err", err)
	}
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
