# Chatbot Engine Analysis

**Project:** Whatomate (WhatsApp CRM)  
**Scope:** Runtime audit of the WhatsApp chatbot engine  
**Date:** 2026-07-19  
**Constraint:** Analysis only — no source code was modified  

This document describes how the chatbot **actually** executes at runtime (Go backend), not the theoretical pipeline assumed by product docs. Every finding references concrete files and functions.

---

# Executive Summary

The chatbot is a **single-process, goroutine-per-message** pipeline:

1. Meta posts to `POST /api/webhook`
2. `WebhookHandler` fans out each message with `go processIncomingMessage(...)`
3. `processIncomingMessage` does a best-effort duplicate check, then calls **`processIncomingMessageFull`**
4. All automation (keywords, flows, AI/RAG, greeting/fallback) lives in **`processIncomingMessageFull`**
5. Multi-step conversations are driven by a **v2 JSON graph** on `chatbot_flows.graph`, executed by **`runChatGraph`**

There is **one live runtime path**. Legacy step tables (`chatbot_flow_steps`) are only used for one-time graph backfill; they are not executed for inbound messages.

### Actual priority order (runtime)

```
Incoming message
  → Save contact + message
  → Active agent transfer? → STOP (no bot)
  → Chatbot disabled? → queue transfer → STOP
  → Excluded number? → STOP
  → Empty text? → STOP
  → Session load/create
  → Transfer keyword? → transfer → STOP
  → Active flow session (CurrentFlowID set)?
        → Cancel keywords? → complete session, re-route as free chat
        → Else if parked on buttons + free text (no buttonID):
              → different flow trigger? → switch flow
              → non-transfer keyword? → reply + RE-SEND same buttons (stay parked)
              → contact hardcode? → reply + RE-SEND buttons
              → AI/RAG? → reply + send GREETING menu (session still on buttons!)
              → else fall into runChatGraph → RE-SEND buttons again
        → Else runChatGraph (consume button/prompt/nfm, advance graph) → STOP
  → Flow trigger keywords? → start flow via runChatGraph → STOP
  → New session + DefaultResponse? → optional keyword/AI then GREETING → STOP
  → Non-transfer keyword? → reply → STOP
  → Contact hardcode? → reply + greeting → STOP
  → AI/RAG (free-text mode) → reply + greeting → STOP
  → Fallback message (existing sessions only)
```

### Critical mismatch vs expected order

| Expected | Actual |
|----------|--------|
| Keyword rules → Flow engine | **Active flow always wins** over keywords (except buttons free-text breakout + transfer keywords) |
| Button/list processing before keywords | Only when already inside a flow; free text at buttons uses keywords as **breakout**, not as graph advance |
| RAG before OpenRouter | Controlled by `ai_free_text_mode`; **legacy empty mode + RAG present = `rag_only`** (OpenRouter skipped). **`local_only` skips RAG entirely** |
| Static knowledge before OpenRouter | Static AI contexts are **only system-prompt context for local LLM**, never a retrieval “hit” gate |
| Clear button state after completion | **No dedicated “active buttons” store** — park state is `session.current_step`; AI/keyword breakouts often **leave the session parked** |

### Top root causes for the 12 reported failures

1. **Buttons park forever** (`session.current_step` + `yield=true`) until a real interactive `buttonID` arrives. Free text does not match button titles.
2. **Buttons free-text breakout** deliberately keeps the user on the same buttons node and re-sends the menu → repeated “breakout to keyword rule”, double menus, “stuck in active button state”.
3. **AI fallback at buttons** sends the **greeting menu** but does **not** clear `current_flow_id` / `current_step` → next messages still run the old interactive node.
4. **Keyword `response_type=flow` does not start a flow** — only `ChatbotFlow.TriggerKeywords` do.
5. **`TriggerButtonID` is never read at runtime**.
6. **No title→button-id fuzzy match** for typed menu labels.
7. **Webhook race** on duplicate WAMIDs (index only, not unique; check-then-act).
8. **Session selection race** (two concurrent messages can create two active sessions).
9. **RAG vs local LLM** depends on free-text mode; static contexts never block OpenRouter by themselves.

---

# System Architecture

## Stack

| Layer | Technology | Role |
|-------|------------|------|
| HTTP | fasthttp + fastglue (`cmd/whatomate/main.go`) | Routes, webhook |
| Chatbot logic | `internal/handlers/chatbot_processor.go` | Orchestration |
| Flow runtime | `internal/handlers/chatbot_graph_runner.go` | Graph execution |
| Graph types | `internal/handlers/chatbot_graph_types.go` | Node/edge parse & resolve |
| Models | `internal/models/chatbot.go` | DB schemas |
| Cache | `internal/handlers/cache.go` + Redis | Settings, flows, keywords, AI contexts |
| WhatsApp API | `pkg/whatsapp` | Send/receive types |
| UI builder | `frontend/src/views/chatbot/ChatbotFlowBuilderView.vue` | Authors v2 graphs |

## Runtime components (executed path)

```
Meta Cloud API
      │
      ▼
POST /api/webhook  ── WebhookHandler (webhook.go)
      │  go processIncomingMessage  (async per message)
      ▼
processIncomingMessage  ── duplicate WAMID check
      ▼
processIncomingMessageFull  ── THE chatbot brain
      │
      ├── contactutil.GetOrCreateContact
      ├── saveIncomingMessage
      ├── hasActiveAgentTransfer
      ├── getChatbotSettingsCached
      ├── getOrCreateSession
      ├── matchKeywordRules
      ├── matchFlowTrigger / getChatbotFlowByIDCached
      ├── runChatGraph  (graph runner)
      ├── generateAIResponse / tryRAGResponse
      └── sendAndSave* → SendOutgoingMessage
```

