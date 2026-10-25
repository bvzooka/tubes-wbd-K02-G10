package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

type ErrorPage struct {
	Status    int
	Heading   string
	Message   string
	RequestID string
}

func HandleError(render *Renderer, logger *slog.Logger) contract.ErrorHandler {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		switch {
		case errors.Is(err, domain.ErrUnauthenticated):
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		case errors.Is(err, domain.ErrForbidden):
			render.Page(w, r, http.StatusForbidden, "errors/403.html", ErrorPage{
				Status:  http.StatusForbidden,
				Heading: "Akses ditolak",
				Message: "Anda tidak memiliki hak untuk melakukan aksi atau melihat halaman ini.",
			})
		case errors.Is(err, domain.ErrNotFound):
			render.Page(w, r, http.StatusNotFound, "errors/404.html", ErrorPage{
				Status:  http.StatusNotFound,
				Heading: "Halaman tidak ditemukan",
				Message: "Sumber daya yang diminta tidak ada atau sudah dihapus.",
			})
		case errors.Is(err, domain.ErrConnectionFailed), errors.Is(err, domain.ErrConnectionInactive):
			// Pesan error koneksi sudah dijamin aman (tanpa kredensial) oleh driver.
			logger.WarnContext(r.Context(), "target database unavailable", "request_id", RequestID(r.Context()), "error", err)
			msg := "Database tujuan tidak dapat dijangkau saat ini. Coba lagi nanti atau hubungi admin."
			if errors.Is(err, domain.ErrConnectionInactive) {
				msg = "Koneksi database ini sedang dinonaktifkan oleh admin."
			}
			render.Page(w, r, http.StatusBadGateway, "errors/502.html", ErrorPage{
				Status:  http.StatusBadGateway,
				Heading: "Database tidak dapat dijangkau",
				Message: msg,
			})
		// User cuman liat nomor request, detailnya ada di log
		default:
			id := RequestID(r.Context())
			logger.ErrorContext(r.Context(), "unhandled error", "request_id", id, "error", err)
			render.Page(w, r, http.StatusInternalServerError, "errors/500.html", ErrorPage{
				Status:    http.StatusInternalServerError,
				Heading:   "Terjadi kesalahan",
				Message:   "Terjadi kesalahan pada server. Silakan coba lagi.",
				RequestID: id,
			})
		}
	}
}
