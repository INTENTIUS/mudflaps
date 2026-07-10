package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/intentius/mudflaps/internal/clock"
	"github.com/intentius/mudflaps/internal/flaps"
)

type harness struct {
	t   *testing.T
	ts  *httptest.Server
	clk *clock.Fake
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clk := clock.NewFake(time.Time{})
	s := New(Options{Version: "test", Clock: clk})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &harness{t: t, ts: ts, clk: clk}
}

// do performs a request and returns status and raw body.
func (h *harness) do(method, path string, body any, headers map[string]string) (int, []byte) {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.ts.URL+path, reader)
	if err != nil {
		h.t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.ts.Client().Do(req)
	if err != nil {
		h.t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (h *harness) mustJSON(raw []byte, dst any) {
	h.t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		h.t.Fatalf("unmarshal %s: %v", raw, err)
	}
}

// createStartedMachine creates an app+machine and drives it to started.
func (h *harness) createStartedMachine(app string) flaps.Machine {
	h.t.Helper()
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: app}, nil); code != http.StatusCreated {
		h.t.Fatalf("create app: %d %s", code, body)
	}
	code, body := h.do(http.MethodPost, "/v1/apps/"+app+"/machines", flaps.CreateMachineRequest{
		Config: &flaps.MachineConfig{Image: "nginx"},
	}, nil)
	if code != http.StatusCreated {
		h.t.Fatalf("create machine: %d %s", code, body)
	}
	var m flaps.Machine
	h.mustJSON(body, &m)
	if m.State != flaps.StateCreating {
		h.t.Fatalf("new machine state = %q, want creating", m.State)
	}
	h.clk.Advance(time.Hour) // fire creating -> starting -> started
	return m
}

func TestHealth(t *testing.T) {
	h := newHarness(t)
	code, body := h.do(http.MethodGet, "/_mudflaps/health", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("health status = %d", code)
	}
	var payload struct {
		Status        string   `json:"status"`
		Version       string   `json:"version"`
		Implemented   []string `json:"implemented"`
		Unimplemented []string `json:"unimplemented"`
	}
	h.mustJSON(body, &payload)
	if payload.Status != "ok" || payload.Version != "test" {
		t.Fatalf("unexpected health payload: %+v", payload)
	}
	if len(payload.Implemented) == 0 || len(payload.Unimplemented) == 0 {
		t.Fatalf("expected non-empty coverage lists: %+v", payload)
	}
}

func TestCreateAndWaitStarted(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	code, body := h.do(http.MethodGet,
		"/v1/apps/demo/machines/"+m.ID+"/wait?state=started&timeout=5", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("wait status = %d %s", code, body)
	}
	var wr flaps.WaitResponse
	h.mustJSON(body, &wr)
	if !wr.OK {
		t.Fatalf("wait ok = false")
	}

	// The machine should report started.
	code, body = h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("get machine = %d %s", code, body)
	}
	var got flaps.Machine
	h.mustJSON(body, &got)
	if got.State != flaps.StateStarted {
		t.Fatalf("machine state = %q, want started", got.State)
	}
}

func TestLeaseBlocksMutationWithoutNonce(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	// Acquire a lease.
	code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/lease",
		flaps.AcquireLeaseRequest{TTL: 30}, nil)
	if code != http.StatusOK {
		t.Fatalf("acquire lease = %d %s", code, body)
	}
	var envelope flaps.MachineLease
	h.mustJSON(body, &envelope)
	if envelope.Data == nil || envelope.Data.Nonce == "" {
		t.Fatalf("lease response missing nonce: %s", body)
	}
	nonce := envelope.Data.Nonce

	// A mutation without the nonce is rejected 409.
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/stop", nil, nil); code != http.StatusConflict {
		t.Fatalf("stop without nonce = %d %s, want 409", code, body)
	}

	// The same mutation with the nonce succeeds.
	code, body = h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/stop", nil,
		map[string]string{LeaseNonceHeader: nonce})
	if code != http.StatusOK {
		t.Fatalf("stop with nonce = %d %s", code, body)
	}
	// Advance only enough to settle the stop transition; keep the 30s lease alive.
	h.clk.Advance(time.Second)
	code, body = h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID+"/wait?state=stopped&timeout=5", nil,
		map[string]string{LeaseNonceHeader: nonce})
	if code != http.StatusOK {
		t.Fatalf("wait stopped = %d %s", code, body)
	}

	// Release the lease and confirm mutation is unguarded again.
	if code, body := h.do(http.MethodDelete, "/v1/apps/demo/machines/"+m.ID+"/lease", nil,
		map[string]string{LeaseNonceHeader: nonce}); code != http.StatusOK {
		t.Fatalf("release lease = %d %s", code, body)
	}
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/start", nil, nil); code != http.StatusOK {
		t.Fatalf("start after release = %d %s", code, body)
	}
}

