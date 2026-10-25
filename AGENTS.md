# Working on tori

Commits and pull requests are authored only by the human operator. Never add
agent, model or tool attribution, including `Co-authored-by:` trailers or
generator footers. Restate this rule when delegating work.

## Go tooling and style

Follow the `go-style` skill and the shared Go guidance in robocode's
`tooling/go/templates/AGENTS-go-style.md` with these project rules:

- Use the versions pinned in `mise.toml`. Run commands through `mise exec --`
  unless your shell has activated Mise; a bare `go` may select another version.
- Use gopls for definitions, references and call sites before text searches
  when an LSP tool is available. Otherwise use the pinned gopls CLI through
  `mise exec -- gopls`. LSP diagnostics do not replace lint.
- Add context to errors returned from the standard library and other modules
  with `fmt.Errorf` and `%w`. Use `errors.Is` and `errors.As` for comparisons.
  Errors from this project's own packages may be returned unchanged.
- Pass `context.Context` first, named `ctx`; never store it in a struct or
  replace the caller's context with `context.Background()`.
- Test helpers call `t.Helper()` first. Prefer `t.TempDir`, `t.Setenv` and
  `t.Context` to equivalent manual setup.
- Name sentinel errors `errXxx`/`ErrXxx` and error types `XxxError`.
- Fix failed checks rather than disabling them. Explain any suppression,
  skipped test, lint-config change or vulnerability allowlist entry; ask before
  adding a suppression or weakening a check.
- Keep changed code simple: CI and pre-commit check complexity, nesting and
  error context on changed lines. Refactor findings rather than hiding them.
- Apply automatic lint fixes with `golangci-lint run --fix`; use
  `golangci-lint fmt` for gofumpt/goimports formatting.

The local git hooks load robocode's shared Go hooks through the ignored
`lefthook-local.yml`: format/lint before commit and tests before push.
Claude's user-level hooks lint Go edits and run an advisory deadcode check.
Codex and Cursor must run lint explicitly before handing back Go changes.

## Before opening a pull request

Run in a Mise-activated shell, or prefix each command with `mise exec --`:

```sh
go test ./...
go test -race ./...
go vet ./...
go mod tidy -diff
golangci-lint run ./...
golangci-lint run --enable-only=gocognit,nestif,wrapcheck --new-from-merge-base=origin/HEAD ./...
./scripts/govulncheck.sh
go build ./cmd/tori/
```

The TUI tests render frames and click positions derived from that output.
Preserve their geometry checks when changing layout or navigation.
