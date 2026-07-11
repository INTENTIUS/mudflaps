// Package store is a thread-safe, in-memory store of apps and machines. It owns
// the canonical state; callers receive deep copies so that concurrent reads and
// writes never share mutable memory.
package store

import (
	"errors"
	"sync"

	"github.com/intentius/mudflaps/internal/flaps"
)

// Sentinel errors returned by the store.
var (
	ErrAppNotFound     = errors.New("app not found")
	ErrAppExists       = errors.New("app already exists")
	ErrMachineNotFound = errors.New("machine not found")
)

// Store holds apps and their machines.
type Store struct {
	mu   sync.RWMutex
	apps map[string]*appEntry
}

type appEntry struct {
	app      flaps.App
	machines map[string]*flaps.Machine
}

// New returns an empty store.
func New() *Store {
	return &Store{apps: make(map[string]*appEntry)}
}

// CreateApp records a new app. It returns ErrAppExists if the name is taken.
func (s *Store) CreateApp(app flaps.App) (flaps.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[app.Name]; ok {
		return flaps.App{}, ErrAppExists
	}
	if app.Status == "" {
		app.Status = "deployed"
	}
	s.apps[app.Name] = &appEntry{app: app, machines: make(map[string]*flaps.Machine)}
	return app, nil
}

// GetApp returns the named app.
func (s *Store) GetApp(name string) (flaps.App, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.apps[name]
	if !ok {
		return flaps.App{}, ErrAppNotFound
	}
	return e.app, nil
}

// ListApps returns every app in an unspecified order.
func (s *Store) ListApps() []flaps.App {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]flaps.App, 0, len(s.apps))
	for _, e := range s.apps {
		out = append(out, e.app)
	}
	return out
}

// DeleteApp removes an app and all of its machines.
func (s *Store) DeleteApp(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[name]; !ok {
		return ErrAppNotFound
	}
	delete(s.apps, name)
	return nil
}

// CreateMachine stores a machine under an app. The caller supplies a fully
// populated machine; the store keeps a private copy and returns a fresh copy.
func (s *Store) CreateMachine(app string, m flaps.Machine) (flaps.Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.apps[app]
	if !ok {
		return flaps.Machine{}, ErrAppNotFound
	}
	stored := cloneMachine(&m)
	e.machines[m.ID] = stored
	return *cloneMachine(stored), nil
}

// GetMachine returns a copy of a machine.
func (s *Store) GetMachine(app, id string) (flaps.Machine, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.lookup(app, id)
	if err != nil {
		return flaps.Machine{}, err
	}
	return *cloneMachine(m), nil
}

// ListMachines returns copies of every machine under an app.
func (s *Store) ListMachines(app string) ([]flaps.Machine, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.apps[app]
	if !ok {
		return nil, ErrAppNotFound
	}
	out := make([]flaps.Machine, 0, len(e.machines))
	for _, m := range e.machines {
		out = append(out, *cloneMachine(m))
	}
	return out, nil
}

// DeleteMachine removes a machine from an app.
func (s *Store) DeleteMachine(app, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.apps[app]
	if !ok {
		return ErrAppNotFound
	}
	if _, ok := e.machines[id]; !ok {
		return ErrMachineNotFound
	}
	delete(e.machines, id)
	return nil
}

// UpdateMachine applies fn to the live machine under the store lock and returns
// a copy of the result. fn sees the canonical machine and may mutate it in
// place; if fn returns an error the mutation is still visible only if fn made
// it before returning, so fn should treat its error as all-or-nothing.
func (s *Store) UpdateMachine(app, id string, fn func(m *flaps.Machine) error) (flaps.Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.lookup(app, id)
	if err != nil {
		return flaps.Machine{}, err
	}
	if err := fn(m); err != nil {
		return flaps.Machine{}, err
	}
	return *cloneMachine(m), nil
}

// lookup finds a machine without locking; callers must hold s.mu.
func (s *Store) lookup(app, id string) (*flaps.Machine, error) {
	e, ok := s.apps[app]
	if !ok {
		return nil, ErrAppNotFound
	}
	m, ok := e.machines[id]
	if !ok {
		return nil, ErrMachineNotFound
	}
	return m, nil
}

// cloneMachine deep-copies a machine so that stored and returned values never
// share mutable maps, slices, or pointers.
func cloneMachine(m *flaps.Machine) *flaps.Machine {
	c := *m
	if m.Config != nil {
		cfg := *m.Config
		cfg.Env = cloneStringMap(m.Config.Env)
		cfg.Metadata = cloneStringMap(m.Config.Metadata)
		if m.Config.Guest != nil {
			g := *m.Config.Guest
			cfg.Guest = &g
		}
		if m.Config.Restart != nil {
			r := *m.Config.Restart
			cfg.Restart = &r
		}
		if m.Config.Services != nil {
			svcs := make([]flaps.Service, len(m.Config.Services))
			for i, svc := range m.Config.Services {
				svc.Ports = clonePorts(svc.Ports)
				svcs[i] = svc
			}
			cfg.Services = svcs
		}
		c.Config = &cfg
	}
	if m.ImageRef != nil {
		ir := *m.ImageRef
		ir.Labels = cloneStringMap(m.ImageRef.Labels)
		c.ImageRef = &ir
	}
	if m.Versions != nil {
		c.Versions = append([]flaps.MachineVersion(nil), m.Versions...)
	}
	return &c
}

// clonePorts deep-copies a service's ports, including each port's Handlers
// slice, so a returned machine never aliases the stored one's nested slices.
func clonePorts(in []flaps.Port) []flaps.Port {
	if in == nil {
		return nil
	}
	out := make([]flaps.Port, len(in))
	for i, p := range in {
		if p.Handlers != nil {
			p.Handlers = append([]string(nil), p.Handlers...)
		}
		out[i] = p
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
