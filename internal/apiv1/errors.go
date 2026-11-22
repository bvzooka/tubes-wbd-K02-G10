package apiv1

import (
	"errors"
	"log/slog"
	"net/http"

	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

type APIError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id"`
}

type errorRule struct {
	target  error
	status  int
	code    string
	message string
}

// Urutan penting: aturan pertama yang cocok dipakai.
var errorRules = []errorRule{
	{domain.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated", "Silakan login terlebih dahulu."},
	{domain.ErrRateLimited, http.StatusTooManyRequests, "rate_limited", "Terlalu banyak percobaan. Coba lagi nanti."},
	{domain.ErrForbidden, http.StatusForbidden, "forbidden", "Anda tidak memiliki akses untuk aksi ini."},
	{domain.ErrNotFound, http.StatusNotFound, "not_found", "Data tidak ditemukan."},
	{domain.ErrValidation, http.StatusUnprocessableEntity, "validation", "Data tidak valid."},
	{domain.ErrUnsupported, http.StatusUnprocessableEntity, "unsupported", "Operasi ini tidak didukung."},
	{domain.ErrNotReversible, http.StatusUnprocessableEntity, "not_reversible", "Migration ini tidak dapat di-rollback."},
	{domain.ErrMigrationOutOfOrder, http.StatusConflict, "out_of_order", "Urutan migration tidak terpenuhi."},
	{domain.ErrDatabaseBusy, http.StatusConflict, "database_busy", "Database sedang menjalankan migration lain."},
	{domain.ErrDatabaseBlocked, http.StatusConflict, "database_blocked", "Database terblok sampai migration bermasalah dipulihkan."},
	{domain.ErrApprovalRequired, http.StatusConflict, "approval_required", "Permintaan ini belum disetujui semua leader."},
	{domain.ErrConflict, http.StatusConflict, "conflict", "Data sudah berubah atau bentrok dengan data lain."},
	{domain.ErrConnectionInactive, http.StatusBadGateway, "connection_inactive", "Koneksi database ini sedang dinonaktifkan."},
	{domain.ErrConnectionFailed, http.StatusBadGateway, "connection_failed", "Database tujuan tidak dapat dijangkau."},
}

// WriteError memetakan error ke JSON {"error": {...}}. Error yang tidak dikenal menjadi
// 500 dengan pesan umum; detailnya hanya ada di log.
func WriteError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		apiErr, status := toAPIError(err)
		apiErr.RequestID = telemetry.CorrelationID(r.Context())
		if status >= 500 {
			logger.ErrorContext(r.Context(), "request failed", "event", "http.error", "status", status, "error", err)
		}
		_ = write(w, status, map[string]APIError{"error": apiErr}) // klien sudah putus bila gagal
	}
}

func toAPIError(err error) (APIError, int) {
	for _, rule := range errorRules {
		if errors.Is(err, rule.target) {
			apiErr := APIError{Code: rule.code, Message: rule.message}
			if ve, ok := errors.AsType[*domain.ValidationError](err); ok {
				apiErr.Fields = ve.Fields
				if ve.Message != "" {
					apiErr.Message = ve.Message
				}
			}
			if me, ok := errors.AsType[*domain.MessageError](err); ok {
				apiErr.Message = me.Message
			}
			return apiErr, rule.status
		}
	}
	return APIError{Code: "internal", Message: "Terjadi kesalahan pada server."}, http.StatusInternalServerError
}
