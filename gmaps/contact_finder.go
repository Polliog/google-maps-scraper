package gmaps

import (
	"net/url"
	"path"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const maxContactPages = 5

var (
	// URL path patterns - higher priority first.
	contactPathPatterns = []string{
		"/contact", "/contacts", "/contatti", "/kontakt", "/contacto",
		"/get-in-touch", "/reach-us",
		"/about", "/about-us", "/chi-siamo", "/impressum", "/who-we-are",
	}

	// Anchor text patterns.
	contactTextPatterns = []string{
		"contact", "contatti", "kontakt", "contacto",
		"chi siamo", "about us", "get in touch", "reach us",
		"impressum", "who we are",
	}

	// File extensions to skip.
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

// discoverContactPages scans all anchor elements in doc looking for internal
// links that are likely to be contact or about pages. It matches both URL
// paths and anchor text against a set of multilingual patterns.
//
// Only links with the same host as baseURL are considered. File links (.pdf,
// .jpg, etc.) and fragment-only links are skipped. Results are sorted by
// priority (contact pages first, then about pages) and capped at
// maxContactPages entries.
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

		// Skip fragment-only links.
		if strings.HasPrefix(href, "#") {
			return
		}

		// Skip javascript: links.
		if strings.HasPrefix(strings.ToLower(href), "javascript:") {
			return
		}

		// Parse and resolve the URL.
		parsed, err := url.Parse(href)
		if err != nil {
			return
		}

		resolved := base.ResolveReference(parsed)

		// Only same host.
		if resolved.Host != base.Host {
			return
		}

		// Skip file extensions.
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

		// Check URL path match.
		lowerPath := strings.ToLower(resolved.Path)
		for i, pattern := range contactPathPatterns {
			if strings.Contains(lowerPath, pattern) {
				seen[fullURL] = true
				candidates = append(candidates, contactPageCandidate{
					url:      fullURL,
					priority: i,
				})

				return
			}
		}

		// Check anchor text match.
		text := strings.ToLower(strings.TrimSpace(s.Text()))
		for _, pattern := range contactTextPatterns {
			if strings.Contains(text, pattern) {
				seen[fullURL] = true
				candidates = append(candidates, contactPageCandidate{
					url:      fullURL,
					priority: 100 + len(candidates),
				})

				return
			}
		}
	})

	// Sort by priority (insertion sort for small slices).
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].priority < candidates[j-1].priority; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}

	// Limit results.
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
