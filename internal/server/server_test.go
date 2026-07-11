package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if code != http.StatusOK {
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

// TestAcquireLeaseConflictReturnsHeldLease asserts that a second acquire on a
// held lease returns a MachineLease envelope (status/code/message + holder), not
// a plain error — and that the holder's nonce is never leaked.
func TestAcquireLeaseConflictReturnsHeldLease(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	// First acquire succeeds and yields a nonce.
	code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/lease",
		flaps.AcquireLeaseRequest{TTL: 30}, nil)
	if code != http.StatusOK {
		t.Fatalf("first acquire = %d %s", code, body)
	}
	var held flaps.MachineLease
	h.mustJSON(body, &held)
	if held.Data == nil || held.Data.Nonce == "" {
		t.Fatalf("first acquire missing nonce: %s", body)
	}
	heldNonce := held.Data.Nonce

	// Second acquire (no nonce) conflicts and returns the envelope.
	code, body = h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/lease",
		flaps.AcquireLeaseRequest{}, nil)
	if code != http.StatusConflict {
		t.Fatalf("second acquire = %d %s, want 409", code, body)
	}
	var conflict flaps.MachineLease
	h.mustJSON(body, &conflict)
	if conflict.Status != "error" || conflict.Code != "lease_currently_held" {
		t.Fatalf("conflict envelope = %+v, want status=error code=lease_currently_held", conflict)
	}
	if conflict.Message == "" {
		t.Fatalf("conflict envelope missing message: %s", body)
	}
	if conflict.Data == nil || conflict.Data.Owner == "" || conflict.Data.ExpiresAt == 0 {
		t.Fatalf("conflict envelope missing holder data: %s", body)
	}
	// The holder's nonce must never appear in a conflict body.
	if conflict.Data.Nonce != "" {
		t.Fatalf("conflict body leaked the holder nonce %q", conflict.Data.Nonce)
	}
	if conflict.Data.Nonce == heldNonce && heldNonce != "" {
		t.Fatalf("conflict body exposed the real nonce")
	}
}

// TestSuspendAndResume covers the suspend lifecycle: started -> suspending ->
// suspended, then resume via start -> started. It also checks the mutation is
// lease-gated.
func TestSuspendAndResume(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/suspend", nil, nil); code != http.StatusOK {
		t.Fatalf("suspend = %d %s", code, body)
	}
	h.clk.Advance(time.Hour)
	if code, body := h.do(http.MethodGet,
		"/v1/apps/demo/machines/"+m.ID+"/wait?state=suspended&timeout=5", nil, nil); code != http.StatusOK {
		t.Fatalf("wait suspended = %d %s", code, body)
	}

	// Resume with a normal start.
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/start", nil, nil); code != http.StatusOK {
		t.Fatalf("start (resume) = %d %s", code, body)
	}
	h.clk.Advance(time.Hour)
	code, body := h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("get after resume = %d %s", code, body)
	}
	var got flaps.Machine
	h.mustJSON(body, &got)
	if got.State != flaps.StateStarted {
		t.Fatalf("state after resume = %q, want started", got.State)
	}
}

// TestSuspendIsLeaseGated confirms suspend obeys an active lease.
func TestSuspendIsLeaseGated(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/lease",
		flaps.AcquireLeaseRequest{TTL: 30}, nil)
	if code != http.StatusOK {
		t.Fatalf("acquire lease = %d %s", code, body)
	}
	var env flaps.MachineLease
	h.mustJSON(body, &env)
	nonce := env.Data.Nonce

	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/suspend", nil, nil); code != http.StatusConflict {
		t.Fatalf("suspend without nonce = %d %s, want 409", code, body)
	}
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/suspend", nil,
		map[string]string{LeaseNonceHeader: nonce}); code != http.StatusOK {
		t.Fatalf("suspend with nonce = %d %s", code, body)
	}
}

