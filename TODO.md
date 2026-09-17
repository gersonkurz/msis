# TODO

Follow-up tasks from the review loop. Checks that need a real machine (an elevated
install, not just a green build) live in [`todo-testme.md`](todo-testme.md) instead.

## Preserved defaults are inserted into XML without escaping

Filed as [#9](https://github.com/gersonkurz/msis/issues/9). Reviewer finding from the
#5 review, recorded verbatim:

> **[task] Preserved defaults are inserted into XML without escaping.** At
> [registry.go:412](C:/NGBT/MSIS/msis-3.x/internal/registry/registry.go:412),
> `defaultValue` is interpolated directly into a single-quoted attribute;
> `encodePreservationDefault` returns string values unchanged. A valid `.reg`
> default such as `O'Brien` or `A&B` therefore produces malformed WXS and
> prevents building. Escape the encoded default at the XML boundary and add
> coverage for those characters. This defect also exists in the original
> three-element implementation, so it is deferrable: issue #5 addresses
> custom-action sequence exhaustion, not XML escaping.

Reproduced against WiX 7.0.0 (`error WIX0104: ... 'Brien' is an unexpected token`) and
confirmed pre-existing — the pre-#5 three-element form emits the same unescaped
`Value='O'Brien'`. The non-preserved path already escapes
(`escapeXML(val.Value)` in `generateRegistryValueXML`); only the preservation path
is missing it. Full repro in #9.

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
