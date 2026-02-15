package gmaps

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/mcnijman/go-emailaddress"
)

// blockedEmailPrefixes are local-part prefixes that indicate automated or
// non-deliverable mailboxes.
var blockedEmailPrefixes = []string{
	"noreply@",
	"no-reply@",
	"no_reply@",
	"mailer-daemon@",
}

// blockedEmailDomains are domains that should never appear in scraped results.
var blockedEmailDomains = []string{
	"example.com",
	"test.com",
	"localhost",
	"sentry.io",
}

// isValidEmail checks whether s is a syntactically valid email address and
// is not on any blocklist (automated prefixes, disposable/test domains).
func isValidEmail(s string) bool {
	if s == "" {
		return false
	}

	_, err := emailaddress.Parse(strings.TrimSpace(s))
	if err != nil {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(s))

	for _, prefix := range blockedEmailPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}

	// Extract the domain part after the last '@'.
	atIdx := strings.LastIndex(lower, "@")
	if atIdx < 0 {
		return false
	}

	domain := lower[atIdx+1:]

	for _, blocked := range blockedEmailDomains {
		if domain == blocked {
			return false
		}
	}

	return true
}

// deduplicateEmails returns a new slice with duplicates removed.
// Comparison is case-insensitive; the lowercased form is kept.
func deduplicateEmails(emails []string) []string {
	seen := make(map[string]bool, len(emails))
	result := make([]string, 0, len(emails))

	for _, e := range emails {
		lower := strings.ToLower(e)
		if seen[lower] {
			continue
		}

		seen[lower] = true
		result = append(result, lower)
	}

	return result
}

// extractEmailsFromDoc extracts emails from a parsed HTML document using two
// strategies: first it looks for mailto: links, then it scans visible text
// (with script and style elements removed). All results are validated and
// deduplicated.
func extractEmailsFromDoc(doc *goquery.Document) []string {
	var emails []string
	seen := make(map[string]bool)

	// Strategy 1: mailto links (case-insensitive selector via goquery).
	doc.Find("a[href^='mailto:'], a[href^='Mailto:'], a[href^='MAILTO:']").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}

		// Normalise and strip the mailto: prefix.
		value := strings.TrimSpace(href)
		lowerValue := strings.ToLower(value)
		if strings.HasPrefix(lowerValue, "mailto:") {
			value = value[len("mailto:"):]
		}

		// Strip query parameters (e.g. ?subject=...).
		if idx := strings.Index(value, "?"); idx >= 0 {
			value = value[:idx]
		}

		value = strings.TrimSpace(value)
		if value == "" {
			return
		}

		lower := strings.ToLower(value)
		if isValidEmail(lower) && !seen[lower] {
			seen[lower] = true
			emails = append(emails, lower)
		}
	})

	// Strategy 2: scan visible text after removing script/style elements.
	docCopy := doc.Clone()
	docCopy.Find("script, style, noscript").Remove()
	text := docCopy.Text()

	found := emailaddress.Find([]byte(text), false)
	for _, addr := range found {
		lower := strings.ToLower(addr.String())
		if isValidEmail(lower) && !seen[lower] {
			seen[lower] = true
			emails = append(emails, lower)
		}
	}

	return emails
}

// extractEmailsFromHTML extracts emails from raw HTML bytes using a regex
// approach via go-emailaddress. Results are filtered through isValidEmail.
func extractEmailsFromHTML(body []byte) []string {
	addresses := emailaddress.Find(body, false)

	seen := make(map[string]bool, len(addresses))
	var emails []string

	for _, addr := range addresses {
		lower := strings.ToLower(addr.String())
		if isValidEmail(lower) && !seen[lower] {
			seen[lower] = true
			emails = append(emails, lower)
		}
	}

	return emails
}

// extractEmailsFromText extracts emails from plain text content using a regex
// approach via go-emailaddress. Results are filtered through isValidEmail.
func extractEmailsFromText(text []byte) []string {
	addresses := emailaddress.Find(text, false)

	seen := make(map[string]bool, len(addresses))
	var emails []string

	for _, addr := range addresses {
		lower := strings.ToLower(addr.String())
		if isValidEmail(lower) && !seen[lower] {
			seen[lower] = true
			emails = append(emails, lower)
		}
	}

	return emails
}
