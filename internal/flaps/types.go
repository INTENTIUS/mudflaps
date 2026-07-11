// Package flaps defines the wire types for the subset of the Fly.io Machines
// API ("flaps") that mudflaps emulates.
//
// The conformance oracle for these shapes is github.com/superfly/fly-go
// (machine_types.go, volume_types.go, flaps/flaps.go, flaps/flaps_machines.go,
// flaps/flaps_machines_wait.go) together with the published OpenAPI document at
// https://docs.machines.dev/spec/openapi3.json. mudflaps deliberately does not
// import fly-go so that the build stays self-contained and offline; instead the
// JSON struct tags below are hand-mirrored against it. Those tags are the
// contract: a client such as fly-go must be able to marshal into and unmarshal
// out of these types unchanged, so the tags (instance_id, metadata, config, and
// so on) must match the real wire format exactly.
package flaps

// MachineState is the lifecycle state of a machine. Fly models three groups of
// states: persistent resting states, transient in-flight states, and terminal
// states a machine never leaves.
type MachineState string

// Persistent states: a machine rests in one of these until acted upon.
const (
	StateCreated   MachineState = "created"
	StateStarted   MachineState = "started"
	StateStopped   MachineState = "stopped"
	StateSuspended MachineState = "suspended"
	StateFailed    MachineState = "failed"
)

// Transient states: a machine passes through these while an operation runs.
const (
	StateCreating   MachineState = "creating"
	StateStarting   MachineState = "starting"
	StateStopping   MachineState = "stopping"
	StateRestarting MachineState = "restarting"
	StateSuspending MachineState = "suspending"
	StateUpdating   MachineState = "updating"
	StateReplacing  MachineState = "replacing"
	StateDestroying MachineState = "destroying"
)

// Terminal states: a machine never leaves these.
const (
	StateDestroyed MachineState = "destroyed"
	StateReplaced  MachineState = "replaced"
)

// Machine is a single Fly machine.
type Machine struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	State      MachineState   `json:"state"`
	Region     string         `json:"region"`
	InstanceID string         `json:"instance_id"`
	PrivateIP  string         `json:"private_ip,omitempty"`
	Config     *MachineConfig `json:"config,omitempty"`
	ImageRef   *ImageRef      `json:"image_ref,omitempty"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`

	// Versions records the instance-ID history for this machine. It is an
	// internal bookkeeping field used to model version churn (an update mints a
	// new instance_id and marks the prior version replaced) and is not part of
	// the wire contract.
	Versions []MachineVersion `json:"-"`

	// Cordoned tracks whether the machine is cordoned (excluded from proxy
	// routing). Surfaced to match fly-go's Machine, which emits `cordoned`;
	// real networking isn't modeled.
	Cordoned bool `json:"cordoned"`
}

// MachineVersion is one entry in a machine's instance-ID history.
type MachineVersion struct {
	InstanceID string       `json:"instance_id"`
	State      MachineState `json:"state"`
}

