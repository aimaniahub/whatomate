# Chatbot Target Architecture & System Design

**Project:** Whatomate  
**Document type:** Target architecture & engineering specification  
**Status:** Reference design for all future chatbot refactors  
**Baseline:** `CHATBOT_ENGINE_ANALYSIS.md` (current implementation — do not re-derive here)  
**Date:** 2026-07-19  

**Constraints for this document**

- Describes how the chatbot **should** work, not how it works today  
- No source code changes, patches, or implementation  
- Implementers must be able to rebuild the engine from this specification alone  

---

# 1. Executive Summary

## 1.1 Current architecture problems (from baseline analysis)

The live engine concentrates orchestration, routing, breakout policy, AI, keywords, sessions, and side effects into a single inbound function, with a graph runner nested underneath. That yields:

| Problem class | Symptom |
|---------------|---------|
| **God-function orchestration** | Unpredictable priority; hard to test; hard to extend |
| **Implicit interactive state** | “Parked buttons” is only `current_step`; no explicit wait contract |
| **Non-deterministic free-text policy** | Keyword/AI breakout leaves graph parked while UI shows greeting |
| **Incomplete product contracts** | Flow response type, button triggers, schedules often unenforced |
| **Weak isolation** | Per-message goroutines without per-contact serialization |
| **Weak idempotency** | Best-effort WAMID checks; duplicate menus/replies |
| **AI pipeline ambiguity** | Mode flags mix RAG, static context, and LLM without a single ranked pipeline |
| **Observability gaps** | Logs exist, but no correlation timeline or structured execution trace |

## 1.2 Target architecture goals

1. **One deterministic router** with an explicit, versioned priority ladder  
2. **Explicit conversation state machine** (not ad-hoc flags)  
3. **Modular engines** (session, flow, interactive, prompt, AI, transfer) with clear interfaces  
4. **Idempotent inbound processing** and **per-contact serialization**  
5. **Honest state after every reply** (UI and session always agree)  
6. **Pluggable nodes** without editing the router  
7. **Enterprise ops**: events, metrics, audits, retries, DLQ, rate limits  
8. **Safe multi-tenant** isolation (org + WhatsApp account scoped)

## 1.3 Expected improvements

| Area | Improvement |
|------|-------------|
| Correctness | No “fake reset” menus; buttons expire or clear; typed titles resolve |
| Debuggability | Correlation ID + execution timeline per message |
| Testability | Pure routing decisions; mockable ports |
| Scalability | Queue workers, horizontal scale behind shared session lock |
| Extensibility | New node types via registry, not `switch` sprawl |
| AI quality | Ranked knowledge → RAG → LLM with confidence/abstain |
| Ops | Metrics, alerts, DLQ, circuit breakers |

## 1.4 Benefits

- Product, support, and engineering share one mental model  
- Refactors become module swaps, not rewrites of a monolith  
- Failures localize to a module boundary  
- Multi-channel readiness (WhatsApp first; omnichannel later)

## 1.5 Migration philosophy

**Strangler fig, not big-bang.**

- Keep webhook surface and WhatsApp APIs stable  
- Introduce a **Conversation Orchestrator** behind a feature flag  
- Shadow-route traffic; compare outcomes; flip per org/account  
- Preserve v2 graph JSON as the flow source of truth (evolve schema, don’t discard graphs)  
- Never leave session state and user-visible menus out of sync during dual-run  

---

# 2. High Level Architecture

## 2.1 Logical pipeline

```
┌──────────────────────────────────────────────────────────────────────────┐
│                           External Systems                               │
│  Meta WhatsApp Cloud API │ RAG Service │ LLM Providers │ CRM / Webhooks  │
└────────────┬─────────────────────┬────────────┬───────────────┬──────────┘
             │                     │            │               │
             ▼                     │            │               │
┌────────────────────────┐         │            │               │
│   Webhook Ingress      │         │            │               │
│  Verify · Parse · Idem │         │            │               │
└────────────┬───────────┘         │            │               │
             ▼                     │            │               │
┌────────────────────────┐         │            │               │
│   Inbound Queue        │         │            │               │
│  (per-org optional)    │         │            │               │
└────────────┬───────────┘         │            │               │
             ▼                     │            │               │
┌────────────────────────┐         │            │               │
│ Contact Resolver       │         │            │               │
│ Message Persister      │         │            │               │
└────────────┬───────────┘         │            │               │
             ▼                     │            │               │
┌────────────────────────┐         │            │               │
│ Session Lock (Redis)   │         │            │               │
│ + Session Manager      │         │            │               │
└────────────┬───────────┘         │            │               │
             ▼                     │            │               │
┌────────────────────────────────────────────────────────────────────────┐
│                 Conversation Orchestrator (single brain)               │
│  Policy · Priority Ladder · State Machine · Unit of Work               │
└───┬──────────┬──────────┬──────────┬──────────┬──────────┬─────────────┘
    │          │          │          │          │          │
    ▼          ▼          ▼          ▼          ▼          ▼
 Flow      Interactive  Keyword    AI/RAG    Transfer   Config
 Engine     Engine      Engine     Engine    Engine     Manager
    │          │          │          │          │
    ▼          ▼          ▼          ▼          ▼
┌────────────────────────────────────────────────────────────────────────┐
│ Node Executor Registry  │  Variable Engine  │  Template Engine         │
└────────────────────────────────────────────────────────────────────────┘
             │
             ▼
┌────────────────────────┐
│ Response Planner       │  (dedupe, merge, order outbound turns)
└────────────┬───────────┘
             ▼
┌────────────────────────┐
│ Outbound Gateway       │  WhatsApp send + message persist + metrics
└────────────────────────┘
             │
             ▼
┌────────────────────────┐
│ Event Bus · Audit · Metrics · Analytics                              │
└────────────────────────┘
```

## 2.2 Component responsibility (one each)

| Component | Single responsibility |
|-----------|----------------------|
| **Webhook Ingress** | Accept Meta traffic; verify; normalize; enqueue |
| **Idempotency Store** | Guarantee at-most-once process per WAMID |
| **Contact Resolver** | Resolve/create contact identity |
| **Message Persister** | Durable CRM message store |
| **Session Manager** | Load/create/update/expire conversation sessions |
| **Session Lock** | Serialize concurrent inbound for same contact+account |
| **Conversation Orchestrator** | Decide *what happens next* (policy + state machine) |
| **Flow Engine** | Execute versioned flow graphs |
| **Node Executor Registry** | Run one node type implementation |
| **Interactive Engine** | Buttons/lists/quick replies/forms wait contracts |
| **Prompt Engine** | Text capture, validation, retries |
| **Keyword Engine** | Match keyword rules only |
| **AI Engine** | Ranked knowledge → RAG → LLM pipeline |
| **Transfer Engine** | Human handoff lifecycle |
| **Variable Engine** | Scoped variable R/W |
| **Template Engine** | `{{var}}` rendering |
| **Response Planner** | Compose ordered outbound turns; prevent double menus |
| **Outbound Gateway** | Send via WhatsApp API; persist outbound |
| **Event Bus** | Domain events for side effects |
| **Config Manager** | Org/account settings resolution |

