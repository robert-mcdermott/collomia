package web

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// This file turns a retrieved response into text a model can read.
//
// A page's HTML is mostly not the page: navigation, cookie banners, scripts,
// and analytics outweigh the prose on most sites, and feeding all of it to a
// model wastes context and buries the answer. The reduction here is
// deliberately structural rather than statistical — drop the elements that are
// never content, prefer the element the page itself marked as its main
// content, and keep the headings, lists, code, and tables that carry meaning.

// Format selects how a page is rendered.
type Format string

const (
	// FormatText is readable prose with structure preserved and link targets
	// dropped. It is the default because most reading does not need URLs.
	FormatText Format = "text"
	// FormatMarkdown additionally keeps link targets, so the model can follow
	// a page's own references with a second fetch.
	FormatMarkdown Format = "markdown"
	// FormatRaw returns the body unchanged. It is for APIs and source files,
	// where reduction would destroy the thing being read.
	FormatRaw Format = "raw"
)

// ParseFormat validates a model-supplied format.
func ParseFormat(value string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(value))) {
	case "":
		return FormatText, nil
	case FormatText:
		return FormatText, nil
	case FormatMarkdown:
		return FormatMarkdown, nil
	case FormatRaw:
		return FormatRaw, nil
	}
	return "", fmt.Errorf("unsupported format %q: use text, markdown, or raw", value)
}

// maxExtractedBytes bounds the rendered result. The agent applies its own
// output cap and retains the remainder for read_tool_result, so this only has
// to stop one pathological page from becoming a pathological allocation.
const maxExtractedBytes = 1 << 20

// Extract renders a page's body as text.
func Extract(page Page, format Format) (string, error) {
	kind := mediaKind(page.ContentType, page.Body)
	if format == FormatRaw {
		if kind == kindBinary {
			return "", unreadable(page)
		}
		return clip(string(page.Body)), nil
	}
	switch kind {
	case kindHTML:
		base, err := url.Parse(page.URL)
		if err != nil {
			base = nil
		}
		text, err := renderHTML(page.Body, base, format)
		if err != nil {
			return "", err
		}
		return clip(text), nil
	case kindText:
		return clip(string(page.Body)), nil
	default:
		return "", unreadable(page)
	}
}

func unreadable(page Page) error {
	kind := strings.TrimSpace(page.ContentType)
	if kind == "" {
		kind = "an unrecognized type"
	}
	return fmt.Errorf("%s returned %s (%d bytes), which is not text. web_fetch returns readable text only", page.URL, kind, len(page.Body))
}

func clip(value string) string {
	if len(value) <= maxExtractedBytes {
		return value
	}
	end := maxExtractedBytes
	for end > 0 && (value[end]&0xC0) == 0x80 {
		end--
	}
	return value[:end] + "\n\n[content truncated at 1 MiB]"
}

type mediaClass int

const (
	kindBinary mediaClass = iota
	kindHTML
	kindText
)

// mediaKind classifies a response. The declared Content-Type is preferred; a
// server that declares nothing gets a sniff, because plenty of them do.
func mediaKind(contentType string, body []byte) mediaClass {
	kind := strings.ToLower(strings.TrimSpace(contentType))
	if semi := strings.IndexByte(kind, ';'); semi >= 0 {
		kind = strings.TrimSpace(kind[:semi])
	}
	switch {
	case kind == "":
		// fall through to sniffing
	case strings.Contains(kind, "html"):
		return kindHTML
	case strings.HasPrefix(kind, "text/"),
		strings.HasSuffix(kind, "+json"), strings.HasSuffix(kind, "+xml"),
		kind == "application/json", kind == "application/xml",
		kind == "application/javascript", kind == "application/x-ndjson",
		kind == "application/yaml", kind == "application/toml":
		return kindText
	default:
		return kindBinary
	}
	head := body
	if len(head) > 1024 {
		head = head[:1024]
	}
	lowered := strings.ToLower(string(head))
	if strings.Contains(lowered, "<!doctype html") || strings.Contains(lowered, "<html") {
		return kindHTML
	}
	for _, b := range head {
		if b == 0 {
			return kindBinary
		}
	}
	return kindText
}

