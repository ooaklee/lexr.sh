## Summary

Explain the problem first, then the approach you chose.

## Related issue

Link the issue for substantial work, or write "none".

## Impact

Note any effect on privacy, privilege, recovery, compatibility, persisted data,
or release behaviour. Write "none" where nothing changes.

## How this was checked

List the commands you ran and the results that give you confidence.

## Checklist

- [ ] I ran `go fmt ./...`, `go test -race ./...`, `go vet ./...`, and `go run ./cmd/lexr-build`, or explained above why a check does not apply.
- [ ] I updated user-facing documentation where behaviour or output changed.
- [ ] I updated the [`Unreleased` changelog](https://github.com/ooaklee/lexr.sh/blob/main/CHANGELOG.md#010---unreleased) for a user-visible change, or this is an internal test or documentation-only change.
- [ ] I described any privacy, privilege, recovery, compatibility, persisted-data, or release impact above.
- [ ] I did not commit generated images, build directories, private diagnostics, captured device data, or secrets.
- [ ] I reviewed [`THIRD_PARTY_NOTICES.md`](https://github.com/ooaklee/lexr.sh/blob/main/THIRD_PARTY_NOTICES.md) for dependency or third-party material changes, or no such material changed.
- [ ] My commit subjects follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