## 2.3 Design decisions

| Decision | Choice | Reason | Trade-off |
|----------|--------|--------|-----------|
| Brain location | Orchestrator, not webhook | Testable policy | Extra layer |
| Concurrency | Per contact+account lock | Determinism | Slight latency under burst |
| Flow format | Evolve v2 graph JSON | Preserve builder investment | Schema migrations needed |
| Free-text mid-wait | Explicit wait policy per node | No silent park | Authors must configure policy |
| AI | Pipeline stages with scores | Grounded answers first | More config |
| Side effects | Events after commit | Decoupled analytics/webhooks | Eventual consistency |

---

# 3. Core Modules

Each module is a package with interfaces (ports) and one default adapter implementation.

---

## 3.1 Webhook Module

**Purpose:** Secure, fast ingress from Meta.

**Responsibilities**

- Verify challenge and HMAC signatures  
- Parse payload into `InboundEnvelope`  
- Reject invalid / unsupported shapes early  
- Publish to inbound queue (or process in-process with same contract)  
- Return HTTP 200 quickly after durable accept  

**Inputs:** HTTP body, headers (`X-Hub-Signature-256`)  
**Outputs:** `InboundEnvelope` events  
**Dependencies:** Config (verify tokens), Account registry, Idempotency (optional at edge)  

**Public interfaces**

```text
WebhookHandler.Verify(challenge) → body|error
WebhookHandler.Accept(request) → Accepted{envelope_ids}|error
```

**Internal components:** SignatureVerifier, PayloadParser, AccountResolver  

**Future extensions:** Multi-app secrets rotation, BSUID-only users, call events share same ingress but different handlers  

| | |
|--|--|
| **Reason** | Keep HTTP edge dumb and safe |
| **Benefits** | Fast ACK, clear security boundary |
| **Trade-offs** | Requires durable queue for true reliability |
| **Impact** | High reliability |
| **Complexity** | Low–medium |
| **Priority** | P0 |

---

## 3.2 Inbound Router / Conversation Orchestrator

**Purpose:** The only place that decides routing priority and state transitions.

**Responsibilities**

- Load session under lock  
- Build `TurnContext` (normalized message, channel, account, org)  
- Run **priority ladder** (Section 13)  
- Invoke one primary handler; collect `TurnResult`  
- Commit session + responses in a unit of work  
- Emit domain events  

**Inputs:** `TurnContext`  
**Outputs:** `TurnResult{actions[], session_delta, events[]}`  
**Dependencies:** All engines (via interfaces), Session Manager, Config  

**Public interfaces**

```text
Orchestrator.HandleTurn(ctx, TurnContext) → TurnResult, error
```

**Internal components:** PriorityLadder, StateMachine, WaitPolicyResolver, Guardrails  

**Future extensions:** A/B routing, per-segment policies  

| | |
|--|--|
| **Reason** | Eliminates god-function branching |
| **Benefits** | Determinism, single test surface for policy |
| **Trade-offs** | Discipline required to keep logic out of nodes |
| **Impact** | Critical |
| **Complexity** | High |
| **Priority** | P0 |

---

## 3.3 Session Manager

**Purpose:** Authoritative conversation session store and lifecycle.

**Responsibilities**

- Create / load active session  
- Update status, wait state, flow pointer, version  
- Expire and close  
- Soft-lock metadata (last processed WAMID, turn sequence)  

**Inputs:** org, account, contact  
**Outputs:** `Session` aggregate  
**Dependencies:** DB, Cache (optional read-through)  

**Public interfaces**

```text
SessionManager.GetOrCreateActive(key, opts) → Session, created bool
SessionManager.Save(session, expected_version) → error  // optimistic concurrency
SessionManager.Complete(session, reason)
SessionManager.ExpireStale(before time)
```

**Internal components:** SessionRepository, WaitState, FlowCursor, Clock  

**Future extensions:** Session snapshots for time-travel debug  

---

## 3.4 Conversation Manager

**Purpose:** Business-level conversation identity separate from ephemeral session.

**Responsibilities**

- Map contact+account → conversation thread  
- Track conversation status (bot / human / closed)  
- Own greeting-once, restart, resume semantics  

**Why separate from session:** Sessions expire; conversations may reopen with history.  

---

## 3.5 Flow Manager / Flow Engine

**Purpose:** Load flow definitions and run graph execution for one turn.

**Responsibilities**

- Resolve flow by id/version  
- Advance graph from cursor  
- Detect loops  
- Support subflows with return stack (optional) or goto without return (explicit)  
- Flow-level cancel keywords  

**Inputs:** Session, flow id, turn input  
**Outputs:** Node effects, new cursor, completion flags  
**Dependencies:** Node Registry, Variable Engine, Expression Engine  

**Public interfaces**

```text
FlowEngine.Start(session, flowRef, trigger) → RunResult
FlowEngine.Resume(session, input) → RunResult
FlowEngine.Cancel(session, reason)
```

---

## 3.6 Node Engine (Executor Registry)

**Purpose:** Execute a single node type.

**Responsibilities**

- Register handlers by type  
- Execute with timeout budget  
- Return structured `NodeResult{outcome, yield, messages, var_writes}`  

**Public interfaces**

```text
type NodeHandler interface {
  Type() string
  Execute(ctx, Node, NodeExecContext) (NodeResult, error)
}
NodeRegistry.Get(type) → NodeHandler
```

**Node types (v1 target):**  
`start`, `message`, `buttons`, `list`, `prompt`, `condition`, `set_variable`, `api_call`, `webhook`, `timing`, `wait_timer`, `ai_response`, `transfer`, `goto_flow`, `call_subflow`, `whatsapp_flow`, `media`, `end`

---

## 3.7 Variable Engine

**Purpose:** Scoped variable storage and resolution.

**Scopes:** system · contact · conversation · session · flow · node(temp)  

