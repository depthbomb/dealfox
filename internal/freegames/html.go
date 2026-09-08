package freegames

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func document(body []byte) *html.Node {
	doc, _ := html.Parse(bytes.NewReader(body))

	return doc
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}

	return ""
}

func hasClass(n *html.Node, name string) bool {
	return strings.Contains(" "+attr(n, "class")+" ", " "+name+" ")
}

func nodes(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var found []*html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n == nil {
			return
		}

		if match(n) {
			found = append(found, n)
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}

	visit(n)

	return found
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n == nil || n.Data == "script" || n.Data == "style" {
			return
		}

		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}

	visit(n)

	return strings.Join(strings.Fields(b.String()), " ")
}

func plain(value string) string {
	return textContent(document([]byte(value)))
}

func resolve(base, target string) string {
	u, err := url.Parse(base)
	if err != nil || target == "" {
		return ""
	}

	v, err := url.Parse(target)
	if err != nil {
		return ""
	}

	return u.ResolveReference(v).String()
}

func metadata(doc *html.Node, key string) string {
	for _, n := range nodes(doc, func(n *html.Node) bool {
		return n.Data == "meta"
	}) {
		if attr(n, "property") == key || attr(n, "name") == key {
			return attr(n, "content")
		}
	}

	return ""
}

// Decode a JSON literal embedded in a script without executing JavaScript.
func scriptJSON(body []byte, marker string) object {
	_, tail, ok := strings.Cut(string(body), marker)
	if !ok {
		return nil
	}

	tail = strings.TrimLeft(tail, " \t\r\n=")
	decoder := json.NewDecoder(strings.NewReader(tail))
	decoder.UseNumber()

	var value object
	if decoder.Decode(&value) != nil {
		return nil
	}

	return value
}

func excluded(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{"free to play", "free-to-play", "free weekend", "free starter", "free access", "demo", "trial", "playtest", "soundtrack", "concert", "mp4", "dlc", "add-on", "subscription", "ubisoft+", "beta"} {
		if strings.Contains(value, marker) {
			return true
		}
	}

	return false
}
