# Daily Chat Reports — Design & Implementation Plan

**Status:** Implemented (v1)  
**Date:** 2026-07-31  
**Scope:** End-of-day AI analysis of all chats → structured PDF → WhatsApp delivery to configured recipients  
**UI home:** Analytics → **Daily Reports**

### Frozen product decisions

| # | Decision |
|---|----------|
| 1 | **All contacts** with message activity that calendar day |
| 2 | **Max 2 recipients** (admin + employee) |
| 3 | Default timezone **Asia/Kolkata** |
| 4 | Empty day → still generate & send **“No chats today”** PDF |
| 5 | Both **Run now** and **scheduled send** |

---

## 1. Product goal

At the end of each day, Whatomate should:

1. Collect **all conversations that had activity that day** (example: 5 people chatted → 5 chat threads).
2. Use **AI** to turn each thread into a clean structured summary.
3. Build a **PDF report** for the day.
4. **Send the PDF** over WhatsApp to one or more configured recipients.
5. Let admins configure this from a new **Analytics → Daily Reports** page (recipients + send time + enable/disable).

### Intended report content (per contact / chat)

| Field | Description |
|--------|-------------|
| **Name** | Contact profile name (WhatsApp name / CRM name) |
| **Number** | Phone number (E.164 when available) |
| **Overall query summary** | AI short summary of what they wanted / discussed |
| **Call** | Call affordance: clickable `tel:` number in PDF, and/or “Call interest: Yes/No” + recommended next action |

Optional extras (discuss): tags, assigned agent, bot vs human, lead type, last message time, message count, sentiment, priority.

---

## 2. User story (happy path)

1. Admin opens **Analytics → Daily Reports**.
2. Clicks **Setup** / Configure:
   - Enable daily reports
   - Timezone (e.g. `Asia/Kolkata`)
   - Send time (e.g. `20:00`)
   - WhatsApp account used to **send** the PDF
   - Recipients: name + WhatsApp number (1 or many)
3. During the day, 5 customers message the business.
4. At 20:00 local time, a background job:
   - Loads today’s messages grouped by contact
   - Calls AI to summarize each chat + day overview
   - Generates `daily-report-YYYY-MM-DD.pdf`
   - Sends document via WhatsApp to each recipient
5. Admin can open the page later, see **run history** (success / failed / empty day), download PDF, **Run now** for a date.

---

## 3. What already exists in this project (reuse)

| Capability | Where | Use for daily reports |
|------------|--------|------------------------|
| Conversation messages | `messages` + `contacts` | Primary source for “today’s chats” |
| Contact name / phone | `contacts.profile_name`, `phone_number` | Report columns |
| Chatbot session logs | `chatbot_sessions` / `chatbot_session_messages` | Optional enrichment (flow path, bot turns) |
| Org AI config | Chatbot settings (provider, model, key, free-text mode) | Structure + clean summaries via LLM |
| WhatsApp document send | `pkg/whatsapp.SendDocumentMessage`, `SendOutgoingMessage` | Deliver PDF |
| Background loops | SLA processor, session sweeper in `cmd/whatomate/main.go` | Same pattern for daily scheduler |
| Analytics UI shell | `frontend/src/views/analytics/*`, nav section | New tab/page |
| Permissions model | `analytics`, `analytics.agents` | New permission or reuse `analytics` |

**Gaps today (must build):**

- No daily report config tables / APIs
- No PDF generation library in-repo for reports
- No scheduled “per-org daily job” for reports
- No Analytics “Daily Reports” page

---

## 4. Proposed architecture

