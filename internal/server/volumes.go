package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/intentius/mudflaps/internal/flaps"
	"github.com/intentius/mudflaps/internal/id"
	"github.com/intentius/mudflaps/internal/store"
)

func (s *Server) listVolumes(w http.ResponseWriter, r *http.Request) {
	vols, err := s.store.ListVolumes(r.PathValue("app"))
	if errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	writeJSON(w, http.StatusOK, vols)
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("app")
	if _, err := s.store.GetApp(app); errors.Is(err, store.ErrAppNotFound) {
		s.writeError(w, http.StatusNotFound, "app not found")
		return
	}
	var req flaps.CreateVolumeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		s.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	size := 1
	if req.SizeGb != nil {
		size = *req.SizeGb
	}
	v := flaps.Volume{
		ID:        id.Volume(),
		Name:      req.Name,
		State:     "created",
		SizeGb:    size,
		Region:    defaultString(req.Region, "local"),
		Zone:      "local-1",
		Encrypted: req.Encrypted == nil || *req.Encrypted, // encrypted by default, like Fly
		CreatedAt: s.clk.Now().UTC().Format(time.RFC3339Nano),
	}
	created, err := s.store.CreateVolume(app, v)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) getVolume(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetVolume(r.PathValue("app"), r.PathValue("vol"))
	if s.handleVolumeLookup(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) updateVolume(w http.ResponseWriter, r *http.Request) {
	app, vol := r.PathValue("app"), r.PathValue("vol")
	var req flaps.UpdateVolumeRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	updated, err := s.store.UpdateVolume(app, vol, func(v *flaps.Volume) error {
		if req.SnapshotRetention != nil {
			v.SnapshotRetention = *req.SnapshotRetention
		}
		if req.AutoBackupEnabled != nil {
			v.AutoBackupEnabled = *req.AutoBackupEnabled
		}
		return nil
	})
	if s.handleVolumeLookup(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.store.DeleteVolume(r.PathValue("app"), r.PathValue("vol"))
	if s.handleVolumeLookup(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, deleted)
}

// handleVolumeLookup writes a 404 for app/volume not-found and reports whether
// it wrote a response.
func (s *Server) handleVolumeLookup(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrAppNotFound):
		s.writeError(w, http.StatusNotFound, "app not found")
	case errors.Is(err, store.ErrVolumeNotFound):
		s.writeError(w, http.StatusNotFound, "volume not found")
	default:
		s.writeError(w, http.StatusInternalServerError, err.Error())
	}
	return true
}
