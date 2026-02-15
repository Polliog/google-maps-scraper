# Smart Email Pipeline — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the current single-shot `EmailExtractJob` with a 3-level hybrid pipeline (HTTP static -> contact page crawl -> browser fallback) that reliably extracts emails from business websites.

**Architecture:** An `EmailPipeline` orchestrator replaces `EmailExtractJob`. It runs 3 extraction levels sequentially, stopping as soon as emails are found. The pipeline reuses existing scrapemate interfaces (`IJob`, `Response`, `BrowserPage`) and integrates at the same point in `PlaceJob.Process()`.

**Tech Stack:** Go 1.25.6, goquery (HTML parsing), go-emailaddress (validation), scrapemate (browser pool + HTTP), net/http (static fetching)

---

### Task 1: Fix blocklist and add email validation helpers

**Files:**
- Modify: `gmaps/entry.go:127-145` (IsWebsiteValidForEmail)
- Create: `gmaps/email_helpers.go` (shared validation + parsing utilities)
- Create: `gmaps/email_helpers_test.go`

**Step 1: Write failing tests for blocklist fix**

Create `gmaps/email_helpers_test.go`:

```go
package gmaps

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsWebsiteValidForEmail(t *testing.T) {
	tests := []struct {
		name    string
		website string
		want    bool
	}{
		{"empty", "", false},
		{"valid", "https://example-business.com", true},
		{"facebook", "https://facebook.com/mybiz", false},
		{"Facebook uppercase", "https://Facebook.com/mybiz", false},
		{"instagram fixed typo", "https://instagram.com/mybiz", false},
		{"twitter", "https://twitter.com/mybiz", false},
		{"linkedin", "https://linkedin.com/in/user", false},
		{"youtube", "https://youtube.com/channel", false},
		{"tiktok", "https://tiktok.com/@user", false},
		{"pinterest", "https://pinterest.com/pin", false},
		{"yelp", "https://yelp.com/biz/foo", false},
		{"tripadvisor", "https://tripadvisor.com/rest", false},
		{"no scheme", "example-business.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Entry{WebSite: tt.website}
			require.Equal(t, tt.want, e.IsWebsiteValidForEmail())
		})
	}
}

func TestIsValidEmail(t *testing.T) {
	tests := []struct {
		email string
		valid bool
	}{
		{"info@realbusiness.com", true},
		{"contact@shop.it", true},
		{"noreply@example.com", false},
		{"no-reply@domain.com", false},
		{"user@example.com", false},
		{"user@test.com", false},
		{"user@localhost", false},
		{"not-an-email", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			result := isValidEmail(tt.email)
			require.Equal(t, tt.valid, result)
		})
	}
}

func TestDeduplicateEmails(t *testing.T) {
	input := []string{"Info@Biz.com", "info@biz.com", "other@biz.com"}
	result := deduplicateEmails(input)
	require.Len(t, result, 2)
}
```

**Step 2: Run tests to verify they fail**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -run "TestIsWebsiteValidForEmail|TestIsValidEmail|TestDeduplicateEmails" -v`
Expected: FAIL — `isValidEmail` and `deduplicateEmails` not defined

**Step 3: Implement blocklist fix and email helpers**

Modify `gmaps/entry.go` — replace `IsWebsiteValidForEmail` (lines 127-145):

```go
func (e *Entry) IsWebsiteValidForEmail() bool {
	if e.WebSite == "" {
		return false
	}

	lower := strings.ToLower(e.WebSite)

	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}

	blocklist := []string{
		"facebook",
		"instagram",
		"twitter",
		"linkedin",
		"youtube",
		"tiktok",
		"pinterest",
		"yelp",
		"tripadvisor",
	}

	for _, blocked := range blocklist {
		if strings.Contains(lower, blocked) {
			return false
		}
	}

	return true
}
```

Create `gmaps/email_helpers.go`:

```go
package gmaps

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/mcnijman/go-emailaddress"
)

var (
	blockedPrefixes = []string{"noreply", "no-reply", "no_reply", "mailer-daemon"}
	blockedDomains  = []string{"example.com", "test.com", "localhost", "sentry.io"}
)

