package gmaps

import (
	"context"
	"fmt"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"
)

const browserFetchTimeout = 15 * time.Second

// pageBrowserFetcher adapts a scrapemate.BrowserPage into a BrowserFetcher
// so the email pipeline can use an already-allocated browser page for
// Level 3 rendering.
type pageBrowserFetcher struct {
	page scrapemate.BrowserPage
}

func (f *pageBrowserFetcher) FetchWithBrowser(ctx context.Context, url string) (result string, err error) {
	if f.page == nil {
		return "", fmt.Errorf("browser page is nil, cannot fetch %s", url)
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// Bound every page operation. page.Goto with WaitUntilNetworkIdle can
	// otherwise hang on Playwright's 30s+ default. Setting the timeout on the
	// underlying page lets the calls below stay SYNCHRONOUS: when this function
	// returns, no operation is still in flight on the page. That is essential
	// because the page is recycled into scrapemate's pool the instant
	// BrowserActions returns — a leaked goroutine still driving the page would
	// corrupt the next job that reuses it.
	if pw, ok := f.page.Unwrap().(playwright.Page); ok {
		pw.SetDefaultNavigationTimeout(float64(browserFetchTimeout.Milliseconds()))
		pw.SetDefaultTimeout(float64(browserFetchTimeout.Milliseconds()))
	}

	// Recover from panics raised when the underlying Playwright page has been
	// closed or recycled by scrapemate's browser pool between jobs.
	defer func() {
		if r := recover(); r != nil {
			result = ""
			err = fmt.Errorf("browser page panic (page likely recycled): %v", r)
		}
	}()

	if _, gotoErr := f.page.Goto(url, scrapemate.WaitUntilNetworkIdle); gotoErr != nil {
		return "", gotoErr
	}

	f.page.WaitForTimeout(500 * time.Millisecond)

	return f.page.Content()
}
