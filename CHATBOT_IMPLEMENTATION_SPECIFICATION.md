# Chatbot Implementation Specification

**Project:** Whatomate  
**Document type:** Final implementation blueprint (pre-coding)  
**Status:** Authoritative refactoring plan  
**Date:** 2026-07-19  

**Source of truth (only)**

1. `CHATBOT_ENGINE_ANALYSIS.md` — current runtime  
2. `CHATBOT_TARGET_ARCHITECTURE.md` — target design  

**Rules for this document**

- No re-analysis of the codebase  
- No architecture redesign  
- No source patches, Go code, or SQL scripts  
- Bridges **current → target → ordered implementation**  
- Implementation-ready for senior engineers and project tracking  

**Companion reading order**

```
Analysis (what is) → Target Architecture (what should be) → This Spec (how to get there)
```

---

# 1. Executive Summary

## 1.1 Current implementation (baseline)

| Aspect | Reality |
|--------|---------|
| Brain | Single inbound orchestrator function owns routing, breakouts, AI, keywords, greeting, fallback |
| Flow runtime | v2 JSON graph runner for node execution |
| Interactive state | Implicit park via `session.current_step` |
| Concurrency | One goroutine per webhook message; weak WAMID dedupe; no per-contact lock |
| AI | Free-text mode flags; static context is LLM prompt material, not a ranked stage |
| Product gaps | Some model fields (flow keyword type, button triggers, schedules) not fully enforced |

**Primary failure modes to eliminate:** stuck button waits, fake menu resets, repeated keyword breakouts, AI/state desync, duplicate sends, racey sessions.

## 1.2 Target implementation

| Aspect | Target |
|--------|--------|
| Brain | Conversation Orchestrator + versioned priority ladder |
| State | Explicit state machine + **WaitContract** |
| Isolation | Session lock + WAMID idempotency |
| Modules | Session, Flow, Nodes, Interactive, Prompt, Keyword, AI pipeline, Transfer, Response Planner, Outbound |
| AI | Knowledge → RAG → rank → LLM → fallback |
| Ops | Outbox events, metrics, turn timelines, feature flags |

## 1.3 Migration strategy

**Strangler-fig behind feature flags**, not big-bang rewrite.

1. Stabilize foundation (idempotency, lock, observability) in front of the existing processor  
2. Introduce Orchestrator that initially **delegates** to existing behavior  
3. Extract engines one module at a time; flip ladder steps to new code per flag  
4. Fix P0 behavioral bugs as early flag-gated policies (wait clear, title match, AI order)  
5. Pin flow versions; retire dual-path when metrics and acceptance criteria pass  

## 1.4 Overall execution approach

- **Vertical thin slices** preferred over horizontal “rewrite all nodes”  
- **Shadow mode** (decide without send) before dual-send risk  
- **Per-org / per-account rollout**  
- Every phase has **acceptance criteria, exit criteria, and rollback**  

## 1.5 Expected outcome

A production chatbot where:

- Routing is deterministic and documented  
- Session state always matches what the user last saw  
- Interactive waits are first-class and expire/clear correctly  
- AI is grounded before generative fallback  
- Turns are correlatable, testable, and safely concurrent  
- Engineers extend the system via module boundaries, not new branches in a god-function  

---

# 2. Implementation Strategy

## 2.1 Options compared

| Strategy | Description | Fit |
|----------|-------------|-----|
| **Big Bang** | Replace processor in one release | Rejected — high outage risk, hard rollback |
| **Parallel (dual system)** | Two full engines side by side forever | Rejected as end state — cost; OK as short shadow |
| **Incremental (strangler)** | Facade + extract modules + flags | **Recommended** |

## 2.2 Recommended approach: Incremental strangler + feature flags

```
Webhook
  → Idempotency + Lock (always on when ready)
  → if flag orchestrator_v2:
        Orchestrator (ladder)
          → new modules OR legacy adapter
     else:
        legacy processIncomingMessageFull
```

### Why

| Reason | Detail |
|--------|--------|
| Matches target migration philosophy | Already specified in target architecture |
| Production continuity | WhatsApp webhooks and CRM message store stay stable |
| Isolates risk | Each ladder step can flip independently |
| Preserves graphs | v2 flow JSON remains; versioning added later |
| Testability | Shadow decisions vs legacy without user impact |

### Feature flags (minimum set)

| Flag | Purpose |
|------|---------|
| `chatbot.idempotency_v1` | Unique WAMID processing |
| `chatbot.session_lock_v1` | Per contact+account lock |
| `chatbot.orchestrator_v2` | Master orchestrator path |
| `chatbot.wait_contract_v1` | Explicit wait + clear rules |
| `chatbot.interactive_title_match_v1` | Typed title → option id |
| `chatbot.priority_ladder_v1` | Canonical routing order |
| `chatbot.ai_pipeline_v1` | Knowledge→RAG→LLM |
| `chatbot.response_planner_v1` | Dedupe / ordered outbound |
| `chatbot.flow_versions_v1` | Pin published flow version |
| `chatbot.shadow_compare_v1` | Log new vs old decision only |

Flags must be scoped: **global default → org override → account override**.

### Backward compatibility rules

- Do not break Meta webhook URL or payload handling  
- Do not break admin CRUD JSON for flows unless versioned endpoints  
- Session rows must remain readable by legacy path until orchestrator is default  
- Flow builder continues to edit graph JSON; version table is additive  

### Parallel / shadow migration

- Phase: same inbound, two decision logs, **one sender**  
- Compare: winning handler name, next state, outbound count  
- Promote only when disagreement rate is within agreed threshold or explained  

---

# 3. Dependency Graph

## 3.1 Build & runtime dependency order

```
Webhook Ingress
      │
      ▼
Idempotency Store
      │
      ▼
Session Lock
      │
      ▼
Contact + Message Persist  (CRM edge; largely keep)
      │
      ▼
Session Manager ──────────────────────────────┐
      │                                         │
      ▼                                         │
Conversation Manager (thin; may phase after)    │
      │                                         │
      ▼                                         │
Configuration Manager + Cache ◄─────────────────┤
      │                                         │
      ▼                                         │
Conversation Orchestrator (priority ladder)     │
      │                                         │
      ├──────────────┬──────────────┬───────────┼──────────────┐
      ▼              ▼              ▼           ▼              ▼
Transfer      Interactive    Flow Engine   Keyword        AI Pipeline
Engine        + WaitContract      │        Engine              │
      │              │            ▼           │         ┌──────┴──────┐
      │              │       Node Registry    │         ▼             ▼
      │              │            │           │    Knowledge        RAG
      │              ▼            ▼           │         │             │
      │         Prompt Engine  Node Handlers  │         └──────┬──────┘
      │              │            │           │                ▼
      │              │            ▼           │              LLM stage
      │              │      Variable Engine   │                │
      │              │      Expression/Cond   │                │
      │              │      Template Engine   │                │
      │              │      API / Media       │                │
      │              └────────────┬───────────┘                │
      │                           ▼                            │
      │                    Response Planner ◄──────────────────┘
      │                           │
      │                           ▼
      │                    Outbound Gateway
      │                           │
      └───────────────────────────┼────────────────────────────
                                  ▼
                    Logging · Metrics · Events · Analytics
```

