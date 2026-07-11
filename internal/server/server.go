// Package server wires the store, lease manager, and lifecycle advancer behind
// an net/http router that speaks the flaps wire protocol over the /v1 path
// space. It uses Go 1.22+ method+pattern routing so it needs no third-party
// router.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/intentius/mudflaps/internal/clock"
	"github.com/intentius/mudflaps/internal/flaps"
	"github.com/intentius/mudflaps/internal/id"
	"github.com/intentius/mudflaps/internal/lease"
	"github.com/intentius/mudflaps/internal/machine"
	"github.com/intentius/mudflaps/internal/store"
)

// LeaseNonceHeader carries the lease nonce on mutating requests, matching Fly.
const LeaseNonceHeader = "fly-machine-lease-nonce"

// waitPollInterval is how often a /wait handler re-checks machine state.
const waitPollInterval = 20 * time.Millisecond

// implementedPaths and unimplementedPaths back the health/coverage endpoint.
var implementedPaths = []string{
	"GET /v1/apps",
	"POST /v1/apps",
	"GET /v1/apps/{app}",
	"DELETE /v1/apps/{app}",
	"POST /v1/apps/{app}/machines",
	"GET /v1/apps/{app}/machines",
	"GET /v1/apps/{app}/machines/{id}",
	"POST /v1/apps/{app}/machines/{id}",
	"DELETE /v1/apps/{app}/machines/{id}",
	"POST /v1/apps/{app}/machines/{id}/start",
	"POST /v1/apps/{app}/machines/{id}/stop",
	"POST /v1/apps/{app}/machines/{id}/restart",
	"POST /v1/apps/{app}/machines/{id}/suspend",
	"POST /v1/apps/{app}/machines/{id}/cordon",
	"POST /v1/apps/{app}/machines/{id}/uncordon",
	"GET /v1/apps/{app}/machines/{id}/wait",
	"GET /v1/apps/{app}/machines/{id}/metadata",
	"POST /v1/apps/{app}/machines/{id}/metadata/{key}",
	"DELETE /v1/apps/{app}/machines/{id}/metadata/{key}",
	"GET /v1/apps/{app}/machines/{id}/lease",
	"POST /v1/apps/{app}/machines/{id}/lease",
	"DELETE /v1/apps/{app}/machines/{id}/lease",
}

var unimplementedPaths = []string{
	"/v1/apps/{app}/volumes",
	"/v1/apps/{app}/secrets",
	"/v1/apps/{app}/certificates",
	"/v1/apps/{app}/ip_assignments",
	"/v1/apps/{app}/machines/{id}/signal",
	"/v1/apps/{app}/machines/{id}/exec",
	"/v1/apps/{app}/machines/{id}/ps",
}

// Options configures a Server.
type Options struct {
	Version  string
	Clock    clock.Clock
	Logger   *slog.Logger
	Delays   machine.Delays
	LeaseTTL time.Duration
}

// Server holds the mudflaps state and serves the API.
type Server struct {
	version  string
	clk      clock.Clock
	log      *slog.Logger
	store    *store.Store
	leases   *lease.Manager
	advancer *machine.Advancer
	leaseTTL time.Duration
	mux      *http.ServeMux
}