// skipped elements never carry page content. Removing them by name is
// predictable in a way that a readability score is not: a page cannot fall on
// the wrong side of a threshold and lose its own text.
var skipped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true,
	atom.Svg: true, atom.Canvas: true, atom.Iframe: true, atom.Object: true,
	atom.Embed: true, atom.Video: true, atom.Audio: true, atom.Map: true,
	atom.Nav: true, atom.Header: true, atom.Footer: true, atom.Aside: true,
	atom.Menu: true, atom.Dialog: true, atom.Select: true, atom.Datalist: true,
	atom.Head: true,
}

// hiddenRoles mark landmarks a page has told us are not its content.
var hiddenRoles = map[string]bool{
	"navigation": true, "banner": true, "contentinfo": true, "search": true,
	"complementary": true, "menubar": true, "toolbar": true, "alert": true,
}

func renderHTML(body []byte, base *url.URL, format Format) (string, error) {
	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("the page could not be parsed as HTML: %w", err)
	}
	r := &renderer{base: base, format: format}
	if title := strings.TrimSpace(textOf(findFirst(root, atom.Title))); title != "" {
		r.out.WriteString("# " + title + "\n\n")
	}
	r.walk(contentRoot(root))
	return tidy(r.out.String()), nil
}

// contentRoot picks the subtree to render. A page that marked its own main
// content is taken at its word; otherwise the body is used and the skip list
// removes the boilerplate.
func contentRoot(root *html.Node) *html.Node {
	for _, name := range []atom.Atom{atom.Main, atom.Article} {
		if node := findFirst(root, name); node != nil && len(strings.Fields(textOf(node))) > 40 {
			return node
		}
	}
	if body := findFirst(root, atom.Body); body != nil {
		return body
	}
	return root
}

func findFirst(node *html.Node, name atom.Atom) *html.Node {
	if node.Type == html.ElementNode && node.DataAtom == name {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirst(child, name); found != nil {
			return found
		}
	}
	return nil
}

func attr(node *html.Node, name string) string {
	for _, a := range node.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

func textOf(node *html.Node) string {
	if node == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type == html.ElementNode && skipped[n.DataAtom] {
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return collapse(b.String())
}

var whitespace = regexp.MustCompile(`[ \t\x{00a0}\r\n]+`)

func collapse(value string) string {
	return strings.TrimSpace(whitespace.ReplaceAllString(value, " "))
}

type renderer struct {
	out    strings.Builder
	base   *url.URL
	format Format
	// preformatted suppresses whitespace collapsing inside <pre>.
	preformatted bool
	// listDepth and ordinals track nested list numbering.
	ordinals []int
}

func (r *renderer) walk(node *html.Node) {
	if node == nil {
		return
	}
	switch node.Type {
	case html.TextNode:
		if r.preformatted {
			r.out.WriteString(node.Data)
			return
		}
		if text := whitespace.ReplaceAllString(node.Data, " "); strings.TrimSpace(text) != "" {
			r.out.WriteString(text)
		} else if text != "" && !strings.HasSuffix(r.out.String(), " ") {
			r.out.WriteString(" ")
		}
		return
	case html.ElementNode:
	default:
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			r.walk(child)
		}
		return
	}
	if r.skip(node) {
		return
	}
	switch node.DataAtom {
	case atom.Br:
		r.out.WriteString("\n")
		return
	case atom.Hr:
		r.block("\n---\n")
		return
	case atom.Img:
		if alt := collapse(attr(node, "alt")); alt != "" {
			r.out.WriteString("[image: " + alt + "]")
		}
		return
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(node.Data[1] - '0')
		if text := textOf(node); text != "" {
			r.block("\n" + strings.Repeat("#", level) + " " + text + "\n")
		}
		return
	case atom.Pre:
		r.renderPre(node)
		return
	case atom.A:
		r.renderLink(node)
		return
	case atom.Code:
		if r.format == FormatMarkdown && !r.preformatted {
			if text := textOf(node); text != "" {
				r.out.WriteString("`" + text + "`")
			}
			return
		}
	case atom.Li:
		r.renderListItem(node)
		return
	case atom.Ul, atom.Ol:
		r.ordinals = append(r.ordinals, listStart(node))
		r.block("\n")
		r.children(node)
		r.ordinals = r.ordinals[:len(r.ordinals)-1]
		r.block("\n")
		return
	case atom.Tr:
		r.renderRow(node)
		return
	case atom.P, atom.Div, atom.Section, atom.Blockquote, atom.Table,
		atom.Dl, atom.Dt, atom.Dd, atom.Figure, atom.Figcaption,
		atom.Details, atom.Summary, atom.Form, atom.Fieldset, atom.Article, atom.Main:
		r.block("\n")
		r.children(node)
		r.block("\n")
		return
	}
	r.children(node)
}

// skip reports the elements this page has marked as non-content.
func (r *renderer) skip(node *html.Node) bool {
	if skipped[node.DataAtom] {
		return true
	}
	if attr(node, "hidden") != "" || strings.EqualFold(attr(node, "aria-hidden"), "true") {
		return true
	}
	return hiddenRoles[strings.ToLower(strings.TrimSpace(attr(node, "role")))]
}

func (r *renderer) children(node *html.Node) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		r.walk(child)
	}
}

