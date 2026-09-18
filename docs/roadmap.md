# msis Roadmap

## Philosophy

msis exists because WiX is powerful but verbose. The core value proposition is **simplicity**: 20 lines of `.msis` instead of 500 lines of `.wxs`.

**Guidelines for new features:**

1. If it can't be expressed in 1-2 lines of XML, it probably doesn't belong in msis
2. msis handles the 80% case; for the 20%, use custom templates or raw WiX
3. Don't recreate WiX's complexity in a different syntax
4. When in doubt, don't add it

**What msis is NOT:**
- A complete WiX replacement
- A deployment system (IIS, scheduled tasks, firewall rules)
- A build system

If WiX ever adds native simplifications (like "add all files from this folder"), that's a win - msis exists to solve a problem, not to exist for its own sake.

---

## Planned Features

### 1. Custom UI Properties

**Status**: Planned
**Priority**: High

Add declarative support for simple installer UI elements: boolean switches, radio buttons, and text input fields.

**Scope**: Simple property dialogs only. Complex multi-page wizards should use custom templates.

**Proposed Syntax**:

```xml
<!-- Radio button group -->
<property name="INSTALL_MODE" type="radio" default="Standard">
  <option value="Standard">Standard Installation</option>
  <option value="Developer">Developer Mode</option>
</property>

<!-- Text input -->
<property name="BRANCH_NAME" type="text" default="main"/>

<!-- Checkbox -->
<property name="ENABLE_TELEMETRY" type="checkbox" default="true"/>
```

---

### 2. Validation / Linting

**Status**: Partially shipped
**Priority**: Medium

`/DRY-RUN` already parses, resolves variables and validates, stopping before `wix build`:

```bash
msis /DRY-RUN setup.msis      # -> [dry-run] Parse and validate complete
```

What it actually catches:

- unknown elements and attributes, and missing required fields (the parser rejects both)
- variable reference cycles and malformed `{{...}}` expressions
- a `<files source=>` that does not exist, and the unsupported wildcard form `dir\*`
- the warnings msis emits at generation time: deprecated variables, the destructive
  uninstall settings, and unresolved values in generated content

What it does **not** catch, and a linter would:

- **An undefined `{{VAR}}`**, which renders as the empty string rather than failing. Only
  cycles and malformed expressions are errors.
- Invalid GUIDs in `UPGRADE_CODE`
- Duplicate shortcut names
- Recommended-but-unset variables

Note that `/DRY-RUN` stops before the build, so build-time checks do not run under it —
the missing-hook-DLL check (`USE_INSTALLER_HOOKS` without the arch-native DLL) is one, and
it fires only under `/BUILD`.

Whether that deserves its own `/VALIDATE` flag or is simply more checks inside `/DRY-RUN`
is open — a second flag that does almost the same thing is the kind of surface this
project avoids.

---

### 3. File Associations

**Status**: Considering
**Priority**: Low

Register file extensions. Common need, simple syntax:

```xml
<file-type extension=".myapp"
           description="MyApp Document"
           icon="[INSTALLDIR]app.ico"
           command="[INSTALLDIR]myapp.exe &quot;%1&quot;"/>
```

---

## Out of Scope

These are explicitly **not** planned for msis:

- **Complex dialog wizards** - Use custom templates
- **Conditional logic / scripting** - Use custom actions or templates
- **IIS / web deployment** - Use dedicated tools
- **Scheduled tasks** - Use custom actions
- **Firewall rules** - Use custom actions
- **Include files** - Leads to "where is this defined?" debugging

---

## Completed (3.0)

- Core MSI generation (files, directories, features)
- Registry import from .reg files, including `preserve="yes"` to keep a value the user
  already has instead of overwriting it
- Desktop and Start Menu shortcuts
- Windows services
- Environment variables (including ADD_TO_PATH)
- Custom actions (execute commands)
- `<create-folder>` and `<remove-on-uninstall>` (folders and registry keys)
- Multi-architecture bundles (x64, x86, ARM64)
- Prerequisites (VC++ Runtime, .NET Framework), including auto-bundling: a package with
  `<requires>` is wrapped in a Burn bundle that chains them, or with `/STANDALONE` emits
  launch conditions instead
- Installer hooks (`USE_INSTALLER_HOOKS`) and the uninstall cleanup actions
- Template customization, custom-template overlays and logo branding
- Command-line variable overrides: `msis /BUILD /SET:PRODUCT_VERSION=2.0.0 setup.msis`,
  applied after the script's own `<set>` elements
- `/SETUP-WIX`: installs the pinned WiX toolset and its extensions, version-matched
- WiX 6 **and 7** integration (major version auto-detected; `-acceptEula` added for v7+)

---

## Keeping this file honest

This roadmap is what a user reads before filing a feature request, so a stale entry
generates work rather than saving it — issue #4 asked for command-line overrides that had
already shipped, and it was listed here as Planned at the time.

The rule: **a feature moves out of Planned in the same change that implements it.** When
closing an issue that adds or completes a feature, check whether this file still describes
it as future work. An entry here is a claim about the code and is checked against the code,
not against memory.

---

## Contributing

Feature requests: https://github.com/gersonkurz/msis/issues

When proposing features, consider:
1. Can it be expressed in 1-2 lines of XML?
2. Is it an 80% use case or an edge case?
3. Could it be done with custom templates instead?

## See Also

- [Tutorial](tutorial.md) - Current feature documentation
- [Developer Overview](overview.md) - Architecture for contributors