// New constructs a Server, filling in sensible defaults for any zero option.
func New(opts Options) *Server {
	if opts.Clock == nil {
		opts.Clock = clock.Real()
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if (opts.Delays == machine.Delays{}) {
		opts.Delays = machine.DefaultDelays()
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = lease.DefaultTTL
	}
	st := store.New()
	s := &Server{
		version:  opts.Version,
		clk:      opts.Clock,
		log:      opts.Logger,
		store:    st,
		leases:   lease.New(opts.Clock),
		advancer: machine.NewAdvancer(st, opts.Clock, opts.Delays, opts.Logger),
		leaseTTL: opts.LeaseTTL,
	}
	s.routes()
	return s
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/apps", s.listApps)
	mux.HandleFunc("POST /v1/apps", s.createApp)
	mux.HandleFunc("GET /v1/apps/{app}", s.getApp)
	mux.HandleFunc("DELETE /v1/apps/{app}", s.deleteApp)

	mux.HandleFunc("POST /v1/apps/{app}/machines", s.createMachine)
	mux.HandleFunc("GET /v1/apps/{app}/machines", s.listMachines)
	mux.HandleFunc("GET /v1/apps/{app}/machines/{id}", s.getMachine)
	mux.HandleFunc("POST /v1/apps/{app}/machines/{id}", s.updateMachine)
	mux.HandleFunc("DELETE /v1/apps/{app}/machines/{id}", s.deleteMachine)
	mux.HandleFunc("POST /v1/apps/{app}/machines/{id}/start", s.startMachine)
	mux.HandleFunc("POST /v1/apps/{app}/machines/{id}/stop", s.stopMachine)
	mux.HandleFunc("POST /v1/apps/{app}/machines/{id}/restart", s.restartMachine)
	mux.HandleFunc("POST /v1/apps/{app}/machines/{id}/suspend", s.suspendMachine)
	mux.HandleFunc("POST /v1/apps/{app}/machines/{id}/cordon", s.cordonMachine)
	mux.HandleFunc("POST /v1/apps/{app}/machines/{id}/uncordon", s.uncordonMachine)
	mux.HandleFunc("GET /v1/apps/{app}/machines/{id}/wait", s.waitMachine)

	mux.HandleFunc("GET /v1/apps/{app}/machines/{id}/metadata", s.getMetadata)
	mux.HandleFunc("POST /v1/apps/{app}/machines/{id}/metadata/{key}", s.setMetadata)
	mux.HandleFunc("DELETE /v1/apps/{app}/machines/{id}/metadata/{key}", s.deleteMetadata)

	mux.HandleFunc("GET /v1/apps/{app}/machines/{id}/lease", s.getLease)
	mux.HandleFunc("POST /v1/apps/{app}/machines/{id}/lease", s.acquireLease)
	mux.HandleFunc("DELETE /v1/apps/{app}/machines/{id}/lease", s.releaseLease)

	mux.HandleFunc("GET /_mudflaps/health", s.health)

	// Endpoints on the roadmap answer honestly with 501 so a client can tell
	// "not built yet" from "wrong URL".
	for _, p := range unimplementedPaths {
		mux.HandleFunc(p, s.notImplemented)
	}

	s.mux = mux
}

// ---- apps ----

func (s *Server) listApps(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, flaps.ListAppsResponse{Apps: s.store.ListApps()})
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	var req flaps.CreateAppRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AppName == "" {
		s.writeError(w, http.StatusBadRequest, "app_name is required")
		return
	}
	app, err := s.store.CreateApp(flaps.App{Name: req.AppName, Organization: req.OrgSlug})
	if errors.Is(err, store.ErrAppExists) {
		s.writeError(w, http.StatusConflict, "app already exists")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) getApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GetApp(r.PathValue("app"))
	if errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("app")
	// Clear any leases held on this app's machines so they don't leak in the
	// lease manager after the machines are dropped.
	if machines, err := s.store.ListMachines(app); err == nil {
		for _, m := range machines {
			s.leases.Clear(leaseKey(app, m.ID))
		}
	}
	err := s.store.DeleteApp(app)
	if errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ---- machines ----

func (s *Server) createMachine(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("app")
	if _, err := s.store.GetApp(app); errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	var req flaps.CreateMachineRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	now := s.clk.Now().UTC().Format(time.RFC3339Nano)
	instance := id.Instance()
	// A skip_launch machine is created but not booted, so it rests directly in
	// `created` rather than the transient `creating` state.
	initialState := flaps.StateCreating
	if req.SkipLaunch {
		initialState = flaps.StateCreated
	}
	m := flaps.Machine{
		ID:         id.Machine(),
		Name:       req.Name,
		State:      initialState,
		Region:     defaultString(req.Region, "local"),
		InstanceID: instance,
		PrivateIP:  "fdaa:0:0:a7b:0:1::",
		Config:     req.Config,
		CreatedAt:  now,
		UpdatedAt:  now,
		Versions:   []flaps.MachineVersion{{InstanceID: instance, State: initialState}},
	}
	if m.Name == "" {
		m.Name = "machine-" + m.ID
	}
	created, err := s.store.CreateMachine(app, m)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !req.SkipLaunch {
		s.advancer.Create(app, created.ID)
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listMachines(w http.ResponseWriter, r *http.Request) {
	machines, err := s.store.ListMachines(r.PathValue("app"))
	if errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	writeJSON(w, http.StatusOK, machines)
}

func (s *Server) getMachine(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMachine(r.PathValue("app"), r.PathValue("id"))
	if s.handleLookupError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// updateMachine applies a new config and churns the instance_id (version churn).
func (s *Server) updateMachine(w http.ResponseWriter, r *http.Request) {
	app, mID := r.PathValue("app"), r.PathValue("id")
	if s.rejectIfLeased(w, r, app, mID) {
		return
	}
	if s.rejectIfTerminal(w, app, mID) {
		return
	}
	var req flaps.CreateMachineRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Version churn is synchronous, matching flaps: minting the new version and
	// marking the previous one replaced is part of the update response, not a
	// later async step. Only the boot (replacing -> started) is deferred to the
	// advancer, so a client can immediately
	// GET .../wait?instance_id=<new>&state=started.
	newInstance := id.Instance()
	updated, err := s.store.UpdateMachine(app, mID, func(m *flaps.Machine) error {
		if req.Config != nil {
			m.Config = req.Config
		}
		if req.Name != "" {
			m.Name = req.Name
		}
		if n := len(m.Versions); n > 0 {
			m.Versions[n-1].State = flaps.StateReplaced
		}
		m.InstanceID = newInstance
		m.State = flaps.StateReplacing
		m.Versions = append(m.Versions, flaps.MachineVersion{
			InstanceID: newInstance,
			State:      flaps.StateReplacing,
		})
		// Set UpdatedAt in the same mutation so the response reflects it (not a
		// later touch that the returned snapshot would miss).
		m.UpdatedAt = s.clk.Now().UTC().Format(time.RFC3339Nano)
		return nil
	})
	if s.handleLookupError(w, err) {
		return
	}
	s.advancer.Update(app, mID)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteMachine(w http.ResponseWriter, r *http.Request) {
	app, mID := r.PathValue("app"), r.PathValue("id")
	if s.rejectIfLeased(w, r, app, mID) {
		return
	}
	_, err := s.store.UpdateMachine(app, mID, func(m *flaps.Machine) error {
		m.State = flaps.StateDestroying
		return nil
	})
	if s.handleLookupError(w, err) {
		return
	}
	s.leases.Clear(leaseKey(app, mID))
	s.touch(app, mID)
	s.advancer.Destroy(app, mID)
	writeJSON(w, http.StatusOK, flaps.WaitResponse{OK: true})
}

func (s *Server) startMachine(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, flaps.StateStarting, s.advancer.Start)
}

func (s *Server) stopMachine(w http.ResponseWriter, r *http.Request) {
	// Accept the optional StopMachineInput (signal/timeout). mudflaps doesn't
	// model real signals, but honoring the documented body shape means a client
	// that sends one isn't rejected.
	var in flaps.StopMachineInput
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	s.transition(w, r, flaps.StateStopping, s.advancer.Stop)
}

func (s *Server) restartMachine(w http.ResponseWriter, r *http.Request) {
	// force_stop is accepted (mudflaps settles instantly regardless).
	_ = r.URL.Query().Get("force_stop")
	s.transition(w, r, flaps.StateRestarting, s.advancer.Restart)
}

// suspendMachine moves a machine suspending -> suspended. Resume is a normal
// start.
func (s *Server) suspendMachine(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, flaps.StateSuspending, s.advancer.Suspend)
}

// cordonMachine / uncordonMachine toggle the machine's cordon flag (excluded
// from proxy routing in real Fly; internal bookkeeping here). Lease-gated.
func (s *Server) cordonMachine(w http.ResponseWriter, r *http.Request) {
	s.setCordon(w, r, true)
}

func (s *Server) uncordonMachine(w http.ResponseWriter, r *http.Request) {
	s.setCordon(w, r, false)
}

func (s *Server) setCordon(w http.ResponseWriter, r *http.Request, cordoned bool) {
	app, mID := r.PathValue("app"), r.PathValue("id")
	if s.rejectIfLeased(w, r, app, mID) {
		return
	}
	if s.rejectIfTerminal(w, app, mID) {
		return
	}
	_, err := s.store.UpdateMachine(app, mID, func(m *flaps.Machine) error {
		m.Cordoned = cordoned
		return nil
	})
	if s.handleLookupError(w, err) {
		return
	}
	s.touch(app, mID)
	writeJSON(w, http.StatusOK, flaps.WaitResponse{OK: true})
}

// transition sets a transient state then schedules the advance to rest.
func (s *Server) transition(w http.ResponseWriter, r *http.Request, transient flaps.MachineState, advance func(app, id string)) {
	app, mID := r.PathValue("app"), r.PathValue("id")
	if s.rejectIfLeased(w, r, app, mID) {
		return
	}
	if s.rejectIfTerminal(w, app, mID) {
		return
	}
	_, err := s.store.UpdateMachine(app, mID, func(m *flaps.Machine) error {
		m.State = transient
		return nil
	})
	if s.handleLookupError(w, err) {
		return
	}
	s.touch(app, mID)
	advance(app, mID)
	writeJSON(w, http.StatusOK, flaps.WaitResponse{OK: true})
}

// waitMachine blocks until the machine reaches the requested state or the
// (clamped) timeout elapses, in which case it returns 408.
func (s *Server) waitMachine(w http.ResponseWriter, r *http.Request) {
	app, mID := r.PathValue("app"), r.PathValue("id")
	// fly-go sends `state` repeated (any match satisfies the wait) and the
	// version filter as `version`; accept `instance_id` too for spec
	// completeness.
	states := r.URL.Query()["state"]
	if len(states) == 0 {
		states = []string{string(flaps.StateStarted)}
	}
	wantVersion := r.URL.Query().Get("version")
	if wantVersion == "" {
		wantVersion = r.URL.Query().Get("instance_id")
	}
	timeout := clampTimeout(r.URL.Query().Get("timeout"))

	// The deadline is measured on the injected clock, so production uses a real
	// wall-clock timeout while tests drive it deterministically by advancing the
	// fake clock. The poll interval below stays real time — it only paces how
	// often state is re-checked.
	deadline := s.clk.Now().Add(timeout)
	for {
		m, err := s.store.GetMachine(app, mID)
		switch {
		case errors.Is(err, store.ErrMachineNotFound):
			// A destroyed machine that has been reaped counts as destroyed.
			if slices.Contains(states, string(flaps.StateDestroyed)) {
				writeJSON(w, http.StatusOK, flaps.WaitResponse{OK: true})
				return
			}
			s.writeError(w, http.StatusNotFound, "machine not found")
			return
		case errors.Is(err, store.ErrAppNotFound):
			s.writeError(w, http.StatusNotFound, "app not found")
			return
		}
		if slices.Contains(states, string(m.State)) && (wantVersion == "" || wantVersion == m.InstanceID) {
			writeJSON(w, http.StatusOK, flaps.WaitResponse{OK: true})
			return
		}
		if s.clk.Now().After(deadline) {
			s.writeError(w, http.StatusRequestTimeout, "timeout waiting for machine to reach "+strings.Join(states, "|"))
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(waitPollInterval):
		}
	}
}

// ---- metadata ----
//
// Metadata is the ownership-marker surface (e.g. managed-by: chant). Matching
// flaps/fly-go, these endpoints are not lease-gated: fly-go sends no nonce.

func (s *Server) getMetadata(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMachine(r.PathValue("app"), r.PathValue("id"))
	if s.handleLookupError(w, err) {
		return
	}
	md := map[string]string{}
	if m.Config != nil && m.Config.Metadata != nil {
		md = m.Config.Metadata
	}
	writeJSON(w, http.StatusOK, md)
}

func (s *Server) setMetadata(w http.ResponseWriter, r *http.Request) {
	app, mID, key := r.PathValue("app"), r.PathValue("id"), r.PathValue("key")
	var req struct {
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	_, err := s.store.UpdateMachine(app, mID, func(m *flaps.Machine) error {
		if m.Config == nil {
			m.Config = &flaps.MachineConfig{}
		}
		if m.Config.Metadata == nil {
			m.Config.Metadata = map[string]string{}
		}
		m.Config.Metadata[key] = req.Value
		return nil
	})
	if s.handleLookupError(w, err) {
		return
	}
	s.touch(app, mID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteMetadata(w http.ResponseWriter, r *http.Request) {
	app, mID, key := r.PathValue("app"), r.PathValue("id"), r.PathValue("key")
	_, err := s.store.UpdateMachine(app, mID, func(m *flaps.Machine) error {
		if m.Config != nil && m.Config.Metadata != nil {
			delete(m.Config.Metadata, key)
		}
		return nil
	})
	if s.handleLookupError(w, err) {
		return
	}
	s.touch(app, mID)
	w.WriteHeader(http.StatusNoContent)
}

// ---- leases ----

func (s *Server) getLease(w http.ResponseWriter, r *http.Request) {
	app, mID := r.PathValue("app"), r.PathValue("id")
	if _, err := s.store.GetMachine(app, mID); s.handleLookupError(w, err) {
		return
	}
	l, err := s.leases.Get(leaseKey(app, mID))
	if errors.Is(err, lease.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "no active lease")
		return
	}
	writeJSON(w, http.StatusOK, leaseEnvelope(l))
}

func (s *Server) acquireLease(w http.ResponseWriter, r *http.Request) {
	app, mID := r.PathValue("app"), r.PathValue("id")
	if _, err := s.store.GetMachine(app, mID); s.handleLookupError(w, err) {
		return
	}
	var req flaps.AcquireLeaseRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	ttl := s.leaseTTL
	if req.TTL > 0 {
		ttl = time.Duration(req.TTL) * time.Second
	}
	key := leaseKey(app, mID)

	// A request that carries a nonce is a refresh of an existing lease.
	if nonce := r.Header.Get(LeaseNonceHeader); nonce != "" {
		l, err := s.leases.Refresh(key, nonce, ttl)
		if err != nil {
			s.writeError(w, http.StatusConflict, "lease refresh rejected: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, leaseEnvelope(l))
		return
	}

	l, err := s.leases.Acquire(key, "mudflaps", req.Description, ttl)
	if errors.Is(err, lease.ErrConflict) {
		// l is the lease currently held by someone else. Flaps reports the
		// conflict as a MachineLease envelope (status/code/message) so a client
		// can see the holder and expiry — but never the nonce, which is the
		// holder's secret.
		writeJSON(w, http.StatusConflict, flaps.MachineLease{
			Status:  "error",
			Code:    "lease_currently_held",
			Message: "machine lease currently held",
			Data: &flaps.MachineLeaseData{
				Owner:     l.Owner,
				ExpiresAt: l.ExpiresAt.Unix(),
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, leaseEnvelope(l))
}

func (s *Server) releaseLease(w http.ResponseWriter, r *http.Request) {
	app, mID := r.PathValue("app"), r.PathValue("id")
	if _, err := s.store.GetMachine(app, mID); s.handleLookupError(w, err) {
		return
	}
	nonce := r.Header.Get(LeaseNonceHeader)
	err := s.leases.Release(leaseKey(app, mID), nonce)
	switch {
	case errors.Is(err, lease.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "no active lease")
		return
	case errors.Is(err, lease.ErrNonceMismatch):
		s.writeError(w, http.StatusConflict, "lease nonce mismatch")
		return
	}
	writeJSON(w, http.StatusOK, flaps.MachineLease{Status: "released"})
}

// ---- meta ----

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"version":       s.version,
		"implemented":   implementedPaths,
		"unimplemented": unimplementedPaths,
	})
}

func (s *Server) notImplemented(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, http.StatusNotImplemented, r.URL.Path+" is on the mudflaps roadmap but not implemented yet")
}

// ---- helpers ----

// rejectIfLeased returns true (and has written a 409) when a mutating request
// lacks the nonce for an actively held lease.
func (s *Server) rejectIfLeased(w http.ResponseWriter, r *http.Request, app, mID string) bool {
	if err := s.leases.Check(leaseKey(app, mID), r.Header.Get(LeaseNonceHeader)); errors.Is(err, lease.ErrConflict) {
		s.writeError(w, http.StatusConflict, "machine is leased; supply the "+LeaseNonceHeader+" header")
		return true
	}
	return false
}

// rejectIfTerminal returns true (and has written a 400) when the machine is
// being destroyed or has been destroyed, so a mutating op cannot resurrect it.
// A missing machine is left to the caller's own lookup (which returns 404).
func (s *Server) rejectIfTerminal(w http.ResponseWriter, app, mID string) bool {
	m, err := s.store.GetMachine(app, mID)
	if err != nil {
		return false
	}
	if m.State == flaps.StateDestroying || m.State == flaps.StateDestroyed {
		s.writeError(w, http.StatusBadRequest, "machine is "+string(m.State)+" and cannot be modified")
		return true
	}
	return false
}

// touch updates a machine's UpdatedAt to the current clock time.
func (s *Server) touch(app, mID string) {
	_, _ = s.store.UpdateMachine(app, mID, func(m *flaps.Machine) error {
		m.UpdatedAt = s.clk.Now().UTC().Format(time.RFC3339Nano)
		return nil
	})
}

// handleLookupError writes a 404 for app/machine not-found errors and reports
// whether it wrote a response.
func (s *Server) handleLookupError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrAppNotFound):
		s.writeError(w, http.StatusNotFound, "app not found")
	case errors.Is(err, store.ErrMachineNotFound):
		s.writeError(w, http.StatusNotFound, "machine not found")
	default:
		s.writeError(w, http.StatusInternalServerError, err.Error())
	}
	return true
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, flaps.ErrorResponse{Error: msg, Status: status})
}

func leaseKey(app, machineID string) string { return app + "/" + machineID }

func leaseEnvelope(l *lease.Lease) flaps.MachineLease {
	return flaps.MachineLease{
		Status: "success",
		Data: &flaps.MachineLeaseData{
			Nonce:       l.Nonce,
			ExpiresAt:   l.ExpiresAt.Unix(),
			Owner:       l.Owner,
			Description: l.Description,
		},
	}
}

func clampTimeout(raw string) time.Duration {
	const minTimeout, maxTimeout = time.Second, 60 * time.Second
	if raw == "" {
		return maxTimeout
	}
	// The flaps timeout parameter is an integer number of seconds. Invalid or
	// non-positive values clamp to the 1s floor, not the 60s ceiling.
	secs, err := strconv.Atoi(raw)
	if err != nil {
		return minTimeout
	}
	d := time.Duration(secs) * time.Second
	switch {
	case d < minTimeout: // includes 0 and negative
		return minTimeout
	case d > maxTimeout:
		return maxTimeout
	default:
		return d
	}
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	err := json.NewDecoder(r.Body).Decode(dst)
	if err == nil || errors.Is(err, io.EOF) {
		// An empty body decodes to the zero value, which is a valid request for
		// the endpoints that accept optional bodies.
		return true
	}
	writeJSON(w, http.StatusBadRequest, flaps.ErrorResponse{
		Error:  "invalid JSON body: " + err.Error(),
		Status: http.StatusBadRequest,
	})
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