func isValidEmail(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	email, err := emailaddress.Parse(s)
	if err != nil {
		return false
	}

	addr := strings.ToLower(email.String())

	parts := strings.SplitN(addr, "@", 2)
	if len(parts) != 2 {
		return false
	}

	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(parts[0], prefix) {
			return false
		}
	}

	for _, domain := range blockedDomains {
		if parts[1] == domain {
			return false
		}
	}

	return true
}

func deduplicateEmails(emails []string) []string {
	seen := make(map[string]bool, len(emails))
	result := make([]string, 0, len(emails))

	for _, email := range emails {
		lower := strings.ToLower(strings.TrimSpace(email))
		if lower != "" && !seen[lower] {
			seen[lower] = true
			result = append(result, lower)
		}
	}

	return result
}

func extractEmailsFromDoc(doc *goquery.Document) []string {
	var emails []string

	// Strategy 1: mailto links
	doc.Find("a[href^='mailto:'], a[href^='Mailto:'], a[href^='MAILTO:']").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}

		value := strings.TrimSpace(href)
		for _, prefix := range []string{"mailto:", "Mailto:", "MAILTO:"} {
			value = strings.TrimPrefix(value, prefix)
		}

		// Remove query params like ?subject=...
		if idx := strings.Index(value, "?"); idx != -1 {
			value = value[:idx]
		}

		if isValidEmail(value) {
			emails = append(emails, strings.ToLower(value))
		}
	})

	if len(emails) > 0 {
		return deduplicateEmails(emails)
	}

	// Strategy 2: visible text extraction
	// Remove script, style, and comment nodes before searching
	doc.Find("script, style, noscript").Remove()
	textEmails := extractEmailsFromText([]byte(doc.Text()))
	if len(textEmails) > 0 {
		return deduplicateEmails(textEmails)
	}

	return nil
}

func extractEmailsFromHTML(body []byte) []string {
	addresses := emailaddress.Find(body, false)
	var emails []string

	for _, addr := range addresses {
		if isValidEmail(addr.String()) {
			emails = append(emails, strings.ToLower(addr.String()))
		}
	}

	return deduplicateEmails(emails)
}

func extractEmailsFromText(text []byte) []string {
	addresses := emailaddress.Find(text, false)
	var emails []string

	for _, addr := range addresses {
		if isValidEmail(addr.String()) {
			emails = append(emails, strings.ToLower(addr.String()))
		}
	}

	return deduplicateEmails(emails)
}
```

**Step 4: Run tests to verify they pass**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -run "TestIsWebsiteValidForEmail|TestIsValidEmail|TestDeduplicateEmails" -v`
Expected: PASS

**Step 5: Commit**

```bash
cd G:/progetti/solture/google-maps-scraper
git add gmaps/entry.go gmaps/email_helpers.go gmaps/email_helpers_test.go
git commit -m "feat(email): fix blocklist and add email validation helpers"
```

---

### Task 2: Add EmailStatus and EmailSource fields to Entry

**Files:**
- Modify: `gmaps/entry.go:60-98` (Entry struct)
- Modify: `gmaps/entry.go:159-234` (CsvHeaders + CsvRow)

**Step 1: Write failing test**

Add to `gmaps/email_helpers_test.go`:

```go
func TestEntryCsvHeadersContainEmailFields(t *testing.T) {
	e := &Entry{}
	headers := e.CsvHeaders()
	require.Contains(t, headers, "email_status")
	require.Contains(t, headers, "email_source")
}

func TestEntryCsvRowContainsEmailFields(t *testing.T) {
	e := &Entry{
		EmailStatus: "found",
		EmailSource: "homepage",
		Emails:      []string{"test@example-biz.com"},
	}
	row := e.CsvRow()
	headers := e.CsvHeaders()
	require.Equal(t, len(headers), len(row))

	// Find index of email_status
	statusIdx := -1
	sourceIdx := -1
	for i, h := range headers {
		if h == "email_status" {
			statusIdx = i
		}
		if h == "email_source" {
			sourceIdx = i
		}
	}

	require.NotEqual(t, -1, statusIdx)
	require.NotEqual(t, -1, sourceIdx)
	require.Equal(t, "found", row[statusIdx])
	require.Equal(t, "homepage", row[sourceIdx])
}
```

