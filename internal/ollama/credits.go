package ollama

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// headingQualifierLimit bounds the trailing word a card heading may add to a
// known label. A longer remainder is a different section, not a badge.
const headingQualifierLimit = 12

var (
	USD_AMOUNT     = regexp.MustCompile(`^(?:USD\s*\$?|\$)\s*(0|[1-9][0-9]*|[1-9][0-9]{0,2}(?:,[0-9]{3})+)(?:\.([0-9]{1,2}))?$`)
	USED_AMOUNT    = regexp.MustCompile(`(?i)^(?:used this month|this month used|monthly usage|monthly credits used)\s*:?\s*(.+)$|^(.+?)\s+(?:used this month|monthly credits used)$`)
	MODEL_REQUESTS = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._:/-]*)\s+([0-9]+(?:,[0-9]{3})*)\s+requests?$`)

	// Parsing shape ported from CodexBar (MIT), OllamaUsageParser.swift.
	//
	// The live settings page splits the card across sibling containers: the
	// heading sits in a header strip while the amounts sit beside it. The wallet
	// is therefore a text slice bounded by the heading and the card's end tag, not
	// a DOM subtree, so no ancestor can widen or narrow the scope.
	CREDITS_HEADING = regexp.MustCompile(`(?is)<h[1-6][^>]*>\s*Usage credits(?:\s*<span[^>]*>[^<]*</span\s*>)?\s*</h[1-6]\s*>`)
	CREDITS_END     = regexp.MustCompile(`(?i)</section\s*>|<h[1-6]\b`)
	// CREDIT_BALANCE is the first span after the heading, optionally wrapped in
	// divs; its text must be a complete USD amount. A page that prints a bare
	// "Total" line instead stays unreadable rather than borrowing a number the
	// wallet never stated as its balance.
	CREDIT_BALANCE = regexp.MustCompile(`(?is)^\s*(?:<div\b[^>]*>\s*)*<span\b[^>]*>([^<]*)</span\s*>`)
	CREDIT_USED    = regexp.MustCompile(`(?is)<span\b[^>]*>\s*Monthly credits used\s*</span\s*>\s*<span\b[^>]*>([^<]*)</span\s*>`)
	// SCRIPT_STYLE is stripped before the slice is measured so a tag inside a
	// script or style body cannot move the card boundary.
	SCRIPT_STYLE = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>|<style\b[^>]*>.*?</style\s*>`)
)

// Read the wallet figures from the credits card. Searching flattened page text
// for a dollar sign can turn unrelated prices or damaged text into balances.
func parseCredits(document string) *quota.OllamaCredits {
	wallet, ok := creditsWallet(document)
	if !ok {
		return nil
	}
	root, err := html.Parse(strings.NewReader(document))
	if err != nil {
		return nil
	}
	walletNodes := parseWalletFragment(wallet)

	credits := &quota.OllamaCredits{Source: "settings_usage_credits"}
	credits.RemainingCents = creditBalance(wallet)
	if credits.RemainingCents != nil {
		// Only read the spend next to a balance from the same slice. A slice without
		// one has no proven card boundary (div-only markup offers none), so a "used
		// this month" string further down may belong to a sibling section; leaving the
		// figure absent keeps the reading unreadable instead of borrowing that spend.
		credits.UsedCents = creditUsed(wallet, walletNodes)
	}
	if walletNodes != nil {
		walkElements(walletNodes, func(node *html.Node) {
			text := nodeText(node)
			if strings.HasPrefix(strings.ToLower(text), "monthly credits refill") && len(text) <= 160 {
				if credits.RefillText == "" || len(text) < len(credits.RefillText) {
					credits.RefillText = text
				}
			}
		})
	}

	if credits.RemainingCents != nil && credits.UsedCents != nil {
		total := *credits.RemainingCents + *credits.UsedCents
		credits.TotalCents = &total
		if total > 0 && *credits.UsedCents > 0 {
			percent := float64(*credits.UsedCents) / float64(total) * 100
			credits.UsedPercent = &percent
		}
	}
	// Model counts belong to their own monthly table, never to CPA telemetry.
	walkElements(root, func(node *html.Node) {
		if !matchesHeading(node, "Models used this month") {
			return
		}
		if section := headingContainer(node); section != nil {
			credits.Models = readModelRequests(section)
		}
	})
	return credits
}

// creditsWallet returns the credits card as a text slice: everything after the
// "Usage credits" heading up to the first section close, the next heading, or the
// end of the document.
func creditsWallet(document string) (string, bool) {
	page := SCRIPT_STYLE.ReplaceAllString(document, "")
	page = strings.ReplaceAll(page, "&nbsp;", " ")
	heading := CREDITS_HEADING.FindStringIndex(page)
	if heading == nil {
		return "", false
	}
	wallet := page[heading[1]:]
	if end := CREDITS_END.FindStringIndex(wallet); end != nil {
		wallet = wallet[:end[0]]
	}
	return wallet, true
}

func creditBalance(wallet string) *int64 {
	match := CREDIT_BALANCE.FindStringSubmatch(wallet)
	if match == nil {
		return nil
	}
	return parseUSDCents(match[1])
}

