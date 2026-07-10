package machine

import (
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

	a.Destroy("demo", "m1")
	clk.Advance(time.Second)
	if got := stateOf(t, s); got != flaps.StateDestroyed {
		t.Fatalf("state = %q, want destroyed", got)
	}
}

func TestUpdateChurnsInstanceID(t *testing.T) {
	s, clk, a := newFixture(t)
	seed(t, s)

	before, err := s.GetMachine("demo", "m1")
	if err != nil {
		t.Fatalf("GetMachine: %v", err)
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
	if after.InstanceID == before.InstanceID {
		t.Fatalf("instance_id did not churn: still %q", after.InstanceID)
	}
	if len(after.Versions) < 2 {
		t.Fatalf("expected version history, got %d entries", len(after.Versions))
	}
	prior := after.Versions[len(after.Versions)-2]
	if prior.State != flaps.StateReplaced {
		t.Fatalf("prior version state = %q, want replaced", prior.State)
	}
	if prior.InstanceID != before.InstanceID {
		t.Fatalf("prior version instance = %q, want %q", prior.InstanceID, before.InstanceID)
	}
	current := after.Versions[len(after.Versions)-1]
	if current.InstanceID != after.InstanceID {
		t.Fatalf("current version instance = %q, want %q", current.InstanceID, after.InstanceID)
	}
}
