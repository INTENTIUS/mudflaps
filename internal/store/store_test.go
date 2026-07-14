package store

import (
	"fmt"
	"sync"
	"testing"

	"github.com/intentius/mudflaps/internal/flaps"
)

func TestAppCRUD(t *testing.T) {
	s := New()

	if _, err := s.CreateApp(flaps.App{Name: "demo"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := s.CreateApp(flaps.App{Name: "demo"}); err != ErrAppExists {
		t.Fatalf("duplicate CreateApp = %v, want ErrAppExists", err)
	}

	got, err := s.GetApp("demo")
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if got.Status != "deployed" {
		t.Fatalf("default status = %q, want deployed", got.Status)
	}

	if apps := s.ListApps(); len(apps) != 1 {
		t.Fatalf("ListApps len = %d, want 1", len(apps))
	}

	if err := s.DeleteApp("demo"); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if _, err := s.GetApp("demo"); err != ErrAppNotFound {
		t.Fatalf("GetApp after delete = %v, want ErrAppNotFound", err)
	}
}

func TestMachineCRUD(t *testing.T) {
	s := New()
	if _, err := s.CreateApp(flaps.App{Name: "demo"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	m := flaps.Machine{ID: "m1", State: flaps.StateCreating, Config: &flaps.MachineConfig{
		Image: "nginx",
		Env:   map[string]string{"A": "1"},
	}}
	created, err := s.CreateMachine("demo", m)
	if err != nil {
		t.Fatalf("CreateMachine: %v", err)
	}

	// Mutating the returned copy must not affect stored state.
	created.Config.Env["A"] = "mutated"
	again, err := s.GetMachine("demo", "m1")
	if err != nil {
		t.Fatalf("GetMachine: %v", err)
	}
	if again.Config.Env["A"] != "1" {
		t.Fatalf("stored env leaked mutation: %q", again.Config.Env["A"])
	}

	if _, err := s.CreateMachine("missing", m); err != ErrAppNotFound {
		t.Fatalf("CreateMachine on missing app = %v, want ErrAppNotFound", err)
	}

	updated, err := s.UpdateMachine("demo", "m1", func(mm *flaps.Machine) error {
		mm.State = flaps.StateStarted
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateMachine: %v", err)
	}
	if updated.State != flaps.StateStarted {
		t.Fatalf("state = %q, want started", updated.State)
	}

	machines, err := s.ListMachines("demo")
	if err != nil || len(machines) != 1 {
		t.Fatalf("ListMachines = %v (err %v), want 1", machines, err)
	}

	if err := s.DeleteMachine("demo", "m1"); err != nil {
		t.Fatalf("DeleteMachine: %v", err)
	}
	if _, err := s.GetMachine("demo", "m1"); err != ErrMachineNotFound {
		t.Fatalf("GetMachine after delete = %v, want ErrMachineNotFound", err)
	}
}

// TestConcurrentAccess exercises the store under -race with many goroutines
// hammering the same app.
func TestConcurrentAccess(t *testing.T) {
	s := New()
	if _, err := s.CreateApp(flaps.App{Name: "demo"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(n int) {
			defer wg.Done()
			mID := fmt.Sprintf("m%d", n)
			if _, err := s.CreateMachine("demo", flaps.Machine{
				ID:     mID,
				State:  flaps.StateCreating,
				Config: &flaps.MachineConfig{Env: map[string]string{"n": mID}},
			}); err != nil {
				t.Errorf("CreateMachine: %v", err)
				return
			}
			for j := 0; j < 20; j++ {
				if _, err := s.UpdateMachine("demo", mID, func(m *flaps.Machine) error {
					m.State = flaps.StateStarted
					return nil
				}); err != nil {
					t.Errorf("UpdateMachine: %v", err)
					return
				}
				_, _ = s.GetMachine("demo", mID)
				_, _ = s.ListMachines("demo")
			}
		}(i)
	}
	wg.Wait()

	machines, err := s.ListMachines("demo")
	if err != nil {
		t.Fatalf("ListMachines: %v", err)
	}
	if len(machines) != workers {
		t.Fatalf("machine count = %d, want %d", len(machines), workers)
	}
}

// TestClonedMachinePortsAreIsolated is the regression for the audit clone
// finding: a returned machine must not alias the stored machine's nested
// service port/handler slices.
func TestClonedMachinePortsAreIsolated(t *testing.T) {
	s := New()
	if _, err := s.CreateApp(flaps.App{Name: "demo"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	m := flaps.Machine{
		ID: "m1",
		Config: &flaps.MachineConfig{
			Services: []flaps.Service{{
				Protocol: "tcp",
				Ports:    []flaps.Port{{Port: 443, Handlers: []string{"tls", "http"}}},
			}},
		},
	}
	if _, err := s.CreateMachine("demo", m); err != nil {
		t.Fatalf("CreateMachine: %v", err)
	}

	// Mutate a returned clone's handler slice in place.
	c1, _ := s.GetMachine("demo", "m1")
	c1.Config.Services[0].Ports[0].Handlers[0] = "MUTATED"

	// A fresh clone must be unaffected.
	c2, _ := s.GetMachine("demo", "m1")
	if got := c2.Config.Services[0].Ports[0].Handlers[0]; got != "tls" {
		t.Fatalf("clone aliased stored handlers: got %q, want tls", got)
	}
}

func TestMachineMountsRoundTrip(t *testing.T) {
	s := New()
	if _, err := s.CreateApp(flaps.App{Name: "demo"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	m := flaps.Machine{
		ID:     "m1",
		State:  flaps.StateCreating,
		Config: &flaps.MachineConfig{Image: "nginx", Mounts: []flaps.MachineMount{{Volume: "data", Path: "/data"}}},
	}
	if _, err := s.CreateMachine("demo", m); err != nil {
		t.Fatalf("CreateMachine: %v", err)
	}

	// The mount survives create → GET, so a mounting machine reconciles to a
	// no-op instead of looking drifted every apply (the bug this fixes).
	got, err := s.GetMachine("demo", "m1")
	if err != nil {
		t.Fatalf("GetMachine: %v", err)
	}
	if len(got.Config.Mounts) != 1 || got.Config.Mounts[0].Volume != "data" || got.Config.Mounts[0].Path != "/data" {
		t.Fatalf("mounts not persisted: %+v", got.Config.Mounts)
	}

	// Clone isolation: mutating a returned clone must not affect stored state.
	got.Config.Mounts[0].Path = "MUTATED"
	again, _ := s.GetMachine("demo", "m1")
	if again.Config.Mounts[0].Path != "/data" {
		t.Fatalf("clone aliased stored mounts: got %q, want /data", again.Config.Mounts[0].Path)
	}
}
