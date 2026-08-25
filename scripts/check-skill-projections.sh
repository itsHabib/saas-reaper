#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

if ! cmp -s AGENTS.md CLAUDE.md; then
  echo "agent entrypoints drifted: AGENTS.md and CLAUDE.md must be byte-identical" >&2
  exit 1
fi

for skill_file in skills/*/SKILL.md; do
  skill=${skill_file#skills/}
  skill=${skill%/SKILL.md}
  for projection in .agents/skills .claude/skills; do
    link="$projection/$skill"
    if [[ ! -L "$link" ]]; then
      echo "missing skill projection: $link" >&2
      exit 1
    fi
    if ! cmp -s "$skill_file" "$link/SKILL.md"; then
      echo "drifted skill projection: $link" >&2
      exit 1
    fi
  done
done

echo "skill projections passed"