// MachineConfig is the desired configuration of a machine. Only the commonly
// used fields are modelled; the applier under test cares chiefly about image,
// env, and metadata.
type MachineConfig struct {
	Image    string            `json:"image,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Guest    *MachineGuest     `json:"guest,omitempty"`
	Services []Service         `json:"services,omitempty"`
	Restart  *Restart          `json:"restart,omitempty"`
}

// MachineGuest is the resource allocation for a machine.
type MachineGuest struct {
	CPUKind  string `json:"cpu_kind,omitempty"`
	CPUs     int    `json:"cpus,omitempty"`
	MemoryMB int    `json:"memory_mb,omitempty"`
}

// Service is a single exposed service on a machine.
type Service struct {
	Protocol     string `json:"protocol,omitempty"`
	InternalPort int    `json:"internal_port,omitempty"`
	Ports        []Port `json:"ports,omitempty"`
}

// Port is a published port on a service.
type Port struct {
	Port     int      `json:"port,omitempty"`
	Handlers []string `json:"handlers,omitempty"`
}

// Restart is a machine's restart policy.
type Restart struct {
	Policy     string `json:"policy,omitempty"`
	MaxRetries int    `json:"max_retries,omitempty"`
}

// ImageRef describes the resolved image backing a machine.
type ImageRef struct {
	Registry   string            `json:"registry,omitempty"`
	Repository string            `json:"repository,omitempty"`
	Tag        string            `json:"tag,omitempty"`
	Digest     string            `json:"digest,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// CreateMachineRequest is the body of POST /v1/apps/{app}/machines. It is also
// used for updates (POST /v1/apps/{app}/machines/{id}).
type CreateMachineRequest struct {
	Name       string         `json:"name,omitempty"`
	Region     string         `json:"region,omitempty"`
	Config     *MachineConfig `json:"config,omitempty"`
	SkipLaunch bool           `json:"skip_launch,omitempty"`
	LeaseNonce string         `json:"lease_nonce,omitempty"`
}

// App is a Fly application as exposed by flaps.
type App struct {
	Name         string `json:"name"`
	Organization string `json:"organization,omitempty"`
	Status       string `json:"status,omitempty"`
}

// CreateAppRequest is the body of POST /v1/apps.
type CreateAppRequest struct {
	AppName string `json:"app_name"`
	OrgSlug string `json:"org_slug,omitempty"`
}

// ListAppsResponse is the body of GET /v1/apps.
type ListAppsResponse struct {
	Apps []App `json:"apps"`
}

// MachineLease is the envelope returned by the lease endpoints.
type MachineLease struct {
	Status  string            `json:"status"`
	Code    string            `json:"code,omitempty"`
	Message string            `json:"message,omitempty"`
	Data    *MachineLeaseData `json:"data,omitempty"`
}

// MachineLeaseData is the lease itself: an owner holds it via a nonce until it
// expires.
type MachineLeaseData struct {
	Nonce     string `json:"nonce"`
	ExpiresAt int64  `json:"expires_at"`
	Owner     string `json:"owner,omitempty"`
	Version   string `json:"version,omitempty"`
}

// AcquireLeaseRequest is the body of POST .../lease.
type AcquireLeaseRequest struct {
	TTL         int    `json:"ttl,omitempty"`
	Description string `json:"description,omitempty"`
}

// WaitResponse is returned by a successful GET .../wait.
type WaitResponse struct {
	OK bool `json:"ok"`
}

// MachineStartResponse is returned by POST .../start, matching fly-go.
type MachineStartResponse struct {
	Message       string `json:"message,omitempty"`
	Status        string `json:"status,omitempty"`
	PreviousState string `json:"previous_state,omitempty"`
}

// IPAssignment is an allocated IP address. Fields mirror fly-go; created_at is
// a string here (marshals to the same RFC3339 a time.Time would).
type IPAssignment struct {
	IP          string `json:"ip"`
	Region      string `json:"region"`
	ServiceName string `json:"service_name"`
	Shared      bool   `json:"shared"`
	CreatedAt   string `json:"created_at"`
}

// ListIPAssignmentsResponse is the GET .../ip_assignments response.
type ListIPAssignmentsResponse struct {
	IPs []IPAssignment `json:"ips"`
}

// AssignIPRequest is the body of POST .../ip_assignments.
type AssignIPRequest struct {
	Type         string `json:"type"`
	Region       string `json:"region"`
	Organization string `json:"org_slug"`
	Network      string `json:"network"`
	ServiceName  string `json:"service_name"`
}

// CreateCertificateRequest is the body of POST .../certificates.
type CreateCertificateRequest struct {
	Hostname string `json:"hostname"`
}

// CertificateSummary is one entry in the certificates list.
type CertificateSummary struct {
	Hostname string `json:"hostname"`
	Status   string `json:"status"`
}

// ListCertificatesResponse is the GET .../certificates response.
type ListCertificatesResponse struct {
	Certificates []CertificateSummary `json:"certificates"`
	TotalCount   int                  `json:"total_count,omitempty"`
}

// CertificateDetailResponse is the GET/POST .../certificates[/{hostname}]
// response. mudflaps models the shape, not real ACME issuance.
type CertificateDetailResponse struct {
	Hostname      string `json:"hostname"`
	Configured    bool   `json:"configured"`
	AcmeRequested bool   `json:"acme_requested"`
	Status        string `json:"status"`
}

// AppSecret is a secret's metadata. mudflaps is apply-only: it stores a digest,
// never the value, so `value` is always null on read.
type AppSecret struct {
	Name      string  `json:"name"`
	Value     *string `json:"value,omitempty"`
	Digest    string  `json:"digest,omitempty"`
	UpdatedAt *string `json:"updated_at,omitempty"`
	CreatedAt *string `json:"created_at,omitempty"`
}

// ListAppSecretsResp is the GET .../secrets response.
type ListAppSecretsResp struct {
	Secrets []AppSecret `json:"secrets"`
}

// SetAppSecretRequest is the body of POST .../secrets/{name}.
type SetAppSecretRequest struct {
	Value string `json:"value"`
}

// SetAppSecretResp is the POST .../secrets/{name} response.
type SetAppSecretResp struct {
	AppSecret
	Version uint64 `json:"version"`
}

// DeleteAppSecretResp is the DELETE .../secrets/{name} response.
type DeleteAppSecretResp struct {
	Version uint64 `json:"version"`
}

// Volume is a Fly volume. Field names mirror fly-go's Volume; created_at is a
// string here (marshals to the same RFC3339 a time.Time would).
type Volume struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	State             string  `json:"state"`
	SizeGb            int     `json:"size_gb"`
	Region            string  `json:"region"`
	Zone              string  `json:"zone"`
	Encrypted         bool    `json:"encrypted"`
	AttachedMachine   *string `json:"attached_machine_id"`
	SnapshotRetention int     `json:"snapshot_retention,omitempty"`
	AutoBackupEnabled bool    `json:"auto_backup_enabled,omitempty"`
	CreatedAt         string  `json:"created_at"`
}