```text
┌─────────────────────────────────────────────────────────────────┐
│  Analytics → Daily Reports (Vue)                                │
│  - Setup (recipients, time, timezone, WA account, enable)       │
│  - History list + download + Run now                            │
└────────────────────────────┬────────────────────────────────────┘
                             │ REST
┌────────────────────────────▼────────────────────────────────────┐
│  API handlers (daily_reports.go)                                │
│  GET/PUT settings · GET runs · POST run-now · GET download      │
└────────────────────────────┬────────────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────────────┐
│  DailyReportScheduler (ticker, similar to SLA processor)        │
│  Every N minutes: for each enabled org setting, if local time   │
│  crossed send_time and not yet run for that calendar day → job  │
└────────────────────────────┬────────────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────────────┐
│  DailyReportJob                                                 │
│  1. Collect chats (DB)                                          │
│  2. AI summarize (structured JSON)                              │
│  3. Render PDF                                                  │
│  4. Store artifact (disk/S3) + run row                          │
│  5. WhatsApp document to each recipient                         │
└─────────────────────────────────────────────────────────────────┘
```

### Data flow (one day, one org)

```text
messages (today, org)
  → group by contact_id
  → [{ name, phone, transcript[] }, ...]
  → AI: return structured ReportJSON
  → PDF builder
  → file storage
  → WhatsApp document(s)
  → daily_report_runs row (status, stats, errors)
```

---

## 5. Data model (proposed)

### 5.1 `daily_report_settings` (one row per org, or per WA account — decide)

| Column | Type | Notes |
|--------|------|--------|
| `id` | UUID | PK |
| `organization_id` | UUID | indexed, unique (or unique with account) |
| `whatsapp_account` | string | Account used to **send** the PDF |
| `enabled` | bool | Master switch |
| `timezone` | string | IANA, e.g. `Asia/Kolkata` |
| `send_time` | string | `HH:MM` local (24h) |
| `ai_provider_mode` | string | `use_chatbot_settings` (default) / override later |
| `include_bot_only` | bool | optional filter |
| `min_messages` | int | skip contacts with fewer than N msgs (default 1) |
| `report_language` | string | e.g. `en` |
| `created_at` / `updated_at` | timestamps | |

### 5.2 `daily_report_recipients`

| Column | Type | Notes |
|--------|------|--------|
| `id` | UUID | |
| `organization_id` | UUID | |
| `settings_id` | UUID | FK |
| `name` | string | Display label (who receives the report) |
| `phone_number` | string | WhatsApp number to send PDF to |
| `is_active` | bool | |

### 5.3 `daily_report_runs`

| Column | Type | Notes |
|--------|------|--------|
| `id` | UUID | |
| `organization_id` | UUID | |
| `report_date` | date | Calendar day in org timezone |
| `status` | string | `pending`, `running`, `completed`, `failed`, `empty` |
| `chat_count` | int | Distinct contacts summarized |
| `message_count` | int | Raw messages included |
| `pdf_path` / `pdf_storage_key` | string | Where PDF lives |
| `pdf_filename` | string | e.g. `daily-report-2026-07-31.pdf` |
| `ai_model` | string | Audit |
| `error_message` | text | If failed |
| `started_at` / `finished_at` | timestamps | |
| `triggered_by` | string | `schedule` \| `manual` |
| unique | `(organization_id, report_date)` | Prevent double send for same day |

### 5.4 Optional `daily_report_deliveries`

Per recipient send status (delivered / failed / WA message id) for debugging multi-recipient setups.

---

## 6. Chat collection rules (propose default)

**Include a contact in “today’s chats” if** they have **≥ 1 message** (inbound or both directions) with `created_at` in the org’s local calendar day `[00:00, 24:00)`.

**Per contact payload for AI:**

```json
{
  "contact_id": "...",
  "name": "Ravi",
  "phone": "+9198xxxxxxxx",
  "message_count": 12,
  "first_message_at": "...",
  "last_message_at": "...",
  "messages": [
    { "direction": "incoming", "at": "...", "text": "..." },
    { "direction": "outgoing", "at": "...", "text": "..." }
  ]
}
```

**Truncation / token budget:**

- Cap messages per contact (e.g. last 40 or max ~8k chars).
- Cap total contacts per run (e.g. 200); if more, summarize in batches then merge overview.
- Strip media blobs; keep captions / “[image]” placeholders.

