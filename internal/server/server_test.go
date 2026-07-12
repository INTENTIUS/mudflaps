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
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: app, OrgSlug: "personal"}, nil); code != http.StatusCreated {
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
	if len(payload.Implemented) == 0 {
		t.Fatalf("expected a non-empty implemented list: %+v", payload)
	}
	// The roadmap is cleared — every documented endpoint mudflaps targets is now
	// implemented, so the unimplemented list is empty (but still reported).
	if len(payload.Unimplemented) != 0 {
		t.Fatalf("expected an empty unimplemented list, got: %+v", payload.Unimplemented)
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

// TestCreateAppRequiresOrgSlug mirrors real Fly, which rejects app creation
// without an org_slug (400) rather than silently creating the app.
func TestCreateAppRequiresOrgSlug(t *testing.T) {
	h := newHarness(t)
	code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "x"}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("create app without org_slug = %d %s, want 400", code, body)
	}
	if !strings.Contains(string(body), "org_slug") {
		t.Fatalf("error body = %s, want it to mention org_slug", body)
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
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo", OrgSlug: "personal"}, nil); code != http.StatusCreated {
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
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo", OrgSlug: "personal"}, nil); code != http.StatusCreated {
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

func TestSignalMachine(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")
	base := "/v1/apps/demo/machines/" + m.ID

	if code, body := h.do(http.MethodPost, base+"/signal", flaps.SignalRequest{Signal: "SIGTERM"}, nil); code != http.StatusOK {
		t.Fatalf("signal SIGTERM = %d %s, want 200", code, body)
	}
	if code, _ := h.do(http.MethodPost, base+"/signal", flaps.SignalRequest{Signal: "SIGBOGUS"}, nil); code != http.StatusBadRequest {
		t.Fatalf("invalid signal, want 400")
	}
	if code, _ := h.do(http.MethodPost, base+"/signal", flaps.SignalRequest{}, nil); code != http.StatusBadRequest {
		t.Fatalf("missing signal, want 400")
	}
	if code, _ := h.do(http.MethodPost, "/v1/apps/demo/machines/nope/signal", flaps.SignalRequest{Signal: "SIGKILL"}, nil); code != http.StatusNotFound {
		t.Fatalf("signal unknown machine, want 404")
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

// TestExecMachine covers POST .../exec: mudflaps returns a deterministic
// ExecResponse (it cannot run a real command).
func TestExecMachine(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")
	base := "/v1/apps/demo/machines/" + m.ID

	code, body := h.do(http.MethodPost, base+"/exec", flaps.MachineExecRequest{Command: []string{"echo", "hi"}}, nil)
	if code != http.StatusOK {
		t.Fatalf("exec = %d %s, want 200", code, body)
	}
	var res flaps.ExecResponse
	h.mustJSON(body, &res)
	if res.ExitCode != 0 || res.Stdout != "echo hi\n" {
		t.Fatalf("exec response = %+v", res)
	}
	if code, _ := h.do(http.MethodPost, base+"/exec", flaps.MachineExecRequest{}, nil); code != http.StatusBadRequest {
		t.Fatalf("exec with no command, want 400")
	}
	if code, _ := h.do(http.MethodPost, "/v1/apps/demo/machines/nope/exec", flaps.MachineExecRequest{Cmd: "ls"}, nil); code != http.StatusNotFound {
		t.Fatalf("exec unknown machine, want 404")
	}
}

// TestPsMachine covers GET .../ps: a deterministic process list.
func TestPsMachine(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")
	base := "/v1/apps/demo/machines/" + m.ID

	code, body := h.do(http.MethodGet, base+"/ps", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("ps = %d %s, want 200", code, body)
	}
	var procs []flaps.ProcessStat
	h.mustJSON(body, &procs)
	if len(procs) == 0 || procs[0].PID != 1 {
		t.Fatalf("ps response = %+v", procs)
	}
	if code, _ := h.do(http.MethodGet, "/v1/apps/demo/machines/nope/ps", nil, nil); code != http.StatusNotFound {
		t.Fatalf("ps unknown machine, want 404")
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
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo", OrgSlug: "personal"}, nil); code != http.StatusCreated {
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

// TestPlatformRegions covers GET /v1/platform/regions (breadth #22).
func TestPlatformRegions(t *testing.T) {
	h := newHarness(t)
	code, body := h.do(http.MethodGet, "/v1/platform/regions", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("regions = %d %s", code, body)
	}
	var rd flaps.RegionData
	h.mustJSON(body, &rd)
	if len(rd.Regions) == 0 {
		t.Fatalf("expected non-empty regions")
	}
	found := map[string]bool{}
	for _, r := range rd.Regions {
		if r.Code == "" || r.Name == "" {
			t.Fatalf("region missing code/name: %+v", r)
		}
		found[r.Code] = true
	}
	for _, want := range []string{"iad", "lhr", "syd"} {
		if !found[want] {
			t.Fatalf("regions missing %q", want)
		}
	}
	// The wire tag is capital-R "Regions" (fly-go contract).
	if !strings.Contains(string(body), `"Regions"`) {
		t.Fatalf("response must use the capital-R Regions tag: %s", body[:80])
	}
}

// TestVolumeCRUD covers the volume endpoints (breadth #18).
func TestVolumeCRUD(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo", OrgSlug: "personal"}, nil); code != http.StatusCreated {
		t.Fatalf("create app = %d %s", code, body)
	}
	// empty list, not 501
	code, body := h.do(http.MethodGet, "/v1/apps/demo/volumes", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("list volumes = %d %s, want 200", code, body)
	}
	var vols []flaps.Volume
	h.mustJSON(body, &vols)
	if len(vols) != 0 {
		t.Fatalf("expected empty volume list, got %d", len(vols))
	}
	// create
	size := 3
	code, body = h.do(http.MethodPost, "/v1/apps/demo/volumes",
		flaps.CreateVolumeRequest{Name: "data", Region: "iad", SizeGb: &size}, nil)
	if code != http.StatusOK {
		t.Fatalf("create volume = %d %s", code, body)
	}
	var v flaps.Volume
	h.mustJSON(body, &v)
	if v.ID == "" || v.Name != "data" || v.SizeGb != 3 || v.Region != "iad" || v.State != "created" {
		t.Fatalf("unexpected created volume: %+v", v)
	}
	// get
	if code, body := h.do(http.MethodGet, "/v1/apps/demo/volumes/"+v.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("get volume = %d %s", code, body)
	}
	// list shows it
	_, body = h.do(http.MethodGet, "/v1/apps/demo/volumes", nil, nil)
	h.mustJSON(body, &vols)
	if len(vols) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(vols))
	}
	// update
	ab := true
	if code, body := h.do(http.MethodPut, "/v1/apps/demo/volumes/"+v.ID,
		flaps.UpdateVolumeRequest{AutoBackupEnabled: &ab}, nil); code != http.StatusOK {
		t.Fatalf("update volume = %d %s", code, body)
	}
	// delete
	if code, body := h.do(http.MethodDelete, "/v1/apps/demo/volumes/"+v.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("delete volume = %d %s", code, body)
	}
	if code, _ := h.do(http.MethodGet, "/v1/apps/demo/volumes/"+v.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", code)
	}
}

// TestSecretsApplyOnly covers the secret endpoints (breadth #19): set/list/get/
// delete, and the invariant that a value is never returned.
func TestSecretsApplyOnly(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo", OrgSlug: "personal"}, nil); code != http.StatusCreated {
		t.Fatalf("create app = %d %s", code, body)
	}
	// set
	code, body := h.do(http.MethodPost, "/v1/apps/demo/secrets/DATABASE_URL",
		flaps.SetAppSecretRequest{Value: "postgres://secret"}, nil)
	if code != http.StatusOK {
		t.Fatalf("set secret = %d %s", code, body)
	}
	var setResp flaps.SetAppSecretResp
	h.mustJSON(body, &setResp)
	if setResp.Name != "DATABASE_URL" || setResp.Digest == "" || setResp.Version == 0 {
		t.Fatalf("unexpected set response: %+v", setResp)
	}
	// the value must never appear in any response
	for _, path := range []string{"/v1/apps/demo/secrets", "/v1/apps/demo/secrets/DATABASE_URL"} {
		_, b := h.do(http.MethodGet, path, nil, nil)
		if strings.Contains(string(b), "postgres://secret") {
			t.Fatalf("secret value leaked in %s: %s", path, b)
		}
	}
	// list shows the name + digest
	_, body = h.do(http.MethodGet, "/v1/apps/demo/secrets", nil, nil)
	var list flaps.ListAppSecretsResp
	h.mustJSON(body, &list)
	if len(list.Secrets) != 1 || list.Secrets[0].Name != "DATABASE_URL" || list.Secrets[0].Value != nil {
		t.Fatalf("unexpected list: %+v", list)
	}
	// delete bumps the version
	_, body = h.do(http.MethodDelete, "/v1/apps/demo/secrets/DATABASE_URL", nil, nil)
	var del flaps.DeleteAppSecretResp
	h.mustJSON(body, &del)
	if del.Version <= setResp.Version {
		t.Fatalf("delete version %d not greater than set version %d", del.Version, setResp.Version)
	}
	if code, _ := h.do(http.MethodGet, "/v1/apps/demo/secrets/DATABASE_URL", nil, nil); code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", code)
	}
}

// TestIPAssignments covers the ip_assignments endpoints (breadth #21).
func TestIPAssignments(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo", OrgSlug: "personal"}, nil); code != http.StatusCreated {
		t.Fatalf("create app = %d %s", code, body)
	}
	// shared v4
	code, body := h.do(http.MethodPost, "/v1/apps/demo/ip_assignments", flaps.AssignIPRequest{Type: "shared_v4", Region: "global"}, nil)
	if code != http.StatusOK {
		t.Fatalf("assign shared_v4 = %d %s", code, body)
	}
	var ip flaps.IPAssignment
	h.mustJSON(body, &ip)
	if ip.IP == "" || !ip.Shared {
		t.Fatalf("unexpected shared ip: %+v", ip)
	}
	// dedicated v6
	_, body = h.do(http.MethodPost, "/v1/apps/demo/ip_assignments", flaps.AssignIPRequest{Type: "v6"}, nil)
	h.mustJSON(body, &ip)
	if ip.Shared {
		t.Fatalf("v6 should not be shared: %+v", ip)
	}
	// list has 2
	_, body = h.do(http.MethodGet, "/v1/apps/demo/ip_assignments", nil, nil)
	var list flaps.ListIPAssignmentsResponse
	h.mustJSON(body, &list)
	if len(list.IPs) != 2 {
		t.Fatalf("expected 2 ips, got %d", len(list.IPs))
	}
	// delete one
	if code, _ := h.do(http.MethodDelete, "/v1/apps/demo/ip_assignments/"+ip.IP, nil, nil); code != http.StatusOK {
		t.Fatalf("delete ip = %d", code)
	}
	if code, _ := h.do(http.MethodDelete, "/v1/apps/demo/ip_assignments/9.9.9.9", nil, nil); code != http.StatusNotFound {
		t.Fatalf("delete missing ip = %d, want 404", code)
	}
}