**Step 2: Run test to verify it fails**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -run "TestEntryCsvHeaders|TestEntryCsvRow" -v`
Expected: FAIL — `EmailStatus` field not found

**Step 3: Add fields to Entry struct**

In `gmaps/entry.go`, add after line 97 (`Emails` field):

```go
	Emails      []string `json:"emails"`
	EmailStatus string   `json:"email_status"`
	EmailSource string   `json:"email_source"`
```

Add `"email_status"` and `"email_source"` to `CsvHeaders()` after `"emails"` (after line 194).

Add `e.EmailStatus` and `e.EmailSource` to `CsvRow()` after `stringSliceToString(e.Emails)` (after line 233).

**Step 4: Run tests to verify they pass**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -run "TestEntryCsvHeaders|TestEntryCsvRow" -v`
Expected: PASS

**Step 5: Run all existing tests to check nothing broke**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -v`
Expected: PASS (the existing `Test_EntryFromJSON` test should still pass — its `require.Equal` compares an `expected` struct that doesn't set the new fields, so they'll match as zero values)

**Step 6: Commit**

```bash
cd G:/progetti/solture/google-maps-scraper
git add gmaps/entry.go gmaps/email_helpers_test.go
git commit -m "feat(email): add EmailStatus and EmailSource fields to Entry"
```

---

### Task 3: Implement contact page discovery

**Files:**
- Create: `gmaps/contact_finder.go`
- Create: `gmaps/contact_finder_test.go`

**Step 1: Write failing tests**

Create `gmaps/contact_finder_test.go`:

```go
package gmaps

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/require"
)

func TestDiscoverContactPages(t *testing.T) {
	html := `<html><body>
		<a href="https://example.com/contact">Contact Us</a>
		<a href="https://example.com/about">About</a>
		<a href="https://example.com/products">Products</a>
		<a href="https://other.com/contact">Other Site</a>
		<a href="https://example.com/contatti">Contatti</a>
		<a href="https://example.com/file.pdf">Download PDF</a>
		<a href="#section">Section</a>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	pages := discoverContactPages(doc, "https://example.com")

	// Should find /contact, /about, /contatti but not /products, not other domain, not .pdf, not #section
	require.GreaterOrEqual(t, len(pages), 2)
	require.LessOrEqual(t, len(pages), 5)

	// /contact should be prioritized over /about
	require.Contains(t, pages[0], "/contact")
}

func TestDiscoverContactPagesWithTextMatch(t *testing.T) {
	html := `<html><body>
		<a href="https://example.com/reach-out">Get in Touch</a>
		<a href="https://example.com/team">Our Team</a>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	pages := discoverContactPages(doc, "https://example.com")
	require.Len(t, pages, 1)
	require.Contains(t, pages[0], "/reach-out")
}

