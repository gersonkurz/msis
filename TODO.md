# TODO

Follow-up tasks from the review loop. Checks that need a real machine (an elevated
install, not just a green build) live in [`todo-testme.md`](todo-testme.md) instead.

## Non-preserved literal REG_SZ content can silently change type

Reviewer finding from the #11 review, recorded verbatim (2026-09-17):

> **[task] Non-preserved literal REG_SZ content can silently change type through
> MSI formatting.** At
> [registry.go:722](C:/NGBT/MSIS/msis-3.x/internal/registry/registry.go:722),
> string values enter the formatted Registry-table field directly. The executed
> probe shows `.reg` string `"a[~]b"` installing as `REG_MULTI_SZ ['a','b']`,
> violating literal value/type preservation. Define and document the
> literal-versus-formatted authoring contract and add regression coverage before
> changing emission, since existing callers may intentionally use MSI references.
> This predates issue #11 and is deferrable because leading-hash encoding neither
> introduces nor fixes bracket interpretation.

Same probe, same run, `a[Foo]b` installs as `ab` without preservation and as
`a[Foo]b` with it — because a preserved value reaches the Registry table through
`[PS_RV_n]` and is substituted in without a second formatting pass.

Not yet classified as a defect: whether a **mid-string** MSI property reference
ought to expand. `shouldPreserveValue` skips values that *start* with `[`, so the
whole-value form is clearly intended to be formatted; `docs/tutorial.md` documents
`$$VAR$$` for variables in `.reg` files and never mentions `[PROPERTY]`. The
reviewer declined to call it confirmed pending a product decision, and noted that
MSI does document bracket escaping (`[\[]`), so authors are not without recourse —
correcting an overclaim of mine that they were.

## Preserved expandable-string defaults lose their registry type

Reviewer finding from the #6 review, recorded verbatim (2026-09-17):

> **[task] Preserved expandable-string defaults lose their registry type.** At
> [registry.go:449](C:/NGBT/MSIS/msis-3.x/internal/registry/registry.go:449),
> `expandable` defaults are returned without the required `#%` marker, while
> [registry.go:658](C:/NGBT/MSIS/msis-3.x/internal/registry/registry.go:658)
> writes preserved values through a string-typed property reference. An ordinary
> default such as `%PATH%` consequently becomes `REG_SZ` on first installation,
> whereas a successful raw search can retain `REG_EXPAND_SZ`. Correct the default
> encoding and test the resulting type. This predates the diff and concerns a
> different registry type, so defer it from issue #6.

Observed in generated output: `"Expand"=hex(2):...` emits `Value='%PATH%'` with no
prefix. Note the inconsistency the finding describes — the same package yields a
different registry type depending on whether the value already existed.

## QWORD conversion discards the distinction from DWORD

Reviewer finding from the #6 review, recorded verbatim (2026-09-17):

> **[task] QWORD conversion discards the distinction from DWORD.** At
> [registry.go:246](C:/NGBT/MSIS/msis-3.x/internal/registry/registry.go:246),
> `REG_QWORD` becomes the same `integer` representation as DWORD;
> [registry.go:437](C:/NGBT/MSIS/msis-3.x/internal/registry/registry.go:437) then
> emits MSI's DWORD `#` encoding. This cannot faithfully represent a QWORD's type
> and full range. Define supported behavior or reject unsupported QWORD authoring,
> with boundary tests. This is pre-existing and separate from binary-prefix
> correction.

Observed in generated output: `"Qword"=hex(b):ef,cd,ab,89,67,45,23,01` emits
`Value='#81985529216486895'`, well beyond DWORD range.

## Missing payload sources can be silently omitted

Reviewer finding from the #16 review, recorded verbatim (2026-09-18):

> **[task] Missing payload sources can be silently omitted.** At
> [context.go:803](C:/NGBT/MSIS/msis-3.x/internal/generator/context.go:803),
> `processFiles` treats every source `os.Stat` failure as success, including during
> real builds. A missing file or directory can consequently produce an incomplete
> installer without a source diagnostic. Add explicit handling and regression
> coverage for real-build source failures. This predates the roadmap diff; fixing
> generator behavior is deferrable because issue #16 concerns documentation
> accuracy.

Confirmed in the code while addressing the #16 review: the failed-`os.Stat` branch calls
`GetOrCreateDirectory` and returns `nil`, with the comment "still create directory
structure for dry-run/testing". The same class as #19 — generated output quietly missing
content the script asked for — and worth the same treatment: fail, or warn loudly.

## An unreadable source directory silently omits its payload

**FIXED** in issue #25 (2026-09-18): the error is now returned instead of discarded.
Kept for the record, as T2 is in `todo-testme.md`.

Reviewer finding from the #24 review, recorded verbatim (2026-09-18):

> **[task] Directory-read failures silently omit payloads.** At
> [context.go:874](C:/NGBT/MSIS/msis-3.x/internal/generator/context.go:874), returns
> success when `os.ReadDir` fails. An existing but unreadable source directory can
> therefore still produce an incomplete package without a diagnostic. Propagate
> enumeration errors and add an executed regression check. This predates the diff and
> is deferrable because #24 addresses missing explicitly named sources; handling
> directory traversal failures expands that scope.

Confirmed in the code while addressing the #24 review:

```go
entries, err := os.ReadDir(absCurrentPath)
if err != nil {
    return nil // Skip if can't read
}
```

Same class as #19 and #24 — generated output quietly missing content the script asked for —
and the last of that family still open in `processFiles`. #24 closed the case where the named
source is absent; this is the case where it is present but cannot be enumerated (permissions,
a directory that disappears mid-build, an I/O error), which `addDirectoryContents` swallows at
every level of the walk, not just the top.

---

## Overwrite checks can delete a different file from the actual build output

Reviewer finding from the #27 review, recorded verbatim (2026-09-19):

> **[task] Overwrite checks can delete a different file from the actual output.**
> [builder.go:75](C:/NGBT/MSIS/msis-3.x/internal/wix/builder.go:75) resolves relative MSI
> outputs against `SourceDir`; [builder.go:391](C:/NGBT/MSIS/msis-3.x/internal/wix/builder.go:391)
> resolves relative bundle outputs against the WXS directory. Both build commands instead
> resolve against the process cwd. For example, bundle target `dist\setup.exe` with WXS
> `dist\setup-bundle.wxs` checks and potentially deletes `dist\dist\setup.exe`, while building
> `dist\setup.exe`. Unify deletion and build path resolution while retaining cwd-relative target
> placement, with an executed sentinel-file regression test. This defect predates #27 and is
> deferrable as separate overwrite-safety work; record it in `TODO.md`.

Predates #27 and was left untouched by it: #27 changed only the *default* output name, never
where a relative `BUILD_TARGET` resolves. The pre-delete is the part with teeth —
`checkOutputWritable` removes the file it resolved, which is not necessarily the file the build
then writes. Touches the same two functions as [issue #28](https://github.com/gersonkurz/msis/issues/28)
(`BUILD_TARGET` plus `<requires>`), so worth doing with or immediately after it.
