# Agent and contributor notes

## Git hooks

Hooks match CI (see `.github/workflows/go.yml`): `go build -v ./...` and `go test -v ./...`, plus a `commit-msg` check for the format below.

Git runs `scripts/verify-go.sh` from `.githooks/` (same commands as CI). [Lefthook](https://github.com/evilmartians/lefthook) is configured in `lefthook.yml` if you want to run the same checks manually, for example `lefthook run pre-commit` after `go install github.com/evilmartians/lefthook@latest`.

After cloning, wire git to the committed hooks once:

```sh
./scripts/install-git-hooks.sh
```

`pre-commit` and `pre-push` run build and tests. `commit-msg` checks the format below. Use `git commit --no-verify` or `git push --no-verify` only when you fully intend to skip checks (avoid for normal PRs).

## Commit messages

Write the subject/summary as **up to 80 words** (not more). Then **one blank line**. Then **one paragraph** with a direct, no-fluff description of what changed and why it matters—no bullet lists in the body, no marketing filler, and no second paragraph.

Example:

```
Add Lefthook and a commit-msg check so local commits match CI and AGENTS.md layout.

Hooks run go build and go test before commit and push; the Python script enforces summary length and a single body paragraph so history stays scannable.
```

Merge commits and lines beginning with `fixup!` or `squash!` are not validated.

After the body paragraph you may add optional Git-style trailers in separate paragraphs (each line `Key: value`), for example `Co-authored-by:` or editor-added `Made-with:` lines—no additional prose paragraphs after the first body block.