**Responsibilities:** Get/Set/Delete, merge for templates, redact secrets, TTL for temp  

---

## 3.8 Prompt Engine

**Purpose:** Blocking text capture with validation and retries.

**Responsibilities**

- Present prompt  
- Validate (regex, type, custom expr)  
- Retry with error message  
- Exhaustion outcomes (`max_retries`, `cancel`)  
- Store into variable scope  

---

## 3.9 Button Engine / List Engine / Interactive Engine

**Purpose:** Unified wait contracts for interactive WhatsApp UI.

**Responsibilities**

- Render buttons/lists (respect Meta limits; auto-split CTA)  
- Persist `WaitContract` (expected IDs, titles, expires_at, invalid_policy)  
- Resolve click ID **or typed title** (normalized match)  
- Handle invalid, expired, duplicate click  
- Navigation intents: back / home / cancel / restart (reserved IDs)  

---

## 3.10 Condition / Expression Engine

**Purpose:** Safe boolean/expression evaluation for conditions and skip rules.

**Responsibilities**

- Compile expressions with allow-list of functions  
- Evaluate against variable snapshot  
- Never throw user-visible errors; map to `false` + warn metric  

---

## 3.11 Template Engine

**Purpose:** Render outbound text and structured fields with `{{var}}`.

**Responsibilities**

- Strict vs lenient missing-var modes  
- Escape rules for WhatsApp formatting  
- Locale-aware number/date formatting  

---

## 3.12 API Engine

**Purpose:** HTTP integrations for `api_call` / dynamic context.

**Responsibilities**

- SSRF-safe dialer  
- Timeouts, retries (idempotent methods only)  
- Response mapping to variables  
- Circuit breaker per host  

---

## 3.13 Media Engine

**Purpose:** Inbound download and outbound media send.

**Responsibilities**

- Download Meta media → storage  
- Virus size limits  
- Outbound type selection (image/video/audio/document)  
- Caption handling (audio: separate text turn)  

---

## 3.14 AI Engine

**Purpose:** Orchestrate grounded answer generation (not a single LLM call).

**Stages:** Knowledge → Context → RAG → LLM → Post-process  

See Section 12.

---

## 3.15 RAG Engine

**Purpose:** External retrieval chat (or internal embeddings if added later).

**Responsibilities**

- Call configured RAG endpoints  
- History windowing  
- Abstain detection  
- Confidence / score thresholds  
- Latency budgets and fallbacks  

---

## 3.16 Knowledge Engine

**Purpose:** First-class static/FAQ/structured knowledge **before** LLM.

**Responsibilities**

- Keyword/FAQ exact and fuzzy match  
- Static document snippets with ranking  
- Return `AnswerCandidate` with confidence  

**Reason:** Static knowledge must be able to answer **without** OpenRouter.

---

## 3.17 Transfer Engine

**Purpose:** Human handoff.

**Responsibilities**

- Create transfer (queue/team/agent)  
- Suppress bot while active  
- Resume bot on agent close  
- SLA hooks (existing SLA processor integrates via events)  

---

## 3.18 Logging / Audit / Metrics / Analytics

**Purpose:** Observability and compliance.

- Structured logs with `correlation_id`, `session_id`, `turn_id`  
- Audit: config changes, transfer, data export  
- Metrics: latency, node counts, AI abstain rate, lock wait  
- Analytics: funnel from flow path `__path__`  

---

## 3.19 Cache Layer

**Purpose:** Read-through cache for settings, flows, keywords, AI contexts.

**Rules**

- Cache **definitions**, not **sessions** (sessions always DB + lock)  
- Invalidate on write paths only  
- Version stamp in cache key (`flow_id:version`)  

---

## 3.20 Database Layer

**Purpose:** Persistence with clear aggregates and constraints.

See Section 14.

---

## 3.21 Notification Engine

**Purpose:** Internal agent WS/push and optional external webhooks.

Triggered by Event Bus only (not from node code directly).

---

## 3.22 Retry Engine

**Purpose:** Shared retry policies for outbound HTTP, WhatsApp send, RAG.

---

## 3.23 Security Layer / Rate Limiter / Config Manager

- Webhook auth, secret encryption, org isolation  
- Per-contact and per-account rate limits  
- Hierarchical config: system → org → account → flow  

---

# 4. Message Lifecycle

## 4.1 Stages

```
1  Accept webhook
2  Verify signature
3  Normalize to InboundEnvelope
4  Idempotency reserve (WAMID)
5  Enqueue (or continue)
6  Acquire session lock (org, account, contact)
7  Resolve contact
8  Persist inbound message
9  Load/create session + conversation
10 Build TurnContext
11 Orchestrator.HandleTurn
12 Plan responses (order, dedupe)
13 Send outbound via gateway
14 Persist session (optimistic version)
15 Commit idempotency complete
16 Emit events
17 Release lock
```

## 4.2 Sequence diagram

```
Meta → WebhookIngress → IdempotencyStore
                      → InboundQueue
Worker → SessionLock.Acquire
       → ContactResolver
       → MessagePersister.Inbound
       → SessionManager.Load
       → Orchestrator.HandleTurn
            ├─ StateMachine.Evaluate
            ├─ PriorityLadder.SelectHandler
            ├─ FlowEngine / Keyword / AI / Transfer ...
            └─ TurnResult
       → ResponsePlanner
       → OutboundGateway (N sends)
       → SessionManager.Save
       → EventBus.Publish
       → SessionLock.Release
```

## 4.3 Decision points (orchestrator only)

1. Is bot enabled for account?  
2. Is contact excluded?  
3. Is transfer active?  
4. Business hours policy  
5. What is `session.state`?  
6. Is there an active **WaitContract**?  
7. Apply wait-specific resolution (button/prompt/form)  
8. Else apply free-chat ladder (keyword → flow trigger → AI → fallback)  
9. Commit state transitions that match messages sent  

## 4.4 Error handling

| Failure | Behavior |
|---------|----------|
| Signature invalid | 403; no process |
| Idempotency conflict | Drop; 200 |
| Lock timeout | Requeue with backoff; do not process unlocked |
| Handler error | State → `Error` or stay in wait with error message; log + metric |
| Partial send | Record failed turn; retry remaining; never advance cursor if send required |
| DB save conflict | Reload session; re-evaluate if WAMID still owned; else abort |

## 4.5 Retries

- Webhook accept: Meta retries → handled by idempotency  
- Outbound WhatsApp: Retry Engine (3x exponential, honor 429)  
- RAG/LLM: stage-specific; never infinite  

## 4.6 Logging & state updates

