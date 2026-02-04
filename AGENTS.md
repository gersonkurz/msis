# AGENTS.md

This is the msis-3.x Go rewrite of MSI-Simplified. The goal is to fully replace the legacy C#/C++ toolchain with a single Go-based pipeline that:

- Parses `.msis` XML
- Builds an intermediate representation (IR)
- Generates WiX 6 WXS
- Builds MSI (and Bundle EXE) via WiX CLI

Key project intent:
- Preserve behavior parity with msis-2.x and the C++ bundler
- Keep output deterministic and compatible with production examples
- Maintain strong automated tests (parser, generator, template, wix, bundle)

Roles (per AI-COLLABORATION.md):
- Claude: Lead Developer
- Codex: Senior Reviewer / Quality Gate
- Gerson: Product Owner

Status snapshot (early Feb 2026):
- Phase 3 core pipeline implemented (CLI, parser/IR, variables, generator, templates, WiX build)
- Phase 4 parity features implemented (registry, shortcuts, permissions, execute)
- Phase 5 bundle support implemented (IR + prerequisites + generator + CLI + docs)
- Some tracking docs may lag actual code; verify with `docs/status/daily-status.md` and `CLAUDE.md`

Where to look first:
- `CLAUDE.md` for architecture and current milestones
- `docs/Bundle.md` for bundle behavior and prerequisites
- `docs/overview.md` and `docs/tutorial.md` for user-facing docs
- `docs/decisions/` and `docs/reviews/` for history and reviews

Workflow expectations:
- Changes should be reviewed and documented in `docs/reviews/`
- Decisions are tracked in `docs/decisions/`
- Prefer parity with msis-2.x behavior unless explicitly changing requirements

Implementation notes:
- WiX 6 is required (`wix` .NET tool); extensions: UI, Util, BootstrapperApplications, Netfx
- Bundle arch selection uses Burn conditions (`NativeMachine = 43620` for ARM64)
- Bundle install folder should resolve via `ProgramFiles6432Folder`

If adding or changing features:
- Confirm behavior in msis-2.x or production examples
- Update tests and docs alongside code changes
- Keep templates and schema/docs aligned with IR and parser behavior
