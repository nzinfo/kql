# Golden snapshot tests (T4)

`pkg/kql/golden_test.go` emits a representative query set through both backends
and compares the emitted SQL against these snapshots. Any emit/optimizer change
that alters the SQL is caught here before reaching a DB.

## Files

Each `<case>.{sqlite,pg}.sql` holds the normalised emitted SQL for that case
on that backend. Cases live in `goldenCases` in golden_test.go.

## Updating snapshots

After an **intentional** emit change (a new optimizer rule, a dialect fix,
a renamed alias), regenerate:

```bash
go test ./pkg/kql/ -run TestGolden -update
```

Then review the diff with `git diff pkg/kql/testdata/golden/` and commit. An
**unintentional** change shows up as a test failure with want/got — investigate
before reaching for `-update`.

## Adding a case

Add a `{name, query}` entry to `goldenCases`, run with `-update`, commit the
new snapshot files. Cover any new emit path (new operator, function, optimizer
rule) so it's locked.
