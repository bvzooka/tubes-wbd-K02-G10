package migration

import (
	"context"
	"math"
	"net/http"
	"regexp"
	"strconv"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
)

// ID job adalah UUID, bentuk lain ditolak sebelum sampai ke database
var jobIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// APIHandler hanya membaca request, memanggil service, dan menulis JSON, tanpa aturan bisnis
type APIHandler struct {
	definitions *DefinitionService
	executions  *ExecutionService
}

func NewAPIHandler(definitions *DefinitionService, executions *ExecutionService) *APIHandler {
	return &APIHandler{definitions: definitions, executions: executions}
}

type draftRequest struct {
	Name               string               `json:"name"`
	SQLUp              string               `json:"sql_up"`
	SQLDown            string               `json:"sql_down"`
	Irreversible       bool                 `json:"irreversible"`
	IrreversibleReason string               `json:"irreversible_reason"`
	Mode               domain.MigrationMode `json:"mode"`
	Batch              *domain.BatchConfig  `json:"batch"`
	Revision           int                  `json:"revision"` // dipakai saat mengubah draft
}

func (r draftRequest) input() DraftInput {
	return DraftInput{
		Name: r.Name, UpSQL: r.SQLUp, DownSQL: r.SQLDown, Irreversible: r.Irreversible,
		IrreversibleReason: r.IrreversibleReason, Mode: r.Mode, Batch: r.Batch,
	}
}

// versionPath membaca ID database dan nomor versi dari URL
func versionPath(r *http.Request) (int64, int, error) {
	connID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return 0, 0, err
	}
	version, err := apiv1.PathID(r, "version")
	if err != nil {
		return 0, 0, err
	}
	// kolom version bertipe INTEGER, jadi angka yang lebih besar pasti tidak ada
	if version > math.MaxInt32 {
		return 0, 0, domain.ErrNotFound
	}
	return connID, int(version), nil
}

func (h *APIHandler) List(w http.ResponseWriter, r *http.Request) error {
	connID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	list, err := h.definitions.List(r.Context(), apiv1.Actor(r), connID)
	if err != nil {
		return err
	}
	canEdit, err := h.executions.CanEdit(r.Context(), apiv1.Actor(r), connID)
	if err != nil {
		return err
	}
	return apiv1.OK(w, map[string]any{"items": mapSlice(list, toDefinitionJSON), "can_edit": canEdit})
}

func (h *APIHandler) Get(w http.ResponseWriter, r *http.Request) error {
	connID, version, err := versionPath(r)
	if err != nil {
		return err
	}
	ctx, actor := r.Context(), apiv1.Actor(r)
	d, err := h.definitions.Get(ctx, actor, connID, version)
	if err != nil {
		return err
	}
	executions, err := h.executions.ForVersion(ctx, actor, connID, version)
	if err != nil {
		return err
	}
	canEdit, err := h.executions.CanEdit(ctx, actor, connID)
	if err != nil {
		return err
	}
	return apiv1.OK(w, map[string]any{
		"migration": toDefinitionJSON(d), "executions": mapSlice(executions, toExecutionJSON), "can_edit": canEdit,
	})
}

func (h *APIHandler) Create(w http.ResponseWriter, r *http.Request) error {
	connID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	var in draftRequest
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	d, err := h.definitions.Create(r.Context(), apiv1.Actor(r), connID, in.input())
	if err != nil {
		return err
	}
	return apiv1.Created(w, toDefinitionJSON(d))
}

func (h *APIHandler) Update(w http.ResponseWriter, r *http.Request) error {
	connID, version, err := versionPath(r)
	if err != nil {
		return err
	}
	var in draftRequest
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	d, err := h.definitions.Update(r.Context(), apiv1.Actor(r), connID, version, in.Revision, in.input())
	if err != nil {
		return err
	}
	return apiv1.OK(w, toDefinitionJSON(d))
}