// TestCertificates covers the certificate endpoints (breadth #20).
func TestCertificates(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: "demo", OrgSlug: "personal"}, nil); code != http.StatusCreated {
		t.Fatalf("create app = %d %s", code, body)
	}
	// create
	code, body := h.do(http.MethodPost, "/v1/apps/demo/certificates", flaps.CreateCertificateRequest{Hostname: "example.com"}, nil)
	if code != http.StatusOK {
		t.Fatalf("create cert = %d %s", code, body)
	}
	var cert flaps.CertificateDetailResponse
	h.mustJSON(body, &cert)
	if cert.Hostname != "example.com" || cert.Status == "" || cert.Configured {
		t.Fatalf("unexpected cert: %+v", cert)
	}
	// get
	if code, body := h.do(http.MethodGet, "/v1/apps/demo/certificates/example.com", nil, nil); code != http.StatusOK {
		t.Fatalf("get cert = %d %s", code, body)
	}
	// list
	_, body = h.do(http.MethodGet, "/v1/apps/demo/certificates", nil, nil)
	var list flaps.ListCertificatesResponse
	h.mustJSON(body, &list)
	if len(list.Certificates) != 1 || list.Certificates[0].Hostname != "example.com" {
		t.Fatalf("unexpected cert list: %+v", list)
	}
	// delete
	if code, _ := h.do(http.MethodDelete, "/v1/apps/demo/certificates/example.com", nil, nil); code != http.StatusOK {
		t.Fatalf("delete cert = %d", code)
	}
	if code, _ := h.do(http.MethodGet, "/v1/apps/demo/certificates/example.com", nil, nil); code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", code)
	}
}

