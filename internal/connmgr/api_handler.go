package connmgr

import (
	"context"
	"errors"
	"net/http"
	"time"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
)

// melayani /api/v1/admin/connections. semua route-nya Admin, dan service mengecek ulang hak admin jadi aturannya tetap berlaku dari pemanggil mana pun.
type APIHandler struct {
	svc *Service
}

// dibuat dengan service koneksi
func NewAPIHandler(svc *Service) *APIHandler { return &APIHandler{svc: svc} }

// bentuk koneksi di respons. Password tidak pernah ikut, bahkan yang terenkripsi.
type connectionJSON struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	DBMS         string    `json:"dbms"`
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	DatabaseName string    `json:"database_name"`
	Username     string    `json:"db_username"`
	SSLMode      string    `json:"ssl_mode"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// isian form koneksi, nama field JSON sama dgn kunci error validasi, biar SPA bisa menaruh pesan error di field yg tepat
// password kosong saat update berarti ga diubah
type connectionBody struct {
	Name         string `json:"name"`
	DBMS         string `json:"dbms"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	DatabaseName string `json:"database_name"`
	Username     string `json:"db_username"`
	Password     string `json:"password"`
	SSLMode      string `json:"ssl_mode"`
}

// menambahkan id opsional: saat mengedit, id dipakai untuk mengambil password tersimpan kalau isian password dikosongkan.
type testBody struct {
	ID int64 `json:"id"`
	connectionBody
}

// hasil uji koneksi. gagal terhubung bkn error HTTP: itu hasil uji yang normal, dan pesannya dijamin aman (tanpa kredensial) oleh driver.
type testResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// pesan hasil uji koneksi yang dibuat di sini
const (
	msgTestOK          = "Koneksi berhasil."
	msgTestUnsupported = "DBMS ini belum didukung."
)

// list menangani GET /admin/connections
func (h *APIHandler) List(w http.ResponseWriter, r *http.Request) error {
	conns, err := h.svc.List(r.Context(), apiv1.Actor(r))
	if err != nil {
		return err
	}
	out := make([]connectionJSON, 0, len(conns))
	for _, c := range conns {
		out = append(out, toConnectionJSON(c))
	}
	return apiv1.OK(w, out)
}

// menangani GET /admin/connections/{dbID}
func (h *APIHandler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	c, err := h.svc.Get(r.Context(), apiv1.Actor(r), id)
	if err != nil {
		return err
	}
	return apiv1.OK(w, toConnectionJSON(c))
}

// menangani POST /admin/connections
func (h *APIHandler) Create(w http.ResponseWriter, r *http.Request) error {
	var body connectionBody
	if err := apiv1.Decode(w, r, &body); err != nil {
		return err
	}
	c, err := h.svc.Create(r.Context(), apiv1.Actor(r), body.input())
	if err != nil {
		return err
	}
	return apiv1.Created(w, toConnectionJSON(c))
}

// menangani PUT /admin/connections/{dbID}
func (h *APIHandler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	var body connectionBody
	if err := apiv1.Decode(w, r, &body); err != nil {
		return err
	}
	c, err := h.svc.Update(r.Context(), apiv1.Actor(r), id, body.input())
	if err != nil {
		return err
	}
	return apiv1.OK(w, toConnectionJSON(c))
}

// menangani POST /admin/connections/{dbID}/activate
func (h *APIHandler) Activate(w http.ResponseWriter, r *http.Request) error {
	return h.setActive(w, r, true)
}

// menangani POST /admin/connections/{dbID}/deactivate
func (h *APIHandler) Deactivate(w http.ResponseWriter, r *http.Request) error {
	return h.setActive(w, r, false)
}

// mengubah status aktif lalu mengembalikan koneksi terbarunya
func (h *APIHandler) setActive(w http.ResponseWriter, r *http.Request, active bool) error {
	id, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	ctx, actor := r.Context(), apiv1.Actor(r)
	if err := h.svc.SetActive(ctx, actor, id, active); err != nil {
		return err
	}
	c, err := h.svc.Get(ctx, actor, id)
	if err != nil {
		return err
	}
	return apiv1.OK(w, toConnectionJSON(c))
}

// menangani POST /admin/connections/test. Validasi yang salah menjadi 422 seperti biasa, sedangkan gagal terhubung dikembalikan sebagai 200 dengan ok=false dan pesannya
func (h *APIHandler) Test(w http.ResponseWriter, r *http.Request) error {
	var body testBody
	if err := apiv1.Decode(w, r, &body); err != nil {
		return err
	}
	if body.ID < 0 {
		return domain.ErrNotFound
	}
	res, err := h.testConnection(r.Context(), apiv1.Actor(r), body)
	if err != nil {
		return err
	}
	return apiv1.OK(w, res)
}

// menerjemahkan hasil Service.TestConnection menjadi testResult
// error yang bukan hasil uji (validasi, akses ditolak, id tidak ada) dikembalikan sebagai error.
func (h *APIHandler) testConnection(ctx context.Context, p domain.Principal, body testBody) (testResult, error) {
	err := h.svc.TestConnection(ctx, p, body.ID, body.input())
	switch {
	case err == nil:
		return testResult{OK: true, Message: msgTestOK}, nil
	case errors.Is(err, domain.ErrConnectionFailed):
		return testResult{Message: err.Error()}, nil
	case errors.Is(err, domain.ErrUnsupported):
		return testResult{Message: msgTestUnsupported}, nil
	}
	return testResult{}, err
}

// mengubah isian JSON menjadi ConnectionInput milik service
func (b connectionBody) input() domain.ConnectionInput {
	return domain.ConnectionInput{
		Name: b.Name, DBMS: domain.DBMS(b.DBMS), Host: b.Host, Port: b.Port,
		DatabaseName: b.DatabaseName, Username: b.Username, Password: b.Password, SSLMode: b.SSLMode,
	}
}

// mengubah Connection domain menjadi bentuk respons, tanpa password
func toConnectionJSON(c domain.Connection) connectionJSON {
	return connectionJSON{
		ID: c.ID, Name: c.Name, DBMS: string(c.DBMS), Host: c.Host, Port: c.Port,
		DatabaseName: c.DatabaseName, Username: c.Username, SSLMode: c.SSLMode,
		IsActive: c.IsActive, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}
