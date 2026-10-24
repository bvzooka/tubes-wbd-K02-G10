package connmgr

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
)

const maxFormBytes = 64 << 10

// urutan tampilan di form
var sslModeOptions = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

type DBMSOption struct {
	Value string
	Label string
}
var dbmsOptions = []DBMSOption{{Value: string(domain.DBMSPostgres), Label: "PostgreSQL"}}

type ListPage struct {
	Connections []domain.Connection
}

type FormValues struct {
	Name string
	DBMS string
	Host string
	Port string
	DatabaseName string
	Username string
	SSLMode string
}

type FormPage struct {
	IsEdit bool
	ID int64
	Heading string
	Action string
	Values FormValues
	Errors map[string]string
	SSLModes []string
	DBMSOptions []DBMSOption
	TestMessage string
	TestOK bool
}

type Handler struct {
	svc *Service
	d httpx.Deps
}

func NewHandler(svc *Service, d httpx.Deps) *Handler { return &Handler{svc: svc, d: d} }

func (h *Handler) Register(mux *http.ServeMux) {
	admin := h.d.Admin()
	mux.Handle("GET /admin/connections", admin(http.HandlerFunc(h.list)))
	mux.Handle("GET /admin/connections/new", admin(http.HandlerFunc(h.newForm)))
	mux.Handle("POST /admin/connections", admin(http.HandlerFunc(h.create)))
	mux.Handle("POST /admin/connections/test", admin(http.HandlerFunc(h.test)))
	mux.Handle("GET /admin/connections/{dbID}/edit", admin(http.HandlerFunc(h.editForm)))
	mux.Handle("POST /admin/connections/{dbID}", admin(http.HandlerFunc(h.update)))
	mux.Handle("POST /admin/connections/{dbID}/deactivate", admin(http.HandlerFunc(h.setActive(false))))
	mux.Handle("POST /admin/connections/{dbID}/activate", admin(http.HandlerFunc(h.setActive(true))))
}

func (h *Handler) principal(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	p, ok := domain.PrincipalFrom(r.Context())
	if !ok {
		h.d.HandleErr(w, r, domain.ErrUnauthenticated)
	}
	return p, ok
}

func (h *Handler) connID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("dbID"), 10, 64)
	if err != nil || id < 1 {
		h.d.HandleErr(w, r, domain.ErrNotFound)
		return 0, false
	}
	return id, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	conns, err := h.svc.List(r.Context(), p)
	if err != nil {
		h.d.HandleErr(w, r, err)
		return
	}
	h.d.Render.Page(w, r, http.StatusOK, "connections/list.html", ListPage{Connections: conns})
}

func newFormPage() FormPage {
	return FormPage{
		Heading:     "Koneksi baru",
		Action:      "/admin/connections",
		Values:      FormValues{DBMS: string(domain.DBMSPostgres), Host: "localhost", Port: "5432", SSLMode: "prefer"},
		SSLModes:    sslModeOptions,
		DBMSOptions: dbmsOptions,
	}
}

func (h *Handler) newForm(w http.ResponseWriter, r *http.Request) {
	h.d.Render.Page(w, r, http.StatusOK, "connections/form.html", newFormPage())
}

func (h *Handler) editForm(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	id, ok := h.connID(w, r)
	if !ok {
		return
	}
	c, err := h.svc.Get(r.Context(), p, id)
	if err != nil {
		h.d.HandleErr(w, r, err)
		return
	}
	page := editFormPage(id)
	page.Values = FormValues{
		Name: c.Name, DBMS: string(c.DBMS), Host: c.Host, Port: strconv.Itoa(c.Port),
		DatabaseName: c.DatabaseName, Username: c.Username, SSLMode: c.SSLMode,
	}
	h.d.Render.Page(w, r, http.StatusOK, "connections/form.html", page)
}

func editFormPage(id int64) FormPage {
	pg := newFormPage()
	pg.IsEdit, pg.ID = true, id
	pg.Heading = "Ubah koneksi"
	pg.Action = "/admin/connections/" + strconv.FormatInt(id, 10)
	return pg
}

func parseForm(w http.ResponseWriter, r *http.Request) (domain.ConnectionInput, FormValues, map[string]string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		return domain.ConnectionInput{}, FormValues{}, nil, err
	}
	vals := FormValues{
		Name: r.PostFormValue("name"),
		DBMS: r.PostFormValue("dbms"),
		Host: r.PostFormValue("host"),
		Port: strings.TrimSpace(r.PostFormValue("port")),
		DatabaseName: r.PostFormValue("database_name"),
		Username: r.PostFormValue("db_username"),
		SSLMode: r.PostFormValue("ssl_mode"),
	}
	in := domain.ConnectionInput{
		Name: vals.Name,
		DBMS: domain.DBMS(vals.DBMS),
		Host: vals.Host,
		DatabaseName: vals.DatabaseName,
		Username: vals.Username,
		Password: r.PostFormValue("password"),
		SSLMode: vals.SSLMode,
	}
	var perrs map[string]string
	port, err := strconv.Atoi(vals.Port)
	if err != nil {
		perrs = map[string]string{"port": "Port harus berupa angka antara 1 sampai 65535."}
	} else {
		in.Port = port
	}
	return in, vals, perrs, nil
}