**Open decision:** count only **inbound-started** threads vs any activity. Recommendation: **any activity** that day.

---

## 7. AI summarization design

### 7.1 Why AI here

- Clean language, consistent structure
- Merge scattered Q&A into one “overall query”
- Detect intent (pricing, registration, call request, complaint, etc.)

### 7.2 Provider

**v1:** Reuse org **chatbot AI settings** (OpenRouter / OpenAI / etc. already configured).  
If AI is not configured → run fails with clear error, or fall back to rule-based stub summaries (discuss).

### 7.3 Structured output (contract)

Ask the model to return **JSON only** (no markdown), e.g.:

```json
{
  "report_date": "2026-07-31",
  "overview": {
    "total_chats": 5,
    "top_intents": ["pricing", "registration", "iot"],
    "needs_callback": 2,
    "notes": "Short day-level narrative"
  },
  "chats": [
    {
      "name": "Ravi",
      "phone": "+9198xxxxxxxx",
      "summary": "Asked sandalwood plantation cost and drip irrigation options.",
      "intent": "pricing",
      "call_requested": true,
      "priority": "high",
      "next_action": "Call back with package pricing"
    }
  ]
}
```

### 7.4 “Call btn”

In a **PDF**, true WhatsApp interactive buttons are not available. Options:

| Option | UX | Recommendation |
|--------|-----|----------------|
| **A.** Phone as clickable `tel:+91…` link in PDF | Tap-to-call on mobile PDF viewers | **v1 default** |
| **B.** Column “Call?” Yes/No from AI | Follow-up prioritization | Include with A |
| **C.** After PDF, send interactive message with Call Us CTA | Extra WA message | v2 optional |
| **D.** Generate a separate “callback list” page in PDF | Ops-friendly | Nice-to-have |

**v1 proposal:** PDF table columns = Name | Number (tel link) | Summary | Call requested | Next action.

---

## 8. PDF generation

### 8.1 Library choice (discuss)

| Approach | Pros | Cons |
|----------|------|------|
| **gofpdf / jung-kurt/gofpdf** | Simple tables, no browser | Styling limited |
| **maroto (v2)** | Good tables/layout in Go | Dependency |
| **HTML → PDF (chromedp / wkhtml)** | Beautiful UI | Heavy ops dependency |
| **External service** | Fancy | Latency + cost |

**Recommendation for v1:** pure Go PDF (`maroto` or `gofpdf`) — matches server model, no Chrome dependency.

### 8.2 PDF layout (draft)

1. **Header:** Org name / logo optional, “Daily Chat Report”, date, generated-at  
2. **Day overview:** totals, top intents, callback count  
3. **Table / cards:** one row or block per contact  
4. **Footer:** page numbers, “Generated by Whatomate”

Filename: `daily-chat-report-{orgSlug}-{YYYY-MM-DD}.pdf`

### 8.3 Storage

- Local uploads dir and/or S3 (project already has S3 helpers for media)
- Store path on `daily_report_runs` for download from UI

---

## 9. WhatsApp delivery

1. Resolve configured **WhatsApp account** (token, phone number id).
2. Upload PDF media to Meta (or use existing media upload path used by document sends).
3. `SendDocumentMessage` to each **recipient phone** with caption e.g.  
   `Daily chat report — 31 Jul 2026 (5 chats)`.
4. Record delivery status per recipient.

**Constraints to handle:**

- Recipient must be reachable on WhatsApp (valid number).
- Outside 24h customer care window → **document may require a template** with DOCUMENT header for cold outreach.  
  - If recipients are **internal staff** who message the business first each day, session messages work.  
  - If cold send: need **approved utility template** with document header (document this in Setup UI).
- Large PDF size limits (Meta media limits).

**Open decision:** v1 requires staff to have messaged recently, **or** require a configured template name for document send. Prefer: try session document first; if Meta rejects, surface clear error + template option in v2.

---

## 10. Scheduler design