## Dual models (only one executes)

| Model | Storage | Runtime? |
|-------|---------|----------|
| **v2 Graph** | `chatbot_flows.graph` JSONB `{version, nodes, edges, entry_node}` | **Yes** — `runChatGraph` |
| **v1 Steps** | `chatbot_flow_steps` rows | **No** — migration/backfill only (`BackfillChatbotFlowGraph`) |

If an active flow has `graph == nil`, the processor logs an error, calls `exitFlow`, and returns (`chatbot_processor.go` ~424–427).

---

# Message Lifecycle

## 1. Incoming webhook

**Files:** `internal/handlers/webhook.go`, routes in `cmd/whatomate/main.go`

| Step | Function | Behavior |
|------|----------|----------|
| Verify | `WebhookVerify` | `hub.mode=subscribe` + global or per-account verify token |
| Receive | `WebhookHandler` | Parse `WebhookPayload`; optional HMAC via `X-Hub-Signature-256` + account `AppSecret` |
| Filter | same | Field `messages` only for chat; calls/templates/preferences handled separately |
| Profile | same | Match `contacts[].wa_id` / `user_id` to set `profileName` |
| Dispatch | same | `go a.processIncomingMessage(phoneNumberID, msg, profileName)` |

**Important:** HTTP always returns 200 after enqueueing goroutines. Processing is **asynchronous** and **not transactional** with the webhook ACK.

### Signature validation

- If signature header present and account has `AppSecret`, invalid signature → 403.
- If no signature or no secret found while iterating accounts, processing may continue without hard fail for all accounts (verification is best-effort per found account).

### Message types extracted in `processIncomingMessageFull`

| WhatsApp type | `messageText` | `buttonID` | Notes |
|---------------|---------------|------------|-------|
| `text` | body | — | |
| `button` (template quick reply) | button text | payload | Forced to `button_reply` |
| `interactive` button_reply | title | id | |
| `interactive` list_reply | title | id | Same path as buttons |
| `interactive` nfm_reply | body | — | Parses form JSON → `flowResponseData` |
| media | caption | — | Downloads media; empty caption skips chatbot |
| `location` / `contacts` | JSON string | — | |
| `reaction` | — | — | Handled early; no chatbot |

Empty `messageText` after extraction → chatbot skipped (`"Skipping message with no text content for chatbot"`).

## 2. Pre-chatbot gates

Order inside `processIncomingMessageFull`:

1. **Account lookup** — `getWhatsAppAccountCached(phoneNumberID)`
2. **Contact** — `contactutil.GetOrCreateContact` (normalize `+`, restore soft-delete, race re-fetch)
3. **BSUID** update if present
4. **`saveIncomingMessage`** — always (even if bot disabled)
5. **`ClearContactChatbotTracking`** — resets client-inactivity SLA flags on contact
6. **`hasActiveAgentTransfer`** → skip bot entirely
7. **Settings** — `getChatbotSettingsCached`; disabled → `createTransferToQueue(..., TransferSourceChatbotDisabled)`
8. **Excluded numbers** — digit-normalized match
9. **Business hours** — if enabled and not allowed outside → out-of-hours message and stop
10. Empty text stop
11. Session + automation

## 3. End-to-end sequence (actual)

```
WebhookHandler
  └─ go processIncomingMessage
       ├─ duplicate check (messages.whats_app_message_id)
       └─ processIncomingMessageFull
            ├─ getWhatsAppAccountCached
            ├─ GetOrCreateContact
            ├─ parse content / buttonID / media / nfm
            ├─ saveIncomingMessage
            ├─ ClearContactChatbotTracking
            ├─ hasActiveAgentTransfer? → return
            ├─ getChatbotSettingsCached
            ├─ !IsEnabled? → createTransferToQueue → return
            ├─ isPhoneExcluded? → return
            ├─ business hours gate
            ├─ getOrCreateSession
            ├─ logSessionMessage(incoming, "keyword_check")
            ├─ matchKeywordRules  (always computed early)
            ├─ transfer keyword path? → send + createTransferFromKeyword → return
            ├─ if session.CurrentFlowID != nil:
            │     ├─ cancel keywords? → exitFlow + new session → fall through
            │     ├─ buttons free-text breakout branches (see Interactive)
            │     └─ runChatGraph → return
            ├─ matchFlowTrigger? → set session flow, runChatGraph → return
            ├─ isNewSession && DefaultResponse?
            │     ├─ non-greeting → contact/keyword/AI optional
            │     └─ sendGreetingMenu → return
            ├─ keyword (non-transfer) → send → return
            ├─ contact bypass → send + greeting → return
            ├─ canAttemptAI? → generateAIResponse → send + greeting → return
            └─ FallbackMessage (existing session only)
```

---

# Session Lifecycle

## Storage model

**Table:** `chatbot_sessions` (`models.ChatbotSession`)

| Column | Role |
|--------|------|
| `status` | `active` \| `completed` \| `cancelled` \| `timeout` |
| `current_flow_id` | Active flow UUID (nullable) |
| `current_step` | **Parked graph node ID** (this is “active buttons / waiting input”) |
| `step_retries` | Prompt validation retries |
| `session_data` | Variables + `__path__` audit trail (JSONB) — **there is no `chatbot_session_variables` table** |
| `last_activity_at` | Touch on load; timeout window |
| `started_at` / `completed_at` | Lifecycle timestamps |

