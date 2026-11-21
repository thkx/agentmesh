#!/usr/bin/env bash
# Create a tiny, dependency-free Git repository for the P6 live acceptance gate.
# The Coding Runtime will create a separate Worktree; this fixture's main branch
# remains unchanged. Existing repositories are never overwritten.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE="${1:-$ROOT/data/p6-live-fixture}"
if [[ "$FIXTURE" != /* ]]; then
  FIXTURE="$ROOT/$FIXTURE"
fi

if [[ -e "$FIXTURE" ]]; then
  if [[ -d "$FIXTURE/.git" ]] && [[ -z "$(git -C "$FIXTURE" status --porcelain)" ]]; then
    printf '%s\n' "$FIXTURE"
    exit 0
  fi
  echo "Refusing to overwrite an existing non-clean fixture: $FIXTURE" >&2
  exit 1
fi

mkdir -p "$FIXTURE"
cat > "$FIXTURE/go.mod" <<'EOF'
module example.com/agentmesh-p6-fixture

go 1.23.0
EOF

cat > "$FIXTURE/ready.go" <<'EOF'
package fixture

// Ready is a tiny deterministic function used by the P6 Docker test gate.
func Ready() bool {
	return true
}
EOF

cat > "$FIXTURE/ready_test.go" <<'EOF'
package fixture

import "testing"

func TestReady(t *testing.T) {
	if !Ready() {
		t.Fatal("fixture should be ready")
	}
}
EOF

git -C "$FIXTURE" init -q
git -C "$FIXTURE" config user.name "AgentMesh P6 Acceptance"
git -C "$FIXTURE" config user.email "agentmesh-p6-acceptance@example.invalid"
git -C "$FIXTURE" add go.mod ready.go ready_test.go
git -C "$FIXTURE" commit -qm "chore: initialize P6 live acceptance fixture"
printf '%s\n' "$FIXTURE"
