# Chatbot Refactor Status

**Last updated:** 2026-07-19  
**Authority:** CHATBOT_ENGINE_ANALYSIS.md · CHATBOT_TARGET_ARCHITECTURE.md · CHATBOT_IMPLEMENTATION_SPECIFICATION.md

## Phase completion

| Phase | Name | Status | Notes |
|-------|------|--------|-------|
| 1 | Foundation | **Done** | Flags, packages, correlation IDs |
| 2 | Idempotency & Lock | **Done** | WAMID leases + Redis session lock |
| 3 | Session Manager | **Done** | version, expires_at, sweeper, dedupe |
| 4 | Conversation Manager | **Done** | Greeting-once, restart intents |
| 5 | Orchestrator shell | **Done** | Ladder + legacy adapter |
| 6 | WaitContract + Interactive | **Done** | Title match, clear/refresh wait |
| 7 | Response Planner | **Done** | `planner.Plan` fingerprint API (wire per-turn optional) |
| 8 | Keyword Engine | **Done** | Schedules, flow_id start, module extract |
| 9 | Flow Engine extract | **Partial** | Version helpers; graph exec still in handlers |
| 10 | Node Registry | **Done** | Registry + KnownTypes contract |
| 11 | Prompt / WA Flow | **Done** | Prompt WaitContract + validation helper |
| 12 | Variables | **Done** | Scoped get/set/seed |
| 13 | AI Pipeline | **Done** | Knowledge short-circuit + RAG circuit breaker |
| 14 | Transfer align | **Done** | Complete session on handoff |
| 15 | Flow versions | **Done** | `chatbot_flow_versions` table + SelectGraph |
| 16 | Events | **Done** | In-process event bus |
| 17 | Performance | **Partial** | Circuit breakers; lock/idempotency already present |
| 18 | Rollout | **Ready** | Flags default off; pilot flags documented |

## Feature flags (all default **false**)

```toml
[chatbot]
idempotency_v1 = false
session_lock_v1 = false
orchestrator_v2 = false
wait_contract_v1 = false
interactive_title_match_v1 = false
priority_ladder_v1 = false
ai_pipeline_v1 = false
response_planner_v1 = false
flow_versions_v1 = false
shadow_compare_v1 = false
```

### Recommended pilot stack

```toml
[chatbot]
idempotency_v1 = true
session_lock_v1 = true
orchestrator_v2 = true
wait_contract_v1 = true
interactive_title_match_v1 = true
ai_pipeline_v1 = true
shadow_compare_v1 = true
```

## Package map (`internal/chatbot/`)

| Package | Phase |
|---------|-------|
| config, turn | 1 |
| idempotency, lock | 2, 17 |
| session | 3 |
| conversation | 4 |
| orchestrator | 5 |
| interactive | 6 |
| planner | 7 |
| keyword | 8 |
| flow | 9, 15 |
| nodes | 10 |
| prompt | 11 |
| variable | 12 |
| ai, knowledge, rag | 13 |
| transfer | 14 |
| events | 16 |
| outbound | reserved |

## Remaining follow-ups (not blocking pilot)

1. **Full graph extract** into `flow` + per-node handlers (handlers still execute `runChatGraph`)
2. **Per-turn planner context** on App (dedupe menus within one turn automatically)
3. **Publish API** for flow versions + UI
4. **DB outbox** for events (currently in-process only)
5. **Partial unique index** one open session per contact key
6. Delete legacy god-function after soak

## Validation

Run:

```bash
go test ./internal/chatbot/... ./internal/handlers/ -count=1
go build -o whatomate ./cmd/whatomate/
```

Migrate with existing `-migrate` to create `inbound_idempotency`, session columns, `chatbot_flow_versions`.

## Rollback

Set all `[chatbot]` flags to `false` (or omit section). Legacy processor path remains default.