func (h *Handler) renderInvalid(w http.ResponseWriter, r *http.Request, page FormPage, vals FormValues, fields map[string]string) {
	page.Values, page.Errors = vals, fields
	h.d.Render.Page(w, r, http.StatusUnprocessableEntity, "connections/form.html", page)
}

func fieldErrors(parse map[string]string, err error) (map[string]string, bool) {
	out := map[string]string{}
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		for k, v := range ve.Fields {
			out[k] = v
		}
	}
	for k, v := range parse {
		out[k] = v
	}
	return out, len(out) > 0
}

func (h *Handler) badForm(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Form tidak valid atau terlalu besar.", http.StatusBadRequest)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	in, vals, perrs, err := parseForm(w, r)
	if err != nil {
		h.badForm(w, r)
		return
	}
	if len(perrs) == 0 {
		var c domain.Connection
		c, err = h.svc.Create(r.Context(), p, in)
		if err == nil {
			h.d.Flash.Set(w, httpx.FlashSuccess, "Koneksi \""+c.Name+"\" berhasil dibuat.")
			http.Redirect(w, r, "/admin/connections", http.StatusSeeOther)
			return
		}
	}
	if fields, bad := fieldErrors(perrs, err); bad {
		h.renderInvalid(w, r, newFormPage(), vals, fields)
		return
	}
	h.d.HandleErr(w, r, err)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	id, ok := h.connID(w, r)
	if !ok {
		return
	}
	in, vals, perrs, err := parseForm(w, r)
	if err != nil {
		h.badForm(w, r)
		return
	}
	if len(perrs) == 0 {
		var c domain.Connection
		c, err = h.svc.Update(r.Context(), p, id, in)
		if err == nil {
			h.d.Flash.Set(w, httpx.FlashSuccess, "Koneksi \""+c.Name+"\" berhasil diubah.")
			http.Redirect(w, r, "/admin/connections", http.StatusSeeOther)
			return
		}
	}
	if fields, bad := fieldErrors(perrs, err); bad {
		h.renderInvalid(w, r, editFormPage(id), vals, fields)
		return
	}
	h.d.HandleErr(w, r, err)
}

// test menguji koneksi dari isi form tanpa menyimpan. Field tersembunyi "id" (opsional)
// menandai form ubah sehingga password kosong memakai password tersimpan.
func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	in, vals, perrs, err := parseForm(w, r)
	if err != nil {
		h.badForm(w, r)
		return
	}
	var id int64
	if raw := strings.TrimSpace(r.PostFormValue("id")); raw != "" {
		if id, err = strconv.ParseInt(raw, 10, 64); err != nil || id < 1 {
			h.d.HandleErr(w, r, domain.ErrNotFound)
			return
		}
	}
	page := newFormPage()
	if id > 0 {
		page = editFormPage(id)
	}
	if len(perrs) > 0 {
		err = ValidateInput(in, id > 0)
		fields, _ := fieldErrors(perrs, err)
		h.renderInvalid(w, r, page, vals, fields)
		return
	}

	err = h.svc.TestConnection(r.Context(), p, id, in)
	var ve *domain.ValidationError
	switch {
	case err == nil:
		page.TestOK, page.TestMessage = true, "Koneksi berhasil."
	case errors.As(err, &ve):
		h.renderInvalid(w, r, page, vals, ve.Fields)
		return
	case errors.Is(err, domain.ErrConnectionFailed):
		page.TestMessage = err.Error() // pesan sudah aman (tanpa kredensial)
	case errors.Is(err, domain.ErrUnsupported):
		page.TestMessage = "DBMS ini belum didukung."
	default:
		h.d.HandleErr(w, r, err)
		return
	}
	page.Values = vals
	h.d.Render.Page(w, r, http.StatusOK, "connections/form.html", page)
}

func (h *Handler) setActive(active bool) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := h.principal(w, r)
		if !ok {
			return
		}
		id, ok := h.connID(w, r)
		if !ok {
			return
		}
		if err := h.svc.SetActive(r.Context(), p, id, active); err != nil {
			h.d.HandleErr(w, r, err)
			return
		}
		msg := "Koneksi dinonaktifkan."
		if active {
			msg = "Koneksi diaktifkan."
		}
		h.d.Flash.Set(w, httpx.FlashSuccess, msg)
		http.Redirect(w, r, "/admin/connections", http.StatusSeeOther)
	}
}