**Table:** `chatbot_session_messages` — audit log only (`logSessionMessage`), not used for routing decisions except AI history.

## Create

`getOrCreateSession` (`chatbot_processor.go` ~981–1011):

```sql
WHERE organization_id = ? AND contact_id = ? AND whats_app_account = ?
  AND status = 'active' AND last_activity_at > now - timeoutMins
```

- Hit → update `last_activity_at`, return `(session, isNew=false)`
- Miss → `Create` new active session with empty `session_data`

**Default timeout:** `chatbot_settings.session_timeout_minutes` (default 30).

## Update

| Event | What is written |
|-------|-----------------|
| Any load | `last_activity_at` |
| Flow start | `current_flow_id`, `current_step=""`, reset data with `_flow_id` / `_flow_name` |
| Graph tick | `persistChatSession` → full `Save` (step, data, status, retries) |
| Path audit | `session_data.__path__` via `appendChatPath` |
| Variables | Keys in `session_data` (`store_as`, `set_variable`, API mapping, nfm fields) |

## Complete / reset / destroy

| Path | Function | Clears flow? | Status |
|------|----------|--------------|--------|
| Graph no edge / end | `runChatGraph` | **No** (`current_flow_id` left set) | `completed` |
| Transfer node | `execChatTransfer` | leaves IDs, yield | `completed` |
| Cancel keywords | `exitFlow` | **Yes** (nil flow, empty step) | `completed` |
| Unloadable graph | `exitFlow` | Yes | `completed` |
| Timeout | Implicit: old active session no longer selected; **row not auto-marked `timeout`** | N/A | Stale `active` rows remain until new session |

**There is no hard delete of sessions.** Completed rows accumulate. Next free-text message creates a **new** active session if the previous one is completed or timed out via `last_activity_at`.

## Missing cleanup (high impact)

1. **Keyword / AI breakout while parked on buttons does not call `exitFlow` or clear `current_step`.** User remains in old flow indefinitely (within timeout).
2. **After AI free-text at buttons**, code sends `sendGreetingMenu` but session still has old `current_flow_id` + buttons `current_step` — greeting is cosmetic only.
3. **Graph completion** marks `completed` but does not null `current_flow_id` (usually OK because status filter excludes it; confusing for analytics/debug).
4. **Timed-out sessions** never flip to `timeout` status — only stop matching.
5. **No session lock** — concurrent goroutines can corrupt step/variables with last-write-wins `Save`.

---

# Flow Engine

## Load

1. Active session: `getChatbotFlowByIDCached` → scans `getChatbotFlowsCached(org)` (all **enabled** flows for org, Redis TTL).
2. Free chat: `matchFlowTrigger` iterates same cache.

`matchFlowTrigger` rules:

- Flow `whatsapp_account` empty **or** equals account name
- For each `trigger_keywords` entry: `flowTriggerKeywordMatches`
  - Multi-word: case-insensitive **substring**
  - Single token: case-insensitive **whole-word** regex `\b...\b` (avoids `hi` matching `this`)
- **First matching flow wins** (DB order of cache, not priority field — flows have no priority column)
- **`TriggerButtonID` is never consulted**

## Graph structure

Parsed by `parseChatGraph` (`chatbot_graph_types.go`):

- Requires `version == 2`, non-empty `entry_node`, node present
- Builds `nodeMap` and `edgeMap[from][]edges`

## Node types (`ChatNodeType`)

| Type | Blocking? | Outcome(s) | Executor |
|------|-----------|------------|----------|
| `start` | No | `default` | fall-through |
| `message` | No | `default` | send text/media |
| `buttons` | **Yes** until `buttonID` | `button:<id>` or yield | `execChatButtons` |
| `prompt` | **Yes** until text input | `default`, `max_retries` | `execChatPrompt` |
| `api_call` | No | `http:2xx`, `http:non2xx` | |
| `condition` | No | `true`, `false` | expr-lang |
| `timing` | No | `in_hours`, `out_of_hours` | |
| `set_variable` | No | `default` | |
| `ai_response` | No | `default` | `generateAIResponse` |
| `transfer` | Terminal yield | — | agent queue/team |
| `webhook` | No | `default` | fire-and-forget HTTP |
| `goto_flow` | Switch | `goto` | reload graph mid-run |
| `whatsapp_flow` | **Yes** until nfm | `default` | Meta flow form |
| `end` | Terminal | empty | optional message |

## Edge selection (`resolveEdge`)

1. Exact match on `condition` string
2. Else `default` edge if present
3. Else if outcome is `button:<id>`, also try bare `id` (legacy graphs)
4. Else `""` → **session completed**

**There is no automatic “keyword edge”, “list edge”, or “fallback edge” type** beyond:

- `button:<id>` (also used for list reply IDs)
- `default`
- `http:2xx` / `http:non2xx`
- `true` / `false`
- `in_hours` / `out_of_hours`
- `max_retries`
- `validation_failed` is documented in comments but **prompt invalid uses yield/retry, not that outcome**

## Runner loop (`runChatGraph`)

```
if CurrentStep == "":
    CurrentStep = EntryNode
    clear userInput + buttonID   // trigger is NOT node input

loop max 100:
    apply skip_condition → default edge
    execute node
    if yield → persist, return
    if CurrentFlowID changed (goto_flow) → reload graph, EntryNode
    next = resolveEdge(outcome)
    if next == "" → complete session, persist
    else CurrentStep = next
```

**First-run semantics:** Trigger keyword that starts a flow is **discarded** before entry execution so it cannot satisfy a prompt on the same tick.

## “User Input” in the product