Every stage writes structured span to execution timeline:

```json
{
  "turn_id": "...",
  "correlation_id": "...",
  "stage": "priority.keyword",
  "decision": "no_match",
  "duration_ms": 12
}
```

Session updates only in **Save** after successful plan (or explicit failure transition).

---

# 5. Session Lifecycle

## 5.1 Session key

```text
(organization_id, whatsapp_account, contact_id)
```

At most **one** `active` or `waiting_*` or `human_transfer` session per key.

## 5.2 Creation

**When:** First eligible inbound after no open session, or after previous session closed/expired.

**Initial state:** `Idle` or `Greeting` (config).  
**Data:** empty variables; `version=1`; `turn_seq=0`.

## 5.3 Loading

Under lock: load by key where `status IN open_statuses` and `expires_at > now`.  
If multiple rows (legacy): pick latest `last_activity_at`, close others as `superseded`.

## 5.4 Updates

Every turn increments `turn_seq` and `version` (optimistic concurrency).  
`last_activity_at` updated; `expires_at = last_activity + session_timeout`.

## 5.5 Completion

**When:** Flow `end`, transfer handoff complete, cancel, user restart, explicit complete, admin force-close.

**Actions:** status=`completed`; clear wait contract; set `completed_at` and `completion_reason`; optional completion message (once).

## 5.6 Expiration

**When:** Background job finds `expires_at < now` and status open.

**Actions:** status=`expired`; optional timeout message (at most once, rate-limited); emit `SessionExpired`.

## 5.7 Recovery

If process dies mid-turn:

- Idempotency row = `processing` longer than lease → reclaim or dead-letter  
- Session version unchanged → safe retry of same WAMID  
- If outbound partially sent: check outbound message table by `turn_id` before re-sending  

## 5.8 Cleanup / GC

- Soft-close sessions  
- Archive `session_messages` older than N days to cold storage  
- Hard-delete expired idempotency keys after TTL  

## 5.9 Migration

Legacy sessions: adapter maps `current_step` → `flow_cursor` + synthesizes `WaitContract` if node type is interactive.

---

# 6. Conversation Lifecycle

Deterministic conversation events (conversation aggregate may span multiple sessions):

| Phase | Trigger | Behavior |
|-------|---------|----------|
| **Start** | First message / campaign reply | Create conversation + session |
| **Greeting** | New session + greeting enabled | Send greeting **once** per session; state `Greeting` → `Idle` or menu wait |
| **Flow entry** | Trigger keyword, button id, keyword→flow, API | `FlowEngine.Start`; state `FlowActive` |
| **Flow exit** | End node, cancel, complete | Clear cursor; state `Idle` or `Completed` |
| **Restart** | Reserved intent / keyword | Complete current; new session; optional same greeting policy |
| **Resume** | Agent resumes bot | Transfer closed; state from snapshot or `Idle` |
| **Cancel** | Cancel keywords / button | Exit flow; policy for keep session vs complete |
| **Timeout** | Inactivity | Expire; message if configured |
| **Transfer** | Transfer engine | Bot muted; state `HumanTransfer` |
| **Completion** | Business done | Close conversation optional |
| **Recovery** | Error state | Recovery node or fallback menu |

**Invariant:** Every outbound menu that implies a wait **creates** a wait contract; every completion **clears** it.

---

# 7. State Machine

## 7.1 States

| State | Meaning |
|-------|---------|
| `Idle` | Open session; no wait; free-chat ladder |
| `Greeting` | Greeting in progress (transient) |
| `MainMenu` | Waiting on global menu contract |
| `FlowActive` | Inside flow; non-blocking chain may be mid-turn only |
| `WaitingButton` | WaitContract type=buttons |
| `WaitingList` | WaitContract type=list |
| `WaitingPrompt` | WaitContract type=prompt |
| `WaitingWhatsAppFlow` | WaitContract type=nfm |
| `WaitingAPI` | Rare: async API callback mode (optional) |
| `WaitingAI` | Transient during AI call (not multi-turn park) |
| `HumanTransfer` | Bot suppressed |
| `Paused` | Admin/system pause |
| `Completed` | Terminal success |
| `Closed` | Conversation closed (no auto reopen without new session) |
| `Error` | Recoverable error state |
| `Expired` | Terminal timeout |
| `Recovery` | Running recovery policy |

## 7.2 Transition table (summary)

| From | Event | To | Notes |
|------|-------|-----|-------|
| Idle | greeting_needed | Greeting | |
| Greeting | greeting_sent | Idle / MainMenu | if greeting has buttons → MainMenu |
| Idle | flow_start | FlowActive | |
| FlowActive | node_yield_buttons | WaitingButton | set WaitContract |
| FlowActive | node_yield_prompt | WaitingPrompt | |
| FlowActive | node_yield_list | WaitingList | |
| FlowActive | node_yield_nfm | WaitingWhatsAppFlow | |
| WaitingButton | valid_click / title_match | FlowActive | advance edge |
| WaitingButton | invalid | WaitingButton | invalid policy |
| WaitingButton | expire | Idle / Recovery | clear wait |
| WaitingButton | cancel / home | Idle / MainMenu | clear wait + flow |
| Waiting* | transfer | HumanTransfer | clear or freeze cursor per policy |
| HumanTransfer | resume | Idle or FlowActive | snapshot |
| * | complete | Completed | |
| * | timeout | Expired | |
| * | unhandled_error | Error → Recovery | |

## 7.3 Forbidden transitions

- `Completed` → `WaitingButton` (must new session)  
- `HumanTransfer` → any bot send (except configured system notices)  
- `WaitingPrompt` → `WaitingButton` without clearing prompt (no dual wait)  
- Free-chat keyword starting a **new** flow while `Waiting*` **without** explicit wait policy `allow_breakout`  

## 7.4 Recovery behaviour

`Error` / `Recovery`:

1. Log + metric  
2. Send short recovery message  
3. If flow has recovery edge → follow  
4. Else clear wait + cursor → `MainMenu` or `Idle`  
5. Never silently leave old wait active while showing new menu  

---

# 8. Flow Engine Design

## 8.1 Flow definition

Versioned document:

```text
FlowDefinition {
  id, org_id, account_scope, version, status (draft|published|archived)
  entry_node
  nodes[]
  edges[]
  cancel_keywords[]
  metadata (locale, tags)
}
```

Runtime always executes **published version id** pinned on session at start (`flow_version`) so mid-conversation publishes do not mutate active users (optional policy: force-upgrade).

## 8.2 Execution model

