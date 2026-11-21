package approval

import (
	"net/http"
	"time"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
)

// APIHandler melayani inbox leader dan tombol setuju/tolak.
type APIHandler struct {
	svc *Service
}

func NewAPIHandler(svc *Service) *APIHandler {
	return &APIHandler{svc: svc}
}

type inboxItemJSON struct {
	ExecutionID  int64                  `json:"execution_id"`
	ConnectionID int64                  `json:"connection_id"`
	Database     string                 `json:"database"`
	Version      int                    `json:"version"`
	Kind         domain.ExecutionKind   `json:"kind"`
	Mode         domain.MigrationMode   `json:"mode"`
	SQL          string                 `json:"sql"`
	RequestedBy  string                 `json:"requested_by"`
	RequestedAt  time.Time              `json:"requested_at"`
	GroupID      int64                  `json:"group_id"`
	GroupName    string                 `json:"group_name"`
	Status       domain.ExecutionStatus `json:"status"`
}

func (h *APIHandler) Inbox(w http.ResponseWriter, r *http.Request) error {
	items, err := h.svc.Inbox(r.Context(), apiv1.Actor(r).UserID)
	if err != nil {
		return err
	}
	out := make([]inboxItemJSON, 0, len(items))
	for _, p := range items {
		e := p.Execution
		out = append(out, inboxItemJSON{
			ExecutionID: e.ID, ConnectionID: e.ConnectionID, Database: p.Database,
			Version: e.Version, Kind: e.Kind, Mode: e.Mode, SQL: e.SQLSnapshot,
			RequestedBy: e.RequestedByName, RequestedAt: e.RequestedAt,
			GroupID: p.Approval.GroupID, GroupName: p.Approval.GroupName, Status: e.Status,
		})
	}
	return apiv1.OK(w, out)
}

func (h *APIHandler) Approve(w http.ResponseWriter, r *http.Request) error {
	return h.review(w, r, true)
}

func (h *APIHandler) Reject(w http.ResponseWriter, r *http.Request) error {
	return h.review(w, r, false)
}

func (h *APIHandler) review(w http.ResponseWriter, r *http.Request, approve bool) error {
	execID, err := apiv1.PathID(r, "execID")
	if err != nil {
		return err
	}
	groupID, err := apiv1.PathID(r, "groupID")
	if err != nil {
		return err
	}
	var in struct {
		Comment string `json:"comment"`
	}
	if r.ContentLength != 0 {
		if err := apiv1.Decode(w, r, &in); err != nil {
			return err
		}
	}
	status, err := h.svc.Review(r.Context(), apiv1.Actor(r), execID, groupID, approve, in.Comment)
	if err != nil {
		return err
	}
	return apiv1.OK(w, map[string]any{"execution_id": execID, "status": status})
}
