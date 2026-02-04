# msis

A Windows installer generator that transforms declarative `.msis` XML scripts into MSI packages via WiX Toolset 6.

## Why Does This Exist?

Writing WiX XML by hand is tedious. A simple installer requires hundreds of lines of boilerplate - GUIDs, component rules, directory structures, feature hierarchies. For most applications, you just want to say "put these files here, create this shortcut, set these registry keys."

`msis` lets you write this:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>

  <feature name="MyApp">
    <files source="bin\*" target="[INSTALLDIR]"/>
    <shortcut name="MyApp" target="DESKTOP" file="[INSTALLDIR]MyApp.exe"/>
    <registry file="settings.reg"/>
  </feature>
</setup>
```

Instead of 500+ lines of WiX XML. The tool handles component GUIDs, directory trees, feature mapping, registry import, and [multi-architecture bundles](docs/Bundle.md).

## Installation

### Prerequisites

**WiX Toolset 6** - Install via .NET:
```bash
dotnet tool install --global wix
wix extension add WixToolset.UI.wixext
wix extension add WixToolset.Util.wixext
```

### Get msis

Download from the [releases page](https://github.com/gersonkurz/msis/releases), or build from source:

```bash
git clone https://github.com/gersonkurz/msis
cd msis/msis-3.x
go build -o msis.exe ./cmd/msis
```

Verify your setup:
```bash
msis /STATUS
```

## Quick Start

1. Create `setup.msis`:

```xml
<setup>
  <set name="PRODUCT_NAME" value="Hello World"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{12345678-1234-1234-1234-123456789ABC}"/>

  <feature name="Main">
    <files source="hello.exe" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

2. Build the MSI:
```bash
msis /BUILD setup.msis
```

That's it. Your installer is ready at `setup.msi`.

## Documentation

| Document | Description |
|----------|-------------|
| **[Tutorial](docs/tutorial.md)** | Step-by-step guides: files, shortcuts, registry, services, and more |
| **[Templates & Customization](docs/templates.md)** | Template locations, logo branding, custom templates |
| **[Bundle Guide](docs/Bundle.md)** | Multi-architecture installers and prerequisites |
| **[Schema](docs/msis.xsd)** | Complete XML element and attribute reference |
| **[Roadmap](docs/roadmap.md)** | Planned features and future direction |
| **[Developer Overview](docs/overview.md)** | Architecture, code structure, and internals |

## Command Line

```
msis [OPTIONS] FILE [FILE...]

Options:
  /BUILD                Generate WXS and build MSI using WiX
  /RETAINWXS            Keep the generated .wxs file after build
  /TEMPLATE:PATH        Use custom WiX template
  /TEMPLATEFOLDER:PATH  Base template folder
  /CUSTOMTEMPLATES:PATH Custom templates overlay
  /DRY-RUN              Parse and validate only, no output
  /STATUS               Show configuration (WiX location, templates)
  /?, /HELP             Show help
```

## Migration from msis-2.x

msis-3.x is largely compatible with msis-2.x scripts:

| Aspect | msis-2.x | msis-3.x |
|--------|----------|----------|
| WiX Version | WiX 3.x/4.x | WiX 6.x |
| Default Architecture | x86 | x64 |
| Bundle Engine | Custom C++ | WiX Burn |

**Migration steps:**
1. Install WiX 6: `dotnet tool install --global wix`
2. Validate: `msis /DRY-RUN setup.msis`
3. If you need x86: add `<set name="PLATFORM" value="x86"/>`
4. Rebuild: `msis /BUILD setup.msis`

Most scripts work unchanged. See the [Tutorial](docs/tutorial.md) for the full element reference.

## History

- **msis-1.x** (C++) - Original implementation, internal use
- **msis-2.x** (C#) - Expanded features, production use since 2013
- **msis-3.x** (Go) - Current version, clean rewrite for WiX 6

All versions share the same `.msis` format.

## License

MIT License - see LICENSE file.

## Author

Gerson Kurz / NG Branch Technology GmbH
