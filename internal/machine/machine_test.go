package machine

import (
	"errors"
	"testing"
	"time"

	"github.com/intentius/mudflaps/internal/clock"
	"github.com/intentius/mudflaps/internal/flaps"
	"github.com/intentius/mudflaps/internal/store"
)

// testDelays uses one-second logical steps so the fake clock can step through
// transient states one at a time.
func testDelays() Delays {
	return Delays{
		Create:  time.Second,
		Start:   time.Second,
		Stop:    time.Second,
		Restart: time.Second,
		Destroy: time.Second,
		Update:  time.Second,
	}
}

func newFixture(t *testing.T) (*store.Store, *clock.Fake, *Advancer) {
	t.Helper()
	s := store.New()
	if _, err := s.CreateApp(flaps.App{Name: "demo"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	clk := clock.NewFake(time.Time{})
	a := NewAdvancer(s, clk, testDelays(), nil)
	return s, clk, a
}

func seed(t *testing.T, s *store.Store) flaps.Machine {
	t.Helper()
	m := flaps.Machine{
		ID:         "m1",
		State:      flaps.StateCreating,
		InstanceID: "INSTANCE0",
		Versions:   []flaps.MachineVersion{{InstanceID: "INSTANCE0", State: flaps.StateCreating}},
	}
	created, err := s.CreateMachine("demo", m)
	if err != nil {
		t.Fatalf("CreateMachine: %v", err)
	}
	return created
}

func stateOf(t *testing.T, s *store.Store) flaps.MachineState {
	t.Helper()
	m, err := s.GetMachine("demo", "m1")
	if err != nil {
		t.Fatalf("GetMachine: %v", err)
	}
	return m.State
}

func TestCreateReachesStarted(t *testing.T) {
	s, clk, a := newFixture(t)
	seed(t, s)

	a.Create("demo", "m1")
	if got := stateOf(t, s); got != flaps.StateCreating {
		t.Fatalf("before advance state = %q, want creating", got)
	}

	// First step: creating -> starting.
	clk.Advance(time.Second)
	if got := stateOf(t, s); got != flaps.StateStarting {
		t.Fatalf("after first step state = %q, want starting", got)
	}

	// Second step: starting -> started.
	clk.Advance(time.Second)
	if got := stateOf(t, s); got != flaps.StateStarted {
		t.Fatalf("after second step state = %q, want started", got)
	}
}

func TestCreateReachesStartedInOneAdvance(t *testing.T) {
	s, clk, a := newFixture(t)
	seed(t, s)

	a.Create("demo", "m1")
	// A single large advance should fire both chained transitions.
	clk.Advance(time.Hour)
	if got := stateOf(t, s); got != flaps.StateStarted {
		t.Fatalf("state = %q, want started", got)
	}
}

func TestStopAndDestroy(t *testing.T) {
	s, clk, a := newFixture(t)
	seed(t, s)

	if _, err := s.UpdateMachine("demo", "m1", func(m *flaps.Machine) error {
		m.State = flaps.StateStopping
		return nil
	}); err != nil {
		t.Fatalf("UpdateMachine: %v", err)
	}
	a.Stop("demo", "m1")
	clk.Advance(time.Second)
	if got := stateOf(t, s); got != flaps.StateStopped {
		t.Fatalf("state = %q, want stopped", got)
	}

	// Destroy reaps the machine: after the delay it is removed from the store.
	a.Destroy("demo", "m1")
	clk.Advance(time.Second)
	if _, err := s.GetMachine("demo", "m1"); !errors.Is(err, store.ErrMachineNotFound) {
		t.Fatalf("after destroy: GetMachine err = %v, want ErrMachineNotFound (reaped)", err)
	}
}

// TestUpdateBootsReplacingToStarted covers the advancer's half of an update:
// the server has already churned the version synchronously (new instance_id,
// state replacing), so the advancer only boots the new version to started.
func TestUpdateBootsReplacingToStarted(t *testing.T) {
	s, clk, a := newFixture(t)
	seed(t, s)

	// Simulate the server's synchronous churn.
	if _, err := s.UpdateMachine("demo", "m1", func(m *flaps.Machine) error {
		m.Versions[len(m.Versions)-1].State = flaps.StateReplaced
		m.InstanceID = "INSTANCE1"
		m.State = flaps.StateReplacing
		m.Versions = append(m.Versions, flaps.MachineVersion{
			InstanceID: "INSTANCE1", State: flaps.StateReplacing,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed churn: %v", err)
	}

	a.Update("demo", "m1")
	clk.Advance(time.Second)

	after, err := s.GetMachine("demo", "m1")
	if err != nil {
		t.Fatalf("GetMachine: %v", err)
	}
	if after.State != flaps.StateStarted {
		t.Fatalf("state = %q, want started", after.State)
	}
	current := after.Versions[len(after.Versions)-1]
	if current.InstanceID != "INSTANCE1" || current.State != flaps.StateStarted {
		t.Fatalf("current version = %+v, want INSTANCE1/started", current)
	}
	if prior := after.Versions[len(after.Versions)-2]; prior.State != flaps.StateReplaced {
		t.Fatalf("prior version state = %q, want replaced", prior.State)
	}
}
