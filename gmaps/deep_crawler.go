package gmaps

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	maxDeepCrawlPages   = 8
	sitemapFetchTimeout = 8 * time.Second
	maxSitemapURLs      = 200
	maxFooterNavLinks   = 50
)

// deepCrawlCandidate is a scored URL candidate for the deep crawl level.
type deepCrawlCandidate struct {
	url   string
	score int // higher = more likely to contain email
}

// XML types for sitemap parsing.
type sitemapIndex struct {
	XMLName  xml.Name       `xml:"sitemapindex"`
	Sitemaps []sitemapEntry `xml:"sitemap"`
}

type sitemapEntry struct {
	Loc string `xml:"loc"`
}

type urlSet struct {
	XMLName xml.Name      `xml:"urlset"`
	URLs    []urlSetEntry `xml:"url"`
}

type urlSetEntry struct {
	Loc string `xml:"loc"`
}

// isSameOrSubdomain reports whether candidate is the same host as base,
// or is a subdomain of base (e.g. shop.example.com vs example.com).
func isSameOrSubdomain(candidate, base string) bool {
	if candidate == base {
		return true
	}

	return strings.HasSuffix(candidate, "."+base)
}

// discoverDeepCrawlPages discovers additional pages to crawl for emails
// by scanning footer/nav links and fetching sitemap.xml. It returns a
// deduplicated, scored, and capped list of URLs that do not overlap
// with alreadySeen (typically the contact pages already queued).
func discoverDeepCrawlPages(
	ctx context.Context,
	doc *goquery.Document,
	baseURL string,
	client *http.Client,
	alreadySeen []string,
) []string {
	seen := make(map[string]bool, len(alreadySeen)+1)
	for _, u := range alreadySeen {
		seen[u] = true
	}

	// Also mark the base URL itself as seen.
	seen[baseURL] = true

	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}

	var candidates []deepCrawlCandidate

	// Pass A: scan footer/nav links from the homepage DOM.
	footerCandidates := scanFooterNavLinks(doc, base, seen)
	candidates = append(candidates, footerCandidates...)

	// Pass B: fetch and parse sitemap.xml.
	sitemapCandidates := fetchSitemapURLs(ctx, base, client, seen)
	candidates = append(candidates, sitemapCandidates...)

	if len(candidates) == 0 {
		return nil
	}

	// Sort by score descending.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Cap at maxDeepCrawlPages.
	limit := maxDeepCrawlPages
	if len(candidates) < limit {
		limit = len(candidates)
	}

	result := make([]string, limit)
	for i := 0; i < limit; i++ {
		result[i] = candidates[i].url
	}

	return result
}

// scanFooterNavLinks scans the homepage DOM for links inside footer, nav,
// aside, and similar structural elements. It returns scored candidates that
// are not in the seen set. Found URLs are added to seen for deduplication.
func scanFooterNavLinks(doc *goquery.Document, base *url.URL, seen map[string]bool) []deepCrawlCandidate {
	var candidates []deepCrawlCandidate
	processed := 0

	selector := "footer a[href], nav a[href], aside a[href], " +
		"[class*=footer] a[href], [class*=nav] a[href], [class*=menu] a[href], " +
		"[id*=footer] a[href], [id*=nav] a[href], [role=navigation] a[href]"

	doc.Find(selector).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if processed >= maxFooterNavLinks {
			return false
		}

		href, exists := s.Attr("href")
		if !exists || href == "" {
			return true
		}

		href = strings.TrimSpace(href)

		if strings.HasPrefix(href, "#") {
			return true
		}

		if strings.HasPrefix(strings.ToLower(href), "javascript:") {
			return true
		}

		if strings.HasPrefix(strings.ToLower(href), "mailto:") {
			return true
		}

		parsed, err := url.Parse(href)
		if err != nil {
			return true
		}

		resolved := base.ResolveReference(parsed)

		if !isSameOrSubdomain(resolved.Host, base.Host) {
			return true
		}

		if shouldSkipExtension(resolved.Path) {
			return true
		}

		fullURL := resolved.String()
		if seen[fullURL] {
			return true
		}

		score := scoreURL(resolved.Path)
		if score <= 0 {
			return true
		}

		seen[fullURL] = true
		processed++

		candidates = append(candidates, deepCrawlCandidate{
			url:   fullURL,
			score: score,
		})

		return true
	})

	return candidates
}

