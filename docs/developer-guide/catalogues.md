# Catalogue maintenance

Lexr keeps its supported inputs in human-readable JSON, but treats that content
as data rather than instructions. Strict schemas make a new entry discoverable
without giving it authority to run commands or write arbitrary paths.

## Image catalogue

[`supported-isos.json`](https://github.com/ooaklee/lexr.sh/blob/main/supported-isos.json)
records stable IDs, user-facing metadata, exact upstream filenames, artefact
formats, HTTPS download and homepage links, support state, adapter, mutability,
compatibility notes and verification dates.

The filename must be a portable basename, match the final URL segment exactly,
and use the extension declared by `artifact_kind`. An optional catalogue
checksum may use SHA-256 or SHA-512. Image creation currently consumes SHA-256
source digests; users can supply `--source-sha256` when an implemented entry has
no publisher digest.

Validate every edit:

```sh
lexr catalog validate supported-isos.json
go test ./internal/catalog/...
```

Validation reports semantic problems together, including duplicate or malformed
IDs, unsupported architectures or formats, insecure URLs, filename and format
disagreement, invalid adapter/support combinations, malformed checksums and
invalid dates. Both `arm64` and `aarch64` inputs normalise to `arm64`.

Use `adapter: "none"` with `support_level: "catalog-only"` to make media
discoverable before an adapter exists. Mark an entry `implemented` only when its
named adapter can create and validate that exact artefact format. At present,
`ubuntu-concept-resolute-x1e` is the only implemented image entry.

## Userspace catalogue

[`supported-userspace.json`](https://github.com/ooaklee/lexr.sh/blob/main/supported-userspace.json)
records maturity, capability, redistribution policy, evidence, remediation,
release assets and the bounded actions supported by the CLI. Its action flags
are declarations only; catalogue content cannot provide shell commands or
writable paths.

```sh
lexr userspace catalog validate supported-userspace.json
go test ./internal/userspace/catalog/...
```

Keep redistribution and qualification language exact. Restricted platform
firmware is never turned into a downloadable Lexr component, and experimental
camera support must not be presented as hardware-qualified merely because a
package set passes structural validation.

## Review checklist

- Use a stable, descriptive ID which does not encode a mutable alias.
- Prefer exact upstream URLs and record when they were last verified.
- State unsupported hardware or workflows plainly in compatibility notes.
- Add an adapter only with creation, validation and refusal-path tests.
- Update user documentation when an entry changes what a person can actually do.
- Run the full test suite after both catalogue-specific validators pass.
