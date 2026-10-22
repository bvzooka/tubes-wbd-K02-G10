package explain

import "testing"

const sampleAnalyzeJSON = `[
  {
    "Plan": {
      "Node Type": "Hash Join",
      "Parallel Aware": false,
      "Join Type": "Inner",
      "Startup Cost": 4.70,
      "Total Cost": 12.34,
      "Plan Rows": 300,
      "Plan Width": 48,
      "Actual Startup Time": 0.120,
      "Actual Total Time": 0.456,
      "Actual Rows": 300,
      "Actual Loops": 1,
      "Hash Cond": "(o.customer_id = c.id)",
      "Shared Hit Blocks": 5,
      "Shared Read Blocks": 1,
      "Plans": [
        {
          "Node Type": "Seq Scan",
          "Parent Relationship": "Outer",
          "Relation Name": "orders",
          "Schema": "public",
          "Alias": "o",
          "Startup Cost": 0.00,
          "Total Cost": 5.00,
          "Plan Rows": 300,
          "Plan Width": 24,
          "Actual Startup Time": 0.010,
          "Actual Total Time": 0.100,
          "Actual Rows": 300,
          "Actual Loops": 1
        },
        {
          "Node Type": "Hash",
          "Parent Relationship": "Inner",
          "Startup Cost": 3.20,
          "Total Cost": 3.20,
          "Plan Rows": 120,
          "Plan Width": 24,
          "Actual Startup Time": 0.050,
          "Actual Total Time": 0.050,
          "Actual Rows": 120,
          "Actual Loops": 1,
          "Plans": [
            {
              "Node Type": "Index Scan",
              "Parent Relationship": "Outer",
              "Relation Name": "customers",
              "Schema": "public",
              "Alias": "c",
              "Index Name": "customers_pkey",
              "Index Cond": "(id > 0)",
              "Startup Cost": 0.14,
              "Total Cost": 3.20,
              "Plan Rows": 120,
              "Plan Width": 24,
              "Actual Startup Time": 0.005,
              "Actual Total Time": 0.030,
              "Actual Rows": 120,
              "Actual Loops": 1
            }
          ]
        }
      ]
    },
    "Planning Time": 0.321,
    "Triggers": [],
    "Execution Time": 0.789
  }
]`

func TestParsePostgresPlan_Analyze(t *testing.T) {
	p, err := ParsePostgresPlan(sampleAnalyzeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 4 {
		t.Fatalf("nodes = %d", len(p.Nodes))
	}
	if p.PlanningTimeMs == nil || *p.PlanningTimeMs != 0.321 || p.ExecutionTimeMs == nil || *p.ExecutionTimeMs != 0.789 {
		t.Fatalf("waktu: %v %v", p.PlanningTimeMs, p.ExecutionTimeMs)
	}
	if p.TotalCost != 12.34 {
		t.Fatalf("total cost = %v", p.TotalCost)
	}
	root := p.Nodes[0]
	if root.NodeType != "Hash Join" || root.JoinType != "Inner" || root.Condition != "(o.customer_id = c.id)" ||
		root.Depth != 0 || root.Prefix != "" || !root.Analyzed || root.SharedHit != 5 || root.SharedRead != 1 {
		t.Fatalf("root = %+v", root)
	}
	seq := p.Nodes[1]
	if seq.NodeType != "Seq Scan" || seq.Relation != "public.orders" || seq.Alias != "o" || seq.Depth != 1 || seq.Relationship != "Outer" {
		t.Fatalf("seq = %+v", seq)
	}
	idx := p.Nodes[3]
	if idx.NodeType != "Index Scan" || idx.IndexName != "customers_pkey" || idx.Depth != 2 || idx.ActualRows != 120 || idx.Condition != "(id > 0)" {
		t.Fatalf("idx = %+v", idx)
	}
	if p.PrettyJSON == "" {
		t.Fatal("PrettyJSON kosong")
	}
}

func TestParsePostgresPlan_WithoutAnalyze(t *testing.T) {
	p, err := ParsePostgresPlan(`[{"Plan": {"Node Type": "Seq Scan", "Relation Name": "t", "Alias": "t", "Startup Cost": 0, "Total Cost": 1.5, "Plan Rows": 10, "Plan Width": 4}}]`)
	if err != nil {
		t.Fatal(err)
	}
	if p.Nodes[0].Analyzed || p.ExecutionTimeMs != nil || p.Nodes[0].Relation != "t" {
		t.Fatalf("plan = %+v", p.Nodes[0])
	}
}

func TestParsePostgresPlan_Invalid(t *testing.T) {
	for _, in := range []string{"", "not json", "[]", `[{"Plan": {}}]`} {
		if _, err := ParsePostgresPlan(in); err == nil {
			t.Errorf("input %q harus error", in)
		}
	}
}