```
Resume(cursor):
  loop (max_non_blocking):
    node = load(cursor)
    if skip_condition → follow default/skip edge
    result = handler.Execute
    append path
    if result.yield:
       set WaitContract
       persist cursor = node.id
       return
    edge = resolve(result.outcome)
    if no edge:
       complete flow
       return
    cursor = edge.to
    if node.type == call_subflow:
       push stack; load subflow entry
    if node.type == goto_flow:
       optional clear stack; switch flow_version
```

## 8.3 Edge resolution order

1. Exact condition match  
2. Pattern match (`button:*` registry)  
3. `default`  
4. Flow-level fallback edge (if defined)  
5. Complete with reason `no_edge` (log warn; do not silent re-park)

## 8.4 Loop detection

- Max non-blocking nodes per turn (e.g. 50)  
- Path fingerprint set for cycle detection  
- Metric + complete with `runaway_cycle`

## 8.5 Nested / subflows

| Mode | Behavior |
|------|----------|
| `goto_flow` | No return; explicit |
| `call_subflow` | Stack frame: return_node + variable export map |

## 8.6 Timeouts

- Per-wait: `WaitContract.expires_at`  
- Per-flow: optional max duration  
- On expire: edge `timeout` if present else complete/expire session  

## 8.7 Dynamic routing

- Condition nodes  
- API outcomes  
- Expression-based edge labels (advanced, optional)  

## 8.8 Versioning & rollback

- Immutable published versions  
- Rollback = re-publish previous version pointer as latest  
- Active sessions keep pinned version unless force flag  

| | |
|--|--|
| **Reason** | Safe deploys of flow content |
| **Benefits** | No mid-chat graph mutation surprises |
| **Trade-offs** | Storage growth of versions |
| **Impact** | High ops safety |
| **Complexity** | Medium |
| **Priority** | P1 |

---

# 9. Interactive Components

## 9.1 WaitContract (core abstraction)

```text
WaitContract {
  type: buttons | list | prompt | whatsapp_flow | quick_reply
  node_id, flow_id, flow_version
  options: [{id, title, aliases[]}]
  expires_at
  invalid_policy: reprompt | fallback_edge | free_text_breakout | ignore
  free_text_policy: match_option | breakout | ignore | ai_assist
  breakout_scope: keywords | flow_triggers | none
  on_match_clear: true  // ALWAYS clear on successful resolve
  navigation: {back?, home?, cancel?, restart?}
  render_fingerprint  // hash of last sent menu to prevent accidental double send
}
```

## 9.2 Buttons & lists

- Authoring validates Meta limits (3 buttons / 10 list rows)  
- Runtime auto-converts 4–10 reply options to list  
- CTA URL/phone never mixed with reply in one payload  

## 9.3 Typed button matching

Normalize: trim, case-fold, collapse whitespace, optional transliteration.

Match order:

1. Exact option id (if user somehow sends id)  
2. Exact title  
3. Alias list  
4. Fuzzy (optional Levenshtein ≤ 1 for short titles)  
5. Else invalid_policy  

## 9.4 Expiration

On expire:

- Clear wait  
- Transition per edge `timeout` or session expire policy  
- Do **not** leave stale options active  

## 9.5 Duplicate clicks

If option already consumed for this wait (`consumed_input_hash`):

- Ignore second click or send “already processed” (config)  
- Do not re-advance graph  

## 9.6 Navigation intents

Reserved option IDs (system):

| ID | Action |
|----|--------|
| `__nav_back` | Pop subflow or previous menu snapshot |
| `__nav_home` | Clear flow → MainMenu |
| `__nav_cancel` | Cancel flow |
| `__nav_restart` | Complete + new session |

## 9.7 Free-text while waiting (policy-driven)

**Default for menus:** `match_option` then `reprompt`  
**Optional:** `free_text_breakout` only if author enables; on breakout **must**:

1. Resolve breakout handler  
2. **Clear or complete** wait (never dual UI)  
3. If answer + return to menu: re-render **new** contract with new fingerprint  

This eliminates “AI answer + greeting while still WaitingButton”.

---

# 10. Prompt System

## 10.1 Lifecycle

```
Enter prompt node
  → render body (+ media)
  → set WaitContract(type=prompt)
  → yield

Inbound text
  → validate
  → success: write variable, clear wait, outcome=default
  → fail: retries++, send validation_error, stay waiting
  → max_retries: outcome=max_retries, clear wait
```

## 10.2 Validation types

`text`, `number`, `email`, `phone`, `date`, `regex`, `enum`, `expr`

## 10.3 Multi-step forms

Prefer **chain of prompt nodes** or **WhatsApp Flow** for multi-field.  
Optional `form` composite node expands to prompts with shared `form_id`.

## 10.4 Cancellation / resume

- Cancel keywords active during prompt if flow cancel list set  
- Pause on transfer; resume returns to same prompt unless expired  

---

# 11. Variable Engine

## 11.1 Scopes

| Scope | Lifetime | Examples |
|-------|----------|----------|
| `system` | process | `now`, `org_id` |
| `contact` | durable | `phone`, `profile_name`, CRM fields |
| `conversation` | conversation | language preference |
| `session` | session | `ai_query` |
| `flow` | while flow active | step answers |
| `temp` | single turn | intermediate API parse |

## 11.2 System variables (read-only)

`phone_number`, `contact_name`, `whatsapp_name`, `whatsapp_account`, `org_id`, `session_id`, `locale`, `channel`

## 11.3 Write rules

- Nodes declare writes  
- Secret variables never logged  
- Serialization: JSON-compatible types only  
- Cleanup: on flow exit clear `flow` scope; on session complete clear `session`  

## 11.4 Namespacing

`flow.customer_name`, `session.last_intent`, `contact.tags`

Template engine resolves unqualified names by scope search order: temp → flow → session → conversation → contact → system.

---

# 12. AI Architecture

## 12.1 Pipeline (mandatory order for free-text & ai_response node)

```
1. Safety / injection filters
2. Knowledge Engine (FAQ / static / structured) → AnswerCandidate[]
3. AI Context pack (static+api) as grounding material (not automatic final answer)
4. RAG Engine → AnswerCandidate[]
5. Ranker (confidence, source priority, freshness)
6. If top candidate >= threshold AND not abstain → return
7. Else if LLM allowed by policy → Prompt Builder → LLM (OpenRouter/etc.)
8. Post-process (strip chain-of-thought, length, language)
9. If empty → Fallback message + optional menu (with new wait contract)
```

