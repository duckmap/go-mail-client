package main

import (
	"strings"

	"golang.org/x/net/html"
)

// HTMLToText — простой конвертер HTML в читаемый текст для терминала.
func HTMLToText(src string) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var sb strings.Builder
	skip := 0 // глубина внутри <script>/<style>
	pre := 0  // внутри <pre>
	var pendingLink string
	newline := func() {
		s := sb.String()
		if s == "" || strings.HasSuffix(s, "\n") {
			return
		}
		sb.WriteString("\n")
	}
	blankline := func() {
		newline()
		s := sb.String()
		if strings.HasSuffix(s, "\n\n") || s == "" {
			return
		}
		sb.WriteString("\n")
	}

	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return cleanText(sb.String())
		case html.TextToken:
			if skip > 0 {
				continue
			}
			txt := string(z.Text())
			if pre > 0 {
				sb.WriteString(txt)
			} else {
				txt = strings.Join(strings.Fields(txt), " ")
				if txt == "" {
					continue
				}
				// не добавлять пробел сразу после перевода строки
				s := sb.String()
				if s != "" && !strings.HasSuffix(s, "\n") {
					sb.WriteString(" ")
				}
				sb.WriteString(txt)
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			switch tag {
			case "script", "style", "head", "title", "noscript", "template":
				if tt == html.StartTagToken {
					skip++
				}
			case "pre":
				pre++
				blankline()
			case "br":
				newline()
			case "p", "div", "section", "article", "table", "tr", "blockquote",
				"h1", "h2", "h3", "h4", "h5", "h6", "hr", "ul", "ol", "dl", "figure", "header", "footer":
				blankline()
			case "li", "dt":
				newline()
				sb.WriteString("  • ")
			case "dd":
				newline()
				sb.WriteString("    ")
			case "td", "th":
				s := sb.String()
				if s != "" && !strings.HasSuffix(s, "\n") && !strings.HasSuffix(s, " ") {
					sb.WriteString("  ")
				}
			case "a":
				if hasAttr {
					for {
						k, v, more := z.TagAttr()
						if string(k) == "href" {
							pendingLink = string(v)
						}
						if !more {
							break
						}
					}
				}
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			switch tag {
			case "script", "style", "head", "title", "noscript", "template":
				if skip > 0 {
					skip--
				}
			case "pre":
				if pre > 0 {
					pre--
				}
				newline()
			case "a":
				if pendingLink != "" {
					href := strings.TrimSpace(pendingLink)
					low := strings.ToLower(href)
					if !strings.HasPrefix(low, "mailto:") && !strings.HasPrefix(low, "#") &&
						!strings.HasPrefix(low, "javascript:") {
						// показать ссылку, если её текст не совпадает с URL
						cur := sb.String()
						last := cur[strings.LastIndexAny(cur, "\n")+1:]
						if strings.TrimSpace(last) != href && len(href) < 200 {
							sb.WriteString(" [" + href + "]")
						}
					}
					pendingLink = ""
				}
			case "p", "div", "section", "article", "table", "tr", "blockquote",
				"h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "dl", "figure", "header", "footer":
				blankline()
			}
		}
	}
}

// cleanText убирает избыточные пробелы/пустые строки.
func cleanText(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, ln := range lines {
		ln = strings.TrimRight(ln, " \t")
		if ln == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
