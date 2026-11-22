package httpx

import (
	"log/slog"
	"net/http"

	"dbplatform/internal/telemetry"
)

// catat kegagalan render template lengkap dengan ID request supaya bisa dicari di log
func logError(r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "render failed", "request_id", telemetry.CorrelationID(r.Context()), "error", err)
}
