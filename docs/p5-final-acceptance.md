# P5 Final Product Acceptance Record

**Branch:** `p5/control-plane`  
**Acceptance date:** 2026-11-20  
**Result:** Accepted based on automated verification and user-run live acceptance on the local macOS environment.

This record supplements the detailed procedure in [p5-acceptance.md](p5-acceptance.md) and closes the previously partial Dashboard and graceful-shutdown gates.

## Acceptance evidence

| Gate | Evidence | Result |
|---|---|---|
| P5 Part 1 implementation | User-run `scripts/verify-p5-part1.sh`; GitHub Actions CI | PASS |
| P5 Part 2 implementation | User-run `scripts/verify-p5-part2.sh`; race tests/build/vet | PASS |
| Automated product smoke | User-run `scripts/accept-p5-e2e.sh` | PASS |
| Full P5 verification | User-run `scripts/verify-p5-all.sh` | PASS |
| Real upstream provider | CTAIGW OpenAI-compatible endpoint; real `glm-5.3-flash` request | PASS |
| Real Model Gateway generation | Upstream completion and expected marker returned | PASS |
| Real Agent execution | Agent task `1e8f4f95cd64468da56da0fc42313326` succeeded | PASS |
| Real durable Workflow | Run `p5-live-run-1795097643-ed6879` succeeded with two real Agent nodes | PASS |
| Workflow history and observations | 11 events; 3 successful node observations | PASS |
| Usage provenance | 5 persisted usage events with provider/model provenance | PASS |
| Dashboard Workflow Run list/detail | User inspected the persisted run and its detail page | PASS |
| Graceful shutdown | `scripts/accept-p5-shutdown.sh` returned `shutdown_requested`; process PID 45856 exited and released the listener | PASS |
| Restart persistence | User restarted AgentMesh with the same config/database and confirmed the same succeeded Run, all three node observations and all 11 events remained visible | PASS |
| Latest GitHub CI | Run [44753526235](https://github.com/thkx/agentmesh/actions/runs/44753526235) for commit `84049aa849358f220f239fea1bd88e05c5d7b2e3` completed successfully | PASS |

## Live Workflow retained after restart

- Workflow: `p5-live-workflow-1795097643-ed6879`
- Run: `p5-live-run-1795097643-ed6879`
- Status: `succeeded`
- Nodes: `draft`, `review`, `finish` — all `succeeded`
- Node observations: 3
- Workflow events: 11
- Created: `2026-11-19T14:14:37Z`
- Finished: `2026-11-19T14:15:12Z`

The user inspected the run detail after the graceful-shutdown script had confirmed process exit and after the service had been restarted. The displayed Run ID, terminal status, node observations and event history match the original live acceptance evidence.

## Scope and limitations

P5 acceptance covers the implemented local-first product control plane and the tested live provider → Model Gateway → Agent → durable Workflow → Usage Ledger path, Dashboard inspection, process-level graceful shutdown and restart persistence.

It does not claim exhaustive validation of every provider, streaming edge case, external tool side effect, remote-access hardening, destructive in-place restore, or multi-machine Runner operation. Those remain separate capabilities or acceptance scopes.

## Sign-off

**P5 product-level acceptance: PASS.**

The previously partial manual Dashboard and live graceful-shutdown/restart-persistence gates are now closed by user-observed evidence. Temporary `p5-live-*` acceptance resources are intentionally retained; remove them only after any desired evidence retention and with an explicit cleanup decision.