// TestMachineVersionsEndpoint covers GET .../versions (fidelity #24).
func TestMachineVersionsEndpoint(t *testing.T) {
	h := newHarness(t)
	m := h.createStartedMachine("demo")
	// After an update, versions should show the prior (replaced) + current.
	if code, body := h.do(http.MethodPost, "/v1/apps/demo/machines/"+m.ID,
		flaps.CreateMachineRequest{Config: &flaps.MachineConfig{Image: "nginx:2"}}, nil); code != http.StatusOK {
		t.Fatalf("update = %d %s", code, body)
	}
	h.clk.Advance(time.Hour)
	code, body := h.do(http.MethodGet, "/v1/apps/demo/machines/"+m.ID+"/versions", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("versions = %d %s", code, body)
	}
	var vers []flaps.MachineVersion
	h.mustJSON(body, &vers)
	if len(vers) < 2 {
		t.Fatalf("expected >=2 versions, got %d: %s", len(vers), body)
	}
	if vers[len(vers)-2].State != flaps.StateReplaced {
		t.Fatalf("prior version not replaced: %+v", vers)
	}
	if vers[len(vers)-1].InstanceID == m.InstanceID {
		t.Fatalf("current version instance_id did not churn")
	}
}

// TestOrgScopedListing covers GET /v1/orgs/{org}/machines|volumes (fidelity #25).
func TestOrgScopedListing(t *testing.T) {
	h := newHarness(t)
	// Two apps under org "acme", one under "other".
	for _, a := range []struct{ name, org string }{{"a1", "acme"}, {"a2", "acme"}, {"a3", "other"}} {
		if code, body := h.do(http.MethodPost, "/v1/apps", flaps.CreateAppRequest{AppName: a.name, OrgSlug: a.org}, nil); code != http.StatusCreated {
			t.Fatalf("create app %s = %d %s", a.name, code, body)
		}
		if code, body := h.do(http.MethodPost, "/v1/apps/"+a.name+"/machines",
			flaps.CreateMachineRequest{Config: &flaps.MachineConfig{Image: "nginx"}}, nil); code != http.StatusOK {
			t.Fatalf("create machine in %s = %d %s", a.name, code, body)
		}
	}
	_, body := h.do(http.MethodGet, "/v1/orgs/acme/machines", nil, nil)
	var machines []flaps.Machine
	h.mustJSON(body, &machines)
	if len(machines) != 2 {
		t.Fatalf("org acme machines = %d, want 2", len(machines))
	}
	// unknown org -> empty list, not 404
	code, body := h.do(http.MethodGet, "/v1/orgs/nobody/machines", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("unknown org = %d, want 200", code)
	}
	h.mustJSON(body, &machines)
	if len(machines) != 0 {
		t.Fatalf("unknown org machines = %d, want 0", len(machines))
	}
}