## 3.2 Dependency explanations

| Edge | Why it exists |
|------|----------------|
| Webhook → Idempotency | Meta retries; at-most-once turns |
| Idempotency → Lock | Do not lock if already completed replay |
| Lock → Session | Session mutations must be serialized |
| Session → Orchestrator | All policy needs authoritative state |
| Config/Cache → Orchestrator | Settings, hours, AI mode, exclusions |
| Orchestrator → Transfer first among engines | Human ownership wins (ladder) |
| Orchestrator → Interactive before Keyword | Wait resolution before free-text rules |
| Flow → Node Registry | Flow only schedules; nodes execute |
| Nodes → Variable / Template / API / Media | Shared services, not duplicated |
| Interactive → Prompt | Prompt is a wait type; shared wait model |
| Keyword → Flow (start) | Keyword `flow` type must start Flow Engine |
| AI → Knowledge → RAG → LLM | Ranked pipeline |
| All handlers → Response Planner | Prevent double menus / order turns |
| Planner → Outbound | Single send path |
| All → Logging/Metrics/Events | Observability after commit |

### Forbidden reverse dependencies

- Nodes must not call Orchestrator  
- Outbound must not mutate session policy  
- Analytics consumers must not block the turn  
- Flow Engine must not implement keyword matching  

---

# 4. Module-by-Module Implementation Plan

Legend for effort: **S** ≤ 3d · **M** 3–10d · **L** 10–20d · **XL** > 20d (one senior eng, excluding pure QA).

---

## 4.1 Webhook

| Field | Content |
|-------|---------|
| **Current responsibility** | Verify, parse, `go process` per message |
| **Current problems** | Async without durable accept contract; weak dedupe coupling |
| **Target responsibility** | Verify, normalize `InboundEnvelope`, durable accept, enqueue or hand off |
| **Files affected** | Webhook handler package; main routes (thin) |
| **Functions affected** | Webhook verify/handler; processIncomingMessage entry |
| **Database impact** | Idempotency table (related) |
| **API impact** | None external |
| **Frontend impact** | None |
| **Configuration impact** | Production require signature flag |
| **Dependencies** | Account secrets, Idempotency |
| **Risk** | Medium |
| **Priority** | P0 |
| **Effort** | M |
| **Testing** | Signature fixtures; payload fixtures; 200 latency |
| **Migration** | Keep URL; add envelope adapter |
| **Rollback** | Flag off new enqueue path |

---

## 4.2 Session Manager

| Field | Content |
|-------|---------|
| **Current responsibility** | getOrCreate by active + last_activity; Save on graph yield |
| **Current problems** | No version lock; timeout doesn’t set status; multi active possible |
| **Target responsibility** | Single open session per key; status enum; version; expires_at; complete/expire APIs |
| **Files affected** | Processor session helpers → new session package; model |
| **Functions affected** | getOrCreateSession, exitFlow, persistChatSession |
| **Database impact** | Columns: status expansion, version, expires_at, wait_contract, flow_version |
| **API impact** | Session list/detail may show new fields |
| **Frontend impact** | Session viewer state labels |
| **Configuration impact** | timeout minutes already in settings |
| **Dependencies** | DB, Lock |
| **Risk** | High |
| **Priority** | P0 |
| **Effort** | L |
| **Testing** | Concurrent create; expire sweeper; optimistic conflict |
| **Migration** | Backfill expires_at; close duplicate actives |
| **Rollback** | Adapter maps old fields |

---

## 4.3 Conversation Manager

| Field | Content |
|-------|---------|
| **Current responsibility** | Implicit (contact + session only) |
| **Current problems** | Greeting/restart semantics tangled with session |
| **Target responsibility** | Conversation identity; bot/human/closed; greeting-once policy |
| **Files affected** | New package; optional table |
| **Functions affected** | New; greet paths in processor |
| **Database impact** | Optional `conversations` table or derive from sessions |
| **API impact** | Optional conversation APIs later |
| **Frontend impact** | Chat UI may bind conversation status later |
| **Configuration impact** | Minimal |
| **Dependencies** | Session Manager |
| **Risk** | Medium |
| **Priority** | P1 (can be thin façade over session initially) |
| **Effort** | M |
| **Testing** | Restart/resume/transfer ownership |
| **Migration** | Lazy create from first session |
| **Rollback** | Disable conversation table writes |

---

## 4.4 Flow Engine

| Field | Content |
|-------|---------|
| **Current responsibility** | runChatGraph parse/execute |
| **Current problems** | Coupled to App; completion doesn’t always clear ownership fields; no version pin |
| **Target responsibility** | Start/Resume/Cancel; version pin; loop guard; subflow optional later |
| **Files affected** | Graph runner → flow package; graph types |
| **Functions affected** | runChatGraph, parseChatGraph, resolveEdge |
| **Database impact** | flow_versions table (phase) |
| **API impact** | Publish flow version endpoint later |
| **Frontend impact** | Publish button; version history later |
| **Configuration impact** | None critical |
| **Dependencies** | Node Registry, Variable, Session wait clear |
| **Risk** | High |
| **Priority** | P0–P1 |
| **Effort** | L–XL |
| **Testing** | Golden multi-turn graphs; runaway cycle |
| **Migration** | Graph JSON unchanged initially |
| **Rollback** | Call legacy runner adapter |

---

## 4.5 Node Engine / Node Registry

