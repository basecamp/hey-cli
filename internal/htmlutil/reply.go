package htmlutil

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ReplyForm is the envelope HEY prepares for replying to an entry.
type ReplyForm struct {
	To  []string
	CC  []string
	BCC []string
}

// ParseReplyFormHTML extracts the recipients from HEY's reply form.
func ParseReplyFormHTML(pageHTML string) (ReplyForm, error) {
	doc, err := html.Parse(strings.NewReader(pageHTML))
	if err != nil {
		return ReplyForm{}, fmt.Errorf("parse reply page: %w", err)
	}

	form := findReplyForm(doc)
	if form == nil {
		return ReplyForm{}, fmt.Errorf("reply page does not contain an entry reply form")
	}

	to := replyFormAddresses(form, "entry[addressed][directly][]")
	cc := replyFormAddresses(form, "entry[addressed][copied][]")
	bcc := replyFormAddresses(form, "entry[addressed][blindcopied][]")
	if len(to)+len(cc)+len(bcc) == 0 {
		return ReplyForm{}, fmt.Errorf("reply form does not contain recipients")
	}

	return ReplyForm{To: to, CC: cc, BCC: bcc}, nil
}

func findReplyForm(node *html.Node) *html.Node {
	if node.Type == html.ElementNode && node.Data == "form" {
		action := getAttr(node, "action")
		if parsed, err := url.Parse(action); err == nil && isReplyFormPath(parsed.Path) {
			return node
		}
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if form := findReplyForm(child); form != nil {
			return form
		}
	}
	return nil
}

func isReplyFormPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "entries" || parts[2] != "replies" {
		return false
	}
	entryID, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && entryID > 0
}

func replyFormAddresses(node *html.Node, name string) []string {
	var inputValues, selectValues []string
	selectFound := false
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && getAttr(current, "name") == name {
			switch current.Data {
			case "select":
				if !selectFound {
					selectFound = true
					selectValues = replyOptionValues(current)
				}
			case "input":
				if value := strings.TrimSpace(getAttr(current, "value")); value != "" {
					inputValues = append(inputValues, value)
				}
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	if selectFound && len(selectValues) > 0 {
		return selectValues
	}
	return inputValues
}

func replyOptionValues(selectNode *html.Node) []string {
	var all, selected []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "option" {
			value := strings.TrimSpace(getAttr(node, "value"))
			if value != "" {
				all = append(all, value)
				if hasAttr(node, "selected") {
					selected = append(selected, value)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(selectNode)
	if len(selected) > 0 {
		return selected
	}
	return all
}

func hasAttr(node *html.Node, key string) bool {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return true
		}
	}
	return false
}
