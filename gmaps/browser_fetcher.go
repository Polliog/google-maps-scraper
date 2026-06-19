package gmaps

import (
	"context"
	"fmt"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"
)

const browserFetchTimeout = 15 * time.Second

// browserFetchWatchdog is the hard ceiling for a single browser fetch. It sits
// above browserFetchTimeout (Playwright's own navigation timeout) so a healthy
// but slow page still completes through Playwright. It only fires when a
// Playwright call hangs PAST its own timeout — the driver-level deadlock that no
// in-Playwright timeout can recover from. When it fires we abandon the page so
// the worker is freed and the batch keeps moving instead of stalling until the
// scrapemate inactivity timeout kills the whole job.
//
// It is a var (not a const) so tests can shrink it.
var browserFetchWatchdog = browserFetchTimeout + 5*time.Second

// pageBrowserFetcher adapts a scrapemate.BrowserPage into a BrowserFetcher
// so the email pipeline can use an already-allocated browser page for
// Level 3 rendering.
type pageBrowserFetcher struct {
	page scrapemate.BrowserPage
}

func (f *pageBrowserFetcher) FetchWithBrowser(ctx context.Context, url string) (string, error) {
	if f.page == nil {
		return "", fmt.Errorf("browser page is nil, cannot fetch %s", url)
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// Bound every page operation at the Playwright level. page.Goto with
	// WaitUntilNetworkIdle would otherwise hang on Playwright's 30s+ default.
	if pw, ok := f.page.Unwrap().(playwright.Page); ok {
		pw.SetDefaultNavigationTimeout(float64(browserFetchTimeout.Milliseconds()))
		pw.SetDefaultTimeout(float64(browserFetchTimeout.Milliseconds()))
	}

	type fetchResult struct {
		html string
		err  error
	}

	// Drive the page from a dedicated goroutine so a hung Playwright call can
	// never pin the worker. The channel is buffered (cap 1) so this goroutine
	// can always deliver its result — or its recovered panic — and exit, even
	// after the watchdog has already given up waiting for it.
	ch := make(chan fetchResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- fetchResult{"", fmt.Errorf("browser page panic (page likely recycled): %v", r)}
			}
		}()

		if _, gotoErr := f.page.Goto(url, scrapemate.WaitUntilNetworkIdle); gotoErr != nil {
			ch <- fetchResult{"", gotoErr}

			return
		}

		f.page.WaitForTimeout(500 * time.Millisecond)

		html, contentErr := f.page.Content()
		ch <- fetchResult{html, contentErr}
	}()

	watchdog := time.NewTimer(browserFetchWatchdog)
	defer watchdog.Stop()

	select {
	case r := <-ch:
		return r.html, r.err
	case <-ctx.Done():
		return "", f.abandon(url, ctx.Err())
	case <-watchdog.C:
		return "", f.abandon(url, fmt.Errorf("browser fetch watchdog fired after %s", browserFetchWatchdog))
	}
}

// abandon closes the page whose operation is stuck. Closing serves two
// purposes, and doing it SYNCHRONOUSLY is deliberate:
//
//  1. It forces the in-flight Playwright call to error out, so the goroutine
//     started by FetchWithBrowser unblocks and exits instead of lingering. By
//     the time FetchWithBrowser returns, no goroutine is still driving this
//     page — which is exactly what prevents the next job (handed the recycled
//     page) from colliding with a leftover one. That two-goroutines-on-one-page
//     collision is the deadlock this whole path guards against.
//  2. A closed page is detected by scrapemate's slot pool, which then recreates
//     a fresh page for the next job rather than recycling this poisoned one.
//
// If Close itself hangs because the driver is fully wedged, only THIS worker
// stalls; the other workers keep the batch moving — far better than every
// worker stalling until the inactivity timeout ends the job.
func (f *pageBrowserFetcher) abandon(url string, cause error) error {
	if f.page != nil {
		_ = f.page.Close()
	}

	return fmt.Errorf("abandoned browser fetch for %s: %w", url, cause)
}