// TestMachineMetadataCRUD covers set/get/delete on machine metadata — the
// ownership-marker surface — and that changes are reflected on the machine.
func TestMachineMetadataCRUD(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	// Set managed-by: chant.
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/metadata/managed-by",
		map[string]string{"value": "chant"}, nil); code != http.StatusNoContent {
		t.Fatalf("set metadata = %d %s, want 204", code, body)
	}

	// Get returns the map.
	code, body := h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID+"/metadata", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("get metadata = %d %s", code, body)
	}
	var md map[string]string
	h.mustJSON(body, &md)
	if md["managed-by"] != "chant" {
		t.Fatalf("metadata = %+v, want managed-by=chant", md)
	}

	// The key is also visible on the machine object.
	code, body = h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("get machine = %d %s", code, body)
	}
	var got flaps.Machine
	h.mustJSON(body, &got)
	if got.Config == nil || got.Config.Metadata["managed-by"] != "chant" {
		t.Fatalf("machine metadata not reflected: %+v", got.Config)
	}

	// Delete removes it.
	if code, body := h.do(http.MethodDelete, "/v1/apps/demo/machines/"+m.ID+"/metadata/managed-by", nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete metadata = %d %s, want 204", code, body)
	}
	code, body = h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID+"/metadata", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("get metadata after delete = %d %s", code, body)
	}
	// Use a fresh map: unmarshaling into a populated map would not clear keys.
	var after map[string]string
	h.mustJSON(body, &after)
	if _, ok := after["managed-by"]; ok {
		t.Fatalf("metadata key not deleted: %+v", after)
	}
}

// TestCordonUncordon covers the cordon endpoints and their lease gating.
func TestCordonUncordon(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/cordon", nil, nil); code != http.StatusOK {
		t.Fatalf("cordon = %d %s", code, body)
	}
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/uncordon", nil, nil); code != http.StatusOK {
		t.Fatalf("uncordon = %d %s", code, body)
	}

	// Cordon is lease-gated.
	code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/lease",
		flaps.AcquireLeaseRequest{TTL: 30}, nil)
	if code != http.StatusOK {
		t.Fatalf("acquire lease = %d %s", code, body)
	}
	var env flaps.MachineLease
	h.mustJSON(body, &env)
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/cordon", nil, nil); code != http.StatusConflict {
		t.Fatalf("cordon without nonce = %d %s, want 409", code, body)
	}
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/cordon", nil,
		map[string]string{LeaseNonceHeader: env.Data.Nonce}); code != http.StatusOK {
		t.Fatalf("cordon with nonce = %d %s", code, body)
	}
}

// TestStopAndRestartAcceptInputs confirms stop honors a StopMachineInput body
// and restart honors ?force_stop= without error.
func TestStopAndRestartAcceptInputs(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/stop",
		flaps.StopMachineInput{Signal: "SIGTERM", Timeout: "30s"}, nil); code != http.StatusOK {
		t.Fatalf("stop with input = %d %s", code, body)
	}
	h.clk.Advance(time.Hour)
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/restart?force_stop=true", nil, nil); code != http.StatusOK {
		t.Fatalf("restart force_stop = %d %s", code, body)
	}
}

// TestStopAcceptsDurationStringTimeout is the regression for audit H1: fly-go
// sends `timeout` as a duration string ("0s"), and stop must accept it, not 400.
func TestStopAcceptsDurationStringTimeout(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")
	// Raw JSON matching fly-go's exact StopMachineInput wire shape.
	for _, body := range []string{`{"id":"x","timeout":"0s"}`, `{"timeout":"10s","signal":"SIGTERM"}`} {
		req, _ := http.NewRequest(http.MethodPost, h.ts.URL+"/v1/apps/demo/machines/"+m.ID+"/stop", strings.NewReader(body))
		resp, err := h.ts.Client().Do(req)
		if err != nil {
			t.Fatalf("stop request: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stop %s -> %d, want 200", body, resp.StatusCode)
		}
	}
}

