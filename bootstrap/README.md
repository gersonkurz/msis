# Bootstrap

This folder contains the `.msis` scripts used to build the msis distribution packages.

## Contents

- `setup.msis` - x64 MSI installer
- `setup-x86.msis` - x86 (32-bit) MSI installer
- `setup-arm64.msis` - ARM64 MSI installer
- `setup-bundle.msis` - Universal bundle combining all architectures
- `dist/` - Output folder for built installers (not tracked in git)

During the build process, Go binaries (`msis-x64.exe`, etc.) are temporarily placed here before being packaged into installers.

## Building

From the project root:

```bash
# Build x64 MSI only
just release

# Build all MSIs and bundle
just release-all

# Clean up
just clean-bootstrap
```

## Output

Built installers are placed in `bootstrap/dist/`:
- `msis-{version}-x64.msi`
- `msis-{version}-x86.msi`
- `msis-{version}-arm64.msi`
- `msis-{version}-setup.exe` (universal bundle)

Each is built with `/SBOM`, so each has its CycloneDX document beside it
(`<artifact>.cdx.json`); the bundle's links to the three MSIs'. Each MSI's document carries,
composed in via `<sbom>` in `setup.msis`, what is inside `msis.exe` (its Go modules and the Go
standard library) and inside each hook DLL (its NuGet libraries). `components/` holds those
component documents, written by `just sbom-components`. `release-all` also writes
`msis-{version}.cdx.json`, the release-wide document from `tools/sbom`.
