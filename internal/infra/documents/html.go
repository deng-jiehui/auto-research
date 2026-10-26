package documents

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"golang.org/x/net/html"
)

func parseHTML(data []byte) (string, error) {
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	var article *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "article" || n.Data == "main") {
			if article == nil {
				article = n
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(root)
	if article == nil {
		article = root
	}
	parts := make([]string, 0)
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "nav", "footer", "header", "head", "noscript":
				return
			}
			if n.Data == "p" || n.Data == "li" || n.Data == "table" || n.Data == "pre" || (len(n.Data) == 2 && n.Data[0] == 'h' && n.Data[1] >= '1' && n.Data[1] <= '6') {
				text := strings.Join(strings.Fields(htmlNodeText(n)), " ")
				if text != "" {
					if n.Data[0] == 'h' {
						text = strings.Repeat("#", int(n.Data[1]-'0')) + " " + text
					}
					parts = append(parts, text)
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(article)
	text := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if len([]rune(text)) < 20 {
		return "", fmt.Errorf("%w：HTML 没有可提取的论文正文", domain.ErrUnavailable)
	}
	return text, nil
}

func htmlNodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "annotation", "noscript":
			return ""
		case "br":
			return "\n"
		}
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(htmlNodeText(c))
		if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th" || c.Data == "tr") {
			b.WriteString(" ")
		}
	}
	return b.String()
}
