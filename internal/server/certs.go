package server

import (
	"errors"
	"net/http"

	"github.com/intentius/mudflaps/internal/flaps"
	"github.com/intentius/mudflaps/internal/store"
)

// Certificates model the flaps API shape, not real ACME issuance: a new
// certificate is "pending" and unconfigured (DNS isn't validated here).

func (s *Server) listCertificates(w http.ResponseWriter, r *http.Request) {
	certs, err := s.store.ListCertificates(r.PathValue("app"))
	if errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	summaries := make([]flaps.CertificateSummary, 0, len(certs))
	for _, c := range certs {
		summaries = append(summaries, flaps.CertificateSummary{Hostname: c.Hostname, Status: c.Status})
	}
	writeJSON(w, http.StatusOK, flaps.ListCertificatesResponse{Certificates: summaries, TotalCount: len(summaries)})
}

func (s *Server) createCertificate(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("app")
	if _, err := s.store.GetApp(app); errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	var req flaps.CreateCertificateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Hostname == "" {
		s.writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}
	cert := flaps.CertificateDetailResponse{
		Hostname:      req.Hostname,
		Configured:    false,
		AcmeRequested: true,
		Status:        "pending",
	}
	created, err := s.store.SetCertificate(app, cert)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) getCertificate(w http.ResponseWriter, r *http.Request) {
	cert, err := s.store.GetCertificate(r.PathValue("app"), r.PathValue("hostname"))
	if s.handleCertLookup(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, cert)
}

func (s *Server) deleteCertificate(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteCertificate(r.PathValue("app"), r.PathValue("hostname"))
	if s.handleCertLookup(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleCertLookup(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrAppNotFound):
		s.writeError(w, http.StatusNotFound, "app not found")
	case errors.Is(err, store.ErrCertNotFound):
		s.writeError(w, http.StatusNotFound, "certificate not found")
	default:
		s.writeError(w, http.StatusInternalServerError, err.Error())
	}
	return true
}
