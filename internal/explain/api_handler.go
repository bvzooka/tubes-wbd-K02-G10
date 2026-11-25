package explain

import (
	"net/http"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
)

type APIHandler struct {
	svc runner
}

func NewAPIHandler(svc runner) *APIHandler {
	return &APIHandler{svc: svc}
}

type resultJSON struct {
	SQL     string      `json:"sql"`
	Analyze bool        `json:"analyze"`
	Dialect domain.DBMS `json:"dialect"`
	Plan    *planJSON   `json:"plan"` // null di MySQL/MariaDB
	RawText string      `json:"raw_text"`
}

type planJSON struct {
	TotalCost       float64    `json:"total_cost"`
	PlanningTimeMs  *float64   `json:"planning_time_ms"`
	ExecutionTimeMs *float64   `json:"execution_time_ms"` // cuma ada kalau ANALYZE
	Nodes           []nodeJSON `json:"nodes"`
	JSON            string     `json:"json"`
}

type nodeJSON struct {
	Depth        int     `json:"depth"`
	NodeType     string  `json:"node_type"`
	Relationship string  `json:"relationship,omitempty"`
	Relation     string  `json:"relation,omitempty"`
	Alias        string  `json:"alias,omitempty"`
	IndexName    string  `json:"index_name,omitempty"`
	JoinType     string  `json:"join_type,omitempty"`
	Condition    string  `json:"condition,omitempty"`
	StartupCost  float64 `json:"startup_cost"`
	TotalCost    float64 `json:"total_cost"`
	PlanRows     float64 `json:"plan_rows"`
	PlanWidth    int     `json:"plan_width"`

	Analyzed        bool    `json:"analyzed"`
	ActualStartupMs float64 `json:"actual_startup_ms"`
	ActualTotalMs   float64 `json:"actual_total_ms"`
	ActualRows      float64 `json:"actual_rows"`
	ActualLoops     float64 `json:"actual_loops"`
	SharedHit       int64   `json:"shared_hit"`
	SharedRead      int64   `json:"shared_read"`
}

func (h *APIHandler) Run(w http.ResponseWriter, r *http.Request) error {
	dbID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	var in struct {
		SQL     string `json:"sql"`
		Analyze bool   `json:"analyze"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}

	res, err := h.svc.Run(r.Context(), apiv1.Actor(r), dbID, in.SQL, in.Analyze)
	if msg := userMessage(err); msg != "" {
		// pesan dari input user aman ditampilkan apa adanya
		return &domain.MessageError{Err: err, Message: msg}
	}
	if err != nil {
		return err
	}
	return apiv1.OK(w, toResultJSON(res))
}

func toResultJSON(res *Result) resultJSON {
	out := resultJSON{SQL: res.SQL, Analyze: res.Analyze, Dialect: res.Dialect, RawText: res.RawText}
	if p := res.Plan; p != nil {
		out.Plan = &planJSON{
			TotalCost:       p.TotalCost,
			PlanningTimeMs:  p.PlanningTimeMs,
			ExecutionTimeMs: p.ExecutionTimeMs,
			Nodes:           make([]nodeJSON, 0, len(p.Nodes)),
			JSON:            p.PrettyJSON,
		}
		for _, n := range p.Nodes {
			out.Plan.Nodes = append(out.Plan.Nodes, nodeJSON{
				Depth: n.Depth, NodeType: n.NodeType, Relationship: n.Relationship, Relation: n.Relation,
				Alias: n.Alias, IndexName: n.IndexName, JoinType: n.JoinType, Condition: n.Condition,
				StartupCost: n.StartupCost, TotalCost: n.TotalCost, PlanRows: n.PlanRows, PlanWidth: n.PlanWidth,
				Analyzed: n.Analyzed, ActualStartupMs: n.ActualStartupMs, ActualTotalMs: n.ActualTotalMs,
				ActualRows: n.ActualRows, ActualLoops: n.ActualLoops, SharedHit: n.SharedHit, SharedRead: n.SharedRead,
			})
		}
	}
	return out
}