func TestDiscoverContactPagesMaxLimit(t *testing.T) {
	links := ""
	for i := 0; i < 20; i++ {
		links += `<a href="https://example.com/contact-` + string(rune('a'+i)) + `">Contact</a>`
	}
	html := `<html><body>` + links + `</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	pages := discoverContactPages(doc, "https://example.com")
	require.LessOrEqual(t, len(pages), 5)
}
```

**Step 2: Run tests to verify they fail**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -run "TestDiscoverContactPages" -v`
Expected: FAIL — `discoverContactPages` not defined

**Step 3: Implement contact page discovery**

Create `gmaps/contact_finder.go`:

```go
package gmaps

import (
	"net/url"
	"path"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const maxContactPages = 5

var (
	// URL path patterns — higher priority first
	contactPathPatterns = []string{
		"/contact", "/contacts", "/contatti", "/kontakt", "/contacto",
		"/get-in-touch", "/reach-us",
		"/about", "/about-us", "/chi-siamo", "/impressum", "/who-we-are",
	}

	// Anchor text patterns
	contactTextPatterns = []string{
		"contact", "contatti", "kontakt", "contacto",
		"chi siamo", "about us", "get in touch", "reach us",
		"impressum", "who we are",
	}

	// File extensions to skip
	skipExtensions = []string{
		".pdf", ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp",
		".zip", ".tar", ".gz", ".doc", ".docx", ".xls", ".xlsx",
		".mp3", ".mp4", ".avi", ".mov",
	}
)

type contactPageCandidate struct {
	url      string
	priority int // lower = higher priority
}

func discoverContactPages(doc *goquery.Document, baseURL string) []string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}

	var candidates []contactPageCandidate
	seen := make(map[string]bool)

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists || href == "" {
			return
		}

		href = strings.TrimSpace(href)

		// Skip fragment-only links
		if strings.HasPrefix(href, "#") {
			return
		}

		// Skip javascript: links
		if strings.HasPrefix(strings.ToLower(href), "javascript:") {
			return
		}

		// Parse and resolve the URL
		parsed, err := url.Parse(href)
		if err != nil {
			return
		}

		resolved := base.ResolveReference(parsed)

		// Only same host
		if resolved.Host != base.Host {
			return
		}

		// Skip file extensions
		ext := strings.ToLower(path.Ext(resolved.Path))
		for _, skip := range skipExtensions {
			if ext == skip {
				return
			}
		}

		fullURL := resolved.String()
		if seen[fullURL] {
			return
		}

		// Check URL path match
		lowerPath := strings.ToLower(resolved.Path)
		for i, pattern := range contactPathPatterns {
			if strings.Contains(lowerPath, pattern) {
				seen[fullURL] = true
				candidates = append(candidates, contactPageCandidate{
					url:      fullURL,
					priority: i, // index = priority (lower = better)
				})
				return
			}
		}

		// Check anchor text match
		text := strings.ToLower(strings.TrimSpace(s.Text()))
		for _, pattern := range contactTextPatterns {
			if strings.Contains(text, pattern) {
				seen[fullURL] = true
				candidates = append(candidates, contactPageCandidate{
					url:      fullURL,
					priority: 100 + len(candidates), // text matches are lower priority than URL matches
				})
				return
			}
		}
	})

	// Sort by priority
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].priority < candidates[j-1].priority; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}

	// Limit results
	limit := maxContactPages
	if len(candidates) < limit {
		limit = len(candidates)
	}

	result := make([]string, limit)
	for i := 0; i < limit; i++ {
		result[i] = candidates[i].url
	}

	return result
}
```

**Step 4: Run tests to verify they pass**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -run "TestDiscoverContactPages" -v`
Expected: PASS

**Step 5: Commit**

```bash
cd G:/progetti/solture/google-maps-scraper
git add gmaps/contact_finder.go gmaps/contact_finder_test.go
git commit -m "feat(email): add contact page discovery logic"
```

---

### Task 4: Implement the EmailPipeline orchestrator (Levels 1 + 2)

**Files:**
- Create: `gmaps/email_pipeline.go`
- Create: `gmaps/email_pipeline_test.go`
- Modify: `gmaps/emailjob.go` (keep for backwards compat but refactor internals)

**Step 1: Write failing tests for the pipeline**

Create `gmaps/email_pipeline_test.go`:

```go
package gmaps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmailPipelineHomepageMailto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><a href="mailto:info@testbiz.com">Email us</a></body></html>`))
	}))
	defer srv.Close()

	entry := &Entry{WebSite: srv.URL}
	pipeline := NewEmailPipeline(entry, nil)

	err := pipeline.Run(context.Background())
	require.NoError(t, err)

	require.Equal(t, []string{"info@testbiz.com"}, entry.Emails)
	require.Equal(t, "found", entry.EmailStatus)
	require.Equal(t, "homepage", entry.EmailSource)
}

func TestEmailPipelineContactPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<html><body><a href="/contact">Contact</a></body></html>`))
		case "/contact":
			w.Write([]byte(`<html><body><a href="mailto:sales@testbiz.com">Contact us</a></body></html>`))
		}
	}))
	defer srv.Close()

	entry := &Entry{WebSite: srv.URL}
	pipeline := NewEmailPipeline(entry, nil)

	err := pipeline.Run(context.Background())
	require.NoError(t, err)

	require.Equal(t, []string{"sales@testbiz.com"}, entry.Emails)
	require.Equal(t, "found", entry.EmailStatus)
	require.Equal(t, "contact_page", entry.EmailSource)
}

func TestEmailPipelineNoEmails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><p>No email here</p></body></html>`))
	}))
	defer srv.Close()

	entry := &Entry{WebSite: srv.URL}
	pipeline := NewEmailPipeline(entry, nil)

	err := pipeline.Run(context.Background())
	require.NoError(t, err)

	require.Empty(t, entry.Emails)
	require.Equal(t, "not_found", entry.EmailStatus)
}