func (h *APIHandler) Freeze(w http.ResponseWriter, r *http.Request) error {
	connID, version, err := versionPath(r)
	if err != nil {
		return err
	}
	var in struct {
		Revision int `json:"revision"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	d, err := h.definitions.Freeze(r.Context(), apiv1.Actor(r), connID, version, in.Revision)
	if err != nil {
		return err
	}
	return apiv1.OK(w, toDefinitionJSON(d))
}

func (h *APIHandler) Cancel(w http.ResponseWriter, r *http.Request) error {
	return h.change(w, r, h.definitions.Cancel)
}

func (h *APIHandler) Supersede(w http.ResponseWriter, r *http.Request) error {
	return h.change(w, r, h.definitions.Supersede)
}

func (h *APIHandler) Reopen(w http.ResponseWriter, r *http.Request) error {
	return h.change(w, r, h.definitions.Reopen)
}

type definitionAction func(ctx context.Context, actor domain.Principal, connID int64, version int) (Definition, error)

// change dipakai tiga aksi ganti status yang bentuk request dan responsnya sama
func (h *APIHandler) change(w http.ResponseWriter, r *http.Request, action definitionAction) error {
	connID, version, err := versionPath(r)
	if err != nil {
		return err
	}
	d, err := action(r.Context(), apiv1.Actor(r), connID, version)
	if err != nil {
		return err
	}
	return apiv1.OK(w, toDefinitionJSON(d))
}

func (h *APIHandler) Request(w http.ResponseWriter, r *http.Request) error {
	connID, version, err := versionPath(r)
	if err != nil {
		return err
	}
	var in struct {
		Kind   domain.ExecutionKind `json:"kind"`
		Resume bool                 `json:"resume"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	e, err := h.executions.Request(r.Context(), apiv1.Actor(r), connID, version, in.Kind, in.Resume)
	if err != nil {
		return err
	}
	return apiv1.Created(w, toExecutionJSON(e))
}

func (h *APIHandler) History(w http.ResponseWriter, r *http.Request) error {
	connID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	query := r.URL.Query()
	// halaman yang bukan angka dianggap halaman pertama
	page, _ := strconv.Atoi(query.Get("page"))
	filter := HistoryFilter{
		HideRollback: query.Get("hide_rollback") == "1",
		Status:       query.Get("status"),
		Page:         max(page, 1),
	}
	entries, hasNext, err := h.executions.History(r.Context(), apiv1.Actor(r), connID, filter)
	if err != nil {
		return err
	}
	return apiv1.OK(w, map[string]any{
		"items": mapSlice(entries, toHistoryJSON), "page": filter.Page, "has_next": hasNext,
	})
}

func (h *APIHandler) Detail(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "execID")
	if err != nil {
		return err
	}
	d, err := h.executions.Detail(r.Context(), apiv1.Actor(r), id)
	if err != nil {
		return err
	}
	return apiv1.OK(w, map[string]any{
		"execution": toExecutionJSON(d.Execution),
		"approvals": mapSlice(d.Approvals, toApprovalJSON),
		"attempts":  mapSlice(d.Attempts, toHistoryJSON),
		"job_id":    d.JobID,
		"can_edit":  d.CanEdit,
	})
}

func (h *APIHandler) CancelExecution(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "execID")
	if err != nil {
		return err
	}
	if err := h.executions.Cancel(r.Context(), apiv1.Actor(r), id); err != nil {
		return err
	}
	return apiv1.NoContent(w)
}

// Run menjawab 202 karena SQL belum dijalankan, hasilnya dipantau lewat job ID
func (h *APIHandler) Run(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "execID")
	if err != nil {
		return err
	}
	jobID, err := h.executions.Run(r.Context(), apiv1.Actor(r), id)
	if err != nil {
		return err
	}
	return apiv1.Accepted(w, map[string]string{"job_id": jobID})
}

func (h *APIHandler) Job(w http.ResponseWriter, r *http.Request) error {
	jobID := r.PathValue("jobID")
	if !jobIDPattern.MatchString(jobID) {
		return domain.ErrNotFound
	}
	status, err := h.executions.Job(r.Context(), apiv1.Actor(r), jobID)
	if err != nil {
		return err
	}
	job := status.Job
	return apiv1.OK(w, map[string]any{
		"id": job.ID, "execution_id": job.ExecutionID, "state": job.State, "attempt": job.Attempt,
		"max_attempts": job.MaxAttempts, "run_after": job.RunAfter, "last_error": job.LastError,
		"execution_status": status.Execution.Status,
	})
}
