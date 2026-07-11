package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/intentius/mudflaps/internal/flaps"
	"github.com/intentius/mudflaps/internal/store"
)

// Secrets are apply-only: mudflaps stores a digest of each value, never the
// value itself, so no endpoint ever returns a secret value.

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	secs, err := s.store.ListSecrets(r.PathValue("app"))
	if errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	writeJSON(w, http.StatusOK, flaps.ListAppSecretsResp{Secrets: secs})
}

func (s *Server) getSecret(w http.ResponseWriter, r *http.Request) {
	sec, err := s.store.GetSecret(r.PathValue("app"), r.PathValue("name"))
	if s.handleSecretLookup(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, sec)
}

func (s *Server) setSecret(w http.ResponseWriter, r *http.Request) {
	app, name := r.PathValue("app"), r.PathValue("name")
	if _, err := s.store.GetApp(app); errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	var req flaps.SetAppSecretRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Digest the value so a change is observable, but never store the value.
	sum := sha256.Sum256([]byte(req.Value))
	digest := hex.EncodeToString(sum[:])[:16]
	now := s.clk.Now().UTC().Format(time.RFC3339Nano)
	sec, version, err := s.store.SetSecret(app, name, digest, now)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, flaps.SetAppSecretResp{AppSecret: sec, Version: version})
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	version, err := s.store.DeleteSecret(r.PathValue("app"), r.PathValue("name"))
	if s.handleSecretLookup(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, flaps.DeleteAppSecretResp{Version: version})
}

func (s *Server) handleSecretLookup(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrAppNotFound):
		s.writeError(w, http.StatusNotFound, "app not found")
	case errors.Is(err, store.ErrSecretNotFound):
		s.writeError(w, http.StatusNotFound, "secret not found")
	default:
		s.writeError(w, http.StatusInternalServerError, err.Error())
	}
	return true
}
