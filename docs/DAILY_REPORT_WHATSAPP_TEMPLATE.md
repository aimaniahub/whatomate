# Daily Report — Simple Utility TEXT Template

## Important (24h window)

| What | Opens free-form session? |
|------|---------------------------|
| Customer messages **you** | Yes (24h window) |
| You send a **template** | Does **not** open free-form for more messages |
| You send free-form document | Only if window already open |

So:

1. **Utility TEXT template** → always notifies admins (even if they never messaged today).  
2. **DOCX file** → always saved in **Daily Reports → History** (download).  
3. Free-form DOCX on WhatsApp → only if that admin already messaged the business within 24h.

---

## Create in Meta Business Manager

**WhatsApp Manager → Message templates → Create**

| Field | Exact value |
|--------|-------------|
| **Category** | **Utility** |
| **Name** | `daily_chat_report` |
| **Language** | English → language code **`en`** (or match Whatomate) |
| **Header** | **Text** |
| **Header content** | `Daily Chat Report` |
| | *(static — do **not** put `{{1}}` in the header unless you know Meta named/positional rules)* |
| **Body** | See below |
| **Footer** | optional, e.g. `Whatomate` |
| **Buttons** | none |

### Body (copy this)

```text
Here is your report for {{1}}.
Total chats: {{2}}.
{{3}}
Open Whatomate → Analytics → Daily Reports to download the full Word file.
```

### Sample values (for Meta review)

| Variable | Sample |
|----------|--------|
| `{{1}}` | `2026-07-31` |
| `{{2}}` | `5` |
| `{{3}}` | `Daily chat report` |

### What Whatomate sends (matches code)

| Body var | Filled by app |
|----------|----------------|
| `{{1}}` | Report date `YYYY-MM-DD` |
| `{{2}}` | Chat count (`0` on no-chat days) |
| `{{3}}` | `Daily chat report` or `Daily chat report (no chats)` |

---

## Whatomate setup

1. **Templates** → Sync for the same WhatsApp account as Daily Reports.  
2. Confirm template **APPROVED**, name `daily_chat_report`.  
3. **Analytics → Daily Reports**:
   - WhatsApp account  
   - Template name: `daily_chat_report`  
   - Language: `en`  
   - Recipients (max 2)  
   - Save  

---

## Optional: header with one variable

If you prefer a dynamic header:

| Header text | Whatomate fills |
|-------------|-----------------|
| `{{1}}` | Report type (`Daily chat report` / `… (no chats)`) |

Meta allows **at most one** variable in a TEXT header.  
Body vars stay `{{1}}` date, `{{2}}` count, `{{3}}` type (header and body variables are separate components).

Recommended: keep header **static** `Daily Chat Report` for simplicity.

---

## Flow in code

```text
For each active recipient on the report page only:
  1. Send APPROVED utility TEXT template (cold OK)
  2. If recipient last_inbound < 24h → also try free-form DOCX
  3. Always keep DOCX in History for download
```

No-chat days still send the template (`{{2}}` = `0`, `{{3}}` = no-chats label).
