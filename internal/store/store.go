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
	ErrVolumeNotFound  = errors.New("volume not found")
	ErrSecretNotFound  = errors.New("secret not found")
	ErrIPNotFound      = errors.New("ip assignment not found")
	ErrCertNotFound    = errors.New("certificate not found")
)

// Store holds apps and their machines.
type Store struct {
	mu   sync.RWMutex
	apps map[string]*appEntry
}

type appEntry struct {
	app        flaps.App
	machines   map[string]*flaps.Machine
	volumes    map[string]*flaps.Volume
	secrets    map[string]*flaps.AppSecret
	secretsVer uint64
	ips        map[string]*flaps.IPAssignment
	certs      map[string]*flaps.CertificateDetailResponse
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
	s.apps[app.Name] = &appEntry{
		app:      app,
		machines: make(map[string]*flaps.Machine),
		volumes:  make(map[string]*flaps.Volume),
		secrets:  make(map[string]*flaps.AppSecret),
		ips:      make(map[string]*flaps.IPAssignment),
		certs:    make(map[string]*flaps.CertificateDetailResponse),
	}
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

// ---- volumes ----

// CreateVolume stores a volume under an app, keyed by its ID.
func (s *Store) CreateVolume(app string, v flaps.Volume) (flaps.Volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.apps[app]
	if !ok {
		return flaps.Volume{}, ErrAppNotFound
	}
	stored := cloneVolume(&v)
	e.volumes[v.ID] = stored
	return *cloneVolume(stored), nil
}

// GetVolume returns a copy of a volume.
func (s *Store) GetVolume(app, id string) (flaps.Volume, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, err := s.lookupVolume(app, id)
	if err != nil {
		return flaps.Volume{}, err
	}
	return *cloneVolume(v), nil
}

// ListVolumes returns copies of every volume under an app.
func (s *Store) ListVolumes(app string) ([]flaps.Volume, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.apps[app]
	if !ok {
		return nil, ErrAppNotFound
	}
	out := make([]flaps.Volume, 0, len(e.volumes))
	for _, v := range e.volumes {
		out = append(out, *cloneVolume(v))
	}
	return out, nil
}

// UpdateVolume applies fn to a stored volume and returns a copy.
func (s *Store) UpdateVolume(app, id string, fn func(v *flaps.Volume) error) (flaps.Volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.lookupVolume(app, id)
	if err != nil {
		return flaps.Volume{}, err
	}
	if err := fn(v); err != nil {
		return flaps.Volume{}, err
	}
	return *cloneVolume(v), nil
}

// DeleteVolume removes a volume and returns a copy of what was removed.
func (s *Store) DeleteVolume(app, id string) (flaps.Volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.lookupVolume(app, id)
	if err != nil {
		return flaps.Volume{}, err
	}
	out := *cloneVolume(v)
	delete(s.apps[app].volumes, id)
	return out, nil
}

// lookupVolume finds a volume without locking; callers must hold s.mu.
func (s *Store) lookupVolume(app, id string) (*flaps.Volume, error) {
	e, ok := s.apps[app]
	if !ok {
		return nil, ErrAppNotFound
	}
	v, ok := e.volumes[id]
	if !ok {
		return nil, ErrVolumeNotFound
	}
	return v, nil
}

func cloneVolume(v *flaps.Volume) *flaps.Volume {
	c := *v
	if v.AttachedMachine != nil {
		am := *v.AttachedMachine
		c.AttachedMachine = &am
	}
	return &c
}

// ---- secrets (apply-only: the store keeps a digest, never the value) ----

// SetSecret records (or replaces) a secret's digest and bumps the app's secrets
// version. It returns the stored metadata (never a value) and the new version.
func (s *Store) SetSecret(app, name, digest, now string) (flaps.AppSecret, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.apps[app]
	if !ok {
		return flaps.AppSecret{}, 0, ErrAppNotFound
	}
	created := now
	if existing, ok := e.secrets[name]; ok && existing.CreatedAt != nil {
		created = *existing.CreatedAt
	}
	sec := &flaps.AppSecret{Name: name, Digest: digest, CreatedAt: &created, UpdatedAt: &now}
	e.secrets[name] = sec
	e.secretsVer++
	return *cloneSecret(sec), e.secretsVer, nil
}

// GetSecret returns a secret's metadata (never a value).
func (s *Store) GetSecret(app, name string) (flaps.AppSecret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.apps[app]
	if !ok {
		return flaps.AppSecret{}, ErrAppNotFound
	}
	sec, ok := e.secrets[name]
	if !ok {
		return flaps.AppSecret{}, ErrSecretNotFound
	}
	return *cloneSecret(sec), nil
}

// ListSecrets returns every secret's metadata (never values).
func (s *Store) ListSecrets(app string) ([]flaps.AppSecret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.apps[app]
	if !ok {
		return nil, ErrAppNotFound
	}
	out := make([]flaps.AppSecret, 0, len(e.secrets))
	for _, sec := range e.secrets {
		out = append(out, *cloneSecret(sec))
	}
	return out, nil
}

// DeleteSecret removes a secret and bumps the version, returning the new version.
func (s *Store) DeleteSecret(app, name string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.apps[app]
	if !ok {
		return 0, ErrAppNotFound
	}
	if _, ok := e.secrets[name]; !ok {
		return 0, ErrSecretNotFound
	}
	delete(e.secrets, name)
	e.secretsVer++
	return e.secretsVer, nil
}

func cloneSecret(sec *flaps.AppSecret) *flaps.AppSecret {
	c := *sec
	if sec.CreatedAt != nil {
		v := *sec.CreatedAt
		c.CreatedAt = &v
	}
	if sec.UpdatedAt != nil {
		v := *sec.UpdatedAt
		c.UpdatedAt = &v
	}
	c.Value = nil // apply-only: never surface a value
	return &c
}

// ---- ip assignments ----

// AssignIP stores an IP assignment (keyed by IP).
func (s *Store) AssignIP(app string, ip flaps.IPAssignment) (flaps.IPAssignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.apps[app]
	if !ok {
		return flaps.IPAssignment{}, ErrAppNotFound
	}
	stored := ip
	e.ips[ip.IP] = &stored
	return stored, nil
}

// ListIPs returns every IP assignment for an app.
func (s *Store) ListIPs(app string) ([]flaps.IPAssignment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.apps[app]
	if !ok {
		return nil, ErrAppNotFound
	}
	out := make([]flaps.IPAssignment, 0, len(e.ips))
	for _, ip := range e.ips {
		out = append(out, *ip)
	}
	return out, nil
}

// DeleteIP removes an IP assignment.
func (s *Store) DeleteIP(app, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.apps[app]
	if !ok {
		return ErrAppNotFound
	}
	if _, ok := e.ips[ip]; !ok {
		return ErrIPNotFound
	}
	delete(e.ips, ip)
	return nil
}

// ---- certificates ----

// SetCertificate stores (or replaces) a certificate, keyed by hostname.
func (s *Store) SetCertificate(app string, cert flaps.CertificateDetailResponse) (flaps.CertificateDetailResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.apps[app]
	if !ok {
		return flaps.CertificateDetailResponse{}, ErrAppNotFound
	}
	stored := cert
	e.certs[cert.Hostname] = &stored
	return stored, nil
}

// GetCertificate returns a certificate by hostname.
func (s *Store) GetCertificate(app, hostname string) (flaps.CertificateDetailResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.apps[app]
	if !ok {
		return flaps.CertificateDetailResponse{}, ErrAppNotFound
	}
	c, ok := e.certs[hostname]
	if !ok {
		return flaps.CertificateDetailResponse{}, ErrCertNotFound
	}
	return *c, nil
}

// ListCertificates returns every certificate for an app.
func (s *Store) ListCertificates(app string) ([]flaps.CertificateDetailResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.apps[app]
	if !ok {
		return nil, ErrAppNotFound
	}
	out := make([]flaps.CertificateDetailResponse, 0, len(e.certs))
	for _, c := range e.certs {
		out = append(out, *c)
	}
	return out, nil
}

// DeleteCertificate removes a certificate by hostname.
func (s *Store) DeleteCertificate(app, hostname string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.apps[app]
	if !ok {
		return ErrAppNotFound
	}
	if _, ok := e.certs[hostname]; !ok {
		return ErrCertNotFound
	}
	delete(e.certs, hostname)
	return nil
}
