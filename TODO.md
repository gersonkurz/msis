# TODO

Follow-up tasks from the review loop.

## Preserved defaults are inserted into XML without escaping

Reviewer finding, recorded verbatim (issue #5 review, 2026-09-17):

> **[task] Preserved defaults are inserted into XML without escaping.** At
> [registry.go:412](C:/NGBT/MSIS/msis-3.x/internal/registry/registry.go:412),
> `defaultValue` is interpolated directly into a single-quoted attribute;
> `encodePreservationDefault` returns string values unchanged. A valid `.reg`
> default such as `O'Brien` or `A&B` therefore produces malformed WXS and
> prevents building. Escape the encoded default at the XML boundary and add
> coverage for those characters. This defect also exists in the original
> three-element implementation, so it is deferrable: issue #5 addresses
> custom-action sequence exhaustion, not XML escaping.

Note: the non-preserved path already escapes (`escapeXML(val.Value)` in
`generateRegistryValueXML`); only the preservation path is missing it.

## Runtime probe: preserved value that already exists as an empty string

Issue #5 replaced the three-element preservation pattern (`PS_RV_` default +
`PS_RS_` search + `SetProperty`) with the two-element form (`PS_RV_` default with
the `RegistrySearch` nested inside), removing the per-value custom action.

One behaviour delta was reasoned about but **never observed at runtime**: when the
target value already exists in the registry as an *empty* `REG_SZ` and the `.reg`
file's default is non-empty, `RegistrySearch Type='raw'` returns `""`, and setting
an MSI property to the empty string undefines it. Expected result: the live empty
value is preserved (written back as empty) rather than overwritten by the `.reg`
default — which is what `preserve="yes"` should mean, and matches msis-2.x. The old
pattern wrote the default in that case, because the `SetProperty` condition was false.

This could not be executed in the session that made the change: `msiexec` needs
elevation. A ready-to-run probe was prepared (seed `HKLM\SOFTWARE\MsisPreserveProbe`
with a live empty string, a live non-empty string and a live DWORD, leave two values
absent, install silently with `/l*v`, dump the resulting values and types, uninstall).

Also unobserved, same reason: the elevated per-machine client→server handoff, and the
unnamed/default-value case (MS RegLocator docs qualify retrieval of a default value
with "if it is not empty").

Reported by the reviewer as a [suggestion] during the approach consultation for issue #5;
deferred with the product owner's explicit acceptance on 2026-09-17.
