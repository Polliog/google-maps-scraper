# Smart Email Pipeline — Design Document

**Date:** 2026-02-15
**Status:** Approved
**Goal:** Improve email extraction reliability and coverage with a 3-level hybrid pipeline.

## Problem

The current `EmailExtractJob` has critical limitations:

- Zero retries — temporary network failures cause silent email loss
- Static HTML only — no JavaScript rendering, missing ~30-40% of modern sites
- Single-page extraction — only visits the homepage, ignores /contact pages
- Incomplete blocklist — typo ("instragram"), case-sensitive, missing social platforms
- No observability — impossible to distinguish "no email exists" from "extraction failed"

## Solution: 3-Level Pipeline

Replace `EmailExtractJob` with an `EmailPipeline` orchestrator that tries 3 extraction levels, stopping as soon as emails are found.

### Level 1: Static HTTP Extraction (Homepage)

- HTTP GET the business website homepage
- 2 retries with exponential backoff (1s, 3s)
- 10s timeout per request
- Apply 3 parsers in order:
  1. **mailto parser**: `<a href="mailto:...">` links, case-insensitive
  2. **text parser**: email patterns in visible text only (excludes `<script>`, `<style>`, HTML comments)
  3. **regex fallback**: scan raw HTML, filter out false positives (`example@`, `noreply@`, etc.)
- If emails found → return results, pipeline stops

### Level 2: Contact Page Crawler

Triggered only if Level 1 returns 0 emails.

**Page discovery:**
- Scan all `<a href="...">` on homepage
- Filter: internal links only (same domain/subdomain)
- Match URL paths and anchor text against multilingual patterns:
  - URL: `/contact`, `/contacts`, `/contatti`, `/about`, `/about-us`, `/chi-siamo`, `/kontakt`, `/contacto`, `/impressum`
  - Text: "contact", "contatti", "chi siamo", "about us", "get in touch", "reach us", "kontakt"
- Prioritize URL path matches over text matches
- Among matches, prioritize specific patterns (`/contact` > `/about`)

**Limits:**
- Max 5 pages visited per site
- 10s timeout per page
- 1 retry per page
- No recursive crawl (1 level deep from homepage only)
- Ignore file links (`.pdf`, `.jpg`, `.zip`, etc.) and fragment-only links (`#section`)

**Stop condition:** first valid email found on any page → return results.

### Level 3: Browser Fallback

Triggered only if Level 1 + 2 return 0 emails.

- Reuse the existing browser pool from `scrapemate` (Playwright or Rod depending on build)
- No new browser instance — shares the pool already used by PlaceJob
- Visit homepage, wait for JS rendering (max 3s, `networkidle` or `domcontentloaded`)
- Apply the same 3 parsers on the rendered DOM
- If no email on homepage: visit up to 3 contact pages discovered at Level 2
- 15s timeout per page (including rendering)
- Max 4 pages total (1 homepage + 3 contact)
- Read-only: no clicking, no form interaction, no popups
- 0 retries (browser is already the final fallback)

**Performance estimate:** ~10-20% of sites will reach Level 3. Browser concurrency is managed by `scrapemate` via the `-c` flag.

## Email Validation

Applied uniformly to all parsers at all levels:

- RFC 5322 basic format validation
- Exclude invalid domains: `example.com`, `test.com`, `localhost`
- Exclude generic prefixes: `noreply@`, `no-reply@`
- Case-insensitive deduplication

## Blocklist Improvements

Fix `IsWebsiteValidForEmail()`:

- Fix typo: `instragram` → `instagram`
- Case-insensitive matching
- Add domains: `linkedin`, `youtube`, `tiktok`, `pinterest`, `yelp`, `tripadvisor`
- Block URLs without valid scheme (`http`/`https`)

## New Entry Fields

### EmailStatus

```go
EmailStatus string `json:"email_status"`
```

| Value | Meaning |
|---|---|
| `found` | Emails extracted successfully |
| `not_found` | Site visited, no emails present |
| `website_error` | Site unreachable or HTTP error |
| `no_website` | Business has no website on Google Maps |
| `blocked_domain` | Website in blocklist (social media) |
| `skipped` | `-email` flag not active |

### EmailSource

```go
EmailSource string `json:"email_source"`
```

Values: `homepage`, `contact_page`, `browser_homepage`, `browser_contact_page`

Both fields are exported in CSV and JSON output.

## Timeouts and Retry Strategy

| Level | Timeout/request | Retries | Backoff |
|---|---|---|---|
| Level 1 (HTTP homepage) | 10s | 2 | Exponential (1s, 3s) |
| Level 2 (HTTP contact pages) | 10s | 1 | Fixed (1s) |
| Level 3 (Browser) | 15s | 0 | N/A |
| **Global pipeline** | **45s** | — | — |

The 45s global timeout prevents any single site from blocking the job queue.

## Logging

- `Info`: level transitions ("Level 1 failed, trying Level 2")
- `Warn`: HTTP errors with status codes
- `Debug`: parsing details (candidates found, candidates filtered, extraction method used)

## Integration Points

The `EmailPipeline` maintains the same interface as the current `EmailExtractJob` toward `PlaceJob`:

- `PlaceJob.Process()` creates `EmailPipeline` instead of `EmailExtractJob`
- Pipeline receives browser provider reference from PlaceJob for Level 3
- Pipeline returns `*Entry` with populated `Emails`, `EmailStatus`, and `EmailSource`
- No changes needed to runner, filerunner, or output writers beyond adding the 2 new CSV columns

## Architecture Diagram

```
PlaceJob
  └→ EmailPipeline (orchestrator)
       ├→ Level 1: StaticEmailExtractor
       │    - HTTP GET homepage
       │    - Parsers: mailto + text + regex
       │    - 2 retries, 10s timeout
       │    └→ Emails found? → STOP
       │
       ├→ Level 2: ContactPageCrawler
       │    - Discover contact/about pages from homepage links
       │    - HTTP GET up to 5 pages
       │    - Same parsers as Level 1
       │    └→ Emails found? → STOP
       │
       └→ Level 3: BrowserEmailExtractor
            - Reuse scrapemate browser pool
            - Render homepage + up to 3 contact pages
            - Parse rendered DOM
            └→ Return results
```