// TestSkipLaunchRestsInCreated is the regression for audit M1.
func TestSkipLaunchRestsInCreated(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo"}, nil); code != http.StatusCreated {
		t.Fatalf("create app = %d %s", code, body)
	}
	code, body := h.do(http.MethodPost, "/v1/apps/demo/machines",
		flaps.CreateMachineRequest{Config: &flaps.MachineConfig{Image: "nginx"}, SkipLaunch: true}, nil)
	if code != http.StatusOK {
		t.Fatalf("create = %d %s", code, body)
	}
	var m flaps.Machine
	h.mustJSON(body, &m)
	if m.State != flaps.StateCreated {
		t.Fatalf("skip_launch state = %q, want created", m.State)
	}
	// Give the clock a shove; a skip_launch machine must not drift to started.
	h.clk.Advance(time.Hour)
	_, body = h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil)
	h.mustJSON(body, &m)
	if m.State != flaps.StateCreated {
		t.Fatalf("skip_launch state after advance = %q, want created", m.State)
	}
}

// TestDestroyReapsAndBlocksResurrection is the regression for audit M2.
func TestDestroyReapsAndBlocksResurrection(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	if code, body := h.do(http.MethodDelete, "/v1/apps/demo/machines/"+m.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("destroy = %d %s", code, body)
	}
	// While destroying (before reap), a mutating op is rejected, not obeyed.
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/start", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("start while destroying = %d %s, want 400", code, body)
	}
	// After the destroy settles, the machine is reaped (gone).
	h.clk.Advance(time.Hour)
	if code, body := h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("get after reap = %d %s, want 404", code, body)
	}
	// wait?state=destroyed is still satisfied by the machine being gone.
	if code, body := h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID+"/wait?state=destroyed&timeout=5", nil, nil); code != http.StatusOK {
		t.Fatalf("wait destroyed after reap = %d %s", code, body)
	}
}