In the flow builder (`ChatNodeProperties.vue`), a Text node with **Expected response ≠ none** becomes type **`prompt`**. That is the only runtime “user input wait” besides buttons / WhatsApp Flow.

**Why user input sometimes never runs:**

1. Node still type `message` (expected response “none”) — fire-and-forget, never waits.
2. Session never reaches the node because prior **buttons park** or **breakout** never advances.
3. Trigger input cleared at entry — if a prompt is entry and author expected the trigger phrase to be the answer, it will **re-ask** (by design).
4. Free text typed at **buttons** never becomes prompt input; it hits breakout/AI or re-prompts buttons.
5. Ghost edges: button click with ID not matching any edge → session **completes** immediately (`UnknownButtonEndsFlow` test; warn log for ghost-edge).

---

# Keyword Engine

## Entry

`matchKeywordRules(orgID, accountName, messageText)` — always invoked early for every text-bearing inbound (result may be ignored later).

## Load order

`getKeywordRulesCached`:

1. Account-specific rules `ORDER BY priority DESC`
2. Global rules (`whats_app_account = ''`) `ORDER BY priority DESC`
3. Concatenate account **then** global — first match wins overall

## Matching

| MatchType | Case-insensitive (default) | Case-sensitive |
|-----------|----------------------------|----------------|
| `exact` | lower equality | exact equality |
| `contains` (default) | substring | substring |
| `starts_with` | prefix | prefix |
| `regex` | `regexp.Compile(keyword)` as authored | same |

**Note:** Flow triggers use whole-word for single tokens; **keywords with `contains` can match inside longer sentences** (e.g. keyword `hi` matches `this`).

## Fields that exist but are **not enforced** at match time

| Field | Model | Runtime |
|-------|-------|---------|
| `active_from` / `active_until` | yes | **ignored** |
| `conditions` | yes | **ignored** |
| `response_type` template/media/script/flow | yes | Treated as **text body** if body/buttons present; **flow type does not start a flow** |

## Priority vs other engines

| Situation | Keywords win? |
|-----------|---------------|
| Transfer keyword | Yes, before flow (even mid-session, **before** active flow branch) |
| Active flow + non-buttons node | **No** — `runChatGraph` only |
| Active flow + buttons + free text | Yes as **breakout** (if non-transfer match) |
| Active flow + buttons + button click | **No** — graph consumes button |
| Free chat | After flow triggers; before AI |
| New session greeting path | Optional answer before greeting |

## Button / session override summary

- **Button click** with active flow: keywords ignored (except transfer already handled earlier — transfer keywords can still fire **before** flow processing even with an active flow!).
- **Actually:** Transfer keywords are checked **before** the active-flow branch. A transfer keyword mid-flow **aborts** the flow without `exitFlow` — session stays active with old step while an agent transfer is created. Bot is then suppressed by `hasActiveAgentTransfer` on subsequent messages. The session row may remain `active` with a flow parked underneath.

---

# Interactive Components

## How “active buttons” are stored

**Not a separate table or flag.**  
“Active button state” = 

```
session.status == active
AND session.current_flow_id IS NOT NULL
AND session.current_step == <buttons node id>
```

Persisted by `persistChatSession` after `execChatButtons` returns `yield: true`.

There is **no TTL specific to buttons** other than the session timeout.

## Buttons / lists send path

`sendAndSaveInteractiveButtons`:

- Reply buttons vs URL/phone CTA separated
- ≤3 reply buttons → WhatsApp `button` interactive
- 4–10 → WhatsApp `list` interactive
- CTA URL/phone sent as separate `cta_url` messages (max 2)

List **replies** reuse the same `buttonID` / `button_reply` typing path as buttons.

## Click handling

`execChatButtons`:

```go
if !ctx.consumed && ctx.buttonID != "" {
    return outcome "button:" + buttonID  // advance via edge
}
// else send buttons again and yield
```

**Typed button title does not match.** Only Meta’s interactive `button_reply.id` / list id / template payload works.

## Free-text at buttons (breakout) — source of multiple bugs

In `processIncomingMessageFull` when `buttonID == ""` and current node type is `buttons`:

| Order | Action | Session state after |
|-------|--------|---------------------|
| 1 | Other flow’s trigger keywords | Switches flow, runs new graph |
| 2 | Non-transfer keyword | Sends keyword reply **+ re-sends same buttons**; **still parked** |
| 3 | Hardcoded contact keywords | Contact card + re-send buttons; **still parked** |
| 4 | AI (`canAttemptAI`) | AI text + **`sendGreetingMenu`**; **still parked on old buttons node** |
| 5 | Fallthrough `runChatGraph` | Re-sends buttons again; **still parked** |

Log line for (2): **`"Active buttons node breakout to keyword rule"`** — this is **by design**, not an accidental log, and will fire **on every free-text keyword match** while parked.

## Why menus appear twice

1. Keyword breakout: keyword buttons/text **and** re-sent node buttons  
2. AI free-text (global or buttons): answer **and** greeting menu  
3. New session non-greeting: AI/keyword **and** greeting  
4. Contact bypass: card **and** greeting/buttons  
5. Race: duplicate webhook processing double-sends  

## Quick replies

Template quick-reply (`type=button`) uses `payload` as `buttonID` and title as text — works with graph if edges use that payload.

---

# AI & RAG Pipeline

## Free-text mode (`AIConfig.FreeTextMode` / `ai_free_text_mode`)

Resolved by `resolveFreeTextMode` (`chatbot_processor.go` ~1231):

