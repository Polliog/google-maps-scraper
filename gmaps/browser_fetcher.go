package gmaps

import (
	"context"
	"time"

	"github.com/gosom/scrapemate"
)

// pageBrowserFetcher adapts a scrapemate.BrowserPage into a BrowserFetcher
// so the email pipeline can use an already-allocated browser page for
// Level 3 rendering.
type pageBrowserFetcher struct {
	page scrapemate.BrowserPage
}

func (f *pageBrowserFetcher) FetchWithBrowser(ctx context.Context, url string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	_, err := f.page.Goto(url, scrapemate.WaitUntilNetworkIdle)
	if err != nil {
		return "", err
	}

	// Small extra wait for any lazy-loaded content that fires after networkidle.
	f.page.WaitForTimeout(500 * time.Millisecond)

	return f.page.Content()
}