// TestWaitHonorsVersionFilter is the regression for audit M3 (version filter):
// fly-go scopes a wait with `version`, which must match the machine's current
// instance_id; a stale version must not match.
func TestWaitHonorsVersionFilter(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	// A wait keyed to the current version succeeds immediately.
	if code, body := h.do(http.MethodGet,
		"/v1/apps/demo/machines/"+m.ID+"/wait?state=started&version="+m.InstanceID+"&timeout=5", nil, nil); code != http.StatusOK {
		t.Fatalf("wait current version = %d %s", code, body)
	}
	// A wait keyed to a stale version times out (never matches the current one).
	done := make(chan int, 1)
	go func() {
		c, _ := h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID+"/wait?state=started&version=STALE&timeout=1", nil, nil)
		done <- c
	}()
	safety := time.After(3 * time.Second)
	for {
		select {
		case c := <-done:
			if c != http.StatusRequestTimeout {
				t.Fatalf("stale-version wait = %d, want 408", c)
			}
			return
		case <-safety:
			t.Fatal("stale-version wait did not time out")
		default:
			h.clk.Advance(500 * time.Millisecond)
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// TestWaitHonorsRepeatableState is the regression for audit M3 (repeatable
// state, folding in #23): any of several requested states satisfies the wait.
func TestWaitHonorsRepeatableState(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	// Machine is started; a wait for stopped OR started matches immediately.
	if code, body := h.do(http.MethodGet,
		"/v1/apps/demo/machines/"+m.ID+"/wait?state=stopped&state=started&timeout=5", nil, nil); code != http.StatusOK {
		t.Fatalf("multi-state wait (started present) = %d %s", code, body)
	}
	// Stop it, then a wait for stopped OR suspended matches once stopped.
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/stop", nil, nil); code != http.StatusOK {
		t.Fatalf("stop = %d %s", code, body)
	}
	h.clk.Advance(time.Hour)
	if code, body := h.do(http.MethodGet,
		"/v1/apps/demo/machines/"+m.ID+"/wait?state=stopped&state=suspended&timeout=5", nil, nil); code != http.StatusOK {
		t.Fatalf("multi-state wait (stopped) = %d %s", code, body)
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

// TestWaitTimesOut drives the injected clock past the wait deadline and asserts
// a 408. The wait runs on the httptest server's goroutine while the test keeps
// advancing the fake clock until the handler returns — no race and no
// multi-second real sleep.
func TestWaitTimesOut(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo"}, nil); code != http.StatusCreated {
		t.Fatalf("create app = %d %s", code, body)
	}
	// skip_launch leaves the machine in 'created', so it never reaches 'started'.
	code, body := h.do(http.MethodPost, "/v1/apps/demo/machines",
		flaps.CreateMachineRequest{Config: &flaps.MachineConfig{Image: "nginx"}, SkipLaunch: true}, nil)
	if code != http.StatusOK {
		t.Fatalf("create machine = %d %s", code, body)
	}
	var m flaps.Machine
	h.mustJSON(body, &m)

	done := make(chan int, 1)
	go func() {
		c, _ := h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID+"/wait?state=started&timeout=1", nil, nil)
		done <- c
	}()

	safety := time.After(3 * time.Second)
	for {
		select {
		case c := <-done:
			if c != http.StatusRequestTimeout {
				t.Fatalf("wait returned %d, want 408", c)
			}
			return
		case <-safety:
			t.Fatal("wait did not time out")
		default:
			h.clk.Advance(500 * time.Millisecond)
			time.Sleep(2 * time.Millisecond)
		}
	}
}

func TestUnimplementedReturns501(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do(http.MethodGet, "/v1/apps/demo/volumes", nil, nil); code != http.StatusNotImplemented {
		t.Fatalf("volumes = %d %s, want 501", code, body)
	}
}

// TestCordonSurfacedOnMachine is the regression for audit M4: cordon status is
// readable on the machine object.
func TestCordonSurfacedOnMachine(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")

	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/cordon", nil, nil); code != http.StatusOK {
		t.Fatalf("cordon = %d %s", code, body)
	}
	_, body := h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil)
	var got flaps.Machine
	h.mustJSON(body, &got)
	if !got.Cordoned {
		t.Fatalf("machine cordoned = false after cordon, want true: %s", body)
	}
	if code, _ := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/uncordon", nil, nil); code != http.StatusOK {
		t.Fatalf("uncordon = %d", code)
	}
	_, body = h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil)
	h.mustJSON(body, &got)
	if got.Cordoned {
		t.Fatalf("machine cordoned = true after uncordon, want false")
	}
}