func creditUsed(wallet string, nodes *html.Node) *int64 {
	if match := CREDIT_USED.FindStringSubmatch(wallet); match != nil {
		if cents := parseUSDCents(match[1]); cents != nil {
			return cents
		}
	}
	// Older cards print "$0.02 used this month" with the amount first and no label
	// element, which USED_AMOUNT still recognizes on the slice's own text.
	if nodes == nil {
		return nil
	}
	cents, _ := labelledAmount(nodes, USED_AMOUNT)
	return cents
}

// parseWalletFragment parses the slice in a div context, so tags left open by the
// cut stay inside the returned tree instead of escaping into a wider document.
func parseWalletFragment(fragment string) *html.Node {
	nodes, err := html.ParseFragment(strings.NewReader(fragment), &html.Node{
		Type:     html.ElementNode,
		Data:     "div",
		DataAtom: atom.Div,
	})
	if err != nil {
		return nil
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, node := range nodes {
		root.AppendChild(node)
	}
	return root
}

func labelledAmount(root *html.Node, pattern *regexp.Regexp) (*int64, bool) {
	values := make(map[string]bool)
	walkElements(root, func(node *html.Node) {
		if match := pattern.FindStringSubmatch(nodeText(node)); match != nil {
			// Prefer the smallest labelled fact; a surrounding card may begin with
			// the same label but also contain the other amount and unrelated copy.
			hasLabelledChild := false
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walkElements(child, func(descendant *html.Node) {
					if pattern.MatchString(nodeText(descendant)) {
						hasLabelledChild = true
					}
				})
			}
			if hasLabelledChild {
				return
			}
			values[strings.TrimSpace(match[1]+match[2])] = true
		}
	})
	if len(values) != 1 {
		return nil, len(values) > 0
	}
	for value := range values {
		return parseUSDCents(value), true
	}
	return nil, false
}

func parseUSDCents(value string) *int64 {
	match := USD_AMOUNT.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return nil
	}
	dollars, err := strconv.ParseInt(strings.ReplaceAll(match[1], ",", ""), 10, 64)
	// Keep both cents and their sum exact in the JavaScript wire consumer.
	if err != nil || dollars > 10_000_000_000_000 {
		return nil
	}
	fraction, _ := strconv.ParseInt((match[2] + "00")[:2], 10, 64)
	cents := dollars*100 + fraction
	return &cents
}

func readModelRequests(root *html.Node) []quota.OllamaModelUsage {
	var models []quota.OllamaModelUsage
	seen := make(map[string]bool)
	walkElements(root, func(node *html.Node) {
		match := MODEL_REQUESTS.FindStringSubmatch(nodeText(node))
		if match == nil || seen[match[1]] {
			return
		}
		requests, err := strconv.ParseInt(strings.ReplaceAll(match[2], ",", ""), 10, 64)
		if err != nil || requests > 9_007_199_254_740_991 {
			return
		}
		seen[match[1]] = true
		models = append(models, quota.OllamaModelUsage{Model: match[1], Requests: requests})
	})
	return models
}

func walkElements(node *html.Node, visit func(*html.Node)) {
	if node.Type == html.ElementNode {
		if node.Data == "script" || node.Data == "style" || node.Data == "template" {
			return
		}
		visit(node)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkElements(child, visit)
	}
}

func nodeText(node *html.Node) string {
	var text strings.Builder
	var appendText func(*html.Node)
	appendText = func(current *html.Node) {
		if current.Data == "script" || current.Data == "style" || current.Data == "template" {
			return
		}
		if current.Type == html.TextNode {
			text.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && child.Data != "span" && child.Data != "strong" && child.Data != "b" {
				text.WriteByte(' ')
			}
			appendText(child)
			if child.Type == html.ElementNode {
				text.WriteByte(' ')
			}
		}
	}
	appendText(node)
	return strings.Join(strings.Fields(text.String()), " ")
}

func matchesHeading(node *html.Node, label string) bool {
	if strings.EqualFold(nodeText(node), label) {
		return true
	}
	if node.Data != "h2" && node.Data != "h3" && node.Data != "h4" {
		return false
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode && strings.EqualFold(strings.TrimSpace(child.Data), label) {
			return true
		}
	}
	// The card heading carries a short qualifier now ("Usage credits max"). Accept it
	// only on a heading and only when the remainder is a badge, so an unrelated
	// section that merely begins with the same words cannot be mistaken for it.
	if qualifier, ok := headingQualifier(nodeText(node), label); ok {
		return len([]rune(qualifier)) <= headingQualifierLimit
	}
	return false
}

// headingQualifier returns the trailing word a heading adds to a known label.
func headingQualifier(text, label string) (string, bool) {
	lowered := strings.ToLower(strings.TrimSpace(text))
	target := strings.ToLower(label)
	if !strings.HasPrefix(lowered, target) {
		return "", false
	}
	rest := strings.TrimSpace(lowered[len(target):])
	if rest == "" {
		return "", false
	}
	return rest, true
}

// Semantic sections bound a fact's ownership. With div-only markup, only the
// heading's immediate parent is eligible; walking to a shared ancestor can borrow
// unrelated monetary facts from a sibling card when this one is empty.
func headingContainer(heading *html.Node) *html.Node {
	for parent := heading.Parent; parent != nil; parent = parent.Parent {
		if parent.Data == "main" || parent.Data == "body" || parent.Data == "html" {
			break
		}
		if parent.Data == "section" || parent.Data == "article" {
			return parent
		}
	}
	parent := heading.Parent
	if parent == nil || parent.Data == "main" || parent.Data == "body" || parent.Data == "html" {
		return nil
	}
	return parent
}
