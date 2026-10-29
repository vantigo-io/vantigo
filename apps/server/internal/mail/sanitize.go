package mail

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// SanitizeHTML answers a received message's HTML safe to show in the
// inbox's sandboxed frame. A pre-pass over golang.org/x/net/html's tree
// rewrites each img whose src is "cid:<id>" to inlineURL(id) — removing it
// when inlineURL answers false or a URL other than an attachment download
// path — removes every other img but a base64 data: PNG, GIF, JPEG or WebP
// (remote images and tracking pixels go), drops background and srcset
// attributes, and drops a link's or an image map area's href unless it is
// http, https or mailto; no relative URL is kept but the img src above.
// bluemonday's UGC policy then removes everything executable — script,
// style, iframe, object, embed, form, input, meta, base, link, svg, math,
// every on* attribute — and keeps a short list of typographic and box style
// properties, never url(…), position or expression. Links get
// rel="nofollow noreferrer noopener" and target="_blank".
func SanitizeHTML(source string, inlineURL func(contentID string) (string, bool)) string {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}
	body := findElement(doc, atom.Body)
	if body == nil {
		return ""
	}
	prepass(body, inlineURL)

	var b strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return ""
		}
	}
	return strings.TrimSpace(inboundPolicy.Sanitize(b.String()))
}

// inlineScheme carries a resolved cid: image through bluemonday, which keeps
// no relative URL: the pre-pass writes the download path as
// "vantigo-inline:<attachment id>", the policy admits that scheme on an
// img's src alone, and RewriteSrc turns it back into the path.
const inlineScheme = "vantigo-inline"

var (
	// inlineDownloadPath is the only relative URL an img may keep: an
	// inbound attachment's download, which the inbox fetches and inlines.
	inlineDownloadPath = regexp.MustCompile(`^/api/v1/communications/attachments/([0-9a-f-]{36})/download$`)
	inlineSource       = regexp.MustCompile(`^` + inlineScheme + `:[0-9a-f-]{36}$`)
	dataImage          = regexp.MustCompile(`^data:image/(png|gif|jpeg|webp);base64,[A-Za-z0-9+/=\s]+$`)
	inboundPolicy      = newInboundPolicy()
)

func newInboundPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.AllowDataURIImages()
	p.AllowAttrs("src").Matching(inlineSource).OnElements("img")
	p.AllowURLSchemeWithCustomPolicy(inlineScheme, func(u *url.URL) bool { return inlineSource.MatchString(u.String()) })
	p.RewriteSrc(func(u *url.URL) {
		if u.Scheme == inlineScheme {
			*u = url.URL{Path: "/api/v1/communications/attachments/" + u.Opaque + "/download"}
		}
	})
	p.AllowRelativeURLs(false)
	p.AllowStyles(
		"color", "background-color",
		"font-family", "font-size", "font-weight", "font-style",
		"text-align", "text-decoration", "line-height",
		"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
		"padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
		"border", "border-top", "border-right", "border-bottom", "border-left",
		"border-color", "border-style", "border-width", "border-collapse", "border-spacing", "border-radius",
		"border-top-color", "border-right-color", "border-bottom-color", "border-left-color",
		"border-top-style", "border-right-style", "border-bottom-style", "border-left-style",
		"border-top-width", "border-right-width", "border-bottom-width", "border-left-width",
		"width", "height", "vertical-align", "white-space",
	).Globally()
	return p
}

func findElement(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, a); found != nil {
			return found
		}
	}
	return nil
}

func prepass(n *html.Node, inlineURL func(string) (string, bool)) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			c.Attr = withoutAttrs(c.Attr, "background", "srcset")
			switch c.DataAtom {
			case atom.Img:
				if !rewriteImage(c, inlineURL) {
					n.RemoveChild(c)
					c = next
					continue
				}
			case atom.A, atom.Area:
				c.Attr = safeHref(c.Attr)
			}
			prepass(c, inlineURL)
		}
		c = next
	}
}

// rewriteImage keeps img n only with a cid: src inlineURL resolves to a
// download path, or a data: image of a safe type; false removes it.
func rewriteImage(n *html.Node, inlineURL func(string) (string, bool)) bool {
	for i, a := range n.Attr {
		if a.Namespace != "" || a.Key != "src" {
			continue
		}
		src := strings.TrimSpace(a.Val)
		if len(src) > 4 && strings.EqualFold(src[:4], "cid:") {
			if inlineURL == nil {
				return false
			}
			id, err := url.PathUnescape(src[4:])
			if err != nil {
				id = src[4:]
			}
			resolved, ok := inlineURL(strings.Trim(id, "<> "))
			m := inlineDownloadPath.FindStringSubmatch(resolved)
			if !ok || m == nil {
				return false
			}
			n.Attr[i].Val = inlineScheme + ":" + m[1]
			return true
		}
		return dataImage.MatchString(src)
	}
	return false
}

func safeHref(attrs []html.Attribute) []html.Attribute {
	out := attrs[:0]
	for _, a := range attrs {
		if a.Namespace == "" && a.Key == "href" {
			u, err := url.Parse(strings.TrimSpace(a.Val))
			if err != nil {
				continue
			}
			switch strings.ToLower(u.Scheme) {
			case "http", "https", "mailto":
			default:
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

func withoutAttrs(attrs []html.Attribute, keys ...string) []html.Attribute {
	out := attrs[:0]
	for _, a := range attrs {
		drop := false
		for _, k := range keys {
			if a.Key == k {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, a)
		}
	}
	return out
}
