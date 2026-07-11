package server

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/intentius/mudflaps/internal/flaps"
	"github.com/intentius/mudflaps/internal/store"
)

func (s *Server) listIPs(w http.ResponseWriter, r *http.Request) {
	ips, err := s.store.ListIPs(r.PathValue("app"))
	if errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	writeJSON(w, http.StatusOK, flaps.ListIPAssignmentsResponse{IPs: ips})
}

func (s *Server) assignIP(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("app")
	if _, err := s.store.GetApp(app); errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	var req flaps.AssignIPRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	addr, shared := allocateIP(req.Type)
	ip := flaps.IPAssignment{
		IP:          addr,
		Region:      defaultString(req.Region, "global"),
		ServiceName: req.ServiceName,
		Shared:      shared,
		CreatedAt:   s.clk.Now().UTC().Format(time.RFC3339Nano),
	}
	assigned, err := s.store.AssignIP(app, ip)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, assigned)
}

func (s *Server) deleteIP(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteIP(r.PathValue("app"), r.PathValue("ip"))
	switch {
	case errors.Is(err, store.ErrAppNotFound):
		s.writeError(w, http.StatusNotFound, "app not found")
	case errors.Is(err, store.ErrIPNotFound):
		s.writeError(w, http.StatusNotFound, "ip assignment not found")
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, struct{}{})
	}
}

// allocateIP synthesizes an address for the requested type. Shared v4 returns a
// fixed shared address (as real Fly does); everything else gets a random one.
func allocateIP(typ string) (addr string, shared bool) {
	switch typ {
	case "shared_v4":
		return "66.241.125.1", true
	case "v6", "private_v6":
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		return fmt.Sprintf("2604:1380:%02x%02x:%02x%02x::1", b[0], b[1], b[2], b[3]), false
	default: // v4 / dedicated_v4
		b := make([]byte, 2)
		_, _ = rand.Read(b)
		return fmt.Sprintf("137.66.%d.%d", b[0], b[1]), false
	}
}
