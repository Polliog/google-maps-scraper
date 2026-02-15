package gmaps

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/require"
)

func TestIsWebsiteValidForEmail(t *testing.T) {
	tests := []struct {
		name    string
		website string
		want    bool
	}{
		{name: "empty", website: "", want: false},
		{name: "valid https", website: "https://example.com", want: true},
		{name: "facebook", website: "https://facebook.com/somepage", want: false},
		{name: "Facebook uppercase", website: "https://Facebook.com/somepage", want: false},
		{name: "instagram", website: "https://instagram.com/somepage", want: false},
		{name: "twitter", website: "https://twitter.com/somepage", want: false},
		{name: "linkedin", website: "https://linkedin.com/in/someone", want: false},
		{name: "youtube", website: "https://youtube.com/channel/abc", want: false},
		{name: "tiktok", website: "https://tiktok.com/@user", want: false},
		{name: "pinterest", website: "https://pinterest.com/user", want: false},
		{name: "yelp", website: "https://yelp.com/biz/something", want: false},
		{name: "tripadvisor", website: "https://tripadvisor.com/Restaurant-abc", want: false},
		{name: "no scheme", website: "example.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Entry{WebSite: tt.website}
			got := e.IsWebsiteValidForEmail()
			require.Equal(t, tt.want, got, "IsWebsiteValidForEmail(%q)", tt.website)
		})
	}
}

func TestIsValidEmail(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  bool
	}{
		{name: "valid email", email: "info@business.com", want: true},
		{name: "noreply prefix", email: "noreply@business.com", want: false},
		{name: "no-reply prefix", email: "no-reply@business.com", want: false},
		{name: "no_reply prefix", email: "no_reply@business.com", want: false},
		{name: "mailer-daemon prefix", email: "mailer-daemon@business.com", want: false},
		{name: "example.com domain", email: "user@example.com", want: false},
		{name: "test.com domain", email: "user@test.com", want: false},
		{name: "localhost domain", email: "user@localhost", want: false},
		{name: "sentry.io domain", email: "user@sentry.io", want: false},
		{name: "not an email", email: "not-an-email", want: false},
		{name: "empty", email: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidEmail(tt.email)
			require.Equal(t, tt.want, got, "isValidEmail(%q)", tt.email)
		})
	}
}

func TestDeduplicateEmails(t *testing.T) {
	tests := []struct {
		name   string
		input  []string
		expect []string
	}{
		{
			name:   "case-insensitive dedup",
			input:  []string{"Info@Example.com", "info@example.com", "OTHER@test.org", "other@test.org"},
			expect: []string{"info@example.com", "other@test.org"},
		},
		{
			name:   "no duplicates",
			input:  []string{"a@b.com", "c@d.com"},
			expect: []string{"a@b.com", "c@d.com"},
		},
		{
			name:   "empty input",
			input:  []string{},
			expect: []string{},
		},
		{
			name:   "nil input",
			input:  nil,
			expect: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deduplicateEmails(tt.input)
			require.Equal(t, tt.expect, got)
		})
	}
}

func TestExtractEmailsFromDoc(t *testing.T) {
	tests := []struct {
		name   string
		html   string
		expect []string
	}{
		{
			name: "finds mailto links",
			html: `<html><body>
				<a href="mailto:contact@business.com">Contact Us</a>
				<a href="mailto:sales@business.com?subject=Hello">Sales</a>
			</body></html>`,
			expect: []string{"contact@business.com", "sales@business.com"},
		},
		{
			name: "finds emails in visible text when no mailto links",
			html: `<html><body>
				<p>Reach us at support@company.org for help.</p>
			</body></html>`,
			expect: []string{"support@company.org"},
		},
		{
			name: "excludes noreply and blocked domain emails",
			html: `<html><body>
				<a href="mailto:noreply@business.com">No Reply</a>
				<a href="mailto:user@example.com">Example</a>
				<a href="mailto:real@business.com">Real</a>
			</body></html>`,
			expect: []string{"real@business.com"},
		},
		{
			name: "deduplicates results",
			html: `<html><body>
				<a href="mailto:info@business.com">Info</a>
				<a href="mailto:INFO@business.com">Info Again</a>
				<p>Contact info@business.com</p>
			</body></html>`,
			expect: []string{"info@business.com"},
		},
		{
			name:   "returns nil for no emails",
			html:   `<html><body><p>No emails here</p></body></html>`,
			expect: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tt.html))
			require.NoError(t, err)
			got := extractEmailsFromDoc(doc)
			require.Equal(t, tt.expect, got)
		})
	}
}

func TestExtractEmailsFromHTML(t *testing.T) {
	tests := []struct {
		name   string
		body   []byte
		expect []string
	}{
		{
			name:   "finds email patterns in raw HTML",
			body:   []byte(`<html><body><a href="mailto:info@shop.com">info@shop.com</a><p>Also reach hello@shop.com</p></body></html>`),
			expect: []string{"info@shop.com", "hello@shop.com"},
		},
		{
			name:   "excludes invalid emails",
			body:   []byte(`<p>noreply@company.com and user@example.com and valid@company.com</p>`),
			expect: []string{"valid@company.com"},
		},
		{
			name:   "returns nil for HTML with no emails",
			body:   []byte(`<html><body><p>No emails here at all.</p></body></html>`),
			expect: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEmailsFromHTML(tt.body)
			require.Equal(t, tt.expect, got)
		})
	}
}

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

func TestExtractEmailsFromText(t *testing.T) {
	tests := []struct {
		name   string
		text   []byte
		expect []string
	}{
		{
			name:   "finds email patterns in plain text",
			text:   []byte("Contact us at info@store.com or sales@store.com for inquiries."),
			expect: []string{"info@store.com", "sales@store.com"},
		},
		{
			name:   "excludes invalid emails",
			text:   []byte("noreply@store.com and user@sentry.io but also hello@store.com"),
			expect: []string{"hello@store.com"},
		},
		{
			name:   "returns nil for text with no emails",
			text:   []byte("This is some plain text with no email addresses."),
			expect: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEmailsFromText(tt.text)
			require.Equal(t, tt.expect, got)
		})
	}
}