| Stored mode | Behavior |
|-------------|----------|
| `off` | No AI |
| `rag_only` | External RAG only |
| `rag_then_local` | RAG then local LLM |
| `local_only` | Local LLM only (**skips RAG**) |
| empty (legacy) | If any enabled RAG context → **`rag_only`**; else if local configured → `local_only`; else `off` |

## Actual free-text order (`generateAIResponse`)

```
1. If mode off → error
2. If mode rag_only OR rag_then_local:
      tryRAGResponse (all enabled type=rag contexts, priority DESC)
      - keywords on RAG contexts are intentionally NOT required
      - first non-empty non-abstained answer wins
3. If mode allows local AND localAIReady (enabled + provider + api key):
      buildAIContext → static + api contexts only (keyword-filtered)
      dispatch OpenAI / Anthropic / Google / OpenRouter
4. Else if RAG was expected/tried → user-facing fallback string
```

### Expected vs actual (product expectation)

| Expected step | Actual |
|---------------|--------|
| Knowledge base | Only via **external RAG HTTP** (`ContextTypeRAG`) or static text stuffed into local prompt |
| AI Context (static) | **Not** an independent answer; only local system context |
| RAG search | `tryRAGResponse` → `callRAGContext` POST `{question, history, session_id, ...}` to configured URL (`/chat` normalized) |
| If nothing found → OpenRouter | Only if mode is `rag_then_local` or `local_only` with OpenRouter configured |

**Static knowledge does not prevent OpenRouter** under `local_only` / `rag_then_local` after abstain — OpenRouter still runs with static text in the **system prompt**.

## RAG context config (`ai_contexts` type=`rag`)

`api_config`: `url`, `headers` / `api_key`, optional `top_k`, `min_score`, `language`, `timeout_seconds` (default 45).

Response shape: `{ "answer": "...", "abstained": bool }`.

## Flow node `ai_response`

Uses same `generateAIResponse` / free-text mode. On failure sends fallback message and still advances `default` edge.

## Hardcoded contact bypass (not AI)

`isContactOrLocationQuery` matches substrings: contact, address, location, maps, office, bengaluru, direction, where is — returns **hardcoded Darvi contact card** (`getContactCardText`). This is product-specific logic inside the generic processor.

---

# Database Structure

## Tables that exist and matter

```
organizations
whatsapp_accounts  (phone_number_id, app_secret, name, ...)
contacts
messages  (whats_app_message_id INDEX only — not UNIQUE)
chatbot_settings
keyword_rules
chatbot_flows  (graph JSONB, trigger_keywords, cancel_keywords, ...)
chatbot_flow_steps  (LEGACY — backfill only)
chatbot_sessions
chatbot_session_messages
ai_contexts
agent_transfers
```

## Tables/names in the brief that do **not** exist as separate tables

| Name in brief | Reality |
|---------------|---------|
| `chatbot_nodes` / `chatbot_edges` | Embedded in `chatbot_flows.graph` |
| `chatbot_session_variables` | `chatbot_sessions.session_data` JSONB |

## Relationships

```
Organization 1──* ChatbotSettings (per account or org default '')
Organization 1──* KeywordRule
Organization 1──* ChatbotFlow
Organization 1──* AIContext
Organization 1──* ChatbotSession ──* ChatbotSessionMessage
Contact 1──* ChatbotSession
Contact 1──* Message
Contact 1──* AgentTransfer
ChatbotFlow 1──* ChatbotFlowStep (legacy)
ChatbotSession.current_flow_id → ChatbotFlow (optional FK relation)
```

## Session state machine (logical)

```
                 create
    [none] ──────────────► ACTIVE (no flow)
                              │
              matchFlowTrigger / breakout to flow
                              ▼
                         ACTIVE_IN_FLOW
                         (current_step set)
                              │
          buttons/prompt/whatsapp_flow yield ◄──┐
                              │                 │
                     user input / click ────────┘
                              │
              end / no edge / transfer node
                              ▼
                          COMPLETED
                              
    ACTIVE ──(last_activity older than timeout)──► orphan ACTIVE row
              (not selected; status not updated)
```

---

# State Machine

## Session-level

| State | Meaning | Entry | Exit |
|-------|---------|-------|------|
| Active free | No `current_flow_id` | New session | Flow start / complete session rarely |
| Active in flow | `current_flow_id` set | Trigger / goto / breakout | Complete, exitFlow, transfer |
| Parked interactive | `current_step` = blocking node | yield from buttons/prompt/nfm | consume input or breakout (partial) |
| Completed | status completed | terminal graph / exitFlow / transfer | new session on next message after timeout filter |

## Buttons node micro-state

```
ENTER buttons (no buttonID)
   → send interactive → YIELD (park)

RESUME with buttonID
   → outcome button:<id> → resolveEdge → next node

RESUME with free text
   → OUTSIDE runner: breakout keyword/AI/contact/other flow
   → OR inside runner: send buttons again → YIELD
```

## Prompt node micro-state

```
ENTER with empty/consumed input → send body → YIELD
RESUME with text → validate → store_as → default edge
invalid → step_retries++ → error message → YIELD
retries exhausted → outcome max_retries
```

---

# Log Flow Mapping