// fetchSitemapURLs fetches /sitemap.xml from the site, parses it, and
// returns scored candidates. Handles both flat sitemaps and sitemap indexes
// (follows the first child sitemap only). Returns nil on any error.
func fetchSitemapURLs(ctx context.Context, base *url.URL, client *http.Client, seen map[string]bool) []deepCrawlCandidate {
	sitemapURL := base.Scheme + "://" + base.Host + "/sitemap.xml"

	ctx, cancel := context.WithTimeout(ctx, sitemapFetchTimeout)
	defer cancel()

	body, err := fetchSitemapBody(ctx, client, sitemapURL)
	if err != nil {
		return nil
	}

	rawURLs := parseSitemapBody(ctx, client, body)

	var candidates []deepCrawlCandidate

	for _, rawURL := range rawURLs {
		if len(candidates) >= maxSitemapURLs {
			break
		}

		parsed, err := url.Parse(rawURL)
		if err != nil {
			continue
		}

		if !isSameOrSubdomain(parsed.Host, base.Host) {
			continue
		}

		if shouldSkipExtension(parsed.Path) {
			continue
		}

		fullURL := parsed.String()
		if seen[fullURL] {
			continue
		}

		score := scoreURL(parsed.Path)
		if score <= 0 {
			continue
		}

		seen[fullURL] = true

		candidates = append(candidates, deepCrawlCandidate{
			url:   fullURL,
			score: score,
		})
	}

	return candidates
}

// fetchSitemapBody performs a single HTTP GET for a sitemap URL.
func fetchSitemapBody(ctx context.Context, client *http.Client, sitemapURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sitemapURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, sitemapURL)
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
}

// parseSitemapBody parses the sitemap XML body. If it's a sitemap index,
// it fetches the first child sitemap and parses that. Returns raw URL strings.
func parseSitemapBody(ctx context.Context, client *http.Client, body []byte) []string {
	// Try as urlset first (most common for small business sites).
	var us urlSet
	if err := xml.Unmarshal(body, &us); err == nil && len(us.URLs) > 0 {
		urls := make([]string, 0, len(us.URLs))
		for _, u := range us.URLs {
			if loc := strings.TrimSpace(u.Loc); loc != "" {
				urls = append(urls, loc)
			}
		}

		return urls
	}

	// Try as sitemap index.
	var si sitemapIndex
	if err := xml.Unmarshal(body, &si); err == nil && len(si.Sitemaps) > 0 {
		// Fetch only the first child sitemap to stay within time budget.
		childURL := strings.TrimSpace(si.Sitemaps[0].Loc)
		if childURL == "" {
			return nil
		}

		childBody, err := fetchSitemapBody(ctx, client, childURL)
		if err != nil {
			return nil
		}

		var childUS urlSet
		if err := xml.Unmarshal(childBody, &childUS); err == nil {
			urls := make([]string, 0, len(childUS.URLs))
			for _, u := range childUS.URLs {
				if loc := strings.TrimSpace(u.Loc); loc != "" {
					urls = append(urls, loc)
				}
			}

			return urls
		}
	}

	return nil
}

// scoreURL returns a relevance score for a URL path. Higher scores indicate
// pages more likely to contain contact email addresses.
func scoreURL(rawPath string) int {
	lower := strings.ToLower(rawPath)

	// Penalize irrelevant pages.
	irrelevantPrefixes := []string{
		"/blog", "/news", "/shop", "/product", "/category",
		"/tag", "/search", "/cart", "/checkout", "/wp-content",
		"/wp-admin", "/feed", "/rss", "/api/", "/cdn-cgi",
	}

	for _, prefix := range irrelevantPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return 0
		}
	}

	// Skip if it looks like a blog post or product (has date-like segments or long slugs).
	segments := strings.Split(strings.Trim(lower, "/"), "/")
	if len(segments) >= 4 {
		return 0
	}

	score := 10 // base score for any internal page

	// High-value keywords.
	highKeywords := []struct {
		keyword string
		points  int
	}{
		{"contact", 90},
		{"kontakt", 90},
		{"contatti", 90},
		{"contacto", 90},
		{"impressum", 85},
		{"imprint", 85},
		{"about", 75},
		{"chi-siamo", 75},
		{"about-us", 75},
		{"who-we-are", 70},
		{"team", 60},
		{"staff", 60},
		{"people", 55},
		{"company", 50},
		{"info", 40},
		{"help", 35},
		{"support", 35},
		{"faq", 30},
		{"privacy", 25},
		{"legal", 25},
	}

	for _, kw := range highKeywords {
		if strings.Contains(lower, kw.keyword) && kw.points > score {
			score = kw.points
		}
	}

	// Bonus for top-level pages (fewer path segments = more likely to be important).
	if len(segments) == 1 && segments[0] != "" {
		score += 15
	} else if len(segments) == 2 {
		score += 5
	}

	return score
}

// shouldSkipExtension checks if the URL path has a file extension that
// should be skipped (images, documents, media files).
func shouldSkipExtension(urlPath string) bool {
	ext := strings.ToLower(path.Ext(urlPath))
	if ext == "" {
		return false
	}

	for _, skip := range skipExtensions {
		if ext == skip {
			return true
		}
	}

	return false
}
