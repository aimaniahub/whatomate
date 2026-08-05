# Daily Reports — Production Issues Plan

**Status:** Planning (no code changes in this step)  
**Date:** 2026-07-31  
**Problems reported**

1. **Admin WhatsApp send fails** because of Meta’s **24-hour messaging window**.  
2. **DOCX is empty** even though there were real chats that day.

This document explains *why*, lists options, and proposes a phased fix plan.

---

## Problem A — 24-hour WhatsApp window (admin cannot receive report)

### What Meta enforces

For the **Cloud API**, free-form messages (text, **session document**, buttons, etc.) can only be sent to a user if that user messaged your business number within the last **24 hours** (the “customer service window”).

| Send type | Outside 24h window? |
|-----------|---------------------|
| Free-form text / document / media | **Blocked** (error ~131047 / similar) |
| **Approved template** message | **Allowed** (utility / marketing / auth per category) |
| Template with **DOCUMENT header** | Allowed — attach the report as header media |

Daily report delivery currently uses:

```text
MessageTypeDocument  →  free-form document send
```

Recipients are **admins/employees** (configured numbers). If they have **not** messaged the business WhatsApp in the last 24 hours, Meta rejects the send. That is expected platform policy, not a random bug.

### Why “just send the file” fails

- Report numbers are **cold or idle** contacts from Meta’s point of view.  
- Creating a contact in Whatomate DB does **not** open a WhatsApp session.  
- Only an **inbound** message from that phone to your WABA phone opens the 24h window.

### Ways to overcome (options)

| Option | How it works | Pros | Cons | Recommendation |
|--------|--------------|------|------|----------------|
| **A1. Utility template + DOCUMENT header** | Approve a template e.g. `daily_chat_report` with DOCUMENT header + body vars (date, chat_count). Upload DOCX, send as template header media. | Works outside 24h; production-correct for Meta | Needs Meta template approval; body copy fixed; DOCUMENT filename rules | **v1 production path** |
| **A2. Keep session open** | Admins message the bot daily (e.g. “ready”) before schedule | No template | Fragile; easy to miss; not ops-friendly | Temporary only |
| **A3. Email / S3 / dashboard only** | Generate DOCX, store, notify via email or “download in app” | Avoids Meta window | Not WhatsApp delivery | Good **fallback** |
| **A4. Marketing template** | Same as A1 but MARKETING category | Works cold | Stricter approval, opt-out rules | Only if utility rejected |
| **A5. Link in template body** | Utility template with URL to download report | Simple if public/signed URL exists | Needs public storage + auth link design | v2 if no DOCUMENT template |

### Recommended product design (send)

```text
Generate DOCX
  → Upload media to Meta
  → Prefer: send APPROVED template (DOCUMENT header) to each recipient
  → If no template configured: try free-form document (only works inside 24h)
  → Always keep downloadable file in Daily Reports history (UI)
  → Persist send_errors with clear Meta code + “outside 24h / use template”
```

**Config to add on Daily Reports setup**

| Field | Purpose |
|-------|---------|
| `report_template_name` | Approved Meta template name (e.g. `daily_chat_report`) |
| `report_template_language` | e.g. `en` / `en_US` |
| Template body params map | e.g. `{{1}}` = report date, `{{2}}` = chat count |
| Optional: `prefer_template` bool (default true when name set) |

**Template sketch (utility)**

- Category: **UTILITY**  
- Header: **DOCUMENT**  
- Body: e.g. `Daily chat report for {{1}}. Chats: {{2}}. Open the attached document.`  
- No need for marketing opt-in if utility is approved correctly.

**Fallback chain**

1. Template + document header (if configured + approved).  
2. Else free-form document (if last_inbound within 24h).  
3. Else mark run `completed` with file saved, `sent_count=0`, `send_errors` explain window; UI still allows download.

**UI**

- Banner when template not configured: “WhatsApp free-form sends need a 24h window. Configure a DOCUMENT utility template for reliable admin delivery.”  
- Show per-recipient send result (success / 24h / Meta error).

---

## Problem B — DOCX empty even when chats exist

### Current pipeline

```text
collectDailyChats(org, whatsapp_account filter, reportDate, timezone)
  → if 0 chats: empty DOCX ("No chats today")
  → else: AI summarize by serial id → merge name/phone → BuildDOCX → send
```

Source of truth today: **`messages` table** joined to **`contacts`**, filtered by:

- `organization_id`  
- `created_at` in **[local midnight, next midnight)** converted to UTC  
- optional **`messages.whats_app_account = settings.whatsapp_account`**  
- non-deleted message + contact  

### Likely root causes (ordered by probability)

| # | Cause | Symptom | How to confirm |
|---|--------|---------|----------------|
| **B1** | **WhatsApp account filter too strict** | Settings “sender account” also filters *inbound* chats. If chats are on account A but filter is B (or wrong name), collect returns 0. | Logs: `chats=0` with messages existing for other account; compare `settings.whatsapp_account` vs `messages.whats_app_account` |
| **B2** | **Timezone / report date mismatch** | Day bounds use org timezone. Server UTC + wrong timezone → “today’s” chats fall outside range. | Logs start/end UTC vs message timestamps; UI date picker vs schedule date |
| **B3** | **Empty `messages.content`** | Button/list/media/template rows often have empty content → AI gets only `[non-text message]` → weak/empty bullets. Still should list names/phones unless collect is 0. | Count messages with content vs empty for that day |
| **B4** | **AI returns empty / bad JSON** | Collect has chats; AI fails or returns empty `items` → merge may leave thin summaries; if job fails entirely user sees error not empty doc. Empty **table** usually means collect=0 or empty-day path. | Logs: `AI summarize complete` rows=N vs empty day path |
| **B5** | **Chats only in chatbot_session_messages** | UI “bot flow” logs not mirrored fully into `messages` (or only partial). | Compare session messages vs messages for contact/day |
| **B6** | **Soft-deleted contacts** | Join drops messages when contact soft-deleted. | Query messages without join |
| **B7** | **Wrong report_date on schedule** | Schedule marks empty for “today” while user looks at different day in history. | Check run `report_date`, `chat_count`, `message_count` columns |