## 12.2 Confidence & abstain

- RAG `abstained=true` or score < `min_score` → no candidate  
- Knowledge match score < threshold → skip  
- LLM may return abstain token / empty → fallback  

## 12.3 Prompt Builder

Inputs: system prompt, ranked evidence snippets, history window, user message, locale, guardrails.

**Never** dump entire PDF into LLM when RAG exists.

## 12.4 Conversation memory

- Short-term: last N session messages  
- Optional summary variable refreshed every K turns  
- Isolation per session; never cross-tenant  

## 12.5 Tool calling (phase 2)

LLM may request tools (API Engine, CRM) via controlled tool registry; results re-enter ranker.

## 12.6 Caching

- Cache RAG answers by `(org, context_id, normalized_question hash)` short TTL  
- Do not cache personalized data without session key  

## 12.7 Modes (config, explicit)

| Mode | Stages enabled |
|------|----------------|
| `off` | none |
| `knowledge_only` | 1–6 only |
| `knowledge_rag` | 1–6, no LLM |
| `knowledge_rag_llm` | full pipeline |
| `llm_only` | 7 only (discouraged for customer support) |

**Default for production support bots:** `knowledge_rag` or `knowledge_rag_llm`.

| | |
|--|--|
| **Reason** | Prevents LLM invention when knowledge exists |
| **Benefits** | Grounding, cost control |
| **Trade-offs** | Requires good KB/RAG ops |
| **Impact** | High answer quality |
| **Complexity** | High |
| **Priority** | P0 |

---

# 13. Routing Priority

## 13.1 Ladder (canonical)

Apply **top to bottom**; first match that claims the turn wins. No fall-through after a claim unless handler returns `continue`.

```
0. System guards
   - bot disabled → transfer/queue policy
   - excluded number → stop
   - rate limit → soft reject
   - business hours block → OOH message

1. Human transfer active → stop bot (optional agent notes only)

2. Session state = Paused / Completed / Expired → open new or ignore

3. Global intents (always on, even mid-wait if enabled)
   - cancel / stop
   - human / agent (transfer keywords)
   - restart / home (if enabled globally)

4. Active WaitContract resolution
   4a. WhatsApp Flow nfm payload
   4b. Button / list / quick-reply id
   4c. Typed option match
   4d. Wait invalid / expire / free_text_policy

5. Flow active (no wait or wait allowed non-blocking) → FlowEngine.Resume

6. Keyword rules (priority DESC, account then global)
   - transfer type → Transfer Engine
   - flow type → FlowEngine.Start
   - text/media/template → respond (may set MainMenu wait)

7. Flow triggers (keywords / trigger_button_id / trigger_list_id)

8. Greeting policy (new session only)

9. AI pipeline (Knowledge → RAG → LLM)

10. Fallback message / menu

11. Silent no-op (metric only)
```

## 13.2 Why this order

| Rank | Why |
|------|-----|
| Guards first | Safety and compliance |
| Transfer second | Human ownership must not be interrupted by bot |
| Global intents next | Escape hatches (cancel/agent) even mid-menu |
| Wait resolution before keywords | **Deterministic interactive UX**; options beat free-text rules |
| Flow before free keywords | Mid-flow integrity |
| Keywords before AI | Cheap, deterministic FAQs |
| Knowledge/RAG before LLM | Grounding |
| Fallback last | Never silent when configured |

## 13.3 Explicit non-goals

- Do not re-send old menus after unrelated answers without new contract  
- Do not match keywords before consuming a valid button click  
- Do not call LLM before knowledge/RAG when mode includes them  

---

# 14. Database Architecture

## 14.1 Core tables (target)

| Table | Purpose |
|-------|---------|
| `chatbot_settings` | Per org/account config |
| `keyword_rules` | Keyword definitions |
| `chatbot_flows` | Flow metadata |
| `chatbot_flow_versions` | Immutable graph JSON per version |
| `chatbot_sessions` | Session aggregate + state + cursor + wait_contract JSONB |
| `chatbot_session_messages` | Session transcript (optional archive) |
| `chatbot_variables` | Optional normalized vars (or JSONB only) |
| `ai_contexts` | Knowledge/RAG/API context defs |
| `inbound_idempotency` | WAMID processing state |
| `outbound_turns` | turn_id → messages sent (dedupe) |
| `agent_transfers` | Handoff |
| `messages` / `contacts` | CRM (existing) |
| `domain_events_outbox` | Reliable event publish |

## 14.2 Session columns (target)

```text
id, org_id, account, contact_id
status  -- state machine enum
flow_id, flow_version, cursor_node_id
wait_contract JSONB
variable_blob JSONB  -- or normalized
path JSONB
version INT  -- optimistic lock
turn_seq BIGINT
last_inbound_wamid
expires_at, last_activity_at
completion_reason
```

## 14.3 Indexes

- `(org_id, account, contact_id, status)` partial where open  
- `(expires_at)` where open — sweeper  
- `inbound_idempotency(wamid)` **UNIQUE**  
- `messages(whats_app_message_id)` **UNIQUE** where not null  
- `keyword_rules(org_id, account, priority DESC)`  

## 14.4 Caching

- Definitions only; short TTL + explicit invalidation  
- Session: not cached across nodes without version  

## 14.5 Archival

- Sessions completed > 90d → archive store  
- Path retained for analytics sample  

## 14.6 Scalability

- Partition sessions by month if volume requires  
- Read replicas for analytics, not for turn processing  

---

# 15. Event System

## 15.1 Pattern

Transactional **outbox**: write event rows in same DB transaction as session save; publisher drains to bus (Redis Streams / NATS / in-process).

## 15.2 Core events

| Event | Payload highlights | Consumers |
|-------|--------------------|-----------|
| `MessageReceived` | wamid, contact, type | Analytics, WS |
| `MessagePersisted` | message_id | Unread, CRM |
| `SessionCreated` | session_id | Metrics |
| `SessionStateChanged` | from, to | Debug UI |
| `FlowStarted` | flow_id, version | Analytics |
| `NodeEntered` / `NodeCompleted` | node_id, outcome | Timeline |
| `VariableUpdated` | keys (not secrets) | Panel UI |
| `WaitStarted` / `WaitResolved` / `WaitExpired` | contract type | Debug |
| `KeywordMatched` | rule_id | Analytics |
| `AIRequested` / `AIReturned` | stage, latency, abstain | Cost metrics |
| `TransferStarted` / `TransferCompleted` | transfer_id | Agent UI, SLA |
| `FlowCompleted` | reason | Analytics |
| `SessionExpired` | | Cleanup |
| `OutboundSent` / `OutboundFailed` | | Retry |
| `TurnCompleted` | full timeline ref | Audit |

