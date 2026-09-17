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
