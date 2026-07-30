# Flow analysis: `test2name` (`test2name_flow_export.json`)

**Scope:** observation and planning only — no runtime code changes described here as done.  
**Sources:** `test2name_flow_export.json`, chatbot processor / graph runner behavior for RAG free-text and buttons.  
**Date of export:** 2026-07-29  

---

## 1. Flow meta

| Field | Value |
|--------|--------|
| Name | `test2name` |
| Description | `ai, rag-node` |
| Enabled | `true` |
| Trigger keywords | `darvi, hi, hello, help, start, bot` |
| Initial message (meta) | `Hi! Let me help you with that.` |
| Completion message (meta) | `Thank you! We have all the information we need.` |
| Nodes | 24 |
| Edges | 36 |

This is a **button-menu chatbot** with one dedicated **Ask Darvi AI** branch that calls an **external RAG API** (not a native `ai_response` graph node).

---

## 2. Node inventory (how each node works)

### Entry / lead capture

| ID | Label | Type | What it does |
|----|--------|------|----------------|
| `__start__` | Start | `start` | Graph entry. |
| `node_imp_1784145021212` | supabase_lead_t1 | `api_call` | Silent POST to Supabase `darvi_leads` with `lead_type: type_1` (name/phone). No user-facing message. |
| `node_imp_1784145021194` | intro_message | `message` | Welcome text for Darvi Group. |
| `node_imp_1784145021195` | main_menu | `buttons` | Main hub: Registration Hub · Service Query · Ask Darvi AI · Contact Us. |

### Branch A — Registration

| ID | Label | Type | What it does |
|----|--------|------|----------------|
| `node_imp_1784145021196` | registration_details | `message` | Program details + registration fee copy. |
| `node_imp_1784145021197` | registration_form_buttons | `buttons` | Form link (`https://darvigroup.in/form`) + **Main Menu** / **Contact Us**. |

### Branch B — Services

| ID | Label | Type | What it does |
|----|--------|------|----------------|
| `node_imp_1784145021198` | services_overview | `message` | Services overview (+ optional media URL). |
| `node_imp_1784145021199` | services_buttons | `buttons` | Sandalwood Farming / IoT Smart Farming / Consultancy. |
| `node_imp_1784145021200` | sandalwood_service | `message` | Sandalwood content. |
| `node_imp_1784145021201` | iot_service | `message` | IoT content. |
| `node_imp_1784145021202` | consultancy_service | `message` | Consultancy content. |
| `node_imp_1784145021203` | service_followup | `buttons` | Main Menu / contact us. |

### Branch C — Contact Us

| ID | Label | Type | What it does |
|----|--------|------|----------------|
| `node_imp_1784145021204` | contact_us | `message` | Phone, email, website as **text** (not a call CTA button). |
| `node_imp_1784145021205` | mailcheck | `prompt` | Asks for email → stores `user_email`. |
| `node_imp_1784145021213` | supabase_lead_t2 | `api_call` | Lead `type_2` with email. |
| `node_imp_1784145021206` | thank_you | `message` | Confirmation with email/phone. |
| `node_imp_1784145021211` | contact_followup | `buttons` | Main Menu only. |

### Branch D — Ask Darvi AI (flow-owned RAG)

| ID | Label | Type | What it does |
|----|--------|------|----------------|
| `node_imp_1784145021207` | darvi_ai_intro | `message` | Explains AI + example questions. |
| `node_imp_1784145021210` | query_ai | `prompt` | “Please type your question below” → stores **`ai_query`**. |
| `node_imp_1784145021214` | supabase_lead_t3 | `api_call` | Lead `type_3` with `ai_query`. |
| `node_imp_1784145021208` | darvi_ai_api | `api_call` | POST to Railway RAG (`/chat`) with `{{ai_query}}`; maps `answer` → `rag_answer`; sends `{{rag_answer}}` via `message_template`. |
| `node_imp_1784145021209` | ai_followup | `buttons` | After answer: **Call Us · Ask Again · Main Menu · Contact Us**. |
| `node_imp_1784145021215` | call_us_message | `message` | Phone **+91 99868 90777** + email as **plain text**. |
| `node_imp_1784145021216` | call_us_followup | `buttons` | Ask Again / Main Menu / Contact Us. |

### Key AI node config (summary)

**darvi_ai_api**

- URL: `https://dchabotrag-production.up.railway.app/chat`
- Body: `{ "question": "{{ai_query}}", "source": "whatomate", "language": "en" }`
- `response_mapping`: `rag_answer` ← `answer`
- `message_template`: `{{rag_answer}}`
- Success edge: default → **ai_followup** only (no empty/non-2xx branch in export)

