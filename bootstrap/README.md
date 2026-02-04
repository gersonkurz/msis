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
