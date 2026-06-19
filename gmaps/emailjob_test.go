package gmaps

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"
)

// fakeBrowserPage is a no-op scrapemate.BrowserPage used to exercise
// EmailExtractJob.BrowserActions without a real browser. The email pipeline
// only touches the page at Level 3, which these tests never reach (emails are
// found earlier via HTTP), so the navigation methods are not expected to run.
type fakeBrowserPage struct {
	gotoCalls  int
	closeCalls int
}

func (f *fakeBrowserPage) Goto(string, scrapemate.WaitUntilState) (*scrapemate.PageResponse, error) {
	f.gotoCalls++

	return &scrapemate.PageResponse{StatusCode: 200}, nil
}
func (f *fakeBrowserPage) URL() string                                  { return "" }
func (f *fakeBrowserPage) Content() (string, error)                     { return "", nil }
func (f *fakeBrowserPage) Reload(scrapemate.WaitUntilState) error       { return nil }
func (f *fakeBrowserPage) Screenshot(bool) ([]byte, error)              { return nil, nil }
func (f *fakeBrowserPage) Eval(string, ...any) (any, error)             { return nil, nil }
func (f *fakeBrowserPage) WaitForURL(string, time.Duration) error       { return nil }
func (f *fakeBrowserPage) WaitForSelector(string, time.Duration) error  { return nil }
func (f *fakeBrowserPage) WaitForTimeout(time.Duration)                 {}
func (f *fakeBrowserPage) Locator(string) scrapemate.Locator            { return nil }
func (f *fakeBrowserPage) Close() error                                 { f.closeCalls++; return nil }
func (f *fakeBrowserPage) Unwrap() any                                  { return nil }

func homepageWithEmailServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<a href="mailto:info@testbiz.com">Email us</a>
		</body></html>`)
	}))
	t.Cleanup(srv.Close)

	return srv
}

// In JS mode scrapemate calls BrowserActions and releases the browser page
// back into the pool as soon as it returns. The email pipeline must therefore
// run inside BrowserActions (while the page is exclusively owned), not in
// Process (which runs after the page has been recycled).
func TestEmailJobRunsPipelineInBrowserActions(t *testing.T) {
	srv := homepageWithEmailServer(t)

	entry := &Entry{WebSite: srv.URL}
	job := NewEmailJob("parent", entry)

	page := &fakeBrowserPage{}
	resp := job.BrowserActions(context.Background(), page)
	require.NoError(t, resp.Error)

	// Pipeline must have completed while the page was owned.
	require.Equal(t, []string{"info@testbiz.com"}, entry.Emails)
	require.Equal(t, "found", entry.EmailStatus)

	// Process must not re-run the pipeline; it just emits the result.
	result, next, err := job.Process(context.Background(), &resp)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Equal(t, entry, result)
	require.Equal(t, []string{"info@testbiz.com"}, entry.Emails)
}

// In non-JS mode BrowserActions is never invoked, so Process must run the
// pipeline itself (HTTP-only, no browser fallback).
func TestEmailJobRunsPipelineInProcessWhenNoBrowser(t *testing.T) {
	srv := homepageWithEmailServer(t)

	entry := &Entry{WebSite: srv.URL}
	job := NewEmailJob("parent", entry)

	resp := scrapemate.Response{StatusCode: 200}
	result, next, err := job.Process(context.Background(), &resp)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Equal(t, entry, result)
	require.Equal(t, []string{"info@testbiz.com"}, entry.Emails)
	require.Equal(t, "found", entry.EmailStatus)
}