| Log message | Function | Typical next step |
|-------------|----------|-------------------|
| `Received message` | `WebhookHandler` | `go processIncomingMessage` |
| `Duplicate message detected, skipping` | `processIncomingMessage` | stop |
| `Processing incoming message` | `processIncomingMessageFull` | account lookup |
| `WhatsApp account not found` | same | stop |
| `Failed to get or create contact` | same | stop |
| `Saved incoming message` | `saveIncomingMessage` | back to processor |
| `Contact has active agent transfer...` | processor | stop |
| `Chatbot settings loaded` | processor | session |
| `Outside business hours...` | processor | stop or continue |
| `Skipping message with no text content...` | processor | stop |
| `Processing message` | processor | keywords/session |
| `Transfer keyword matched` | processor | transfer |
| `Cancel keyword matched; exiting flow` | processor | free chat routing |
| `Active buttons node breakout to different flow` | processor | `runChatGraph` new flow |
| **`Active buttons node breakout to keyword rule`** | processor | send keyword + re-send buttons |
| `Active buttons node contact bypass triggered` | processor | contact card + buttons |
| `Active buttons node fallback to AI` | processor | AI + greeting menu |
| `Chat graph runner failed` | processor | stop |
| `chat graph node not found` | `runChatGraph` | error |
| `chat graph: no edge matched button outcome` | `runChatGraph` | complete session |
| `New session - sending greeting message` | processor | greeting |
| `Keyword rule matched` | processor | send response |
| `Attempting AI response` / `Free-text AI path` | processor / `generateAIResponse` | RAG/local |
| `RAG attempt` / `RAG answer` / `RAG abstained` | `tryRAGResponse` / `callRAGContext` | next context or local |
| `Local LLM attempt` | `generateAIResponse` | provider call |
| `Sending fallback message` | processor | fallback |
| `persist chat session` (error) | `persistChatSession` | state may be lost |

---

# Root Cause Analysis

## Problem 1 — User Input Trigger sometimes never executes

**Meaning:** `prompt` nodes (UI: Text + expected response).

| Cause | Location |
|-------|----------|
| Session never reaches prompt (stuck on buttons) | Buttons park + breakout |
| Node saved as `message` not `prompt` | Builder `setExpectedResponse` |
| Trigger text cleared at entry so prompt re-asks / appears skipped in same turn | `runChatGraph` lines 110–116 |
| Free text consumed by keyword/AI breakout at previous buttons | processor 445–547 |

## Problem 2 — Messages remain inside Active Button state

**Cause:** Design of `execChatButtons` + breakout handlers that **never clear** `current_step`.

Free text without `buttonID` cannot leave the node except via:

- matching **another flow’s** trigger (switches flow), or  
- never — keyword/AI/contact leave the node parked.

## Problem 3 — Previous interactive nodes affect new messages

**Cause:** Active session within timeout still has `current_flow_id` + `current_step`.  
Especially after **AI fallback at buttons** which shows greeting but does not complete the session.

## Problem 4 — “Breakout to keyword rule” repeatedly

**Cause:** Intentional branch at processor ~466–497. Every free-text keyword match while parked re-enters this path, logs the same line, re-sends the menu.

## Problem 5 — Keyword rules restart instead of next flow node

| Cause | Detail |
|-------|--------|
| `ResponseTypeFlow` not implemented | Falls through to text body only (`matchKeywordRules` ~786–802) |
| Breakout does not call `runChatGraph` advance | Stays on same buttons node |
| Mid-flow keywords ignored | Except buttons free-text breakout |
| Flow restart via trigger keywords | Whole-word `hi`/`start` on free chat starts flow from entry again |

## Problem 6 — Session state persists incorrectly

| Issue | Detail |
|-------|--------|
| Breakouts don’t update flow step | Parked buttons after AI/keyword |
| Concurrent `Save` | No locking |
| Timeout doesn’t set status | Orphan `active` rows |
| Graph complete leaves flow id set | Harmless for routing, noisy for operators |
| Transfer keyword mid-flow | Transfer created; session still active with old step |

## Problem 7 — Active buttons not cleared after completion

**Cause:** “Completion” of a **breakout answer** is not flow completion. Only graph terminal / `exitFlow` / transfer node clear activity (and exitFlow alone clears flow fields).

## Problem 8 — RAG not used before AI fallback

| When | Why |
|------|-----|
| `ai_free_text_mode = local_only` | RAG skipped by design |
| No enabled `type=rag` contexts | `hasEnabledRAGContext` false |
| Mode empty + no RAG | Defaults to local only |
| Cache stale after enabling RAG | Redis `chatbot:ai_contexts:*` TTL 6h — must invalidate on save |

## Problem 9 — OpenRouter even when static knowledge exists

Static AI contexts are **not answers**; they are appended in `buildAIContext` for the local LLM system prompt. OpenRouter still runs whenever local path is allowed. RAG-only mode avoids OpenRouter but also **ignores static contexts for free-text**.

## Problem 10 — Interactive menus sent twice

See Interactive section. Primary code paths: keyword breakout dual-send; AI+greeting; new-session dual-send; duplicate webhooks.

## Problem 11 — New conversations enter old flows

| Cause | Detail |
|-------|--------|
| Session still active within timeout | Same contact continues `current_flow_id` |
| Greeting after AI doesn’t exit flow | User thinks they’re “at menu” but graph still owns them |
| Broad flow trigger keywords | e.g. `hi`, `help` restart flows when free chat |
| Concurrent sessions race | Wrong session row updated |

## Problem 12 — Node transitions skip expected routes

| Cause | Detail |
|-------|--------|
| Ghost button edges | Button ID renamed; no edge → complete |
| Bare vs `button:` prefix | Partial compat in `resolveEdge` |
| `skip_condition` true | Jumps default without executing node |
| `goto_flow` | Switches graph mid-run |
| Max 100 non-blocking nodes | Cycle aborts with error |
| First matching keyword/flow | Not necessarily the intended rule |
| Transfer keyword preempts flow | Skips remaining graph |

