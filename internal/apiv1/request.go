package apiv1

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"dbplatform/internal/domain"
)

const maxBodyBytes = 1 << 20

// Decode membaca body JSON ke dst. Body lebih dari 1 MB, field tak dikenal, atau JSON
// rusak menjadi ValidationError (422).
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			return &domain.ValidationError{Message: "Body request terlalu besar (maksimal 1 MB)."}
		case errors.Is(err, io.EOF):
			return &domain.ValidationError{Message: "Body request kosong."}
		default:
			return &domain.ValidationError{Message: "Body request bukan JSON yang valid: " + err.Error()}
		}
	}
	if dec.More() {
		return &domain.ValidationError{Message: "Body request hanya boleh berisi satu objek JSON."}
	}
	return nil
}

// PathID membaca parameter path berupa ID positif. ID rusak dianggap tidak ditemukan.
func PathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.ErrNotFound
	}
	return id, nil
}

// Actor mengembalikan user yang sedang login. Selalu ada di route selain Public.
func Actor(r *http.Request) domain.Principal {
	p, _ := domain.PrincipalFrom(r.Context())
	return p
}