func TestEmailPipelineWebsiteError(t *testing.T) {
	entry := &Entry{WebSite: "http://127.0.0.1:1"}  // unreachable
	pipeline := NewEmailPipeline(entry, nil)

	err := pipeline.Run(context.Background())
	require.NoError(t, err)  // pipeline doesn't error, it sets status

	require.Empty(t, entry.Emails)
	require.Equal(t, "website_error", entry.EmailStatus)
}

func TestEmailPipelineRegexFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><p>Reach us at hello@mybusiness.com for info</p></body></html>`))
	}))
	defer srv.Close()

	entry := &Entry{WebSite: srv.URL}
	pipeline := NewEmailPipeline(entry, nil)

	err := pipeline.Run(context.Background())
	require.NoError(t, err)

	require.Equal(t, []string{"hello@mybusiness.com"}, entry.Emails)
	require.Equal(t, "found", entry.EmailStatus)
}
```

**Step 2: Run tests to verify they fail**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -run "TestEmailPipeline" -v`
Expected: FAIL — `NewEmailPipeline` not defined

**Step 3: Implement EmailPipeline**

Create `gmaps/email_pipeline.go`:

```go
package gmaps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/scrapemate"
)

const (
	httpTimeout       = 10 * time.Second
	globalTimeout     = 45 * time.Second
	maxRetryLevel1    = 2
	maxRetryLevel2    = 1
	retryBackoff1     = 1 * time.Second
	retryBackoff2     = 3 * time.Second
	maxResponseBytes  = 5 * 1024 * 1024 // 5MB
)

// BrowserFetcher abstracts browser page access for Level 3.
// When nil, Level 3 is skipped.
type BrowserFetcher interface {
	FetchWithBrowser(ctx context.Context, url string) (string, error)
}

type EmailPipeline struct {
	entry          *Entry
	browserFetcher BrowserFetcher
	httpClient     *http.Client
	contactPages   []string // discovered at Level 2, reused at Level 3
}

func NewEmailPipeline(entry *Entry, browserFetcher BrowserFetcher) *EmailPipeline {
	return &EmailPipeline{
		entry:          entry,
		browserFetcher: browserFetcher,
		httpClient: &http.Client{
			Timeout: httpTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

func (p *EmailPipeline) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, globalTimeout)
	defer cancel()

	// Level 1: Static HTTP on homepage
	emails, source, err := p.level1(ctx)
	if err == nil && len(emails) > 0 {
		p.entry.Emails = emails
		p.entry.EmailStatus = "found"
		p.entry.EmailSource = source
		return nil
	}

	if err != nil {
		// If homepage fetch completely failed, set error status
		p.entry.Emails = []string{}
		p.entry.EmailStatus = "website_error"
		return nil
	}

	// Level 2: Crawl contact pages
	emails, source, err = p.level2(ctx)
	if err == nil && len(emails) > 0 {
		p.entry.Emails = emails
		p.entry.EmailStatus = "found"
		p.entry.EmailSource = source
		return nil
	}

	// Level 3: Browser fallback
	if p.browserFetcher != nil {
		emails, source = p.level3(ctx)
		if len(emails) > 0 {
			p.entry.Emails = emails
			p.entry.EmailStatus = "found"
			p.entry.EmailSource = source
			return nil
		}
	}

	p.entry.Emails = []string{}
	p.entry.EmailStatus = "not_found"
	return nil
}

// level1 fetches the homepage via HTTP and extracts emails.
func (p *EmailPipeline) level1(ctx context.Context) ([]string, string, error) {
	doc, body, err := p.fetchWithRetry(ctx, p.entry.WebSite, maxRetryLevel1)
	if err != nil {
		return nil, "", err
	}

	emails := p.extractEmails(doc, body)
	if len(emails) > 0 {
		return emails, "homepage", nil
	}

	// Save contact pages discovered from homepage for Level 2
	if doc != nil {
		p.contactPages = discoverContactPages(doc, p.entry.WebSite)
	}

	return nil, "", nil
}

// level2 fetches discovered contact pages via HTTP and extracts emails.
func (p *EmailPipeline) level2(ctx context.Context) ([]string, string, error) {
	for _, pageURL := range p.contactPages {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		default:
		}

		doc, body, err := p.fetchWithRetry(ctx, pageURL, maxRetryLevel2)
		if err != nil {
			continue
		}

		emails := p.extractEmails(doc, body)
		if len(emails) > 0 {
			return emails, "contact_page", nil
		}
	}

	return nil, "", nil
}

