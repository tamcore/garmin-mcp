package loginweb

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// assets holds the whole user interface. Embedding it is what makes the pages
// dependency-free: there is no CDN to reach, no font to fetch, and no file to read
// from disk at run time, so the login form works on a machine with no network route
// to anything but Garmin.
//
//go:embed pages
var assets embed.FS

// The page names, which are also the template file names.
const (
	pageDisclosure  = "disclosure"
	pageCredentials = "credentials"
	pageMFA         = "mfa"
	pageDone        = "done"
	pageNotFound    = "notfound"
)

// stylesheetPath is the one asset a page references. It is same-origin, which is
// what lets the Content-Security-Policy forbid everything else.
const stylesheetPath = "/style.css"

// pageData is everything a template may render.
//
// The field set is closed, and there is no field for an email address, a password,
// a one-time code, or a transaction capability. A template therefore cannot print
// one, however it is written.
type pageData struct {
	// CSRFToken is the form token the next submission must carry.
	CSRFToken string
	// Message is server-authored text explaining a refusal. It never quotes a
	// submitted value.
	Message string
	// MFAMethod is the delivery method Garmin named, and DeliveryUncertain reports
	// that delivery could not be confirmed.
	MFAMethod         string
	DeliveryUncertain bool

	// The field bounds, so the form advertises the limits the server enforces.
	MaxEmailLen    int
	MaxPasswordLen int
	MaxCodeLen     int
}

// newPageData returns the data every page starts from.
func newPageData(token, message string) pageData {
	return pageData{
		CSRFToken:      token,
		Message:        message,
		MaxEmailLen:    MaxEmailLen,
		MaxPasswordLen: MaxPasswordLen,
		MaxCodeLen:     MaxCodeLen,
	}
}

// pageSet holds one parsed template per page, each already combined with the shared
// document. Parsing happens once, at construction, so a broken template is a
// start-up failure rather than a blank page during a login.
type pageSet struct {
	templates  map[string]*template.Template
	stylesheet []byte
}

// loadPages parses the loopback templates and reads the stylesheet.
func loadPages() (*pageSet, error) {
	return loadPageSet("pages",
		[]string{pageDisclosure, pageCredentials, pageMFA, pageDone, pageNotFound})
}

// loadPageSet parses one profile's templates from dir. Each profile has its own
// document and its own set of pages; the stylesheet is shared, because it is the
// only asset and it carries no policy.
func loadPageSet(dir string, names []string) (*pageSet, error) {
	stylesheet, err := assets.ReadFile("pages/style.css")
	if err != nil {
		return nil, fmt.Errorf("loginweb: reading the embedded stylesheet: %w", err)
	}

	set := &pageSet{
		templates:  make(map[string]*template.Template),
		stylesheet: stylesheet,
	}
	for _, name := range names {
		parsed, err := template.New("base.html").ParseFS(assets,
			dir+"/base.html", dir+"/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("loginweb: parsing the %s page: %w", name, err)
		}
		set.templates[name] = parsed
	}
	return set, nil
}

// render writes one page with the given status.
//
// A template failure after the status line can only truncate the page:
// html/template escapes as it writes, so nothing half-escaped reaches the browser.
//
// The data argument is the profile's own page data type. It is typed as any so the
// two profiles can render different field sets through one page set; each set only
// ever receives the type its own templates were written against.
func (p *pageSet) render(w http.ResponseWriter, status int, name string, data any) {
	parsed, ok := p.templates[name]
	if !ok {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = parsed.ExecuteTemplate(w, "base", data)
}

// serveStylesheet writes the embedded stylesheet.
func (p *pageSet) serveStylesheet(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(p.stylesheet)
}

// sanitizedMessage keeps a page message on one line and bounded, so no text a
// template renders can become a wall of output or carry a control character.
func sanitizedMessage(message string) string {
	const maxMessageLen = 200

	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, message)
	if len(cleaned) > maxMessageLen {
		return cleaned[:maxMessageLen]
	}
	return cleaned
}