// TestSignalExecPsReturn501 is the regression for audit M6: fly-go's
// signal/exec/ps answer an honest 501 and appear in health coverage.
func TestSignalExecPsReturn501(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")
	base := "/v1/apps/demo/machines/" + m.ID
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, base + "/signal"},
		{http.MethodPost, base + "/exec"},
		{http.MethodGet, base + "/ps"},
	} {
		if code, body := h.do(tc.method, tc.path, nil, nil); code != http.StatusNotImplemented {
			t.Fatalf("%s %s = %d %s, want 501", tc.method, tc.path, code, body)
		}
	}
	// They must be disclosed in the health coverage list.
	_, body := h.do(http.MethodGet, "/_mudflaps/health", nil, nil)
	for _, want := range []string{"signal", "exec", "ps"} {
		if !strings.Contains(string(body), "/"+want) {
			t.Fatalf("health unimplemented missing %q: %s", want, body)
		}
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

// TestClampTimeoutFloor is the regression for the audit clampTimeout finding:
// non-positive/garbage timeouts clamp to the 1s floor, not the 60s ceiling.
func TestClampTimeoutFloor(t *testing.T) {
	cases := map[string]time.Duration{
		"":    60 * time.Second, // unspecified -> default max
		"0":   time.Second,      // non-positive -> floor
		"-5":  time.Second,
		"abc": time.Second, // garbage -> floor
		"30":  30 * time.Second,
		"120": 60 * time.Second, // above ceiling -> max
	}
	for raw, want := range cases {
		if got := clampTimeout(raw); got != want {
			t.Fatalf("clampTimeout(%q) = %v, want %v", raw, got, want)
		}
	}
}

// TestUpdateResponseHasFreshUpdatedAt is the regression for the audit stale
// updated_at finding: the update response carries the bumped timestamp.
func TestUpdateResponseHasFreshUpdatedAt(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")
	h.clk.Advance(time.Hour) // move the clock past create time
	_, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID,
		flaps.CreateMachineRequest{Config: &flaps.MachineConfig{Image: "nginx:2"}}, nil)
	var resp flaps.Machine
	h.mustJSON(body, &resp)
	if resp.UpdatedAt == m.UpdatedAt {
		t.Fatalf("update response updated_at not refreshed from %q", m.UpdatedAt)
	}
	_, body = h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil)
	var got flaps.Machine
	h.mustJSON(body, &got)
	if got.UpdatedAt != resp.UpdatedAt {
		t.Fatalf("updated_at diverged: response %q vs get %q", resp.UpdatedAt, got.UpdatedAt)
	}
}

// TestDeleteAppWithLeasedMachine exercises the audit cleanup path: deleting an
// app that has a leased machine succeeds and clears its leases (no leak).
func TestDeleteAppWithLeasedMachine(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/lease",
		flaps.AcquireLeaseRequest{TTL: 30}, nil); code != http.StatusOK {
		t.Fatalf("acquire lease = %d %s", code, body)
	}
	if code, body := h.do(http.MethodDelete, "/v1/apps/demo", nil, nil); code != http.StatusAccepted {
		t.Fatalf("delete app = %d %s, want 202", code, body)
	}
	// The app and its machines are gone.
	if code, _ := h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("machine still present after app delete = %d", code)
	}
}

// TestResponseShapesMatchFlaps is the regression for the audit cosmetic bundle:
// create -> 200, start -> MachineStartResponse, delete -> empty body, and lease
// data has no description field.
func TestResponseShapesMatchFlaps(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo"}, nil); code != http.StatusCreated {
		t.Fatalf("create app = %d %s", code, body)
	}
	// create machine -> 200 (not 201)
	code, body := h.do(http.MethodPost, "/v1/apps/demo/machines",
		flaps.CreateMachineRequest{Config: &flaps.MachineConfig{Image: "nginx"}}, nil)
	if code != http.StatusOK {
		t.Fatalf("create machine = %d, want 200", code)
	}
	var m flaps.Machine
	h.mustJSON(body, &m)
	h.clk.Advance(time.Hour)

	// start -> MachineStartResponse with previous_state
	_, body = h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/start", nil, nil)
	var sr flaps.MachineStartResponse
	h.mustJSON(body, &sr)
	if sr.Status != "ok" || sr.PreviousState == "" {
		t.Fatalf("start response = %+v, want status=ok + previous_state", sr)
	}
	if strings.Contains(string(body), `"ok":`) {
		t.Fatalf("start body should not be the wait envelope: %s", body)
	}

	// lease data has no `description` field
	_, body = h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID+"/lease", flaps.AcquireLeaseRequest{TTL: 30, Description: "x"}, nil)
	if strings.Contains(string(body), `"description"`) {
		t.Fatalf("lease data should not surface description: %s", body)
	}

	// delete -> empty object body (no "ok")
	_, body = h.do(http.MethodDelete, "/v1/apps/demo/machines/"+m.ID, nil, map[string]string{})
	if strings.Contains(string(body), `"ok"`) {
		t.Fatalf("delete body should be empty object, got: %s", body)
	}
}
