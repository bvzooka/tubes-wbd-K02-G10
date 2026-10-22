package explain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// PlanNode adalah satu node plan PostgreSQL yang sudah diratakan beserta kedalamannya.
type PlanNode struct {
	Depth        int
	Prefix       string // indentasi teks untuk tampilan (tanpa CSS inline)
	NodeType     string
	Relationship string // Parent Relationship: Outer, Inner, SubPlan, ...
	Relation     string // schema.tabel jika ada
	Alias        string
	IndexName    string
	JoinType     string
	Condition    string // Index Cond / Hash Cond / Merge Cond / Join Filter / Filter
	StartupCost  float64
	TotalCost    float64
	PlanRows     float64
	PlanWidth    int
	// Hanya terisi untuk EXPLAIN ANALYZE.
	Analyzed        bool
	ActualStartupMs float64
	ActualTotalMs   float64
	ActualRows      float64
	ActualLoops     float64
	SharedHit       int64
	SharedRead      int64
}

// Plan adalah hasil parse output EXPLAIN (FORMAT JSON).
type Plan struct {
	Nodes           []PlanNode
	PlanningTimeMs  *float64
	ExecutionTimeMs *float64
	TotalCost       float64
	PrettyJSON      string
}

type rawExplain struct {
	Plan          rawNode  `json:"Plan"`
	PlanningTime  *float64 `json:"Planning Time"`
	ExecutionTime *float64 `json:"Execution Time"`
}

type rawNode struct {
	NodeType        string    `json:"Node Type"`
	ParentRel       string    `json:"Parent Relationship"`
	RelationName    string    `json:"Relation Name"`
	Schema          string    `json:"Schema"`
	Alias           string    `json:"Alias"`
	IndexName       string    `json:"Index Name"`
	JoinType        string    `json:"Join Type"`
	IndexCond       string    `json:"Index Cond"`
	HashCond        string    `json:"Hash Cond"`
	MergeCond       string    `json:"Merge Cond"`
	JoinFilter      string    `json:"Join Filter"`
	Filter          string    `json:"Filter"`
	StartupCost     float64   `json:"Startup Cost"`
	TotalCost       float64   `json:"Total Cost"`
	PlanRows        float64   `json:"Plan Rows"`
	PlanWidth       int       `json:"Plan Width"`
	ActualStartup   *float64  `json:"Actual Startup Time"`
	ActualTotal     *float64  `json:"Actual Total Time"`
	ActualRows      *float64  `json:"Actual Rows"`
	ActualLoops     *float64  `json:"Actual Loops"`
	SharedHitBlocks int64     `json:"Shared Hit Blocks"`
	SharedReadBlock int64     `json:"Shared Read Blocks"`
	Plans           []rawNode `json:"Plans"`
}

// ParsePostgresPlan mem-parse output EXPLAIN (FORMAT JSON) PostgreSQL.
func ParsePostgresPlan(raw string) (*Plan, error) {
	var docs []rawExplain
	if err := json.Unmarshal([]byte(raw), &docs); err != nil {
		return nil, fmt.Errorf("parse explain json: %w", err)
	}
	if len(docs) == 0 || docs[0].Plan.NodeType == "" {
		return nil, fmt.Errorf("parse explain json: empty plan")
	}
	doc := docs[0]
	p := &Plan{
		PlanningTimeMs:  doc.PlanningTime,
		ExecutionTimeMs: doc.ExecutionTime,
		TotalCost:       doc.Plan.TotalCost,
	}
	flatten(&p.Nodes, doc.Plan, 0)

	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err == nil {
		p.PrettyJSON = buf.String()
	} else {
		p.PrettyJSON = raw
	}
	return p, nil
}

func flatten(out *[]PlanNode, n rawNode, depth int) {
	node := PlanNode{
		Depth:        depth,
		Prefix:       prefix(depth),
		NodeType:     n.NodeType,
		Relationship: n.ParentRel,
		Alias:        n.Alias,
		IndexName:    n.IndexName,
		JoinType:     n.JoinType,
		Condition:    firstNonEmpty(n.IndexCond, n.HashCond, n.MergeCond, n.JoinFilter, n.Filter),
		StartupCost:  n.StartupCost,
		TotalCost:    n.TotalCost,
		PlanRows:     n.PlanRows,
		PlanWidth:    n.PlanWidth,
		SharedHit:    n.SharedHitBlocks,
		SharedRead:   n.SharedReadBlock,
	}
	if n.RelationName != "" {
		node.Relation = n.RelationName
		if n.Schema != "" {
			node.Relation = n.Schema + "." + n.RelationName
		}
	}
	if n.ActualTotal != nil {
		node.Analyzed = true
		node.ActualStartupMs = deref(n.ActualStartup)
		node.ActualTotalMs = deref(n.ActualTotal)
		node.ActualRows = deref(n.ActualRows)
		node.ActualLoops = deref(n.ActualLoops)
	}
	*out = append(*out, node)
	for _, child := range n.Plans {
		flatten(out, child, depth+1)
	}
}

func prefix(depth int) string {
	if depth == 0 {
		return ""
	}
	return strings.Repeat("   ", depth-1) + "└─ "
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
