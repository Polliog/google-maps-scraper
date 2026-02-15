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