// CreateVolumeRequest is the body of POST .../volumes.
type CreateVolumeRequest struct {
	Name      string `json:"name"`
	Region    string `json:"region"`
	SizeGb    *int   `json:"size_gb"`
	Encrypted *bool  `json:"encrypted"`
}

// UpdateVolumeRequest is the body of PUT .../volumes/{vol}.
type UpdateVolumeRequest struct {
	SnapshotRetention *int  `json:"snapshot_retention"`
	AutoBackupEnabled *bool `json:"auto_backup_enabled"`
}

// Region is a Fly region. The capitalized JSON tags on RegionData match fly-go's
// GetRegions response exactly (they are the wire contract).
type Region struct {
	Code             string  `json:"code"`
	Name             string  `json:"name"`
	Latitude         float32 `json:"latitude,omitempty"`
	Longitude        float32 `json:"longitude,omitempty"`
	GatewayAvailable bool    `json:"gateway_available,omitempty"`
}

// RegionData is the GET /v1/platform/regions response.
type RegionData struct {
	Regions []Region `json:"Regions"`
	Nearest string   `json:"Nearest,omitempty"`
}

// StopMachineInput is the optional body of POST .../stop. mudflaps accepts the
// signal/timeout to honor the request shape; it does not model real signals.
// Timeout is a duration string ("0s", "10s") to match fly-go, whose Timeout is
// a Duration that marshals as a string and is sent on every Stop call.
type StopMachineInput struct {
	Signal  string `json:"signal,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

// ErrorResponse is the JSON body mudflaps returns for any non-2xx status. Fly's
// flaps errors carry an "error" message; mudflaps adds a machine-readable
// status for convenience.
type ErrorResponse struct {
	Error  string `json:"error"`
	Status int    `json:"status,omitempty"`
}
