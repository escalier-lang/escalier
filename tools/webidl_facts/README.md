# `webidl_facts`

Writes [internal/webidl/table.go](../../internal/webidl/table.go) from the
`@webref/idl` package that [package.json](../../package.json) pins. The table
records four facts per WebIDL interface, callback interface and namespace:
`[Exposed]`, `[Global]`, `[Transferable]`, and whether it declares a
`constructor()`. See §5.2 of
[planning/web_api_types/design.md](../../planning/web_api_types/design.md).

## Bumping the pinned version

1. Change the `@webref/idl` version in package.json to an exact version.
2. Run `pnpm install`.
3. Run `go run ./tools/webidl_facts` from the repository root.
4. Run `go test ./internal/webidl/`.

`TestTableMatchesInstalledPackage` fails when table.go differs from what the
tool writes from the installed package, and `TestPinnedVersionMatchesPackageJSON`
fails when the table's version differs from the pin. `TestTableFacts` and
`TestTableTransferable` name the facts later stages read, so a bump that
changes one of them fails by name and needs a look before the expected values
are updated.
