package gmaps

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmailPipelineHomepageMailto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<h1>Welcome to Test Biz</h1>
			<a href="mailto:info@testbiz.com">Email us</a>
		</body></html>`)
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
			fmt.Fprintf(w, `<html><body>
				<h1>Welcome</h1>
				<a href="%s/contact">Contact Us</a>
			</body></html>`, "")
		case "/contact":
			fmt.Fprint(w, `<html><body>
				<h1>Contact Page</h1>
				<a href="mailto:contact@testbiz.com">Write to us</a>
			</body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	entry := &Entry{WebSite: srv.URL}
	pipeline := NewEmailPipeline(entry, nil)

	err := pipeline.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"contact@testbiz.com"}, entry.Emails)
	require.Equal(t, "found", entry.EmailStatus)
	require.Equal(t, "contact_page", entry.EmailSource)
}

func TestEmailPipelineNoEmails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<h1>Welcome</h1>
			<p>We have no contact info here.</p>
		</body></html>`)
	}))
	defer srv.Close()

	entry := &Entry{WebSite: srv.URL}
	pipeline := NewEmailPipeline(entry, nil)

	err := pipeline.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{}, entry.Emails)
	require.Equal(t, "not_found", entry.EmailStatus)
	require.Empty(t, entry.EmailSource)
}

func TestEmailPipelineWebsiteError(t *testing.T) {
	// Use an address that will refuse connections
	entry := &Entry{WebSite: "http://127.0.0.1:1"}
	pipeline := NewEmailPipeline(entry, nil)

	err := pipeline.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, "website_error", entry.EmailStatus)
}

func TestEmailPipelineRegexFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Email in plain text only, no mailto link
		fmt.Fprint(w, `<html><body>
			<h1>Welcome</h1>
			<p>For inquiries email us at hello@regexbiz.com today!</p>
		</body></html>`)
	}))
	defer srv.Close()

	entry := &Entry{WebSite: srv.URL}
	pipeline := NewEmailPipeline(entry, nil)

	err := pipeline.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"hello@regexbiz.com"}, entry.Emails)
	require.Equal(t, "found", entry.EmailStatus)
	require.Equal(t, "homepage", entry.EmailSource)
}
