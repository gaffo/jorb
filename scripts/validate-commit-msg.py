#!/usr/bin/env python3
"""Enforce AGENTS.md commit layout: summary (<=80 words), blank line, one body paragraph."""
import re
import sys


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: validate-commit-msg.py <path-to-commit-message-file>", file=sys.stderr)
        return 2
    path = sys.argv[1]
    raw = open(path, encoding="utf-8", errors="replace").read()
    lines = [ln for ln in raw.splitlines() if not ln.lstrip().startswith("#")]
    while lines and not lines[0].strip():
        lines.pop(0)
    while lines and not lines[-1].strip():
        lines.pop()
    text = "\n".join(lines)
    if not text.strip():
        return 0

    first = text.split("\n", 1)[0]
    if first.startswith("Merge ") or first.startswith("fixup!") or first.startswith("squash!"):
        return 0

    parts = text.split("\n\n")
    if len(parts) < 2:
        print(
            "Commit message must have a summary, a blank line, then one body paragraph "
            "(see AGENTS.md).",
            file=sys.stderr,
        )
        return 1
    summary = parts[0]
    body = parts[1]
    trailer_line = re.compile(r"^[\w-]+: .+$")
    for extra in parts[2:]:
        if not extra.strip():
            continue
        for line in extra.splitlines():
            if not line.strip():
                print(
                    "Extra content after the body must be Git-style trailers only "
                    "(e.g. Co-authored-by), one line per paragraph.",
                    file=sys.stderr,
                )
                return 1
            if not trailer_line.match(line.strip()):
                print(
                    "Only one prose paragraph is allowed after the summary; remove extra "
                    "paragraphs or express them as key: value trailers.",
                    file=sys.stderr,
                )
                return 1
    summary_words = len(summary.split())
    if summary_words > 80:
        print(f"Summary must be at most 80 words (got {summary_words}).", file=sys.stderr)
        return 1
    if not body.strip():
        print("Body paragraph must be non-empty.", file=sys.stderr)
        return 1
    if re.search(r"\n\s*\n", body):
        print("Body must be a single paragraph (no blank lines inside the body).", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
