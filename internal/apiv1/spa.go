package apiv1

import (
	"io/fs"
	"net/http"
	"path"
)

// SPA melayani file hasil build frontend. Path yang bukan file (misalnya /admin/users)
// dijawab index.html agar React Router yang menentukan halamannya.
func SPA(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SPA hanya menyajikan file, jadi method lain ditolak daripada dijawab halaman HTML
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method tidak diizinkan", http.StatusMethodNotAllowed)
			return
		}
		name := path.Clean(r.URL.Path)[1:]
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(dist, name); err != nil {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, dist, "index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}
