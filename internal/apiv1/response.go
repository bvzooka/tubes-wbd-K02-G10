// Package apiv1 adalah lapisan JSON untuk /api/v1: router berlevel akses, envelope
// respons, pemetaan error domain, dan decoder request.
package apiv1

import (
	"encoding/json"
	"net/http"
)

type envelope struct {
	Data any            `json:"data"`
	Meta map[string]any `json:"meta,omitempty"`
}

func OK(w http.ResponseWriter, data any) error {
	return write(w, http.StatusOK, envelope{Data: data})
}

func Created(w http.ResponseWriter, data any) error {
	return write(w, http.StatusCreated, envelope{Data: data})
}

func Accepted(w http.ResponseWriter, data any) error {
	return write(w, http.StatusAccepted, envelope{Data: data})
}

func NoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func write(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}
