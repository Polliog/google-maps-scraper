package gmaps

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/require"
)

func TestDiscoverContactPages(t *testing.T) {
	html := `<html><body>
		<a href="https://example.com/contact">Contact Us</a>
		<a href="https://example.com/about">About</a>
		<a href="https://example.com/products">Products</a>
		<a href="https://other.com/contact">Other Site</a>
		<a href="https://example.com/contatti">Contatti</a>
		<a href="https://example.com/file.pdf">Download PDF</a>
		<a href="#section">Section</a>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	pages := discoverContactPages(doc, "https://example.com")

	// Should find /contact, /about, /contatti but not /products, not other domain, not .pdf, not #section
	require.GreaterOrEqual(t, len(pages), 2)
	require.LessOrEqual(t, len(pages), 5)

	// /contact should be prioritized over /about
	require.Contains(t, pages[0], "/contact")
}

func TestDiscoverContactPagesWithTextMatch(t *testing.T) {
	html := `<html><body>
		<a href="https://example.com/reach-out">Get in Touch</a>
		<a href="https://example.com/team">Our Team</a>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	pages := discoverContactPages(doc, "https://example.com")
	require.Len(t, pages, 1)
	require.Contains(t, pages[0], "/reach-out")
}

func TestDiscoverContactPagesMaxLimit(t *testing.T) {
	var links strings.Builder
	for i := 0; i < 20; i++ {
		links.WriteString(`<a href="https://example.com/contact-`)
		links.WriteByte(byte('a' + i))
		links.WriteString(`">Contact</a>`)
	}
	html := `<html><body>` + links.String() + `</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	pages := discoverContactPages(doc, "https://example.com")
	require.LessOrEqual(t, len(pages), 5)
}
