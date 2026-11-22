# docs/

The required project documents (README, ARCHITECTURE, DEVELOPMENT,
SECURITY, CONTRIBUTING, CHANGELOG) live at the repository root, not here -
that's where a reader looks for them first, and section 29 of the
project's build spec lists them at the root explicitly.

This directory is reserved for documentation that's more detailed than
belongs in any single root document: per-subsystem design notes once
Phase 2+ subsystems exist (e.g. a future `docs/os-abstraction.md`
covering the `internal/system` interface design in more depth than
ARCHITECTURE.md's summary), API reference material, or UX research -
added as those subsystems are actually built, not speculatively now.
