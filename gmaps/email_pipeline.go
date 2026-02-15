package gmaps

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	httpTimeout      = 10 * time.Second
	globalTimeout    = 45 * time.Second
	maxRetryLevel1   = 2
	maxRetryLevel2   = 1
	retryBackoff1    = 1 * time.Second
	retryBackoff2    = 3 * time.Second
	maxResponseBytes = 5 * 1024 * 1024 // 5MB

	userAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	acceptHeader = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
)

// BrowserFetcher provides browser-rendered page content for Level 3
// extraction. When nil is passed to NewEmailPipeline, Level 3 is skipped.
type BrowserFetcher interface {
	FetchWithBrowser(ctx context.Context, url string) (string, error)
}

// EmailPipeline orchestrates a 3-level email extraction process:
//
//	Level 1: HTTP fetch of the homepage
//	Level 2: HTTP fetch of discovered contact/about pages
//	Level 3: Browser-rendered fetch of homepage and contact pages
type EmailPipeline struct {
	entry          *Entry
	browserFetcher BrowserFetcher
	httpClient     *http.Client
	contactPages   []string // discovered at Level 2, reused at Level 3
}

// NewEmailPipeline creates an EmailPipeline for the given entry.
// If browserFetcher is nil, Level 3 (browser rendering) is skipped.
func NewEmailPipeline(entry *Entry, browserFetcher BrowserFetcher) *EmailPipeline {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	client := &http.Client{
		Timeout: httpTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("stopped after 3 redirects")
			}

			return nil
		},
		Transport: transport,
	}

	return &EmailPipeline{
		entry:          entry,
		browserFetcher: browserFetcher,
		httpClient:     client,
	}
}

// Run executes the 3-level pipeline. It modifies entry.Emails,
// entry.EmailStatus, and entry.EmailSource in place.
func (p *EmailPipeline) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, globalTimeout)
	defer cancel()

	// --- Level 1: fetch homepage via HTTP ---
	body, err := p.fetchWithRetry(ctx, p.entry.WebSite, maxRetryLevel1)
	if err != nil {
		// Homepage fetch completely failed.
		p.entry.EmailStatus = "website_error"

		return nil
	}

	emails, doc := p.extractEmails(body)
	if len(emails) > 0 {
		p.entry.Emails = emails
		p.entry.EmailStatus = "found"
		p.entry.EmailSource = "homepage"

		return nil
	}

	// Discover contact pages from homepage links.
	if doc != nil {
		p.contactPages = discoverContactPages(doc, p.entry.WebSite)
	}

	// --- Level 2: fetch each contact page via HTTP ---
	for _, pageURL := range p.contactPages {
		select {
		case <-ctx.Done():
			p.entry.Emails = []string{}
			p.entry.EmailStatus = "not_found"

			return nil
		default:
		}

		pageBody, fetchErr := p.fetchWithRetry(ctx, pageURL, maxRetryLevel2)
		if fetchErr != nil {
			continue
		}

		pageEmails, _ := p.extractEmails(pageBody)
		if len(pageEmails) > 0 {
			p.entry.Emails = pageEmails
			p.entry.EmailStatus = "found"
			p.entry.EmailSource = "contact_page"

			return nil
		}
	}

	// --- Level 3: browser rendering (only if browserFetcher is available) ---
	if p.browserFetcher != nil {
		// Try homepage with browser.
		html, browserErr := p.browserFetcher.FetchWithBrowser(ctx, p.entry.WebSite)
		if browserErr == nil && html != "" {
			browserEmails, _ := p.extractEmails([]byte(html))
			if len(browserEmails) > 0 {
				p.entry.Emails = browserEmails
				p.entry.EmailStatus = "found"
				p.entry.EmailSource = "browser_homepage"

				return nil
			}
		}

		// Try contact pages with browser (max 3).
		limit := 3
		if len(p.contactPages) < limit {
			limit = len(p.contactPages)
		}

		for i := 0; i < limit; i++ {
			select {
			case <-ctx.Done():
				p.entry.Emails = []string{}
				p.entry.EmailStatus = "not_found"

				return nil
			default:
			}

			pageHTML, browserErr := p.browserFetcher.FetchWithBrowser(ctx, p.contactPages[i])
			if browserErr != nil || pageHTML == "" {
				continue
			}

			browserEmails, _ := p.extractEmails([]byte(pageHTML))
			if len(browserEmails) > 0 {
				p.entry.Emails = browserEmails
				p.entry.EmailStatus = "found"
				p.entry.EmailSource = "browser_contact_page"

				return nil
			}
		}
	}

	// Nothing found at any level.
	p.entry.Emails = []string{}
	p.entry.EmailStatus = "not_found"

	return nil
}

// fetchWithRetry fetches the given URL with exponential backoff retries.
func (p *EmailPipeline) fetchWithRetry(ctx context.Context, url string, maxRetries int) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := retryBackoff1
			if attempt > 1 {
				backoff = retryBackoff2
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		body, err := p.fetchPage(ctx, url)
		if err == nil {
			return body, nil
		}

		lastErr = err
	}

	return nil, lastErr
}

// fetchPage performs a single HTTP GET and returns the response body.
func (p *EmailPipeline) fetchPage(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", url, err)
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", acceptHeader)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading body of %s: %w", url, err)
	}

	return body, nil
}

// extractEmails tries goquery-based extraction first; only if it finds
// nothing does it fall back to regex on the raw HTML. This avoids false
// positives from script/style tags that the regex would otherwise match.
// It returns the deduplicated email list and the parsed document (which
// may be nil if parsing failed).
func (p *EmailPipeline) extractEmails(body []byte) ([]string, *goquery.Document) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err == nil {
		if docEmails := filterValid(extractEmailsFromDoc(doc)); len(docEmails) > 0 {
			return docEmails, doc
		}
	}

	// Regex fallback on raw bytes — only reached when goquery found nothing.
	if htmlEmails := filterValid(extractEmailsFromHTML(body)); len(htmlEmails) > 0 {
		return htmlEmails, doc
	}

	return nil, doc
}

// filterValid deduplicates and validates a list of emails.
func filterValid(emails []string) []string {
	deduped := deduplicateEmails(emails)

	var valid []string

	for _, e := range deduped {
		if isValidEmail(e) {
			valid = append(valid, e)
		}
	}

	return valid
}
