# escalier

[![codecov](https://codecov.io/github/escalier-lang/escalier/graph/badge.svg?token=AMM20YA614)](https://codecov.io/github/escalier-lang/escalier)

Next version of the Escalier compiler written in Go

## Licence

Escalier is licensed under the [Apache License, Version 2.0](LICENSE).
`@escalier/runtime`, which the compiled JavaScript imports, is licensed
under [0BSD](packages/runtime/LICENSE), so a program built with Escalier
owes no attribution for the runtime it bundles.

The standard library under `internal/interop/data/` is generated from
TypeScript's `lib.*.d.ts` files, which Microsoft licenses under
Apache-2.0. Each generated file carries their notice, and
[ThirdPartyNoticeText.txt](internal/interop/data/ThirdPartyNoticeText.txt)
holds the third-party notices TypeScript ships with them.
[cmd/escalier/THIRD_PARTY_NOTICES.txt](cmd/escalier/THIRD_PARTY_NOTICES.txt)
lists the third-party software linked into the `escalier` binary.

## Development

To update the snapshots run the following command:
`UPDATE_SNAPS=true go test ./...`

### Changesets

The npm packages `@escalier/runtime` and `@escalier/cli` are versioned with
[changesets](https://github.com/changesets/changesets). A PR that changes
either package, or the compiler that `@escalier/cli` ships, should include
a changeset:

1. Run `pnpm changeset`.
2. Pick the packages the PR changes and the kind of version bump each needs.
3. Write a one-line summary for the changelog.
4. Commit the file it creates under `.changeset/`.

`@escalier/cli` and its `@escalier/cli-<platform>-<arch>` binary packages
always share a version, so a changeset for `@escalier/cli` bumps all of
them. `pnpm version-packages` applies the pending changesets to the
`package.json` and `CHANGELOG.md` files.

The `check_changesets` CI job fails a PR that changes a published package
without a changeset that releases it. It counts the `.go`, `.esc` and
`.json` files under `cmd/escalier/` and `internal/`, plus `go.mod` and
`go.sum`, as part of `@escalier/cli`, since they build into its binary.
Docs, Go test files, `cmd/lsp-server/` and private packages such as the
fixtures don't need a changeset. For a change that shouldn't release, such
as a refactor, run `pnpm changeset --empty` and commit the empty changeset
it creates.

The `Release` workflow (`.github/workflows/release.yaml`) publishes the
packages. While changesets are pending on `main`, it keeps a "Version
Packages" pull request open with the result of `pnpm version-packages`.
Merging that pull request publishes each package whose version isn't on npm
yet. `pnpm release` builds the platform binaries and the two packages first,
so a build failure stops the release before anything is published.
