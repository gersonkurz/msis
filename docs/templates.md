# Templates and Customization

msis uses Handlebars templates to generate WiX XML. This document covers template locations, structure, and customization options including logo branding.

## Template Locations

msis searches for templates in the following order:

1. **Custom templates** (command line): `/CUSTOMTEMPLATES:path`
2. **User templates**: `%LOCALAPPDATA%\msis\custom`
3. **Template folder** (command line): `/TEMPLATEFOLDER:path`
4. **Installed templates**: `%LOCALAPPDATA%\msis\templates`
5. **Portable templates**: `<executable-dir>\templates`
6. **Current directory**: `.\templates`

Files in earlier locations override later ones, so you can selectively replace individual templates without copying the entire folder.

## Template Structure

```
templates/
├── x64/                    # 64-bit MSI templates
│   ├── template.wxs        # Full UI installer
│   └── template-silent.wxs # Silent/minimal installer
├── x86/                    # 32-bit MSI templates
│   ├── template.wxs
│   └── template-silent.wxs
├── minimal/                # Minimal templates (no UI)
│   └── template.wxs
├── minimal-x86/            # Minimal 32-bit templates
│   └── template.wxs
├── bundle.wxs              # Bundle with UI
├── bundle-silent.wxs       # Silent bundle
├── wixlib/                 # Shared WiX libraries
├── custom/                 # User customizations (empty by default)
└── mergemodules/           # Merge modules
```

## Selecting Templates

By default, msis uses `x64/template.wxs` for 64-bit builds and `x86/template.wxs` for 32-bit builds.

Override with command line options:

```bash
# Use minimal template (no UI)
msis /BUILD /TEMPLATE:templates/minimal/template.wxs setup.msis

# Use custom template folder
msis /BUILD /TEMPLATEFOLDER:my-templates setup.msis

# Override with custom templates (highest priority)
msis /BUILD /CUSTOMTEMPLATES:my-overrides setup.msis
```

## Logo Customization

The installer UI displays logo images at various stages. msis supports customizing these via variables.

### Logo Variables

| Variable | Purpose | Default Size |
|----------|---------|--------------|
| `LOGO_BANNER` | Top banner on wizard pages | 493 x 58 pixels |
| `LOGO_DIALOG` | Side panel on welcome/finish pages | 493 x 312 pixels |
| `LOGO_BOOTSTRAP` | Bundle/bootstrapper UI logo | 75 x 75 pixels |
| `LOGO_PREFIX` | Prefix for auto-discovered logo files | (none) |

### Method 1: Explicit Logo Paths

Set the logo variables directly in your `.msis` file:

```xml
<set name="LOGO_BANNER" value="branding\banner.bmp"/>
<set name="LOGO_DIALOG" value="branding\dialog.bmp"/>
```

### Method 2: Logo Prefix (Convention-Based)

Set `LOGO_PREFIX` and msis will look for files following a naming convention:

```xml
<set name="LOGO_PREFIX" value="MyCompany"/>
```

With this setting, msis looks for:
- `MyCompany_WixUiBanner.bmp` (banner)
- `MyCompany_WixUiDialog.bmp` (dialog)
- `MyCompany_LogoBootstrap.bmp` (bundle)

Place these files in your custom templates folder or alongside your `.msis` file.

### Logo Image Requirements

| Image | Format | Size | Notes |
|-------|--------|------|-------|
| Banner | BMP | 493 x 58 | Horizontal strip at top of wizard |
| Dialog | BMP | 493 x 312 | Left panel on welcome/complete pages |
| Bootstrap | BMP | 75 x 75 | Bundle UI icon |

All images must be Windows BMP format. PNG/JPG are not supported by WiX.

### Example: Custom Branding

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{GUID}"/>

  <!-- Custom branding -->
  <set name="LOGO_PREFIX" value="MyCompany"/>

  <feature name="Main">
    <files source="bin\*" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

With files:
```
project/
  setup.msis
  bin/
    myapp.exe
  templates/
    custom/
      MyCompany_WixUiBanner.bmp
      MyCompany_WixUiDialog.bmp
```

## Custom Templates Folder

The `custom/` folder (in templates or `%LOCALAPPDATA%\msis\custom`) is for user overrides. It's searched first, so files here take precedence.

### Use Cases

1. **Custom logos**: Place logo BMPs here with your `LOGO_PREFIX`
2. **Modified templates**: Copy and modify any template file
3. **Additional resources**: License files, icons, etc.

### Setup for Custom Branding

1. Create the custom folder:
   ```
   %LOCALAPPDATA%\msis\custom\
   ```

2. Add your logo files:
   ```
   %LOCALAPPDATA%\msis\custom\MyCompany_WixUiBanner.bmp
   %LOCALAPPDATA%\msis\custom\MyCompany_WixUiDialog.bmp
   ```

3. Use in your `.msis`:
   ```xml
   <set name="LOGO_PREFIX" value="MyCompany"/>
   ```

## Template Variables

Templates use Handlebars syntax. Key variables available:

### Product Information
- `{{PRODUCT_NAME}}` - Product display name
- `{{PRODUCT_VERSION}}` - Version string
- `{{MANUFACTURER}}` - Company name
- `{{UPGRADE_CODE}}` - Upgrade GUID
- `{{PLATFORM}}` - Target platform (x64, x86, arm64)

### Generated Content
- `{{{FEATURES}}}` - Feature XML (triple braces = unescaped)
- `{{{INSTALLDIR_FILES}}}` - Directory/component XML for INSTALLDIR
- `{{{REGISTRY_ENTRIES}}}` - Registry XML
- `{{{CUSTOM_ACTIONS}}}` - CustomAction XML

### Logos (if set)
- `{{LOGO_BANNER}}` - Path to banner image
- `{{LOGO_DIALOG}}` - Path to dialog image
- `{{LOGO_BOOTSTRAP}}` - Path to bootstrap image

## Creating Custom Templates

1. Copy an existing template as a starting point:
   ```bash
   cp templates/x64/template.wxs templates/custom/template.wxs
   ```

2. Modify the WiX XML as needed

3. Use your template:
   ```bash
   msis /BUILD /TEMPLATE:templates/custom/template.wxs setup.msis
   ```

### Template Tips

- Use `{{{variable}}}` (triple braces) for XML content to prevent escaping
- Use `{{variable}}` (double braces) for text values
- Test with `/RETAINWXS` to inspect generated output
- Check WiX 6 documentation for element syntax

## See Also

- [Tutorial](tutorial.md) - Step-by-step guides
- [Bundle Guide](Bundle.md) - Bundle-specific options
- [Schema Reference](msis.xsd) - Complete XML element reference
