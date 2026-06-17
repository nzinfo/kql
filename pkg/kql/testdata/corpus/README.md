# Real-world KQL corpus

Extracted from the `.source-projects/kql-parser` reference's
`fuzz_corpus_test.go` (Microsoft Sentinel / Defender XDR / community hunting
queries; see that file's header for upstream sources and license).

Each `*.kql` file is one real-world query, named `<NN>_<Name>.kql>` where `NN`
is the extraction order. The corpus is used by `pkg/kql/corpus_test.go` to
measure parser/translator coverage and guard against regressions (T1–T3).

Re-extraction:

```bash
cd /tmp/extract  # the extractor's isolated module
go build -o extractor main.go
./extractor <repo>/.source-projects/kql-parser/fuzz_corpus_test.go <repo>/testdata/corpus/sentinel
```

The extractor source lives at `/tmp/extract/main.go` (a throwaway go/ast
walker); if it's gone, recreate from `git log` of this commit.

The corpus queries are **not executed** (they reference Sentinel tables we
don't have). They validate the parse → translate → emit surface only.
