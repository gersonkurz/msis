# T3 — x86 auto-bundle on 64-bit Windows

Closes the runtime half of [#8](https://github.com/gersonkurz/msis/issues/8), recorded as
**T3** in `todo-testme.md`. #8 shipped on the generated XML plus a structural argument and was
never installed, because the bug only shows on a 64-bit machine that does **not** have the x86
VC++ runtime — and on a machine that already has it, the detect condition passes either way and
the run proves nothing.

It runs in two halves, because the VM has no repo, no Go, no WiX and no msis.

## 1. Here — build and stage

```powershell
uv run t3_x86_bundle_probe.py
```

No elevation, installs nothing. It builds msis from the working tree, builds the auto-bundle,
checks the generated chain, and assembles `vm-payload/`:

| | |
|---|---|
| `X86 Bundle Probe-1.0.exe` | the bundle under test |
| `t3_vm_probe.py` | the machine-side probe |
| `README.md` | what to do on the VM |

The chain check is proof item 1 on the ticket and is worth having before touching a VM: the
x86 prerequisite must carry `DetectCondition='VcppRuntimeX86Installed'` and **no**
`InstallCondition`. If `InstallCondition='NOT VersionNT64'` is still there, the build predates
the fix and there is no point installing anything.

## 2. On the test VM — install and check

Copy `vm-payload/` over, snapshot the VM, then from an **elevated** shell:

```powershell
uv run t3_vm_probe.py        # or: python t3_vm_probe.py
```

It records the machine state, installs, checks the runtime and the MSI landed, installs a
second time to confirm the prerequisite is skipped, uninstalls, and prints a verdict. Copy the
output back. `vm/README.md` has the detail.

The VM script has no dependency on this repo and degrades to plain `print` when loguru is
absent, so a bare Python is enough if uv is not installed there.

## ⚠ It installs software, and the test is one-shot

The **Microsoft Visual C++ 2015-2022 Redistributable (x86)** is installed machine-wide, and
bundles mark prerequisites `Permanent='yes'` — uninstalling the probe does not remove it. Once
present, the case under test is gone.

**Rolling the VM back to a snapshot is the only reset**, which is why the VM README says to take
one first. The probe deliberately offers no runtime uninstall: removing a machine-wide runtime
is destructive and there is no way to test it from here.

## What is being confirmed

The bundle's `VcppRuntimeX86Installed` comes from a `util:RegistrySearch` with
`Bitness="always32"`, which on 64-bit Windows reads
`HKLM\SOFTWARE\WOW6432Node\Microsoft\VisualStudio\14.0\VC\Runtimes\x86`. The 32-bit MSI's own
launch condition reads `HKLM\SOFTWARE\...\VC\Runtimes\x86` under WOW64 redirection — the same
physical key. So bundle and MSI should agree about whether the runtime is present. Before the
fix the prerequisite carried `InstallCondition='NOT VersionNT64'`, so on 64-bit Windows it was
skipped and the MSI then refused to install.

## Two msis bugs this steers between

Both were found while building this probe:

- **[#27](https://github.com/gersonkurz/msis/issues/27)** — without `BUILD_TARGET` the bundle
  lands in the *working directory* under a name derived from `PRODUCT_NAME`, msis reports a
  different path that does not exist, and the patch version is eaten (`1.0.0` → `1.0`, hence
  the filename). The build therefore runs with this directory as its working directory, and the
  artifact is located afterwards rather than assumed.
- **[#28](https://github.com/gersonkurz/msis/issues/28)** — setting `BUILD_TARGET`, the obvious
  workaround for #27, hands the value to the MSI build as well, so wix is asked to produce an
  MSI at a path ending in `.exe` and fails with `WIX0341`.

## Files

`pyproject.toml`, `README.md`, `t3_x86_bundle_probe.py` and `vm/` are the project. Everything
else — `probe.msis`, `readme.txt`, the `.wxs` files, `probe.msi`, the bundle, `msis.exe`,
`vm-payload/`, the logs — is generated and safe to delete.