## 15.3 Rules

- Nodes **do not** call webhooks directly for business events; emit events  
- Consumers must be idempotent  

---

# 16. Error Handling

## 16.1 Classification

| Class | Examples | Strategy |
|-------|----------|----------|
| Transient | 429, 503, lock timeout | Retry / requeue |
| Permanent | bad graph, invalid config | Fail turn; Recovery state |
| Partial | 1 of 2 messages sent | Compensating send or mark incomplete |
| Security | bad signature | Drop |

## 16.2 Retry policy (defaults)

- WhatsApp send: 3 attempts, exp backoff, jitter  
- RAG: 2 attempts  
- LLM: 2 attempts  
- API node: per-node config; default 0 for POST  

## 16.3 Circuit breaker

Per dependency host (Meta, RAG, OpenRouter, customer API): open after N failures; short-circuit to fallback.

## 16.4 Dead letter queue

Turns that fail after max attempts → DLQ with payload + last error; admin replay tool.

## 16.5 Alerts

- DLQ depth  
- Error rate by org  
- Lock wait p95  
- AI latency p95  
- Idempotency reclaim rate  

---

# 17. Logging Strategy

## 17.1 Levels

| Level | Use |
|-------|-----|
| DEBUG | Edge resolution, expression values (redacted) |
| INFO | Turn start/end, routing decision winner |
| WARN | Fallback used, abstain, ghost edge |
| ERROR | Failed send, panic recovered, data corruption |
| PERF | Stage timings |
| SECURITY | Signature failures, rate limits |
| AUDIT | Config change, transfer, export |

## 17.2 Correlation

```text
correlation_id  // from webhook accept
turn_id         // per processing attempt
session_id
org_id
account
contact_id
wamid
```

## 17.3 Execution timeline

Stored JSON array or separate `turn_traces` table for “why did the bot do X?” support UI.

---

# 18. Testing Strategy

## 18.1 Pyramid

| Layer | What |
|-------|------|
| Unit | Priority ladder pure functions; edge resolve; title match; state transitions |
| Node contract tests | Each NodeHandler golden I/O |
| Flow tests | Graph fixtures → multi-turn scripts |
| Orchestrator integration | DB + fake WhatsApp + fake RAG |
| AI tests | Mock stages; ranking; abstain |
| Contract tests | Meta payload fixtures |
| Load | N contacts concurrent; lock correctness |
| Chaos | Kill mid-turn; duplicate webhook; RAG timeout |
| Regression | Recorded production timelines (sanitized) |

## 18.2 Flow test DSL (example)

```text
GIVEN flow "registration" v3
WHEN user sends "hi"
THEN state WaitingButton menu "main"
WHEN user types "English"
THEN matches option en
AND node "ask_name" prompt sent
```

## 18.3 CI gates

- Unit + flow tests required  
- No merge if ladder priority snapshot test changes without review  

---

# 19. Security

| Control | Design |
|---------|--------|
| Webhook | HMAC mandatory when app secret configured; reject otherwise in production mode |
| Multi-tenant | Every query scoped by `org_id`; defense in depth |
| Replay | Unique WAMID; idempotency TTL ≥ Meta retry window |
| Rate limit | Per contact, per account, per IP (webhook) |
| Secrets | Encrypt API keys at rest; never log |
| Session | Lock + version; no cross-contact access |
| Prompt injection | Strip instruction markers; isolate evidence; system prompt hard rules |
| RAG injection | Treat retrieved text as untrusted data, not instructions |
| PII | Redact phones in logs by policy |
| AuthZ | Admin APIs use existing RBAC |

---

# 20. Performance

## 20.1 Latency targets (p95)

| Stage | Target |
|-------|--------|
| Webhook accept | < 50 ms |
| Turn without AI | < 400 ms |
| Turn with RAG | < 3 s |
| Turn with LLM | < 6 s |
| Lock wait | < 200 ms |

## 20.2 Scaling

- Stateless workers consuming queue  
- Redis locks + Postgres primary for sessions  
- Connection pools sized per worker  
- Parallelism: never parallelize two turns same session; may parallelize AI stages only when independent  

## 20.3 Caching

- Hot flow versions in memory LRU per worker  
- Settings/keywords Redis  

## 20.4 Background jobs

- Session expiration sweeper  
- Outbox publisher  
- DLQ redrive  
- Analytics rollups  

---

# 21. Migration Plan

## Phase 0 — Preparation (1–2 weeks)

- Freeze new ad-hoc branches in monolith  
- Add unique indexes (idempotency) carefully with backfill  
- Define `TurnContext` / `TurnResult` types and feature flag `chatbot.orchestrator_v2`  
- Build execution timeline logger (can wrap old path)

**Risk:** Low  

## Phase 1 — Strangler skeleton (2–4 weeks)

- Implement Session Lock + Idempotency in front of existing processor  
- Implement WaitContract synthesis from current `current_step`  
- Fix **P0 behavioral bugs** behind flag: clear wait on breakout; title match; no fake greeting without state clear  

**Risk:** Medium — behavior changes; shadow compare  

## Phase 2 — Extract modules (4–8 weeks)

- Move keyword, AI pipeline, flow runner behind interfaces  
- Orchestrator implements ladder; old function becomes adapter calling new modules  
- Dual-run: old vs new decision log for sample % traffic  

**Risk:** Medium–High  

## Phase 3 — Cutover (2–3 weeks)

- Enable orchestrator per org  
- Remove breakout dual-send paths  
- Introduce `chatbot_flow_versions`  
- Deprecate unused fields or enforce them (`TriggerButtonID`, schedules)  

**Risk:** Medium  

## Phase 4 — Hardening (ongoing)

- Subflows, tool calling, omnichannel ports  
- Remove dead legacy step executor paths  

## Rollback

- Feature flag off → previous processor  
- Session rows remain compatible via adapter  
- Do not run dual writers on incompatible schemas without migration  

## Risk assessment

| Risk | Mitigation |
|------|------------|
| State machine mismatch | Compatibility adapter + golden multi-turn tests |
| Double send during dual-run | Shadow mode = decide only, no send |
| Cache inconsistency | Invalidate on all write APIs |
| Long AI latency | Timeouts + fallback messages |

---

# 22. Coding Standards

## 22.1 Suggested package layout (Go)

