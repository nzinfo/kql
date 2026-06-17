package stats

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads a stats catalog from a YAML file. Missing optional fields
// (CorrVs, MCV, Hist, CostModel.CacheHitRate) are not errors — the catalog is
// valid without them. Unknown fields produce warnings (returned, not fatal) so
// that programmatically-generated catalogs (e.g. the pg collect script writing
// pg_oid/stats_target) don't fail to load.
//
// Version/Source are read from the file; an empty Version is accepted (treated
// as "unversioned") but callers may want to validate.
func Load(path string) (*Catalog, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parse(data)
}

// parse decodes YAML into a Catalog, collecting unknown-field warnings. It
// uses yaml.v3's strict-ish mode by decoding twice: once strict (to catch
// unknown fields as warnings), once lenient (to get the values). Unknown
// fields are warnings, not errors (O0.S3 revision: pg-collect writes extras).
func parse(data []byte) (*Catalog, []string, error) {
	// Decode lenient first to get the typed catalog (yaml tags map fields).
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, nil, fmt.Errorf("parse yaml: %w", err)
	}
	// Second pass into a generic map to detect unknown top-level fields /
	// unknown per-table-per-column fields for warnings.
	var raw map[string]interface{}
	warnings := []string{}
	if err := yaml.Unmarshal(data, &raw); err == nil {
		warnings = detectUnknownFields(raw)
	}
	if c.Tables == nil {
		c.Tables = map[string]*Table{}
	}
	for _, t := range c.Tables {
		if t.Columns == nil {
			t.Columns = map[string]*ColumnStats{}
		}
	}
	return &c, warnings, nil
}

// detectUnknownFields scans the raw decoded map for field names not in the
// known schema, returning human-readable warnings. This is a pragmatic check
// (top-level + table-level + column-level known keys); it doesn't recurse into
// arbitrary nested structures.
func detectUnknownFields(raw map[string]interface{}) []string {
	knownTop := map[string]bool{
		"version": true, "source": true, "schema": true,
		"tables": true, "views": true, "cost_model": true,
	}
	knownTable := map[string]bool{
		"row_count": true, "avg_row_bytes": true, "columns": true, "indexes": true,
	}
	knownColumn := map[string]bool{
		"card": true, "nulls": true, "type": true, "mcv": true, "hist": true, "corr_vs": true,
	}
	var warns []string
	for k := range raw {
		if !knownTop[k] {
			warns = append(warns, "unknown top-level field: "+k)
		}
	}
	if tables, ok := raw["tables"].(map[string]interface{}); ok {
		for tname, tv := range tables {
			tm, ok := tv.(map[string]interface{})
			if !ok {
				continue
			}
			for k := range tm {
				if !knownTable[k] {
					warns = append(warns, "unknown field on table "+tname+": "+k)
				}
			}
			if cols, ok := tm["columns"].(map[string]interface{}); ok {
				for cname, cv := range cols {
					cm, ok := cv.(map[string]interface{})
					if !ok {
						continue
					}
					for k := range cm {
						if !knownColumn[k] {
							warns = append(warns, "unknown field on "+tname+"."+cname+": "+k)
						}
					}
				}
			}
		}
	}
	return warns
}

// LoadFor loads a backend-specific catalog from the conventional directory
// layout `stats/<backend>/<schema>.yaml` (O0.S4 multi-backend isolation).
// backend is the dialect name (pg/duckdb/sqlite); path is the base stats dir.
func LoadFor(backend, baseDir, schema string) (*Catalog, []string, error) {
	path := baseDir + "/" + backend + "/" + schema + ".yaml"
	return Load(path)
}