### Diagnostic fields already useful

On each run (`daily_report_runs`):

- `chat_count`, `message_count`  
- `status` (`empty` vs `completed`)  
- `error_message`, `summary_json`  
- logs: `Daily report: collecting chats` / `empty day` / `starting AI summarize`

**If `chat_count=0`** → collection/filter/timezone problem (B1–B2, B5–B7).  
**If `chat_count>0` but DOCX looks blank** → merge/render/AI bullets (B3–B4) or user opened wrong file.

### Recommended fixes for empty data

#### Phase B — Collection (must-fix)

1. **Decouple send account vs chat source account**  
   - `whatsapp_account` = account used to **send** the report.  
   - New optional `source_whatsapp_account` / multi-select / **“all accounts”** (default **all org messages**).  
   - **Default change:** do **not** filter by send account unless user opts in.

2. **Log collect diagnostics**  
   - total messages in day (no account filter)  
   - count with account filter  
   - distinct contacts  
   - empty-content count  
   - start/end UTC + timezone  

3. **Enrich empty content**  
   - From `interactive_data`, `template_name`, `message_type`, media caption.  
   - Prefer inbound text; include last N outbound for context if needed.

4. **Secondary source (optional)**  
   - Union `chatbot_session_messages` for sessions active that day if `messages` sparse.

5. **Pre-DOCX validation**  
   - If `len(chats)>0` but all summaries empty after AI, **fallback rule-based bullets** from inbound text so DOCX never has blank name/phone rows.  
   - Never ship empty table when collect returned contacts.

6. **UI run detail**  
   - Show `chat_count` / `message_count` on history.  
   - On empty: “0 contacts matched filters (account=X, tz=Y, date=Z)” not just “No chats today”.

#### Phase B — AI merge safety

7. If AI JSON missing ids, **always** emit rows for every serial from collect (name/phone from DB, bullets from fallback).  
8. Cap/retry AI; on parse failure use full rule-based summarize (or fail run with error — prefer partial filled DOCX for ops).

---

## Combined architecture (target)

```text
                    ┌─────────────────────────────┐
                    │ Daily Reports settings      │
                    │ - schedule / recipients     │
                    │ - AI                        │
                    │ - send WA account           │
                    │ - source accounts (all/…)   │
                    │ - utility template name     │
                    └─────────────┬───────────────┘
                                  │
         collect (all/source) ────┤
                                  ▼
                         AI + fallback merge
                                  ▼
                            Build DOCX
                                  ▼
              ┌───────────────────┴───────────────────┐
              │                                       │
     Store file + run row                    Delivery
     (always downloadable)                   │
              │                              ├─ Template DOCUMENT (preferred)
              │                              ├─ Free-form doc (if 24h open)
              │                              └─ Record send_errors
              ▼
     History UI shows status / download
```

---

## Implementation phases (suggested)

### Phase 0 — Confirm (1 short check)

On a failed empty run, inspect `daily_report_runs`:

- `chat_count`, `message_count`, `status`, `send_errors`  
- Settings: `whatsapp_account`, `timezone`, `send_time`, `enabled`  
- SQL: messages that day for org with/without account filter  

### Phase 1 — Empty data fixes (P0)

- Default collect = **all accounts** in org for that day.  
- Collect diagnostics in logs + optional `summary_json.diagnostics`.  
- Guarantee DOCX rows when collect > 0 (AI fail → rule-based).  
- History shows chat counts + clearer empty reason.  

### Phase 2 — 24h delivery (P0 for prod send)

- Settings: template name + language.  
- Send path: template DOCUMENT → else free-form → else save-only.  
- Clear Meta errors in UI.  
- Docs: how to create utility template in Meta Business Manager.  

### Phase 3 — Hardening (P1)

- Per-recipient delivery log table.  
- Signed download link in template body (optional).  
- Email notify optional.  
- Content enrichment from interactive/media.  

### Phase 4 — QA checklist

- [ ] Day with known N chats → DOCX has N rows, names/phones filled.  
- [ ] Wrong account filter no longer zeros report by default.  
- [ ] Admin never messaged bot → template send succeeds.  
- [ ] Admin messaged bot recently → free-form works if no template.  
- [ ] Empty day still produces “No chats” DOCX without send spam (or template with 0 chats).  
- [ ] Schedule fires once/day; run row status sticky.  

---

## Decisions to lock before coding

1. **Default chat source:** all WhatsApp accounts in org, or keep filter? *(Recommend: all accounts.)*  
2. **Delivery:** require DOCUMENT utility template for prod, or allow save-only + download? *(Recommend: template preferred + always UI download.)*  
3. **Empty day send:** still send “no chats” DOCX via template, or skip send? *(Current product: send no-chats doc; keep unless you want skip.)*  
4. **AI failure:** fail whole run vs rule-based DOCX? *(Recommend: rule-based DOCX so ops still get list of contacts.)*  

---

## Summary

| Issue | Core reason | Fix direction |
|-------|-------------|----------------|
| Admin not receiving | Free-form document outside Meta **24h session** | **Utility template with DOCUMENT header** (+ always store download in app) |
| Empty DOCX with chats | Collect/filter/timezone or empty content + empty-day path | **All-accounts collect**, diagnostics, **non-empty rows** even if AI weak |

No code was changed in this step — this is the plan for the next implementation pass after you confirm the decisions above (especially default account filter + template delivery).