---

# High Risk Areas

1. **Buttons free-text breakout block** (`chatbot_processor.go` ~440–548) — largest source of “stuck menu”, double send, false greeting state  
2. **`execChatButtons`** — no title match, no expire, re-send on any free text  
3. **Async webhook goroutines** without per-contact mutex  
4. **Duplicate detection** non-atomic (`messages.whats_app_message_id` non-unique)  
5. **Transfer keyword before flow** without clearing session  
6. **Hardcoded Darvi contact card** in generic processor  
7. **Keyword `contains` default** overlapping flow whole-word triggers  
8. **Redis caches** (settings/flows/keywords/contexts) with long TTL if invalidation missed on admin edits  
9. **`ResponseTypeFlow` / `TriggerButtonID` / schedule fields** — data model promises features the runtime does not implement  
10. **OpenRouter injection inside `api_call` nodes** when URL contains `openrouter.ai` (org settings key) — separate from free-text mode  

---

# Recommended Fixes

> Ordered by impact. Do not apply until product decides intended priority (strict graph vs open free-text).

### P0 — Session & interactive integrity

1. **On successful free-text AI or keyword breakout at buttons**, either:  
   - `exitFlow` / complete session then send greeting, **or**  
   - advance to a configured “menu recovered” node — never leave `current_step` on the old buttons while showing a different menu.  
2. **Match free text to button titles** (case-insensitive) → synthesize `buttonID` before breakout.  
3. **Per-contact processing lock** (Redis or DB advisory lock) around `processIncomingMessageFull`.  
4. **Unique index** on `whats_app_message_id` (or insert-first with conflict ignore) to kill duplicate webhook execution.

### P1 — Keyword / flow contract

5. Implement **`response_type=flow`** to set `current_flow_id` and `runChatGraph`.  
6. Implement **`TriggerButtonID`** matching on interactive clicks in free chat.  
7. Enforce **`active_from` / `active_until`** (and decide on `conditions`).  
8. Decide transfer-keyword vs active-flow priority; if transfer wins, call **`exitFlow`**.

### P2 — AI/RAG contract

9. Document and expose free-text modes in admin UI clearly.  
10. If product wants static KB before OpenRouter: add a **static match / FAQ path** that returns without calling the LLM, or force `rag_only` / hybrid retrieval.  
11. Default for “has static only” should not silently be `local_only` inventing answers without grounding flags.

### P3 — UX polish

12. Avoid re-sending full button menus after every keyword answer (send once, or soft reminder).  
13. On graph complete, null `current_flow_id` and clear `current_step`.  
14. Background job: mark timed-out sessions `timeout`.  
15. Remove or configure hardcoded contact card strings.

---

# Sequence Diagrams

## A. Happy path — flow trigger + button click

```
User                Meta              WebhookHandler           processIncomingMessageFull        runChatGraph
 |-- "hi" ---------->|-- POST ------->|-- go process ------->|
 |                   |                |                      |-- session, matchFlowTrigger
 |                   |                |                      |-- CurrentStep="", runChatGraph
 |                   |                |                      |                 |-- start→message→buttons yield
 |<-- buttons -------|<-- send -------|<---------------------|<----------------|
 |-- click id=x ---->|-- POST ------->|-- go process ------->|
 |                   |                |                      |-- active flow, buttonID=x
 |                   |                |                      |-- runChatGraph
 |                   |                |                      |                 |-- button:x → next → ...
 |<-- next msgs -----|<-- send -------|<---------------------|<----------------|
```

## B. Failure path — free text at buttons (current)

```
User (parked on buttons) → free text "price list"
  → matchKeywordRules hit
  → log "Active buttons node breakout to keyword rule"
  → send keyword response
  → re-send SAME buttons
  → current_step UNCHANGED
  → next free text repeats forever
```

## C. Failure path — AI at buttons (current)

```
User free text (no keyword)
  → generateAIResponse (RAG/local per mode)
  → send AI answer
  → sendGreetingMenu  (user thinks reset)
  → session still Active + old buttons current_step
  → next message still hits buttons breakout/graph
```

## D. Free-text AI order

```
canAttemptAI?
  → generateAIResponse
       → resolveFreeTextMode
       → [rag_*] tryRAGResponse → callRAGContext (HTTP)
       → [local allowed] buildAIContext(static/api) → OpenRouter/OpenAI/...
       → else fallback string
```

---

# State Diagrams

## Overall chatbot routing

```
                    ┌──────────────────┐
                    │ Inbound text/btn │
                    └────────┬─────────┘
                             ▼
                    ┌──────────────────┐
            yes     │ Agent transfer?  │──► STOP
                    └────────┬─────────┘
                             │ no
                             ▼
                    ┌──────────────────┐
            no      │ Chatbot enabled? │──► Queue transfer STOP
                    └────────┬─────────┘
                             │ yes
                             ▼
              ┌──────────────────────────────┐
       yes    │ session.CurrentFlowID set?   │
      ┌───────┤                              │
      │       └──────────────┬───────────────┘
      │                      │ no
      ▼                      ▼
 Cancel? → exit           matchFlowTrigger?
 Buttons free-text          yes → runChatGraph
   breakout / graph         no  → greeting / keyword / AI / fallback
      │
      └── runChatGraph or breakout handlers
```

## Buttons park

```
        ┌─────────────┐
        │  buttons    │◄──────── free text (re-send)
        │  YIELD park │◄──────── keyword breakout (re-send)
        └──────┬──────┘
               │ buttonID present
               ▼
        resolveEdge(button:id)
               │
        ┌──────┴──────┐
        │ match       │ no match → COMPLETE session
        ▼
     next node
```

