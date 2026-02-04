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

### 2. Command-Line Variable Overrides

**Status**: Planned
**Priority**: Medium

Override variables at build time without editing the .msis file. Essential for CI/CD pipelines.

```bash
msis /BUILD /D:PRODUCT_VERSION=2.0.0 setup.msis
```

---

### 3. Validation / Linting

**Status**: Planned
**Priority**: Medium

A `/VALIDATE` command to catch errors before WiX does:

- Missing required variables (PRODUCT_NAME, UPGRADE_CODE, etc.)
- Invalid GUIDs
- Source files/folders that don't exist
- Duplicate shortcut names

```bash
msis /VALIDATE setup.msis
```

---

### 4. File Associations

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
- Registry import from .reg files
- Desktop and Start Menu shortcuts
- Windows services
- Environment variables (including ADD_TO_PATH)
- Custom actions (execute commands)
- Multi-architecture bundles (x64, x86, ARM64)
- Prerequisites (VC++ Runtime, .NET Framework)
- Template customization and logo branding
- WiX 6 integration

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