**ai_followup body**

> Need more help?  
> If that did not fully answer you, tap Call Us below. Our team can help with delivery, quantity, pricing, and more.

**call_us_message**

> Call Darvi Group — +91 99868 90777 — darvigroup@gmail.com  
> (text only; not WhatsApp `phone` CTA)

---

## 3. Graph (intended happy path)

```text
Start
  → supabase_lead_t1
  → intro_message
  → main_menu
       ├─ Registration Hub → registration_details → registration_form_buttons
       │                         ├─ Main Menu → main_menu
       │                         └─ Contact Us → contact_us path
       │
       ├─ Service Query → services_overview → services_buttons
       │                    ├─ Sandalwood → sandalwood_service → service_followup
       │                    ├─ IoT        → iot_service        → service_followup
       │                    └─ Consultancy → consultancy_service → service_followup
       │                         service_followup → Main Menu | contact us
       │
       ├─ Ask Darvi AI → darvi_ai_intro → query_ai → supabase_lead_t3
       │                    → darvi_ai_api → ai_followup
       │                         ├─ Call Us    → call_us_message → call_us_followup
       │                         ├─ Ask Again  → darvi_ai_intro
       │                         ├─ Main Menu  → main_menu
       │                         └─ Contact Us → contact_us path
       │
       └─ Contact Us → contact_us → mailcheck → supabase_lead_t2
                          → thank_you → contact_followup → Main Menu
```

### Readable edge list

| From | Handle | To |
|------|--------|-----|
| Start | default | supabase_lead_t1 |
| supabase_lead_t1 | default | intro_message |
| intro_message | default | main_menu |
| main_menu | button:register | registration_details |
| main_menu | button:service | services_overview |
| main_menu | button:darviai | darvi_ai_intro |
| main_menu | button:contact | contact_us |
| registration_details | default | registration_form_buttons |
| registration_form_buttons | button:main_menu | main_menu |
| registration_form_buttons | button:contact_us | contact_us |
| services_overview | default | services_buttons |
| services_buttons | button:…_0 | sandalwood_service |
| services_buttons | button:…_1 | iot_service |
| services_buttons | button:…_2 | consultancy_service |
| sandalwood / iot / consultancy | default | service_followup |
| service_followup | Main Menu | main_menu |
| service_followup | contact us | contact_us |
| contact_us | default | mailcheck |
| mailcheck | default | supabase_lead_t2 |
| supabase_lead_t2 | default | thank_you |
| thank_you | default | contact_followup |
| contact_followup | button:main_menu | main_menu |
| darvi_ai_intro | default | query_ai |
| query_ai | default | supabase_lead_t3 |
| supabase_lead_t3 | default | darvi_ai_api |
| darvi_ai_api | default | ai_followup |
| ai_followup | button:call_us | call_us_message |
| ai_followup | button:ask_again | darvi_ai_intro |
| ai_followup | button:main_menu | main_menu |
| ai_followup | button:contact | contact_us |
| call_us_message | default | call_us_followup |
| call_us_followup | ask_again / main_menu / contact_us | darvi_ai_intro / main_menu / contact_us |

**Important:** In this export, the **Call Us + number path only exists under Ask Darvi AI → ai_followup**.  
Registration / Services / Contact content paths never go through `call_us_message`.

---

## 4. Button counts (WhatsApp UX)

Engine converts **>3 reply buttons** into a **list** interactive message.

| Node | Count | Titles | Note |
|------|-------|--------|------|
| main_menu | **4** | Registration Hub, Service Query, Ask Darvi AI, Contact Us | Sent as list if >3 |
| services_buttons | 3 | Sandalwood, IoT, Consultancy | Reply buttons OK |
| service_followup | 2 | Main Menu, contact us | OK |
| registration_form_buttons | 2 | Main Menu, Contact Us | OK |
| ai_followup | **4** | Call Us, Ask Again, Main Menu, Contact Us | Sent as list if >3 |
| contact_followup | 1 | Main Menu | OK |
| call_us_followup | 3 | Ask Again, Main Menu, Contact Us | OK |

WhatsApp **phone CTA** is supported by the app (`type: "phone"` → `tel:`), but **this flow uses plain text** for the number, not a phone button.

---

## 5. Root cause: failure message with no buttons

### Exact user-facing string

> I could not find that in our documents. Please try rephrasing your question, or choose Contact Us / Contact Expert from the menu.

### Where it lives

**Not** in `test2name_flow_export.json`.

Hardcoded engine constant in `internal/handlers/chatbot_processor.go`:

```go
const ragUserFacingFallback = "I could not find that in our documents. Please try rephrasing your question, or choose Contact Us / Contact Expert from the menu."
```

Used when org free-text / RAG mode has no usable answer and no local LLM fallback (or after local failure following RAG). Prefer `settings.FallbackMessage` when set.

### Why no buttons appear with it

| What happens | Result |
|--------------|--------|
| `generateAIResponse` fails / RAG abstains | Returns that string as **plain text** |
| Message sent via **text**, not a **buttons** node | User sees text only |
| Copy says “choose Contact Us / Contact Expert from the menu” | Those are **not** WhatsApp buttons on this message |
| Flow nodes `ai_followup` / `call_us_*` are **not** run | Graph never advanced for this path |
| “Contact Expert” | **Does not exist** in this flow |

So this path is **engine free-text AI / buttons AI breakout**, not the designed flow path after `darvi_ai_api`.

---

## 6. Why AI interferes with the button flow

When the user is **parked on a buttons node** (main menu, service followup, ai_followup, etc.) and types free text instead of tapping a button, the processor roughly does:

1. Try another flow trigger  
2. Try keyword breakout  
3. **AI fallback** → `generateAIResponse`  
4. On RAG miss → send **`ragUserFacingFallback`** as text  
5. Optionally re-send **settings greeting menu** (org chatbot settings — not necessarily this flow’s `main_menu` / Call Us)

Relevant processor behavior (conceptual):

- Active flow wins while `current_flow_id` is set  
- Free text at **buttons** → breakout ladder (other flow / keyword / **AI**)  
- AI path is **global RAG/settings**, not the flow’s Railway `darvi_ai_api` node  
- After AI text, greeting may be re-sent from settings; Call Us graph path is skipped  

### Conflict summary

| User intent | What should run | What often runs instead |
|-------------|-----------------|-------------------------|
| Tap menu buttons | Graph edges only | — |
| Free text while on buttons | Stay in flow / re-prompt buttons | **Global RAG AI** |
| Ask Darvi AI path | `query_ai` → `darvi_ai_api` → `ai_followup` | Free-text AI if not on the prompt step |
| RAG “no answer” | Flow buttons with Call Us | Engine plain-text fallback, **no Call Us button** |

**One-line diagnosis:** Button flow is correct for taps; free-text + org RAG steals the turn, emits a hardcoded no-docs message as text-only, and never runs the flow’s Call Us button nodes.

---

## 7. Two RAG systems (important)

| System | Where | On failure |
|--------|--------|------------|
| **Flow RAG** | `darvi_ai_api` → Railway `/chat` | Still advances `default` → `ai_followup` (buttons exist in graph) |
| **Engine RAG** | Free-text / buttons breakout via `generateAIResponse` | Plain `ragUserFacingFallback` — **no buttons** |

The exact failure string users reported is from the **engine** path.

### Gaps on flow RAG path

- No dedicated **empty answer** branch after `darvi_ai_api`  
- Edges only use **default** (export does not wire `http:non2xx` for a fail UI)  
- `ai_followup` has 4 options (list conversion)  
- Call Us is **message text**, not phone CTA  

---

## 8. Plan: which nodes to add, where, and how

**Goal:** After every failure / no-help content, show **Call Us** with the number (prefer phone CTA), without AI hijacking the button session.

### Phase 1 — Flow design (no engine code)

#### 1) Shared “failure + call” cluster (reuse)

| New node | Type | Purpose |
|----------|------|---------|
| `rag_or_help_fail_message` *(optional)* | `message` | Short failure copy (or put copy only in buttons body). |
| `fail_call_us_buttons` | `buttons` | Body = failure text + “tap Call Us”. Keep **≤3** buttons: **Call Us** · **Main Menu** · **Ask Again** (AI branch only). |

**Preferred Call Us button config (when implementing):**

| Field | Value |
|-------|--------|
| `id` | `call_us` |
| `title` | `Call Us` |
| `type` | `phone` |
| `phone_number` | `+919986890777` |

Then edge: **Call Us → existing `call_us_message`** (or skip text if CTA is enough) → **`call_us_followup`**.

#### 2) Where to attach

| Priority | Location | Insert after | Why |
|----------|----------|--------------|-----|
| **P0** | AI path failure (in-flow) | After `darvi_ai_api` on empty / non-2xx | Stay inside designed bot with Call Us |
| **P1** | After AI success follow-up | Keep `ai_followup`, trim to ≤3, Call Us = phone | Already planned Call Us here |
| **P2** | Service dead-ends | `service_followup` (or each service message) | Content paths currently have no Call Us |
| **P3** | Registration | `registration_form_buttons` | Optional Call Us |
| **P4** | Contact | After `contact_us` | Already has number in text; optional CTA |