---

# Code File Responsibilities

| File | Responsibility |
|------|----------------|
| `cmd/whatomate/main.go` | Routes, startup backfill `BackfillChatbotFlowGraph` |
| `internal/handlers/webhook.go` | Meta verify/receive, async dispatch, duplicate WAMID check |
| `internal/handlers/chatbot_processor.go` | **Main orchestration**, keywords, session, AI/RAG, greeting, breakout |
| `internal/handlers/chatbot_graph_runner.go` | v2 graph execution, all node types |
| `internal/handlers/chatbot_graph_types.go` | Graph parse, edge resolve |
| `internal/handlers/chatbot_flow_migration.go` | Legacy steps → graph backfill |
| `internal/handlers/chatbot.go` | Admin CRUD API for settings/keywords/flows/AI contexts/sessions |
| `internal/handlers/cache.go` | Redis caches for settings/flows/keywords/AI contexts |
| `internal/handlers/agent_transfers.go` | Transfers, `hasActiveAgentTransfer` |
| `internal/handlers/messages.go` | `SendOutgoingMessage`, `ChatbotSendOptions` |
| `internal/handlers/template_engine.go` | `processTemplate` for `{{vars}}` |
| `internal/models/chatbot.go` | Schema for all chatbot entities |
| `internal/models/constants.go` | Enums (match types, free-text modes, session status) |
| `internal/contactutil/contactutil.go` | Contact get-or-create |
| `pkg/whatsapp/*` | WhatsApp API client types |
| `frontend/.../ChatbotFlowBuilderView.vue` | Graph authoring |
| `frontend/.../ChatNodeProperties.vue` | message↔prompt toggle, button slug IDs |

---

# Function Dependency Map

```
WebhookHandler
  └─ processIncomingMessage
       └─ processIncomingMessageFull
            ├─ getWhatsAppAccountCached
            ├─ contactutil.GetOrCreateContact
            ├─ saveIncomingMessage
            │    ├─ willChatbotHandle
            │    ├─ broadcastNewMessage
            │    └─ DispatchWebhook
            ├─ ClearContactChatbotTracking
            ├─ hasActiveAgentTransfer
            ├─ getChatbotSettingsCached
            ├─ createTransferToQueue / createTransferFromKeyword
            ├─ isPhoneExcluded
            ├─ isWithinBusinessHours
            ├─ getOrCreateSession
            ├─ logSessionMessage
            ├─ matchKeywordRules ← getKeywordRulesCached
            ├─ matchFlowTrigger ← getChatbotFlowsCached
            ├─ getChatbotFlowByIDCached
            ├─ exitFlow
            ├─ parseChatGraph / runChatGraph
            │    ├─ executeChatNode
            │    │    ├─ execChatMessage / Buttons / Prompt / APICall / ...
            │    │    ├─ execChatAIResponse → generateAIResponse
            │    │    └─ execChatTransfer → createTransferToTeam/Queue
            │    ├─ resolveEdge
            │    └─ persistChatSession
            ├─ sendGreetingMenu → sendAndSaveInteractiveButtons
            ├─ canAttemptAI / generateAIResponse
            │    ├─ tryRAGResponse → callRAGContext
            │    ├─ buildAIContext → fetchAPIContext
            │    └─ generateOpenRouterResponse / OpenAI / ...
            └─ sendAndSaveTextMessage / InteractiveButtons
                 └─ SendOutgoingMessage
```

---

# Conclusion

The chatbot engine is a **monolithic inbound processor** (`processIncomingMessageFull`) plus a solid **v2 graph runner** (`runChatGraph`). The graph runner itself is coherent and well-tested for click/prompt happy paths.

Production failures cluster around the **policy layer around parked interactive nodes**, not around basic message send/receive:

1. **Parked buttons are sticky** — free text almost never advances the graph; breakout handlers answer but leave the park in place.  
2. **Greeting after AI is a fake reset** — UI messages change, session graph position does not.  
3. **Keywords and flows are two parallel systems** with incomplete bridges (`ResponseTypeFlow`, `TriggerButtonID` unused).  
4. **AI order is mode-driven** — RAG-before-OpenRouter only when free-text mode allows; static contexts never short-circuit the LLM.  
5. **Concurrency and duplicate webhooks** can amplify double menus and session corruption.

A developer fixing the engine should treat **session `current_step` + `current_flow_id` lifecycle** and the **buttons free-text breakout block** as the primary defect surface, then align keyword/flow/AI product contracts with the code paths documented above.

---

## Appendix: Actual vs expected pipeline (one-page)

```
EXPECTED                              ACTUAL (code)
────────                              ─────────────
Webhook                               WebhookHandler ✓
Save Contact                          GetOrCreateContact ✓
Save Message                          saveIncomingMessage ✓
Load Chatbot Settings                 getChatbotSettingsCached ✓
Load Active Session                   getOrCreateSession ✓
Determine Current State               CurrentFlowID + CurrentStep ✓
Button / List Processing              Only if in flow; free text ≠ click ✗ partial
Keyword Rules                         Early match; often deferred/ignored ✗ order differs
Flow Engine                           runChatGraph if active/triggered ✓
AI Context Search (RAG)               tryRAGResponse if mode allows ~ 
OpenRouter (fallback only)            only if mode allows local after RAG ~
Reply                                 sendAndSave* ✓
Save State                            persistChatSession / incomplete on breakouts ✗
```

---

*End of analysis. No source files were modified for this audit.*