| Field | Content |
|-------|---------|
| **Current responsibility** | switch in executeChatNode |
| **Current problems** | Hard to extend; mixed concerns (OpenRouter inject in API node) |
| **Target responsibility** | Registry of NodeHandler; pure NodeResult |
| **Files affected** | Graph runner split into nodes/* |
| **Functions affected** | All execChat* |
| **Database impact** | None |
| **API impact** | None |
| **Frontend impact** | New node types only when added |
| **Configuration impact** | None |
| **Dependencies** | Template, Variable, API, Media, AI, Transfer |
| **Risk** | Medium |
| **Priority** | P1 |
| **Effort** | L |
| **Testing** | Per-node contract tests (existing tests ported) |
| **Migration** | Move one node type per PR |
| **Rollback** | Keep switch adapter |

---

## 4.6 Variable Engine

| Field | Content |
|-------|---------|
| **Current responsibility** | session_data JSONB ad hoc |
| **Current problems** | No scopes; cleanup inconsistent |
| **Target responsibility** | Scoped get/set; system vars; flow clear on exit |
| **Files affected** | New package; processTemplate call sites |
| **Functions affected** | processTemplate consumers; store_as paths |
| **Database impact** | Keep JSONB blob initially |
| **API impact** | Contact panel vars unchanged |
| **Frontend impact** | Variable picker can use scope prefixes later |
| **Configuration impact** | None |
| **Dependencies** | Session |
| **Risk** | Medium |
| **Priority** | P1 |
| **Effort** | M |
| **Testing** | Scope isolation; template resolution order |
| **Migration** | Read legacy flat keys as session/flow |
| **Rollback** | Flat map writer |

---

## 4.7 Button / List / Interactive Engine

| Field | Content |
|-------|---------|
| **Current responsibility** | execChatButtons + sendAndSaveInteractiveButtons + breakout block in processor |
| **Current problems** | No title match; breakout leaves park; double send; no expiry contract |
| **Target responsibility** | WaitContract; match id/title/alias; expire; nav intents; single render fingerprint |
| **Files affected** | Processor breakout block; graph buttons; interactive send helpers |
| **Functions affected** | execChatButtons; free-text breakout; send interactive |
| **Database impact** | wait_contract JSONB on session |
| **API impact** | None |
| **Frontend impact** | Node config: free_text_policy, invalid_policy, aliases |
| **Configuration impact** | Defaults for policies |
| **Dependencies** | Session, Response Planner, Flow |
| **Risk** | **Critical** (UX) |
| **Priority** | **P0** |
| **Effort** | L |
| **Testing** | Title match; expire; duplicate click; breakout clears wait |
| **Migration** | Synthesize WaitContract from current_step + node type |
| **Rollback** | Flag wait_contract off |

---

## 4.8 Prompt Engine

| Field | Content |
|-------|---------|
| **Current responsibility** | execChatPrompt + retries |
| **Current problems** | Coupled to graph runner; cancel/transfer edge cases |
| **Target responsibility** | Wait type prompt; validation; max_retries outcomes |
| **Files affected** | Graph runner prompt funcs → prompt package |
| **Functions affected** | execChatPrompt, handleChatPromptInvalid |
| **Database impact** | step_retries remains or inside wait |
| **API impact** | None |
| **Frontend impact** | Expected response UI already maps to prompt |
| **Configuration impact** | None |
| **Dependencies** | Interactive wait model, Variable |
| **Risk** | Medium |
| **Priority** | P1 |
| **Effort** | M |
| **Testing** | Port existing prompt tests |
| **Migration** | Adapter |
| **Rollback** | Legacy exec |

---

## 4.9 WhatsApp Flow Engine

| Field | Content |
|-------|---------|
| **Current responsibility** | execChatWhatsAppFlow + nfm parse in processor |
| **Current problems** | Wait not first-class; partial misconfig advance |
| **Target responsibility** | WaitContract nfm; merge fields; timeout |
| **Files affected** | Graph runner; inbound nfm parse |
| **Functions affected** | execChatWhatsAppFlow; NFM branch in full processor |
| **Database impact** | wait_contract |
| **API impact** | None |
| **Frontend impact** | Node props unchanged initially |
| **Configuration impact** | None |
| **Dependencies** | Outbound flow send; Session |
| **Risk** | Medium |
| **Priority** | P1 |
| **Effort** | M |
| **Testing** | Submit advances; missing flow_id policy |
| **Migration** | Envelope carries nfm map |
| **Rollback** | Legacy path |

---

## 4.10 Keyword Engine

| Field | Content |
|-------|---------|
| **Current responsibility** | matchKeywordRules; partial use by priority |
| **Current problems** | flow type not starting flows; ActiveFrom/Until unused; conditions unused; contains vs whole-word mismatch with flows |
| **Target responsibility** | Pure matcher + action descriptors; enforce schedules; flow action starts Flow Engine |
| **Files affected** | Keyword match in processor; CRUD cache invalidation |
| **Functions affected** | matchKeywordRules |
| **Database impact** | None required; enforce existing columns |
| **API impact** | Document response_type=flow behavior as live |
| **Frontend impact** | Show schedule actually enforced |
| **Configuration impact** | None |
| **Dependencies** | Cache, Orchestrator ladder, Flow start |
| **Risk** | Medium |
| **Priority** | P0 (behavior) / P1 (schedules) |
| **Effort** | M |
| **Testing** | Priority order; schedule window; flow action |
| **Migration** | Default match semantics documented; optional whole-word mode |
| **Rollback** | Flag ladder keyword step |

---

## 4.11 Template / Condition / Expression Engines

| Field | Content |
|-------|---------|
| **Current responsibility** | processTemplate; expr-lang condition; skip_condition |
| **Current problems** | Scattered; error handling uneven |
| **Target responsibility** | Shared packages; safe expr allow-list; missing-var policy |
| **Files affected** | template_engine; condition helpers in graph runner |
| **Functions affected** | processTemplate; evaluateConditionExpression |
| **Database impact** | None |
| **API impact** | None |
| **Frontend impact** | Simulation may reuse same rules later |
| **Configuration impact** | Strict vs lenient templates |
| **Dependencies** | Variable Engine |
| **Risk** | Low–Medium |
| **Priority** | P1 |
| **Effort** | S–M |
| **Testing** | Expression suite; missing vars |
| **Migration** | Drop-in |
| **Rollback** | Old functions |

---

## 4.12 API Engine / Media Engine

| Field | Content |
|-------|---------|
| **Current responsibility** | executeConfiguredAPI; media download/send |
| **Current problems** | OpenRouter key injection inside API node; timeouts mixed |
| **Target responsibility** | SSRF-safe API; retries; media caps; no provider secrets in node code (config port) |
| **Files affected** | API helpers; media handlers; graph api_call |
| **Functions affected** | executeConfiguredAPI; sendAndSaveNodeMessage; downloads |
| **Database impact** | None |
| **API impact** | None |
| **Frontend impact** | None |
| **Configuration impact** | Timeout defaults |
| **Dependencies** | HTTP client, storage, secrets |
| **Risk** | Medium |
| **Priority** | P1 |
| **Effort** | M |
| **Testing** | Non-2xx mapping; media split for audio |
| **Migration** | Move secret injection to config adapter |
| **Rollback** | Legacy helpers |

---

## 4.13 AI / Knowledge / RAG Engines

| Field | Content |
|-------|---------|
| **Current responsibility** | generateAIResponse; tryRAG; buildAIContext; free-text mode |
| **Current problems** | Static never short-circuits; mode can skip RAG; contact hardcode in processor |
| **Target responsibility** | Pipeline stages + ranker; explicit modes; no product hardcode in core |
| **Files affected** | AI section of processor → ai/, knowledge/, rag/ |
| **Functions affected** | generateAIResponse; tryRAG*; buildAIContext; canAttemptAI; resolveFreeTextMode |
| **Database impact** | settings free_text / pipeline mode fields clarified |
| **API impact** | Settings schema: pipeline mode enum |
| **Frontend impact** | Settings UI for pipeline mode; AI context types |
| **Configuration impact** | Defaults: knowledge_rag or knowledge_rag_llm |
| **Dependencies** | HTTP, cache, session history |
| **Risk** | High (answer quality + cost) |
| **Priority** | **P0** |
| **Effort** | L |
| **Testing** | Stage order tests; abstain; fallback; no LLM when knowledge hits |
| **Migration** | Map old free_text modes → new pipeline modes |
| **Rollback** | `chatbot.ai_pipeline_v1` off |

---

## 4.14 Transfer Engine

| Field | Content |
|-------|---------|
| **Current responsibility** | hasActiveAgentTransfer; create transfer helpers; transfer node |
| **Current problems** | Transfer keyword may leave flow session active underneath |
| **Target responsibility** | Atomic transfer + session state HumanTransfer; resume snapshot |
| **Files affected** | agent_transfers; processor transfer paths; transfer node |
| **Functions affected** | createTransfer*; hasActive*; execChatTransfer |
| **Database impact** | Align session status with transfer |
| **API impact** | Existing transfer APIs |
| **Frontend impact** | Agent queue UI unchanged |
| **Configuration impact** | Business hours + transfer |
| **Dependencies** | Session, Orchestrator |
| **Risk** | Medium–High |
| **Priority** | P0–P1 |
| **Effort** | M |
| **Testing** | Bot silenced; resume restores correctly |
| **Migration** | On transfer, complete or freeze cursor per policy (choose freeze+status) |
| **Rollback** | Legacy create paths |

---

## 4.15 Response Planner

| Field | Content |
|-------|---------|
| **Current responsibility** | None (callers send immediately) |
| **Current problems** | Dual sends; greeting after AI without state |
| **Target responsibility** | Order turns; dedupe by fingerprint; bind to state transitions |
| **Files affected** | New package; all send sites eventually |
| **Functions affected** | sendAndSave* call sites |
| **Database impact** | outbound_turns optional |
| **API impact** | None |
| **Frontend impact** | None |
| **Configuration impact** | Max messages per turn |
| **Dependencies** | Session wait fingerprints |
| **Risk** | Medium |
| **Priority** | **P0** |
| **Effort** | M |
| **Testing** | No double menu for same fingerprint; AI+menu requires new wait |
| **Migration** | Wrap legacy sends |
| **Rollback** | Planner no-op pass-through |

---

## 4.16 Outbound Gateway

| Field | Content |
|-------|---------|
| **Current responsibility** | SendOutgoingMessage + ChatbotSendOptions |
| **Current problems** | OK core; many wrappers |
| **Target responsibility** | Single gateway; retries; metrics |
| **Files affected** | messages send path; chatbot wrappers |
| **Functions affected** | SendOutgoingMessage; sendAndSave* |
| **Database impact** | messages table |
| **API impact** | None |
| **Frontend impact** | Chat stream same |
| **Configuration impact** | Retry policy |
| **Dependencies** | WhatsApp client |
| **Risk** | Medium |
| **Priority** | P1 |
| **Effort** | M |
| **Testing** | 429 retry; failure surfaces to planner |
| **Migration** | Rename wrappers |
| **Rollback** | Direct send |

---

## 4.17 Logging / Metrics / Analytics

| Field | Content |
|-------|---------|
| **Current responsibility** | Structured logs; limited metrics |
| **Current problems** | No turn timeline; hard to debug ladder |
| **Target responsibility** | correlation_id; turn timeline; metrics; outbox events |
| **Files affected** | processor logs; new telemetry |
| **Functions affected** | All turn entry/exit |
| **Database impact** | turn_traces / outbox optional |
| **API impact** | Optional debug session timeline API |
| **Frontend impact** | Session viewer timeline |
| **Configuration impact** | Log level; sample rate |
| **Dependencies** | Orchestrator spans |
| **Risk** | Low |
| **Priority** | P0 (timeline) / P1 (analytics) |
| **Effort** | M–L |
| **Testing** | Every fixture turn has timeline |
| **Migration** | Additive logs first |
| **Rollback** | Disable persistence of traces |

---

## 4.18 Cache / Configuration / Database / Security layers

| Module | Priority | Effort | Notes |
|--------|----------|--------|-------|
| **Cache** | P1 | S–M | Keep Redis definition cache; always invalidate on write; version keys for flows |
| **Configuration Manager** | P1 | M | Hierarchical resolve; flag store |
| **Database Layer** | P0–P1 | L | Migrations for session/wait/idempotency/versions |
| **Security** | P0 | M | Signature enforce mode; rate limit; redaction |

---

# 5. File-Level Refactoring Plan

Paths reflect current layout and target package split (from analysis + target). “App handlers” remain HTTP adapters.

| File / area | Purpose today | Future | Action | Risk | Priority | Depends on |
|-------------|---------------|--------|--------|------|----------|------------|
| `cmd/whatomate/main.go` | Routes, wiring | Wire new packages + flags | **Modify** | Low | P0 | DI |
| `internal/handlers/webhook.go` | Ingress | Thin → inbound envelope | **Refactor** | Med | P0 | Idempotency |
| `internal/handlers/chatbot_processor.go` | God orchestrator | Shrink to legacy adapter / delete logic | **Refactor → Replace** | **Critical** | P0 | Orchestrator |
| `internal/handlers/chatbot_graph_runner.go` | Flow+nodes | Move to `internal/chatbot/flow` + `nodes` | **Refactor / Split** | High | P1 | Registry |
| `internal/handlers/chatbot_graph_types.go` | Graph types | `internal/chatbot/flow/graph` | **Move** | Low | P1 | — |
| `internal/handlers/chatbot_flow_migration.go` | Legacy backfill | Keep until steps retired | **Keep** | Low | P3 | — |
| `internal/handlers/chatbot.go` | Admin CRUD | CRUD + version publish later | **Modify** | Med | P1 | DB |
| `internal/handlers/cache.go` | Redis caches | Config/cache service | **Refactor** | Med | P1 | — |
| `internal/handlers/agent_transfers.go` | Transfers | Transfer engine port | **Refactor** | Med | P1 | Session |
| `internal/handlers/messages.go` | Outbound send | Outbound gateway | **Refactor** | Med | P1 | Planner |
| `internal/handlers/template_engine.go` | Templates | Variable+template packages | **Move** | Low | P1 | — |
| `internal/models/chatbot.go` | Models | Add wait/version fields | **Modify** | Med | P0 | Migrations |
| `internal/models/constants.go` | Enums | State + pipeline enums | **Modify** | Low | P0 | — |
| `internal/contactutil/*` | Contacts | Keep | **Keep** | Low | — | — |
| `pkg/whatsapp/*` | WA client | Keep | **Keep** | Low | — | — |
| `internal/chatbot/orchestrator/*` | — | Ladder + state machine | **Create** | High | P0 | Session |
| `internal/chatbot/session/*` | — | Session manager | **Create** | High | P0 | DB |
| `internal/chatbot/conversation/*` | — | Conversation manager | **Create** | Med | P1 | Session |
| `internal/chatbot/flow/*` | — | Flow engine | **Create** | High | P1 | Nodes |
| `internal/chatbot/nodes/*` | — | Handlers | **Create** | Med | P1 | Ports |
| `internal/chatbot/interactive/*` | — | Wait + buttons/lists | **Create** | High | P0 | Session |
| `internal/chatbot/prompt/*` | — | Prompt engine | **Create** | Med | P1 | Interactive |
| `internal/chatbot/keyword/*` | — | Keyword engine | **Create** | Med | P0 | Cache |
| `internal/chatbot/variable/*` | — | Variables | **Create** | Med | P1 | — |
| `internal/chatbot/ai/*` | — | Pipeline | **Create** | High | P0 | RAG/KB |
| `internal/chatbot/knowledge/*` | — | Knowledge stage | **Create** | Med | P0 | AI contexts |
| `internal/chatbot/rag/*` | — | RAG stage | **Create** | Med | P0 | HTTP |
| `internal/chatbot/transfer/*` | — | Transfer | **Create** | Med | P1 | DB |
| `internal/chatbot/planner/*` | — | Response planner | **Create** | Med | P0 | — |
| `internal/chatbot/outbound/*` | — | Gateway | **Create** | Med | P1 | WA |
| `internal/chatbot/idempotency/*` | — | WAMID store | **Create** | Med | P0 | DB |
| `internal/chatbot/lock/*` | — | Redis lock | **Create** | Med | P0 | Redis |
| `internal/chatbot/events/*` | — | Outbox | **Create** | Med | P1 | DB |
| `internal/chatbot/config/*` | — | Flags + settings | **Create** | Med | P0 | Cache |
| `internal/chatbot/testkit/*` | — | Flow DSL | **Create** | Low | P1 | — |
| Frontend flow builder | Author graphs | Policy fields on nodes | **Modify** | Med | P1 | API |
| Frontend settings | AI modes | Pipeline mode UI | **Modify** | Med | P1 | API |
| Frontend session views | Limited | Timeline viewer | **Create/Modify** | Low | P2 | API |
| Tests `*_processor_test.go` | Processor | Port to orchestrator | **Refactor** | Med | P0 | — |
| Tests `*_graph_runner_test.go` | Graph | Port to flow/nodes | **Refactor** | Med | P1 | — |

**Delete only after** orchestrator default + soak: dead breakout blocks, unreachable legacy branches, unused step executor references (not table drop until data policy allows).

---

# 6. Database Migration Plan

## 6.1 Existing tables (keep)

`chatbot_settings`, `keyword_rules`, `chatbot_flows`, `chatbot_flow_steps` (legacy), `chatbot_sessions`, `chatbot_session_messages`, `ai_contexts`, `agent_transfers`, `messages`, `contacts`, org/account tables.

## 6.2 New tables (additive)

| Table | Purpose | Phase |
|-------|---------|-------|
| `inbound_idempotency` | WAMID processing lease/state | Foundation |
| `chatbot_flow_versions` | Immutable published graphs | Flow versioning |
| `domain_events_outbox` | Reliable events | Observability |
| `chatbot_turn_traces` (optional) | Execution timelines | Observability |
| `outbound_turns` (optional) | Send dedupe per turn | Planner |
| `conversations` (optional) | Conversation aggregate | Conversation phase |

## 6.3 Columns to add (sessions — critical)

| Column | Purpose |
|--------|---------|
| `version` | Optimistic concurrency |
| `expires_at` | Explicit expiry |
| `state` or expanded `status` | State machine |
| `wait_contract` JSONB | Interactive/prompt wait |
| `flow_version` | Pinned definition |
| `cursor_node_id` | Prefer over overloading current_step (or alias) |
| `completion_reason` | Audit |
| `last_inbound_wamid` | Debug / dedupe assist |
| `turn_seq` | Ordering |

## 6.4 Columns / tables deprecated (later)

| Item | When |
|------|------|
| Sole reliance on `current_step` without wait_contract | After wait_contract_v1 default |
| `chatbot_flow_steps` as runtime source | Already dead; drop only after archival policy |
| Unenforced keyword fields without enforcement | Do not remove — **implement** ActiveFrom/Until |

## 6.5 Indexes / constraints (conceptual)

- **UNIQUE** inbound WAMID (idempotency and/or messages)  
- Partial unique: one open session per (org, account, contact)  
- Index open sessions by `expires_at`  
- Flow versions by `(flow_id, version)` unique  

## 6.6 Migration order

1. Idempotency table + write path  
2. Session additive columns (nullable) + backfill expires_at  
3. Application dual-read  
4. Enforce unique open session (cleanup duplicates first)  
5. wait_contract population  
6. flow_versions + pin on start  
7. outbox / traces  
8. Optional conversations  

## 6.7 Data migration

- Duplicate active sessions → keep latest last_activity; complete others `superseded`  
- Synthesize wait_contract for sessions parked on buttons/prompt by loading flow graph node type  
- Map free_text_mode → pipeline mode in settings  

## 6.8 Rollback

- Additive columns remain (safe)  
- Stop writing new tables; ignore columns  
- Do not drop columns in same release as introduce  
- Unique constraints: use non-blocking create when possible; have cleanup job before enforce  

---

# 7. API Migration Plan

## 7.1 Current APIs (keep stable)

- Webhook GET/POST  
- Chatbot settings GET/PUT  
- Keywords CRUD  
- Flows CRUD  
- AI contexts CRUD  
- Transfers list/pick/resume  
- Sessions list/get  
- Analytics chatbot  

## 7.2 Future APIs (additive preferred)

| Endpoint concept | Purpose | Breaking? |
|------------------|---------|-----------|
| Flow **publish** / list versions | Version pin | No if additive |
| Session **timeline** | Debug turn traces | No |
| Feature flag admin (or use config) | Rollout | No |
| Conversation list (optional) | CRM | No |

## 7.3 Deprecated behaviors (not necessarily endpoints)

| Behavior | Replacement |
|----------|-------------|
| Keyword flow type as text-only | Starts flow |
| Settings free_text only | Pipeline mode (map old values) |
| Implicit breakout always on | Node/global free_text_policy |

## 7.4 Versioning

- HTTP: additive JSON fields; no /v2 unless breaking  
- Flow content: integer `flow_version`  
- Feature flags as rollout versioning  

## 7.5 Breaking changes (avoid in pilot)

- Renaming graph node types  
- Removing session_data keys  
- Requiring signature without grace period  

---

# 8. Frontend Migration Plan

| Surface | Remains | Changes | Risk |
|---------|---------|---------|------|
| **Flow Builder** | Graph edit, node palette | Node advanced: free_text_policy, invalid_policy, aliases, timeout; later publish version | Med |
| **Chat UI** | Message stream | None required for P0 | Low |
| **Admin Settings** | AI provider fields | Pipeline mode selector; document defaults | Med |
| **AI Context UI** | CRUD types | Labels: knowledge vs RAG roles; priority | Low |
| **Keyword editor** | CRUD | Show schedule enforced; flow action works | Low |
| **Session Viewer** | Basic | State, wait contract summary, timeline | Med |
| **Analytics** | Existing | Funnel from path/events later | Low |
| **Agent transfers** | Existing | Reflect HumanTransfer consistently | Low |

**Migration risks:** authors enable breakout policies incorrectly; settings mode mis-map causes sudden LLM use — mitigate with defaults and admin warnings.

---

# 9. Configuration Migration

| Area | Action |
|------|--------|
| **PostgreSQL** | Run additive migrations per phase |
| **Redis** | Locks key namespace `chatbot:lock:`; keep definition caches; shorter TTL optional |
| **OpenRouter / LLM** | Stay in settings encrypted; pipeline decides when called |
| **RAG** | ai_contexts type=rag URLs/headers unchanged |
| **Env vars** | `CHATBOT_REQUIRE_WEBHOOK_SIGNATURE`, `CHATBOT_ORCHESTRATOR_DEFAULT`, lock TTL, DLQ |
| **Feature flags** | Prefer DB/org settings over env for per-tenant; env for global kill switch |
| **Defaults** | Pipeline `knowledge_rag` when RAG exists; else `knowledge_only` or mapped legacy; session timeout keep 30m; lock TTL 15–30s |
| **Secrets** | No new plaintext; encrypt as today |

---

# 10. Refactoring Phases

---

### Phase 1 — Foundation

| | |
|--|--|
| **Objectives** | Correlation IDs; feature flag harness; package skeleton; no behavior change |
| **Dependencies** | None |
| **Deliverables** | `internal/chatbot/*` stubs; flags; turn_id on logs |
| **Risks** | Premature abstraction |
| **Acceptance** | Flags readable; legacy path 100% traffic |
| **Exit** | Staging deploy green |

### Phase 2 — Idempotency & Lock

| | |
|--|--|
| **Objectives** | At-most-once WAMID; per contact+account lock |
| **Dependencies** | Phase 1 |
| **Deliverables** | Idempotency store; Redis lock; wrap entry |
| **Risks** | Deadlocks; lock timeout storms |
| **Acceptance** | Duplicate webhook → one bot turn; concurrent messages serialized |
| **Exit** | Load test pass; flag default on pilot org |

### Phase 3 — Session Manager

| | |
|--|--|
| **Objectives** | version, expires_at, complete/expire APIs; duplicate active cleanup |
| **Dependencies** | Phase 2 |
| **Deliverables** | Session service; sweeper job |
| **Risks** | Wrong session selected |
| **Acceptance** | One open session; expired not resumed; optimistic conflict handled |
| **Exit** | Metrics on expire counts |

### Phase 4 — Conversation Manager (thin)

| | |
|--|--|
| **Objectives** | Greeting-once / restart semantics detached from ad-hoc ifs |
| **Dependencies** | Phase 3 |
| **Deliverables** | Thin manager or session policies |
| **Risks** | Double greeting |
| **Acceptance** | New session greeting rules match product doc |
| **Exit** | Product sign-off on greeting matrix |

### Phase 5 — Orchestrator shell + ladder skeleton

| | |
|--|--|
| **Objectives** | HandleTurn; ladder steps call **legacy adapter** |
| **Dependencies** | Phases 2–3 |
| **Deliverables** | Orchestrator; shadow compare optional |
| **Risks** | Divergence logs noise |
| **Acceptance** | Flag on: same external behavior as legacy |
| **Exit** | Shadow disagreement < threshold |

### Phase 6 — WaitContract + Interactive Engine (P0 behavior)

| | |
|--|--|
| **Objectives** | Explicit waits; title match; clear on resolve/breakout; no fake reset |
| **Dependencies** | Phases 3, 5 |
| **Deliverables** | Interactive engine; planner hooks; flags |
| **Risks** | UX change mid-conversation |
| **Acceptance** | Baseline problems 2,3,4,7,10 fixed under flag |
| **Exit** | Multi-turn interactive suite green |

### Phase 7 — Response Planner

| | |
|--|--|
| **Objectives** | Single ordered outbound plan per turn |
| **Dependencies** | Phase 6 |
| **Deliverables** | Planner; fingerprint |
| **Risks** | Dropped messages if plan bug |
| **Acceptance** | No duplicate identical menus in one turn |
| **Exit** | Pilot org 7 days |

### Phase 8 — Keyword Engine + ladder position

| | |
|--|--|
| **Objectives** | Pure match; flow action starts flow; schedules; ladder after wait |
| **Dependencies** | Phase 5–6 |
| **Deliverables** | Keyword package; tests |
| **Risks** | Intent matching changes |
| **Acceptance** | Wait click beats keyword; flow type works |
| **Exit** | Keyword fixture suite |

### Phase 9 — Flow Engine extract

| | |
|--|--|
| **Objectives** | Flow package; pin optional version later |
| **Dependencies** | Phase 5; nodes still in place OK |
| **Deliverables** | FlowEngine.Start/Resume |
| **Risks** | Regression on edges |
| **Acceptance** | Graph tests ported 100% |
| **Exit** | No legacy runChatGraph call when flag on |

### Phase 10 — Node Registry split

| | |
|--|--|
| **Objectives** | One handler file per node type |
| **Dependencies** | Phase 9 |
| **Deliverables** | Registry; handlers |
| **Risks** | Missed node type |
| **Acceptance** | All node types registered; tests green |
| **Exit** | Code owners on nodes/ |

### Phase 11 — Prompt & WhatsApp Flow engines

| | |
|--|--|
| **Objectives** | Wait-type engines share WaitContract |
| **Dependencies** | Phase 6, 10 |
| **Deliverables** | Prompt + NFM handlers |
| **Risks** | Form field merge bugs |
| **Acceptance** | Prompt/NFM tests ported |
| **Exit** | — |

### Phase 12 — Variable / Template / Expression

| | |
|--|--|
| **Objectives** | Scoped variables; shared template/expr |
| **Dependencies** | Phase 3, 10 |
| **Deliverables** | Packages; cleanup on flow exit |
| **Risks** | Template break |
| **Acceptance** | Existing template tests + scope tests |
| **Exit** | — |

### Phase 13 — AI Pipeline (Knowledge + RAG + LLM)

| | |
|--|--|
| **Objectives** | Ranked pipeline; mode mapping; remove hardcode from core or move to config plugin |
| **Dependencies** | Phase 5; history access |
| **Deliverables** | ai/knowledge/rag packages; settings UI |
| **Risks** | Cost spikes; quality regressions |
| **Acceptance** | Knowledge hit skips LLM; RAG before LLM; abstain fallback |
| **Exit** | Quality eval set agreed with product |

### Phase 14 — Transfer Engine alignment

| | |
|--|--|
| **Objectives** | Session state HumanTransfer; clear bot; resume |
| **Dependencies** | Phase 3, 5 |
| **Deliverables** | Transfer integration tests |
| **Risks** | Lost context on resume |
| **Acceptance** | No bot messages while active transfer |
| **Exit** | Agent team UAT |

### Phase 15 — Flow versioning

| | |
|--|--|
| **Objectives** | Publish immutable versions; pin on session |
| **Dependencies** | Phase 9 |
| **Deliverables** | versions table; publish API; builder publish |
| **Risks** | Editors confused |
| **Acceptance** | In-flight sessions unchanged by publish |
| **Exit** | — |

### Phase 16 — Events, Metrics, Analytics

| | |
|--|--|
| **Objectives** | Outbox; dashboards; business metrics |
| **Dependencies** | Orchestrator stable |
| **Deliverables** | Event consumers; Grafana/etc. |
| **Risks** | Event spam |
| **Acceptance** | TurnCompleted always emitted |
| **Exit** | On-call runbook |

### Phase 17 — Performance & hardening

| | |
|--|--|
| **Objectives** | Latency targets; circuit breakers; rate limits |
| **Dependencies** | Phases 2–14 |
| **Deliverables** | Load test report |
| **Risks** | Premature optimization earlier avoided |
| **Acceptance** | p95 targets from target arch met on staging |
| **Exit** | — |

### Phase 18 — Testing completion & Production rollout

| | |
|--|--|
| **Objectives** | Full suite; pilot → gradual default on |
| **Dependencies** | All P0 phases |
| **Deliverables** | Rollout plan execution; freeze legacy |
| **Risks** | Long-tail graph edge cases |
| **Acceptance** | See §18 + checklist complete for P0/P1 |
| **Exit** | Legacy processor deleted or compile-time excluded |

---

# 11. Risk Assessment

| Risk | Prob. | Impact | Mitigation | Owner |
|------|-------|--------|------------|-------|
| Behavior change breaks live customers | H | H | Flags, pilot, shadow, gradual | Eng lead |
| Session desync after partial send | M | H | Planner + outbound_turns; don’t advance on required send fail | Backend |
| Lock contention under burst | M | M | Short TTL; fair queue; metrics on wait | Backend |
| Unique WAMID migration fails on dup history | M | H | Cleanup job before unique; nullable phase | DBA/Eng |
| Ladder order regresses keywords | M | M | Snapshot tests for ladder | Backend |
| AI cost explosion | M | M | knowledge_rag default; budgets; metrics | Product+Eng |
| RAG downtime | M | M | Circuit breaker; fallback message | Backend |
| Prompt injection | L | H | Pipeline guards; treat RAG as data | Security |
| Dual-write schema drift | M | H | Single writer module; no dual session formats long | Eng lead |
| Frontend authors misconfigure free_text_policy | M | M | Safe defaults; docs in builder | Product |
| Incomplete test port | H | H | Phase exit requires ported suite | QA+Eng |
| Rollback leaves half-migrated rows | M | M | Additive schema; dual-read | Eng |

---

# 12. Testing Strategy

| Layer | Scope | When |
|-------|-------|------|
| **Unit** | Ladder pure decisions; title match; edge resolve; mode mapping | Every PR |
| **Node contract** | Each handler I/O | Phase 10+ |
| **Flow multi-turn** | Graph fixtures DSL | Phase 9+ |
| **Interactive** | Wait clear; expire; duplicate click; breakout | Phase 6 |
| **Prompt** | Validate/retry/max | Phase 11 |
| **Keyword** | Priority, schedule, flow action | Phase 8 |
| **AI pipeline** | Stage order, abstain, no LLM on knowledge hit | Phase 13 |
| **RAG** | HTTP mock; timeout; history | Phase 13 |
| **Transfer** | Silence bot; resume | Phase 14 |
| **Integration** | DB+Redis+fake WA | Continuous |
| **Regression** | Sanitized production timelines | Pre-rollout |
| **Load** | Concurrent contacts; lock correctness | Phase 2, 17 |
| **Chaos** | Kill mid-turn; duplicate webhook; RAG 500 | Phase 17–18 |
| **UAT / production validation** | Pilot org scripts | Phase 18 |
| **Acceptance** | Invariants in target Appendix B | Gate release |

**Minimum invariant tests (must stay green):**

1. Same WAMID → one bot turn  
2. Button click not stolen by keyword  
3. Title match equals click  
4. Breakout clears or replaces wait  
5. Transfer active → no automation  
6. Knowledge/RAG before LLM when mode requires  

---

# 13. Performance Plan

| Concern | Plan |
|---------|------|
| Caching | Definitions only; invalidate on write; optional in-process LRU for flow versions |
| Indexes | As §6; verify explain on session lookup |
| Pooling | Existing DB/Redis pools; size workers × connections |
| Parallelism | Never parallel turns same session; optional parallel independent AI stages later |
| Queues | Optional inbound queue after foundation if webhook latency tight |
| Memory | Cap path length; limit history window |
| Latency | Measure per stage; budget AI separately |
| Scaling | Stateless workers + Redis lock + primary Postgres |
| Monitoring | p95 turn latency; lock wait; AI stage latency |

---

# 14. Security Plan

| Control | Implementation phase |
|---------|---------------------|
| Webhook HMAC enforce mode | Foundation / Security |
| Org isolation on all queries | Continuous |
| Idempotency / replay | Phase 2 |
| Rate limit per contact/account | Phase 17 (basic earlier if needed) |
| Session lock | Phase 2 |
| Secret encryption unchanged | Keep |
| Prompt/RAG injection guards | Phase 13 |
| Audit on config + transfer | Phase 16 |
| Redact PII in logs | Phase 1–2 |
| RBAC on admin APIs | Keep existing |

---

# 15. Monitoring Plan

| Signal | Type | Alert |
|--------|------|-------|
| Turn success/error rate | Metric | Error > baseline |
| Lock wait p95 | Metric | > 200ms sustained |
| Idempotency conflicts | Metric | Info; spike unusual |
| Duplicate outbound | Metric | > 0 investigate |
| Wait expired rate | Business | — |
| Keyword vs flow vs AI share | Business | — |
| RAG latency / abstain | AI | Latency SLO |
| LLM calls / cost proxy | AI | Budget |
| Transfer created | Business | — |
| DLQ depth | Ops | > 0 critical |
| Shadow disagreement | Rollout | > threshold pause |
| Health | Process + DB + Redis | Standard |

**Dashboards:** Turn overview · Interactive waits · AI pipeline · Rollout flags  

**Tracing:** correlation_id → turn timeline (logs or traces table)

---

# 16. Rollback Plan

| Phase | Feature rollback | Data rollback | Notes |
|-------|------------------|---------------|-------|
| Flags | Global kill `orchestrator_v2` | N/A | Instant |
| Idempotency | Flag off | Keep table | Safe |
| Lock | Flag off | N/A | Possible races return |
| Session columns | Stop writing extras | Keep columns | Dual-read legacy |
| Wait contract | Flag off | Ignore JSONB | Resume current_step path |
| AI pipeline | Flag off | Mode map reverse | Legacy generateAIResponse |
| Flow versions | Use latest graph on flow row | Versions retained | |
| DB unique | Cannot easily drop under load | Have expand/contract | Plan carefully |
| Deploy | Previous artifact | — | Standard |
| Emergency | Disable chatbot / queue to agents | — | Product call |

**Emergency recovery**

1. Kill switch orchestrator + AI if needed  
2. Enable human transfer default if bot unsafe  
3. Replay DLQ only after fix  
4. Do not mass-rewrite session state without backup  

---

# 17. Deployment Plan

```
Dev → CI (unit/flow) → Staging (full) → Pilot org(s) → % production → Default on → Remove legacy
```

| Stage | Actions |
|-------|---------|
| **Dev** | Flags on; fake WA |
| **Testing** | CI gates §12 |
| **Staging** | Production-like Meta test numbers; chaos |
| **Pilot** | 1–3 orgs; dense logging; daily review |
| **Production** | Gradual account enable |
| **Monitoring** | §15 dashboards live |
| **Rollback triggers** | Error rate, double-send, stuck-wait tickets, shadow disagreement, AI cost |
| **Post deploy** | Scripted UAT: greeting, menu click, title type, keyword, RAG, transfer, cancel |

---

# 18. Acceptance Criteria

For each module, four levels:

| Level | Meaning |
|-------|---------|
| **Done** | Code merged behind flag; unit tests pass |
| **Working** | Staging multi-turn scenarios pass |
| **Validated** | Pilot production metrics + UAT sign-off |
| **Production ready** | Default on; rollback tested; runbook; SLOs green 7 days |

### Module quick gates

| Module | Production ready means |
|--------|------------------------|
| Webhook | Signature mode OK; accept p95 met |
| Idempotency/Lock | Dup & concurrency tests live |
| Session | One open; expire job; no stuck orphans spike |
| Orchestrator | Ladder snapshot tests; shadow OK |
| Interactive | Invariants 2–4; support tickets down |
| Planner | Zero duplicate menu metric |
| Keyword | Schedules + flow action validated |
| Flow/Nodes | Full graph suite |
| AI pipeline | Eval set; cost within budget |
| Transfer | No bot leak during human |
| Observability | Timeline for every pilot turn |

---

# 19. Implementation Checklist

Status values for tracking: `TODO` · `IN_PROGRESS` · `BLOCKED` · `DONE`

| ID | Task | Pri | Depends | Effort | Owner | Completion criteria | Status |
|----|------|-----|---------|--------|-------|---------------------|--------|
| C01 | Flag harness + package skeleton | P0 | — | S | | Flags work in staging | TODO |
| C02 | Correlation / turn_id logging | P0 | C01 | S | | Every log has ids | TODO |
| C03 | Idempotency store + entry wrap | P0 | C01 | M | | Dup WAMID single turn | TODO |
| C04 | Session Redis lock | P0 | C01 | M | | Concurrent serialized | TODO |
| C05 | Session columns + backfill expires | P0 | C03–4 | M | | Dual-read OK | TODO |
| C06 | Duplicate active session cleanup | P0 | C05 | S | | One open per key | TODO |
| C07 | Expire sweeper job | P0 | C05 | S | | Status expired set | TODO |
| C08 | Orchestrator shell + legacy adapter | P0 | C03–5 | L | | Flag parity with legacy | TODO |
| C09 | Shadow compare logger | P1 | C08 | S | | Disagreement rate metric | TODO |
| C10 | WaitContract model + synthesize | P0 | C05,C08 | M | | Parked sessions load contract | TODO |
| C11 | Interactive match id+title | P0 | C10 | M | | Title advances graph | TODO |
| C12 | Breakout policy clears wait | P0 | C10–11 | M | | No stuck after keyword/AI | TODO |
| C13 | Response Planner v1 | P0 | C12 | M | | No dual same menu | TODO |
| C14 | Keyword engine extract | P0 | C08 | M | | Tests ported | TODO |
| C15 | Keyword flow action + schedules | P0 | C14, flow start | M | | Product UAT | TODO |
| C16 | Ladder order enforcement | P0 | C08,C10,C14 | M | | Snapshot tests | TODO |
| C17 | AI pipeline Knowledge→RAG→LLM | P0 | C08 | L | | Stage order tests | TODO |
| C18 | Settings mode mapping + UI | P0 | C17 | M | | Admins can set mode | TODO |
| C19 | Transfer sets HumanTransfer state | P0 | C05,C08 | M | | No bot while active | TODO |
| C20 | Flow engine package extract | P1 | C08 | L | | Graph tests green | TODO |
| C21 | Node registry split | P1 | C20 | L | | All types registered | TODO |
| C22 | Prompt/NFM wait engines | P1 | C10,C21 | M | | Tests green | TODO |
| C23 | Variable scopes + template move | P1 | C05,C21 | M | | Scope tests | TODO |
| C24 | API/Media engines cleanup | P1 | C21 | M | | No secrets in node | TODO |
| C25 | Flow versions publish | P1 | C20 | L | | Pin on session | TODO |
| C26 | Outbox events + metrics dashboards | P1 | C08 | L | | TurnCompleted emitted | TODO |
| C27 | Rate limit + circuit breakers | P2 | C17 | M | | Chaos tests | TODO |
| C28 | Session timeline API + UI | P2 | C02,C26 | M | | Support uses UI | TODO |
| C29 | Load/chaos test report | P1 | C03–4,C13 | M | | Targets documented | TODO |
| C30 | Pilot rollout | P0 | All P0 | M | | 7-day soak | TODO |
| C31 | Default orchestrator on | P0 | C30 | S | | >95% traffic | TODO |
| C32 | Remove legacy god-path | P2 | C31 | M | | Dead code gone | TODO |

---

# 20. Final Implementation Roadmap

## 20.1 Safest execution order

```
Foundation (flags, packages, correlation)
        ↓
Idempotency + Session Lock
        ↓
Session Manager (version, expire, single open)
        ↓
Orchestrator shell (legacy adapter, parity)
        ↓
WaitContract + Interactive + Response Planner   ← fixes worst UX bugs early
        ↓
Priority Ladder (wire waits before keywords)
        ↓
Keyword Engine (correct actions + schedules)
        ↓
AI Pipeline (Knowledge → RAG → LLM)
        ↓
Transfer state alignment
        ↓
Flow Engine extract → Node Registry
        ↓
Prompt / WhatsApp Flow / Variables / API / Media
        ↓
Flow versioning
        ↓
Events · Metrics · Analytics · Security harden
        ↓
Performance · Chaos · Full regression
        ↓
Pilot → Gradual production → Default on
        ↓
Remove legacy processor branches
```

## 20.2 Why this order is safest

| Ordering choice | Rationale |
|-----------------|-----------|
| Lock/idempotency first | Stops races before rewriting logic |
| Session before orchestrator | New brain needs reliable state |
| Orchestrator shell before extracts | One seam for flags and rollback |
| Interactive/planner before full node split | Highest user pain fixed without boiling ocean |
| Ladder before AI | Correct ownership of turn before spending tokens |
| Keyword after waits | Prevents reintroducing “keyword beats button” |
| AI before flow extract | Quality path independent of package move |
| Flow/node extract after behavior stable | Refactors don’t mix with behavior changes |
| Versioning after engine extract | Avoid migrating twice |
| Delete legacy last | Always have rollback until soak |

## 20.3 Definition of project complete

- [ ] All P0 checklist items DONE and production ready  
- [ ] Target architecture invariants (Appendix B) enforced by tests  
- [ ] Analysis failure modes 1–12 closed or accepted with ticket  
- [ ] Orchestrator default on all production accounts  
- [ ] Legacy god-function logic removed or unreachable  
- [ ] Runbooks for rollback, DLQ, stuck wait, AI outage  

## 20.4 Document set (no further design docs required)

| Doc | Role |
|-----|------|
| `CHATBOT_ENGINE_ANALYSIS.md` | Current system truth |
| `CHATBOT_TARGET_ARCHITECTURE.md` | Design authority |
| **`CHATBOT_IMPLEMENTATION_SPECIFICATION.md`** | Execution authority |

Implementation work proceeds from the checklist and phases above. New ADRs only if changing ladder order, state machine, or WaitContract semantics — and those ADRs must update the target architecture, then this spec’s affected sections.

---

*End of implementation specification. Coding may begin at Phase 1 with feature flags defaulting to legacy behavior.*
