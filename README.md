# escalier

[![codecov](https://codecov.io/github/escalier-lang/escalier/graph/badge.svg?token=AMM20YA614)](https://codecov.io/github/escalier-lang/escalier)

Next version of the Escalier compiler written in Go

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
without a changeset that releases it. It treats changes to `cmd/escalier/`,
`internal/`, `go.mod` and `go.sum` as changes to `@escalier/cli`, since
they build into its binary. Go test files, `cmd/lsp-server/` and private
packages such as the fixtures don't need a changeset. For a change that shouldn't release, such as a
refactor, run `pnpm changeset --empty` and commit the empty changeset it
creates.