// block writes a separator without stacking blank lines, so a deeply nested
// document does not render as mostly whitespace.
func (r *renderer) block(value string) {
	current := r.out.String()
	trailing := len(current) - len(strings.TrimRight(current, "\n"))
	needed := len(value) - len(strings.TrimLeft(value, "\n"))
	if trailing >= 2 && strings.TrimSpace(value) == "" {
		return
	}
	if strings.TrimSpace(value) == "" {
		for i := 0; i < needed-trailing; i++ {
			r.out.WriteString("\n")
		}
		return
	}
	r.out.WriteString(value)
}

func (r *renderer) renderPre(node *html.Node) {
	text := strings.Trim(rawText(node), "\n")
	if text == "" {
		return
	}
	r.block("\n")
	r.out.WriteString("```\n" + text + "\n```\n")
}

// rawText preserves whitespace, which is the entire point of <pre>.
func rawText(node *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			b.WriteString(n.Data)
		case n.Type == html.ElementNode && n.DataAtom == atom.Br:
			b.WriteString("\n")
		case n.Type == html.ElementNode && skipped[n.DataAtom]:
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return b.String()
}

func (r *renderer) renderLink(node *html.Node) {
	text := textOf(node)
	if text == "" {
		return
	}
	if r.format != FormatMarkdown {
		r.out.WriteString(text)
		return
	}
	href := strings.TrimSpace(attr(node, "href"))
	target := r.resolve(href)
	if target == "" || strings.HasPrefix(href, "#") {
		r.out.WriteString(text)
		return
	}
	r.out.WriteString("[" + text + "](" + target + ")")
}

// resolve turns a page-relative href into a URL the model can fetch. A
// relative link the model cannot resolve is a link it will guess at.
func (r *renderer) resolve(href string) string {
	if href == "" {
		return ""
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if r.base != nil {
		parsed = r.base.ResolveReference(parsed)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.String()
	}
	return ""
}

func listStart(node *html.Node) int {
	if node.DataAtom != atom.Ol {
		return 0
	}
	start := 1
	if value := attr(node, "start"); value != "" {
		if _, err := fmt.Sscanf(value, "%d", &start); err != nil {
			start = 1
		}
	}
	return start
}

func (r *renderer) renderListItem(node *html.Node) {
	marker := "- "
	depth := len(r.ordinals)
	if depth > 0 && r.ordinals[depth-1] > 0 {
		marker = fmt.Sprintf("%d. ", r.ordinals[depth-1])
		r.ordinals[depth-1]++
	}
	indent := strings.Repeat("  ", max(depth-1, 0))
	r.block("\n")
	r.out.WriteString(indent + marker)
	r.children(node)
	r.block("\n")
}

func (r *renderer) renderRow(node *html.Node) {
	var cells []string
	for cell := node.FirstChild; cell != nil; cell = cell.NextSibling {
		if cell.Type == html.ElementNode && (cell.DataAtom == atom.Td || cell.DataAtom == atom.Th) {
			cells = append(cells, textOf(cell))
		}
	}
	if len(cells) == 0 {
		return
	}
	r.block("\n")
	r.out.WriteString("| " + strings.Join(cells, " | ") + " |\n")
}

var blankRun = regexp.MustCompile(`\n{3,}`)
var trailingSpace = regexp.MustCompile(`[ \t]+\n`)

func tidy(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = trailingSpace.ReplaceAllString(value, "\n")
	value = blankRun.ReplaceAllString(value, "\n\n")
	return strings.TrimSpace(value)
}