// level3 uses the browser to render pages and extract emails.
func (p *EmailPipeline) level3(ctx context.Context) ([]string, string) {
	// Try homepage with browser
	htmlContent, err := p.browserFetcher.FetchWithBrowser(ctx, p.entry.WebSite)
	if err == nil && htmlContent != "" {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err == nil {
			emails := p.extractEmails(doc, []byte(htmlContent))
			if len(emails) > 0 {
				return emails, "browser_homepage"
			}

			// Discover contact pages from browser-rendered homepage if we don't have any
			if len(p.contactPages) == 0 {
				p.contactPages = discoverContactPages(doc, p.entry.WebSite)
			}
		}
	}

	// Try contact pages with browser (max 3)
	limit := 3
	if len(p.contactPages) < limit {
		limit = len(p.contactPages)
	}

	for i := 0; i < limit; i++ {
		select {
		case <-ctx.Done():
			return nil, ""
		default:
		}

		htmlContent, err := p.browserFetcher.FetchWithBrowser(ctx, p.contactPages[i])
		if err != nil {
			continue
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			continue
		}

		emails := p.extractEmails(doc, []byte(htmlContent))
		if len(emails) > 0 {
			return emails, "browser_contact_page"
		}
	}

	return nil, ""
}

func (p *EmailPipeline) extractEmails(doc *goquery.Document, body []byte) []string {
	if doc != nil {
		emails := extractEmailsFromDoc(doc)
		if len(emails) > 0 {
			return emails
		}
	}

	if len(body) > 0 {
		emails := extractEmailsFromHTML(body)
		if len(emails) > 0 {
			return emails
		}
	}

	return nil
}

func (p *EmailPipeline) fetchWithRetry(ctx context.Context, targetURL string, maxRetries int) (*goquery.Document, []byte, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := retryBackoff1
			if attempt > 1 {
				backoff = retryBackoff2
			}

			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		doc, body, err := p.fetch(ctx, targetURL)
		if err == nil {
			return doc, body, nil
		}

		lastErr = err
	}

	return nil, nil, lastErr
}

func (p *EmailPipeline) fetch(ctx context.Context, targetURL string) (*goquery.Document, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		// Return body even if parsing fails — regex can still work
		return nil, body, nil
	}

	return doc, body, nil
}
```

**Step 4: Run tests to verify they pass**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -run "TestEmailPipeline" -v -timeout 60s`
Expected: PASS

**Step 5: Commit**

```bash
cd G:/progetti/solture/google-maps-scraper
git add gmaps/email_pipeline.go gmaps/email_pipeline_test.go
git commit -m "feat(email): implement EmailPipeline orchestrator (levels 1+2)"
```

---

### Task 5: Integrate EmailPipeline into PlaceJob and EmailExtractJob

**Files:**
- Modify: `gmaps/emailjob.go` (refactor to use EmailPipeline)
- Modify: `gmaps/place.go:97-110` (update email job creation)

**Step 1: Refactor EmailExtractJob to use EmailPipeline**

Replace the contents of `gmaps/emailjob.go` with:

```go
package gmaps

import (
	"context"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
)

type EmailExtractJobOptions func(*EmailExtractJob)

type EmailExtractJob struct {
	scrapemate.Job

	Entry       *Entry
	ExitMonitor exiter.Exiter
}

func NewEmailJob(parentID string, entry *Entry, opts ...EmailExtractJobOptions) *EmailExtractJob {
	const (
		defaultPrio       = scrapemate.PriorityHigh
		defaultMaxRetries = 0
	)

	job := EmailExtractJob{
		Job: scrapemate.Job{
			ID:         uuid.New().String(),
			ParentID:   parentID,
			Method:     "GET",
			URL:        entry.WebSite,
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
	}

	job.Entry = entry

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithEmailJobExitMonitor(exitMonitor exiter.Exiter) EmailExtractJobOptions {
	return func(j *EmailExtractJob) {
		j.ExitMonitor = exitMonitor
	}
}

func (j *EmailExtractJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	defer func() {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
	}()

	log := scrapemate.GetLoggerFromContext(ctx)
	log.Info("Processing email pipeline", "url", j.URL)

	pipeline := NewEmailPipeline(j.Entry, nil)

	if err := pipeline.Run(ctx); err != nil {
		log.Warn("Email pipeline failed", "url", j.URL, "error", err)
		j.Entry.Emails = []string{}
		j.Entry.EmailStatus = "website_error"
	}

	log.Info("Email pipeline completed",
		"url", j.URL,
		"emails_found", len(j.Entry.Emails),
		"status", j.Entry.EmailStatus,
		"source", j.Entry.EmailSource,
	)

	return j.Entry, nil, nil
}

func (j *EmailExtractJob) ProcessOnFetchError() bool {
	return true
}
```

