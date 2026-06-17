-- Canonical test dataset for the kql pg e2e suite (pkg/kql/pg_e2e_test.go).
-- Mirrors the in-memory sqlite dataset in pkg/kql/e2e_test.go so both backends
-- run identical assertions. Run with:
--   docker exec -i kql-pg psql -U kql -d kql < testdata/pg-seed.sql
--
-- NOTE: pg lowercases unquoted identifiers (EventType → eventtype). KQL
-- references the stored name; case-folding is tracked in backend/NOTES.md as
-- the rationale for ColID binding (DESIGN §5).
DROP TABLE IF EXISTS events;
CREATE TABLE events (id INTEGER PRIMARY KEY, state TEXT, damage REAL, eventtype TEXT);

INSERT INTO events VALUES
  (1, 'TEXAS',    1500.0, 'Hail'),
  (2, 'TEXAS',    3200.5, 'Wind'),
  (3, 'OKLAHOMA',  500.0, 'Flood'),
  (4, 'TEXAS',     100.0, 'Hail'),
  (5, 'FLORIDA',  9000.0, 'Hurricane'),
  (6, 'OKLAHOMA',  750.0, 'Wind');

SELECT count(*) AS seeded FROM events;