```text
internal/chatbot/
  orchestrator/       # priority ladder, state machine
  session/            # manager, wait contract
  flow/               # engine, versions
  nodes/              # handlers per type
  interactive/        # buttons, lists, matchers
  prompt/
  keyword/
  ai/                 # pipeline, ranker
  rag/
  knowledge/
  transfer/
  variable/
  template/
  api/
  media/
  outbound/
  inbound/            # envelope, normalize
  events/
  idempotency/
  lock/
  config/
  metrics/
  testkit/            # flow DSL, fakes
```

HTTP handlers stay in `internal/handlers` but become thin adapters.

## 22.2 Naming

- Interfaces: `FlowEngine`, `SessionRepository`  
- Implementations: `PostgresSessionRepository`, `MetaOutboundGateway`  
- Events: past tense `FlowStarted`  
- States: PascalCase enums matching Section 7  

## 22.3 Dependency injection

- Constructor injection of interfaces  
- No global mutable engines  
- App composition root wires adapters  

## 22.4 Error style

- Wrapped errors with `%w`  
- Domain errors: `ErrWaitExpired`, `ErrNoEdge`, `ErrIdempotentReplay`  
- Never panic in turn handlers  

## 22.5 Logging style

- Structured key-value  
- Always include correlation fields  
- No PII in DEBUG without flag  

## 22.6 Testing standards

- Table-driven unit tests  
- Flow DSL for multi-turn  
- Fakes preferred over heavy mocks  
- Race detector on lock tests  

## 22.7 Documentation standards

- Each package `doc.go` describes invariants  
- ADR for ladder or state changes  
- Update this target doc when architecture changes  

---

# 23. Future Roadmap

| Horizon | Capability | Architecture hook |
|---------|------------|-------------------|
| Near | Voice notes → STT → same TurnContext | Media Engine + STT port |
| Near | Image/document understanding | Vision/RAG adapters in AI pipeline |
| Mid | Multi-agent AI (specialist agents) | AI Engine agent router |
| Mid | MCP / external tools | Tool registry in AI Engine |
| Mid | CRM writeback | Event consumers + API Engine |
| Mid | Calendar / booking | Node type + OAuth secrets |
| Mid | Payments | PCI-safe redirect nodes; never store PAN |
| Mid | Workflow automation | Event-driven rules outside chat turn |
| Long | Omnichannel (IG, web widget, SMS) | Channel port; same Orchestrator |
| Long | Voice calls IVR parity | Shared expression/variable engines with calling |

All future channels must produce `TurnContext` and consume `OutboundPlan` — never fork orchestrator logic.

---

# 24. Final Recommendations

## 24.1 Critical improvements (build first)

1. **Conversation Orchestrator + priority ladder** (single brain)  
2. **WaitContract** as first-class state (replace implicit `current_step` parking)  
3. **Per-contact session lock + WAMID uniqueness**  
4. **AI pipeline** with knowledge/RAG before LLM  
5. **Response Planner** (no dual menus without dual contracts)  
6. **Execution timeline** for every turn  

## 24.2 Architecture principles (non-negotiable)

- One owner for routing decisions  
- Explicit state machine  
- Interactive wait is a contract, not a side effect  
- State and user-visible UI always match after a turn  
- Modules communicate via interfaces and events  
- Definitions cached; sessions strongly consistent under lock  
- Prefer deterministic handlers before generative AI  

## 24.3 Refactoring priorities

| Priority | Item | Complexity | Impact |
|----------|------|------------|--------|
| P0 | Orchestrator + WaitContract + lock + idempotency | High | Correctness |
| P0 | Fix breakout/state desync behavior | Medium | User trust |
| P0 | Knowledge→RAG→LLM order | Medium | Answer quality |
| P1 | Flow versioning | Medium | Safe publishes |
| P1 | Keyword `flow` + trigger button id enforcement | Low | Product completeness |
| P1 | Outbox events + metrics | Medium | Ops |
| P2 | Subflows, tools, omnichannel | High | Growth |

## 24.4 Long-term vision

Whatomate’s chatbot becomes a **channel-agnostic conversation runtime**:

- WhatsApp is an adapter  
- Flows are versioned programs  
- AI is a ranked knowledge pipeline, not a default catch-all  
- Humans and bots share clear ownership states  
- Every turn is explainable, testable, and replayable  

## 24.5 Recommendation card template (for future ADRs)

When changing this architecture, document:

| Field | Content |
|-------|---------|
| **Reason** | Why change |
| **Benefits** | Measurable gains |
| **Trade-offs** | Costs/risks |
| **Impact** | Users, data, APIs |
| **Complexity** | Eng effort |
| **Priority** | P0–P3 |

---

# Appendix A — TurnContext / TurnResult (contract sketch)

```text
TurnContext {
  correlation_id, turn_id
  org_id, account, contact
  inbound: { wamid, type, text, button_id, list_id, nfm, media, locale }
  session
  settings
  now
}

TurnResult {
  claimed: bool
  handler: string
  state_transition: {from, to}
  session_mutations
  outbound: [{type, body, buttons, media, ...}]
  events[]
  timeline[]
  error?
}
```

---

# Appendix B — Invariants checklist (acceptance criteria)

- [ ] Same WAMID never produces two bot turns  
- [ ] Two concurrent messages for one contact are serialized  
- [ ] Valid button click never loses to keyword/AI  
- [ ] Typed exact option title advances same as click  
- [ ] After any breakout answer, wait contract is cleared or replaced atomically with messages  
- [ ] Greeting/menu send always creates or updates wait if interactive  
- [ ] Transfer active ⇒ zero automation replies  
- [ ] AI mode with knowledge/RAG never calls LLM before those stages  
- [ ] Flow publish does not change in-flight pinned version  
- [ ] Every turn has correlation_id and timeline  

---

# Appendix C — Mapping baseline problems → target fixes

| Baseline problem | Target fix |
|------------------|------------|
| God-function processor | Orchestrator + modules |
| Stuck buttons | WaitContract + resolve/clear rules |
| Repeated keyword breakout | free_text_policy + clear wait |
| Fake greeting reset | Response Planner + state transition together |
| Flow type keyword ignored | Keyword Engine starts FlowEngine |
| TriggerButtonID unused | Flow triggers include button/list ids |
| No title match | Interactive matcher |
| Webhook races | Idempotency + lock |
| RAG/LLM confusion | Ranked AI pipeline |
| Double menus | Planner fingerprint + outbound_turns |

---

*End of target architecture specification. This document is the design authority for chatbot refactors; implementation must not begin by re-expanding logic inside a single inbound function.*