**Step 2: Update PlaceJob to set EmailStatus for non-email cases**

In `gmaps/place.go`, update lines 97-112 to set status when email is skipped or website is blocked:

```go
	if j.ExtractEmail && entry.IsWebsiteValidForEmail() {
		opts := []EmailExtractJobOptions{}
		if j.ExitMonitor != nil {
			opts = append(opts, WithEmailJobExitMonitor(j.ExitMonitor))
		}

		emailJob := NewEmailJob(j.ID, &entry, opts...)

		j.UsageInResultststs = false

		return nil, []scrapemate.IJob{emailJob}, nil
	}

	if j.ExtractEmail {
		// Email extraction was requested but website is invalid
		if entry.WebSite == "" {
			entry.EmailStatus = "no_website"
		} else {
			entry.EmailStatus = "blocked_domain"
		}
		entry.Emails = []string{}
	}

	if j.ExitMonitor != nil {
		j.ExitMonitor.IncrPlacesCompleted(1)
	}

	return &entry, nil, err
```

**Step 3: Run all tests**

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./gmaps/ -v -timeout 120s`
Expected: PASS

Run: `cd G:/progetti/solture/google-maps-scraper && go test ./... -timeout 120s`
Expected: PASS (all packages)

**Step 4: Commit**

```bash
cd G:/progetti/solture/google-maps-scraper
git add gmaps/emailjob.go gmaps/place.go
git commit -m "feat(email): integrate EmailPipeline into PlaceJob/EmailExtractJob"
```

---

### Task 6: Build and lint

**Step 1: Run go vet**

Run: `cd G:/progetti/solture/google-maps-scraper && go vet ./...`
Expected: No errors

**Step 2: Run gofmt**

Run: `cd G:/progetti/solture/google-maps-scraper && gofmt -l .`
Expected: No files listed (all formatted)

If files need formatting:
Run: `cd G:/progetti/solture/google-maps-scraper && gofmt -w .`

**Step 3: Run go mod tidy**

Run: `cd G:/progetti/solture/google-maps-scraper && go mod tidy`

**Step 4: Build both variants**

Run: `cd G:/progetti/solture/google-maps-scraper && go build -o /dev/null .`
Expected: Build succeeds

Run: `cd G:/progetti/solture/google-maps-scraper && go build -tags rod -o /dev/null .`
Expected: Build succeeds

**Step 5: Run full test suite with race detection**

Run: `cd G:/progetti/solture/google-maps-scraper && go test -race ./... -timeout 120s`
Expected: PASS

**Step 6: Commit any formatting/tidy changes**

```bash
cd G:/progetti/solture/google-maps-scraper
git add -A
git diff --cached --quiet || git commit -m "chore: go mod tidy and formatting"
```

---

### Task 7: Manual smoke test

**Step 1: Build the binary**

Run: `cd G:/progetti/solture/google-maps-scraper && go build -o gmaps-scraper .`

**Step 2: Create a test query file**

Create a file `test_queries.txt` with a single query like:
```
restaurants in Rome
```

**Step 3: Run with email extraction**

Run: `cd G:/progetti/solture/google-maps-scraper && ./gmaps-scraper -input test_queries.txt -results test_results.csv -email -depth 1 -c 1`

**Step 4: Verify output**

Check `test_results.csv` — verify:
- `emails` column is populated for some entries
- `email_status` column shows correct values (`found`, `not_found`, `no_website`, `blocked_domain`)
- `email_source` column shows where emails were found (`homepage`, `contact_page`)

**Step 5: Clean up test files**

```bash
rm -f test_queries.txt test_results.csv gmaps-scraper
```
