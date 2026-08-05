# Daily Report — WhatsApp Utility Template Setup

Daily reports are sent only to the **recipients** configured on **Analytics → Daily Reports**.

Meta blocks free-form documents outside the **24-hour** customer care window. Production delivery uses an **approved UTILITY template with a DOCUMENT header**.

---

## 1. Create the template in Meta

1. Open [Meta Business Suite](https://business.facebook.com/) → **WhatsApp Manager** → **Message templates**.
2. **Create template**.
3. Fill:

| Field | Value |
|--------|--------|
| **Name** | `daily_chat_report` (must match Whatomate field) |
| **Category** | **Utility** |
| **Language** | e.g. English (`en` or `en_US` — match Whatomate language field) |
| **Header** | **Document** |
| **Body** | See sample below |
| **Footer** | Optional (e.g. `Whatomate`) |

### Recommended body text

```text
Here is your {{3}} for {{1}}.
Chats today: {{2}}.
Please open the attached document for the full summary.
```

| Variable | Whatomate fills |
|----------|-----------------|
| `{{1}}` | Report date (`YYYY-MM-DD`) |
| `{{2}}` | Chat count (number, `0` for no-chat days) |
| `{{3}}` | Report type label (`Daily chat report` or `Daily chat report (no chats)`) |

4. Submit for review and wait until status is **APPROVED**.

---

## 2. Sync template into Whatomate

1. In Whatomate: **Templates** → sync from Meta for the same WhatsApp account you use for reports.
2. Confirm the template shows:
   - Status: **APPROVED**
   - Header type: **DOCUMENT**
   - Name matches exactly (e.g. `daily_chat_report`)

---

## 3. Configure Daily Reports page

**Analytics → Daily Reports → Setup**

1. **WhatsApp account** — same account that owns the template (also used as chat source).
2. **Template name** — `daily_chat_report`
3. **Language** — same as Meta (e.g. `en`)
4. **Recipients** — max 2 (admin / employee) with full WhatsApp numbers
5. **Enable daily schedule** + time + timezone if needed
6. **AI settings** — enable + API key for summaries
7. **Save setup**

---

## 4. What happens on send

```text
Generate DOCX
  → Upload DOCX to Meta media
  → For each active recipient only:
       Send UTILITY template with DOCUMENT header = report file
       Body params: date, chat_count, report_type
  → If template missing/fails: try free-form document (24h window only)
  → Always keep file downloadable in History
```

- **With chats:** full table report + `{{3}}` = “Daily chat report”  
- **No chats:** still generates DOCX (“No chats today”) and still sends via template (`{{2}}` = `0`)

---

## 5. Checklist if send fails

| Check | |
|--------|--|
| Template **APPROVED** | |
| Header is **DOCUMENT** (not TEXT) | |
| Template **name** matches Daily Reports field | |
| Template synced under the **same WhatsApp account** | |
| Recipients are **active** with correct country code | |
| Run history **send_errors** column | |

Common Meta issues:

- Outside 24h + no template → free-form fails  
- DOCUMENT header without filename → Meta error 132012  
- Wrong language code → template not found  

---

## 6. Security note

Only numbers listed under **Recipients** receive the report. Customers are never bulk-messaged by this job.