Mirror **SLA processor** pattern:

- Background goroutine in `main.go` when API server starts.
- Tick every **1 minute** (or 5).
- For each enabled `daily_report_settings`:
  - Compute “now” in `timezone`.
  - If local `HH:MM >= send_time` and no successful/empty run for **today’s report_date**, enqueue/run job.
- Idempotent via unique `(organization_id, report_date)`.
- Catch-up: if server was down at 20:00, still run once when it comes up same day (optional window until midnight).

**Manual “Run now”:** creates run for selected date (default today) with `triggered_by=manual`; can re-run failed day.

---

## 11. API surface (draft)

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/daily-reports/settings` | Load config + recipients |
| `PUT` | `/api/daily-reports/settings` | Save enable, time, timezone, account, recipients |
| `GET` | `/api/daily-reports/runs` | Paginated history |
| `GET` | `/api/daily-reports/runs/:id` | Run detail |
| `GET` | `/api/daily-reports/runs/:id/download` | PDF download |
| `POST` | `/api/daily-reports/run` | Body: `{ "date": "YYYY-MM-DD" }` optional — generate + send now |
| `POST` | `/api/daily-reports/runs/:id/resend` | Resend PDF to recipients |

Permission: **`analytics` read** for list/download; **`analytics` write** for setup + run now  
(or new resource `analytics.daily_reports` if you want finer RBAC — discuss).

---

## 12. Frontend — Analytics → Daily Reports

### 12.1 Navigation

In `navigation.ts` under **Analytics**:

- Agent Analytics  
- Meta Insights  
- **Daily Reports** → `/analytics/daily-reports`

Route + i18n keys + permission gate.

### 12.2 Page sections

**A. Header**  
Title: Daily Reports · subtitle: AI end-of-day chat summaries as PDF  

**B. Setup card (configure)**

- Toggle: Enabled  
- Timezone select / text  
- Send time (`time` input)  
- WhatsApp account select (existing accounts)  
- Recipients table: Name | Number | Active | remove  
- Add recipient button  
- Save  

**C. Actions**

- **Run now** (confirm dialog)  
- Optional date picker for backfill  

**D. History table**

| Date | Chats | Status | Trigger | Finished | Actions |
|------|-------|--------|---------|----------|---------|
| 2026-07-31 | 5 | completed | schedule | 20:01 | Download · Resend |

**E. Empty / error states**

- AI not configured  
- No recipients  
- No chats that day  
- Send failed (show Meta error)

### 12.3 UX notes

- Validate phone numbers (country code required).  
- Show next scheduled run preview: “Next send: today 20:00 IST”.  
- Do not expose AI API keys on this page (reuse chatbot settings; link to Settings → Chatbot).

---

## 13. Implementation phases (recommended)

### Phase 0 — Align product decisions (this doc)

Discuss open questions in §16; freeze v1 scope.

### Phase 1 — Backend skeleton

- Models + migration  
- Settings / recipients CRUD API  
- Run table + list/download stubs  

### Phase 2 — Collect + AI + PDF

- Chat aggregation query  
- AI JSON summarizer with retries / schema validation  
- PDF render + store  
- Manual **Run now** (download in UI first, send optional)

### Phase 3 — WhatsApp send + scheduler

- Document send to recipients  
- Daily ticker + idempotency  
- Delivery audit  

### Phase 4 — Frontend page

- Daily Reports view + nav + i18n  
- Setup form + history + download + run now  

### Phase 5 — Hardening

- Tests (aggregation, timezone, idempotent schedule, AI JSON parse)  
- Limits, redaction of secrets in logs  
- Large-day batching  

**Suggested first shippable slice:** Phase 1–2 + UI setup/history + manual run (PDF download). Scheduler + WA send right after.

---

## 14. File / package map (proposed)

| Area | Path |
|------|------|
| Models | `internal/models/daily_report.go` |
| Handlers | `internal/handlers/daily_reports.go` |
| Job / service | `internal/handlers/daily_report_job.go` or `internal/dailyreport/` |
| Scheduler | `internal/handlers/daily_report_scheduler.go` |
| PDF | `internal/dailyreport/pdf.go` |
| AI prompt | `internal/dailyreport/summarize.go` |
| Tests | `*_test.go` beside packages |
| Frontend view | `frontend/src/views/analytics/DailyReportsView.vue` |
| API client | `frontend/src/services/api.ts` (or dedicated service) |
| Nav / router | `navigation.ts`, `router/index.ts` |
| i18n | `frontend/src/i18n/locales/*.json` |
| Main wire-up | `cmd/whatomate/main.go` |

---

## 15. Non-goals (v1)

- Real-time streaming reports  
- Multi-language PDF beyond one `report_language`  
- Editing AI summaries in UI before send  
- Email delivery (WhatsApp only first)  
- Customer-facing report (internal ops report only)  
- Changing chatbot free-text / button behavior (separate issue)

---

## 16. Open questions for discussion

Please decide / prefer before coding:

1. **Scope of “chat”:** all `messages` contacts, or only bot sessions, or only assigned agent chats?  
   - *Default proposal:* all contacts with message activity that day.

2. **One settings row per org vs per WhatsApp account?**  
   - *Default:* per org + one send account; filter chats by that account (or all accounts in org).

3. **AI unavailable:** fail the run, or generate non-AI bullet summaries from last inbound message only?

4. **Cold WhatsApp document:** require utility template, or assume internal recipients are in-session?

5. **Multiple recipients:** always PDF to all, or primary + CC later?

6. **Timezone:** single org timezone field (recommended) vs use server local time?

7. **Backfill:** allow “Run for date” for past N days?

8. **PII / retention:** how long to keep PDFs on disk/S3?

9. **Call button meaning:** only `tel:` links, or also AI “wants a callback” flag?

10. **Permission:** reuse `analytics` or new `analytics.daily_reports`?

11. **Cost control:** max chats/day, max tokens, skip weekends?

12. **Include agent name** who handled transfer, if any?

---

## 17. Acceptance criteria (v1)

- [ ] Analytics nav shows **Daily Reports**
- [ ] Admin can save recipients (name + number), send time, timezone, enable flag, send WA account
- [ ] Manual **Run now** produces a PDF with name, number, AI summary, call-oriented fields for each of today’s chats
- [ ] At configured time, job runs once per org per day (no duplicates)
- [ ] PDF is sent to all active recipients as WhatsApp document (or clear error if Meta rejects)
- [ ] History lists runs with download
- [ ] Day with zero chats → status `empty`, no spam PDF (or short “no chats” PDF — decide)
- [ ] Failures logged + visible in UI

---

## 18. Risks

| Risk | Mitigation |
|------|------------|
| AI cost / latency on busy days | Batch + caps; run async; timeouts |
| Hallucinated summaries | Ground prompt on transcript only; short quotes optional |
| Double send after restart | Unique (org, date) + status checks |
| Meta document policy | Document template path; Setup warning |
| Huge transcripts | Truncate + sample |
| Secrets in logs | Never log full transcripts at Info |

---

## 19. Discussion summary (for us)

**You want:** daily AI report of all chats → PDF → WhatsApp, with a setup UI under Analytics.

**Plan:** new Daily Reports module (settings + recipients + runs), AI JSON summarizer, Go PDF, scheduler like SLA, Analytics page for setup/history.

**Nothing implemented yet** — this file is the base for alignment. Once we lock §16 answers, implementation can follow Phase 1 → 5.

---

## 20. Suggested next step in discussion

Reply with preferences on at least:

1. Chat scope (all messages vs bot-only)  
2. Single recipient vs multi  
3. Send time timezone (IST default?)  
4. Empty day: skip vs send “no chats” PDF  
5. v1: **manual run + download first**, or full schedule+WhatsApp in first PR?

Then we can freeze the plan and start Phase 1.
