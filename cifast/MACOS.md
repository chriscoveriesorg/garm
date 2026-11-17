# macOS external-provider admission

The patch series adds the lowercase API/storage/bootstrap value `macos`, with
GitHub runner-tool names `osx` / `arm64` for the current Mac provider. Display
labels remain `macOS`. The captured runner-download listing and its provenance
are in the applied tree's `internal/testing/testdata/runner-downloads-20261117.*`.

Create an **admin-owned** GitHub/macOS template with the admission-only body in
`internal/templates/userdata/github_macos_userdata.tmpl`, verify its body, forge
and OS, and pass its explicit ID to scale-set creation. `findTemplate` remains
strict: without an ID it selects SYSTEM-owned templates only, not an admin
fallback. Explicit `template restore --forge-type github --os-type macos` can
restore the same safety stub as a system template, but the Mac controller must
never use that implicit default. Restore-all keeps its existing Linux/Windows
set; it does not silently create a Mac system template.

The body prints
`cifast macdad: GARM install template is admission-only; the provider uses JIT credentials`
and exits 1. It is not an installer. The Cifast external provider fetches only the
three JIT credential files; it ignores this template body and the GARM wrapper.
The Mac agent downloads its own pinned Actions `osx-arm64` runner. The metadata
install-script endpoint nevertheless now selects the correct macOS tool and
renders the stub honestly if called.

This is **not** support for the WebUI's Mac selectors, GARM-agent, systemd,
generic cloud-init, Gitea/macOS templates or AgentMode on macOS. These routes
remain unsupported. No Linux relabel or copied Linux installer is supplied.
Linux/Windows behavior and the strict template OS/forge gates remain intact.

The PR check replays all patches at upstream
`158fe569f61a491198501162805be4cd0c843da0`, runs the full vendored Go suite,
and builds both binaries on a GitHub-hosted runner. No artifact-quota upload is
used. The release workflow records upstream, patch-source and applied-tree
identities, each patch SHA256, and server **and** CLI SHA256s as release assets.
Release tags/branches are unique per workflow run and attempt; no force push or
asset overwrite is used. The native version string still names upstream only.

## Swap preflight and recovery

Before swapping, the operator checks the native GARM database read-only for any
stored OS value exactly `macOS` in templates, scale_sets, pools or instances.
Any such row stops the swap for a reviewed migration; none may be silently
retagged. Both server and CLI must be replaced and hash-verified. Restart
acceptance must prove consumers for sets **25, 26 and 29**, not just an active
service. The later Cifast change must bind both binary hashes and own the
idempotent template create/verify/undo operation.

A failed set create can have reached GitHub before local persistence and left
an orphan despite best-effort rollback. Preserve the create claim and receipt;
inspect both GARM's local set identity and GitHub's runner-scale-set identity
before removing any claim. Local DB absence alone is not proof of absence.
Only a operator-reviewed exact matching set can be reconciled or deleted. Template
undo is `garm-cli template delete <recorded-owned-id>` after proving that no set
or pool references that exact owned template. Keep unrelated templates intact.