#### 3) Recommended graph for AI branch (P0)

```text
darvi_ai_api
  ├─ success (has answer) → (answer already sent via template) → ai_followup
  │                              buttons ≤3: Call Us | Ask Again | Main Menu
  │                              Call Us → call_us_message → call_us_followup
  │
  └─ fail / empty        → fail_call_us_buttons
                              ├─ Call Us   → call_us_message → call_us_followup
                              ├─ Ask Again → darvi_ai_intro
                              └─ Main Menu → main_menu
```

**Note:** Flow-only work does **not** wrap the engine string `ragUserFacingFallback`. That needs Phase 2.

### Phase 2 — Engine / settings (required for the exact failure message)

| Change | Effect |
|--------|--------|
| On buttons wait: do not free-text AI (or only after “Ask Darvi AI”) | Stops AI hijacking menus |
| When sending `ragUserFacingFallback`, also send interactive Call Us + Main Menu | Matches expectation for that exact line |
| Or: free-text AI off while `current_flow_id` is set | Button flow always wins |
| Align `FallbackMessage` / greeting buttons with this flow’s Call Us | Greeting after AI is useful and consistent |
| Remove “Contact Expert” from copy unless that button exists | Copy matches UI |

### Phase 3 — Cleanup of existing nodes

| Item | Recommendation |
|------|----------------|
| `ai_followup` | Drop to **3** buttons: Call Us · Ask Again · Main Menu |
| `main_menu` | Prefer 3 buttons or intentional list UX |
| `call_us_message` | Keep number text; pair with **phone** CTA if required |
| Fallback copy | Drop “Contact Expert” unless added as a real option |

---

## 9. FAQ: “this msg triggers, no btn in chat”

| Question | Answer |
|----------|--------|
| Is that string from the flow? | **No** |
| Which system? | **Engine RAG free-text fallback** |
| Why no buttons? | Sent as **plain text**; graph never reaches `ai_followup` / `call_us_*` |
| Why AI mid button flow? | Free text on an active **buttons** node → **AI breakout** |
| Can only adding flow nodes fix *this* string? | **No** — need engine behavior or disable free-text AI during active flow |
| Can flow nodes fix in-flow AI failure UX? | **Yes** — branch after `darvi_ai_api` + shared Call Us buttons |

---

## 10. Engine fix applied (button flow vs free-text AI)

**Problem (not a flow-graph authoring bug):** While a session was parked on a **buttons** node, free text triggered **org RAG free-text AI** (`Active buttons node fallback to AI`). That sent plain-text fallbacks such as `ragUserFacingFallback` and often left flow state inconsistent — so the next interaction felt random (sometimes button click advances, sometimes AI answers).

**Code change:** `internal/handlers/chatbot_processor.go`

| Before | After |
|--------|--------|
| Free text on active buttons → AI + greeting menu | Free text on active buttons → **re-send the same flow menu** (AI suppressed) |
| Title match only if interactive flags on | **Always** resolve typed button titles/ids against live button config |
| Native button clicks | Unchanged — still advance the graph via `buttonID` |
| Free-chat AI when **no** active flow | Unchanged — AI still runs outside a flow |
| Ask Darvi AI inside flow (`prompt` → `api_call`) | Unchanged — intentional AI path |

**Log to confirm fix in production:**  
`Active buttons node free-text ignored; re-sending menu (AI suppressed)`  
(old line `Active buttons node fallback to AI` should no longer appear)

### Suggested later (optional product work)

1. Branch after `darvi_ai_api` for empty answers + Call Us phone CTA (flow UX polish).  
2. Attach buttons to engine `ragUserFacingFallback` when free-chat AI runs **outside** a flow.  
3. Trim menus to ≤3 reply buttons if list UX is unwanted.

---

## 11. File reference

| File | Role |
|------|------|
| `test2name_flow_export.json` | Exported graph analyzed above |
| `internal/handlers/chatbot_processor.go` | Free-text AI, buttons breakout, `ragUserFacingFallback` |
| `internal/handlers/chatbot_graph_runner.go` | `api_call`, buttons, prompts, optional `ai_response` node |
| `CHATBOT_ENGINE_ANALYSIS.md` | Broader engine conflict notes |

---

## 12. Security note (export hygiene)

The flow export includes **live API keys / JWTs** in `api_call` headers (Supabase anon key, Railway `X-API-Key`). Treat the JSON as sensitive; rotate if it was shared widely. Do not commit secrets into public docs beyond what already exists in the export file.
