package gmaps

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"
)

// hangingPage simulates a Playwright page whose navigation deadlocks at the
// driver level: Goto never returns on its own (it ignores its own timeout).
// Closing the page unblocks the hung call, exactly like real Playwright, which
// errors every pending operation when the page is closed.
type hangingPage struct {
	fakeBrowserPage

	mu        sync.Mutex
	released  bool
	release   chan struct{}
	started   chan struct{}
	startOnce sync.Once
}

func newHangingPage() *hangingPage {
	return &hangingPage{release: make(chan struct{}), started: make(chan struct{})}
}

func (p *hangingPage) Goto(string, scrapemate.WaitUntilState) (*scrapemate.PageResponse, error) {
	p.startOnce.Do(func() { close(p.started) })

	<-p.release // block until the page is closed (or forever)

	return &scrapemate.PageResponse{StatusCode: 200}, nil
}

func (p *hangingPage) Close() error {
	p.mu.Lock()
	if !p.released {
		p.released = true
		close(p.release)
	}
	p.closeCalls++
	p.mu.Unlock()

	return nil
}

func (p *hangingPage) closes() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.closeCalls
}

// A browser fetch that hangs must NOT pin the worker forever. The watchdog has
// to free it and abandon (close) the poisoned page so the batch keeps moving.
func TestFetchWithBrowserWatchdogFreesWorkerOnHang(t *testing.T) {
	orig := browserFetchWatchdog
	browserFetchWatchdog = 100 * time.Millisecond
	t.Cleanup(func() { browserFetchWatchdog = orig })

	page := newHangingPage()
	f := &pageBrowserFetcher{page: page}

	done := make(chan struct{})

	var (
		html string
		err  error
	)

	go func() {
		html, err = f.FetchWithBrowser(context.Background(), "http://example.com")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("FetchWithBrowser never returned: the watchdog failed to free the worker")
	}

	require.Error(t, err)
	require.Empty(t, html)
	require.GreaterOrEqual(t, page.closes(), 1, "a hung page must be abandoned (closed) so it is not recycled")
}

// A cancelled context must also free the worker promptly and abandon the page.
func TestFetchWithBrowserAbandonsOnContextCancel(t *testing.T) {
	page := newHangingPage()
	f := &pageBrowserFetcher{page: page}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	var err error

	go func() {
		_, err = f.FetchWithBrowser(ctx, "http://example.com")
		close(done)
	}()

	<-page.started // ensure the fetch is in flight before cancelling
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("FetchWithBrowser ignored context cancellation")
	}

	require.Error(t, err)
	require.GreaterOrEqual(t, page.closes(), 1, "a cancelled fetch must abandon (close) the page")
}

// The happy path must return content and must NOT close the page (it stays in
// scrapemate's pool for reuse).
func TestFetchWithBrowserHappyPathKeepsPage(t *testing.T) {
	page := &fakeBrowserPage{}
	f := &pageBrowserFetcher{page: page}

	html, err := f.FetchWithBrowser(context.Background(), "http://example.com")
	require.NoError(t, err)
	require.Equal(t, "", html)
	require.Equal(t, 1, page.gotoCalls)
	require.Equal(t, 0, page.closeCalls, "a healthy page must not be closed")
}
