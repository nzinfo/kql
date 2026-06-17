package stats

import (
	"testing"
)

// TestLoadExample loads the example catalog and checks field round-trip.
func TestLoadExample(t *testing.T) {
	c, warns, err := Load("testdata/stormevents.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != "1" {
		t.Errorf("Version = %q, want 1", c.Version)
	}
	if c.Source != SourcePgAnalyze {
		t.Errorf("Source = %q, want pg_analyze", c.Source)
	}
	tbl, ok := c.Tables["StormEvents"]
	if !ok {
		t.Fatal("StormEvents table missing")
	}
	if tbl.RowCount != 1000000 {
		t.Errorf("RowCount = %d, want 1000000", tbl.RowCount)
	}
	if tbl.AvgRowBytes != 240 {
		t.Errorf("AvgRowBytes = %d, want 240", tbl.AvgRowBytes)
	}
	state := tbl.Columns["State"]
	if state == nil || state.Card != 62 {
		t.Errorf("State.Card = %v, want 62", state)
	}
	if state.MCV == nil || len(state.MCV.Values) != 4 {
		t.Errorf("State.MCV missing or wrong length")
	}
	if state.Hist == nil || state.Hist.Kind != HistEquiFreq {
		t.Errorf("State.Hist kind = %v, want equi_freq", state.Hist)
	}
	// CorrVs absent in example (pg doesn't provide) → nil, NOT an error.
	if state.CorrVs != nil {
		t.Error("State.CorrVs should be nil (pg doesn't expose ρ)")
	}
	// indexes
	if len(tbl.Indexes) != 2 {
		t.Fatalf("Indexes = %d, want 2", len(tbl.Indexes))
	}
	if tbl.Indexes[1].Name != "idx_event_type_state" || len(tbl.Indexes[1].Include) != 1 {
		t.Errorf("composite index wrong: %+v", tbl.Indexes[1])
	}
	// cost model
	if c.CostModel == nil || c.CostModel.RandomPageCost != 4.0 {
		t.Errorf("CostModel wrong: %+v", c.CostModel)
	}
	// no unknown-field warnings for the clean example
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got: %v", warns)
	}
}

// TestLoadMissingOptionalFields: a catalog without MCV/Hist/CorrVs loads fine.
func TestLoadMissingOptionalFields(t *testing.T) {
	yaml := []byte(`
version: "1"
source: manual
schema: test
tables:
  T:
    row_count: 100
    avg_row_bytes: 10
    columns:
      a:
        card: 50
        nulls: 5
        type: long
`)
	c, _, err := parse(yaml)
	if err != nil {
		t.Fatal(err)
	}
	col := c.Tables["T"].Columns["a"]
	if col.MCV != nil || col.Hist != nil || col.CorrVs != nil {
		t.Error("optional fields should be nil")
	}
	if col.Card != 50 || col.Nulls != 5 {
		t.Errorf("core fields = %d/%d, want 50/5", col.Card, col.Nulls)
	}
}

// TestUnknownFieldWarning: unknown fields warn, not error.
func TestUnknownFieldWarning(t *testing.T) {
	yaml := []byte(`
version: "1"
source: pg_analyze
schema: public
pg_oid: 12345
tables:
  T:
    row_count: 100
    pg_stats_target: 100
    columns:
      a:
        card: 10
        nulls: 0
        type: int
        extra_meta: foo
`)
	c, warns, err := parse(yaml)
	if err != nil {
		t.Fatalf("unknown fields should not error: %v", err)
	}
	// catalog still loads
	if c.Tables["T"].RowCount != 100 {
		t.Error("core data not loaded")
	}
	if len(warns) < 2 {
		t.Errorf("expected >=2 unknown-field warnings, got %d: %v", len(warns), warns)
	}
}

// TestConfidencePgAnalyzeFull: pg_analyze + all core fields → near 0.9.
func TestConfidencePgAnalyzeFull(t *testing.T) {
	c := &Catalog{Source: SourcePgAnalyze, Tables: map[string]*Table{
		"T": {Columns: map[string]*ColumnStats{
			"a": {Card: 10, Nulls: 1, MCV: &MCV{}, Hist: &Hist{}},
		}},
	}}
	conf := c.Confidence("T")
	if conf < 0.85 || conf > 0.95 {
		t.Errorf("pg_analyze full confidence = %v, want ~0.9", conf)
	}
}

// TestConfidenceManualMissingFields: manual + no mcv/hist → < 0.5.
func TestConfidenceManualMissingFields(t *testing.T) {
	c := &Catalog{Source: SourceManual, Tables: map[string]*Table{
		"T": {Columns: map[string]*ColumnStats{
			"a": {Card: 10, Nulls: 1}, // no mcv/hist
		}},
	}}
	conf := c.Confidence("T")
	if conf >= 0.5 {
		t.Errorf("manual missing-fields confidence = %v, want < 0.5", conf)
	}
}

// TestConfidenceCorrVsAbsenceNoPenalty: missing CorrVs must NOT lower confidence.
func TestConfidenceCorrVsAbsenceNoPenalty(t *testing.T) {
	withCorr := &Catalog{Source: SourcePgAnalyze, Tables: map[string]*Table{
		"T": {Columns: map[string]*ColumnStats{
			"a": {Card: 10, Nulls: 1, MCV: &MCV{}, Hist: &Hist{}, CorrVs: &CorrVs{}},
		}},
	}}
	withoutCorr := &Catalog{Source: SourcePgAnalyze, Tables: map[string]*Table{
		"T": {Columns: map[string]*ColumnStats{
			"a": {Card: 10, Nulls: 1, MCV: &MCV{}, Hist: &Hist{}}, // no CorrVs
		}},
	}}
	if withCorr.Confidence("T") != withoutCorr.Confidence("T") {
		t.Errorf("CorrVs presence changed confidence: %v vs %v (should be equal)",
			withCorr.Confidence("T"), withoutCorr.Confidence("T"))
	}
}

// TestConfidenceUnknownTable: unknown table → 0.
func TestConfidenceUnknownTable(t *testing.T) {
	c := &Catalog{Source: SourcePgAnalyze, Tables: map[string]*Table{}}
	if c.Confidence("nope") != 0 {
		t.Error("unknown table confidence should be 0")
	}
}

// TestColumnConfidence
func TestColumnConfidence(t *testing.T) {
	c := &Catalog{Source: SourcePgAnalyze, Tables: map[string]*Table{
		"T": {Columns: map[string]*ColumnStats{
			"full":   {Card: 10, Nulls: 1, MCV: &MCV{}, Hist: &Hist{}},
			"sparse": {Card: 10}, // missing nulls/mcv/hist
		}},
	}}
	if c.ColumnConfidence("T", "sparse") >= c.ColumnConfidence("T", "full") {
		t.Error("sparse column should have lower confidence than full")
	}
	if c.ColumnConfidence("T", "absent") != 0 {
		t.Error("absent column confidence should be 0")
	}
}

// TestSourceBaseConfidence
func TestSourceBaseConfidence(t *testing.T) {
	cases := map[Source]float64{
		SourcePgAnalyze: 0.9,
		SourceSampling:  0.7,
		SourceManual:    0.6,
	}
	for src, want := range cases {
		if got := src.baseConfidence(); got != want {
			t.Errorf("%s base = %v, want %v", src, got, want)
		}
	}
}
