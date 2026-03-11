package gmaps

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	httpTimeout      = 10 * time.Second
	globalTimeout    = 90 * time.Second
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

// EmailPipeline orchestrates a multi-level email extraction process:
//
//	Level 1:   HTTP fetch of the homepage
//	Level 2:   HTTP fetch of discovered contact/about pages
//	Level 2.5: HTTP fetch of deep-crawl pages (sitemap + footer/nav links)
//	Level 3:   Browser-rendered fetch of homepage, contact pages, and deep-crawl pages
type EmailPipeline struct {
	entry          *Entry
	browserFetcher BrowserFetcher
	httpClient     *http.Client
	contactPages   []string // discovered at Level 2, reused at Level 3
	deepCrawlPages []string // discovered at Level 2.5 via sitemap + footer/nav
}

// NewEmailPipeline creates an EmailPipeline for the given entry.
// If browserFetcher is nil, Level 3 (browser rendering) is skipped.
func NewEmailPipeline(entry *Entry, browserFetcher BrowserFetcher) *EmailPipeline {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // scraper must handle sites with bad certs
	}

	client := &http.Client{
		Timeout: httpTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}

			return nil
		},
		Transport: transport,
	}

	// Sanitize entry URL before pipeline starts.
	entry.WebSite = sanitizeURL(entry.WebSite)

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
	var doc *goquery.Document

	body, err := p.fetchWithRetry(ctx, p.entry.WebSite, maxRetryLevel1)
	if err == nil {
		var emails []string
		emails, doc = p.extractEmails(body)

		if len(emails) > 0 {
			p.entry.Emails = emails
			p.entry.EmailStatus = "found"
			p.entry.EmailSource = "homepage"

			return nil
		}
	}
	// If Level 1 failed (403, TLS error, timeout, etc.), doc is nil.
	// The pipeline continues to Level 3 (browser) which can handle these cases.

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

	// Discover deep-crawl pages from footer/nav links and sitemap.
	// Done after Level 2 so the sitemap fetch doesn't delay contact page checks.
	if doc != nil {
		p.deepCrawlPages = discoverDeepCrawlPages(
			ctx,
			doc,
			p.entry.WebSite,
			p.httpClient,
			p.contactPages,
		)
	}

	// --- Level 2.5: fetch deep-crawl pages via HTTP ---
	for _, pageURL := range p.deepCrawlPages {
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
			p.entry.EmailSource = "deep_crawl_page"

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
		contactBrowserLimit := 3
		if len(p.contactPages) < contactBrowserLimit {
			contactBrowserLimit = len(p.contactPages)
		}

		for i := 0; i < contactBrowserLimit; i++ {
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

		// Try deep-crawl pages with browser (max 3).
		deepBrowserLimit := 3
		if len(p.deepCrawlPages) < deepBrowserLimit {
			deepBrowserLimit = len(p.deepCrawlPages)
		}

		for i := 0; i < deepBrowserLimit; i++ {
			select {
			case <-ctx.Done():
				p.entry.Emails = []string{}
				p.entry.EmailStatus = "not_found"

				return nil
			default:
			}

			pageHTML, browserErr := p.browserFetcher.FetchWithBrowser(ctx, p.deepCrawlPages[i])
			if browserErr != nil || pageHTML == "" {
				continue
			}

			browserEmails, _ := p.extractEmails([]byte(pageHTML))
			if len(browserEmails) > 0 {
				p.entry.Emails = browserEmails
				p.entry.EmailStatus = "found"
				p.entry.EmailSource = "browser_deep_crawl_page"

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
func (p *EmailPipeline) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	cleanURL := sanitizeURL(rawURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cleanURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", cleanURL, err)
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("Accept-Language", "it-IT,it;q=0.9,en-US;q=0.8,en;q=0.7")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", cleanURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, cleanURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading body of %s: %w", cleanURL, err)
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

// sanitizeURL fixes common URL malformations found on business websites:
//   - trims whitespace and control characters
//   - decodes HTML entities (&amp; → &)
//   - adds https:// scheme if missing
//   - fixes double protocols (http://https://...)
//   - percent-encodes spaces and non-ASCII characters
func sanitizeURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}

	// Trim whitespace and control characters.
	u := strings.TrimSpace(rawURL)
	u = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1 // strip control characters
		}

		return r
	}, u)

	// Decode HTML entities (some sites have &amp; in URLs).
	u = html.UnescapeString(u)

	// Fix double protocol (http://https://example.com → https://example.com).
	if strings.HasPrefix(u, "http://https://") {
		u = "https://" + u[len("http://https://"):]
	} else if strings.HasPrefix(u, "https://http://") {
		u = "http://" + u[len("https://http://"):]
	}

	// Add scheme if missing.
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		if strings.HasPrefix(lower, "//") {
			u = "https:" + u
		} else {
			u = "https://" + u
		}
	}

	// Parse and re-encode to fix spaces, non-ASCII, etc.
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}

	// Ensure path is properly encoded (handles spaces → %20, etc.).
	parsed.RawPath = ""
	parsed.RawQuery = parsed.Query().Encode()

	return parsed.String()
}