func TestUpdateChurnsInstanceIDOverHTTP(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID,
		flaps.CreateMachineRequest{Config: &flaps.MachineConfig{Image: "nginx:2"}}, nil)
	if code != http.StatusOK {
		t.Fatalf("update = %d %s", code, body)
	}
	h.clk.Advance(time.Hour)

	code, body = h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("get after update = %d %s", code, body)
	}
	var got flaps.Machine
	h.mustJSON(body, &got)
	if got.State != flaps.StateStarted {
		t.Fatalf("state after update = %q, want started", got.State)
	}
	if got.InstanceID == m.InstanceID {
		t.Fatalf("instance_id did not churn from %q", m.InstanceID)
	}
	if got.Config == nil || got.Config.Image != "nginx:2" {
		t.Fatalf("config not updated: %+v", got.Config)
	}
}

// TestUpdateReturnsNewInstanceIDSynchronously asserts the fidelity guarantee:
// the update response itself carries the new version (instance_id + replaced
// prior version), and a wait keyed to that new instance_id succeeds once it
// boots — the client never has to discover the new id by polling.
func TestUpdateReturnsNewInstanceIDSynchronously(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID,
		flaps.CreateMachineRequest{Config: &flaps.MachineConfig{Image: "nginx:2"}}, nil)
	if code != http.StatusOK {
		t.Fatalf("update = %d %s", code, body)
	}
	var resp flaps.Machine
	h.mustJSON(body, &resp)

	// The new instance_id and replacing state are in the RESPONSE, before any
	// async boot. (Version history is internal — flaps doesn't expose it on the
	// machine object — so it's asserted at the store level in the machine tests.)
	if resp.InstanceID == m.InstanceID {
		t.Fatalf("update response instance_id did not churn from %q", m.InstanceID)
	}
	if resp.State != flaps.StateReplacing {
		t.Fatalf("update response state = %q, want replacing", resp.State)
	}

	// The new version boots and a wait keyed to the new instance_id succeeds —
	// the client never had to poll to learn the new id.
	h.clk.Advance(time.Hour)
	code, body = h.do(http.MethodGet,
		"/v1/apps/demo/machines/"+m.ID+"/wait?state=started&instance_id="+resp.InstanceID+"&timeout=5", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("wait new instance = %d %s", code, body)
	}
}

func TestDestroyAndWaitDestroyed(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	if code, body := h.do(http.MethodDelete, "/v1/apps/demo/machines/"+m.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("destroy = %d %s", code, body)
	}
	h.clk.Advance(time.Hour)

	code, body := h.do(http.MethodGet,
		"/v1/apps/demo/machines/"+m.ID+"/wait?state=destroyed&timeout=5", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("wait destroyed = %d %s", code, body)
	}
	var wr flaps.WaitResponse
	h.mustJSON(body, &wr)
	if !wr.OK {
		t.Fatalf("wait destroyed ok = false")
	}
}

func TestUnimplementedReturns501(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do(http.MethodGet, "/v1/apps/demo/volumes", nil, nil); code != http.StatusNotImplemented {
		t.Fatalf("volumes = %d %s, want 501", code, body)
	}
}

func TestNotFoundPaths(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.do(http.MethodGet, "/v1/apps/nope", nil, nil); code != http.StatusNotFound {
		t.Fatalf("missing app = %d, want 404", code)
	}
	if code, _ := h.do(http.MethodGet, "/v1/apps/nope/machines/x", nil, nil); code != http.StatusNotFound {
		t.Fatalf("missing machine app = %d, want 404", code)
	}
}
