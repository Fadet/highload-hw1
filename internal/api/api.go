// Package api contains HTTP handlers of the items service.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Fadet/highload-hw1/internal/store"
)

// Router is the minimal registration interface. *http.ServeMux satisfies it;
// later it can be replaced by a wrapper that instruments every handler.
type Router interface {
	Handle(pattern string, handler http.Handler)
}

// ItemRequest is the request body for creating and updating items.
type ItemRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Handlers serves the business endpoints backed by a store.
type Handlers struct {
	store *store.Store
}

// New creates handlers backed by the given store.
func New(s *store.Store) *Handlers {
	return &Handlers{store: s}
}

// Register adds the business endpoints to the router.
func (h *Handlers) Register(r Router) {
	r.Handle("POST /items", http.HandlerFunc(h.createItem))
	r.Handle("GET /items/{id}", http.HandlerFunc(h.getItem))
	r.Handle("PUT /items/{id}", http.HandlerFunc(h.updateItem))
}

// Health reports that the service is ready to accept requests.
func Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handlers) createItem(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeItemRequest(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, h.store.Create(req.Name, req.Value))
}

func (h *Handlers) getItem(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	item, err := h.store.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handlers) updateItem(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	req, ok := decodeItemRequest(w, r)
	if !ok {
		return
	}
	item, err := h.store.Update(id, req.Name, req.Value)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

func decodeItemRequest(w http.ResponseWriter, r *http.Request) (ItemRequest, bool) {
	var req ItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return req, false
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return req, false
	}
	return req, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
