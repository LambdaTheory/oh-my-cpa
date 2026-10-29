package ollama

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"golang.org/x/net/html"
)

// headingQualifierLimit bounds the trailing word a card heading may add to a
// known label. A longer remainder is a different section, not a badge.
const headingQualifierLimit = 12

var (
	USD_AMOUNT       = regexp.MustCompile(`^(?:USD\s*\$?|\$)\s*(0|[1-9][0-9]*|[1-9][0-9]{0,2}(?:,[0-9]{3})+)(?:\.([0-9]{1,2}))?$`)
	REMAINING_AMOUNT = regexp.MustCompile(`(?i)^(?:remaining(?: credits)?|balance remaining)\s*:?\s*(.+)$|^(.+?)\s+(?:remaining(?: credits)?|balance remaining)$`)
	// BALANCE_AMOUNT is the label the page uses now: the credits card prints a
	// single "Total" line for the balance (monthly allowance plus added credits)
	// where it used to print "... remaining". Without it the collector reports no
	// trustworthy reading even though the page is intact and the session is valid.
	BALANCE_AMOUNT = regexp.MustCompile(`(?i)^total\s*:?\s*(.+)$|^(.+?)\s+total$`)
	USED_AMOUNT    = regexp.MustCompile(`(?i)^(?:used this month|this month used|monthly usage|monthly credits used)\s*:?\s*(.+)$|^(.+?)\s+(?:used this month|monthly credits used)$`)
	MODEL_REQUESTS = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._:/-]*)\s+([0-9]+(?:,[0-9]{3})*)\s+requests?$`)
)

// Read the labelled credit card in the response DOM. Searching flattened page
// text for a dollar sign can turn unrelated prices or damaged text into balances.
func parseCredits(document string) *quota.OllamaCredits {
	root, err := html.Parse(strings.NewReader(document))
	if err != nil {
		return nil
	}
	var heading *html.Node
	walkElements(root, func(node *html.Node) {
		if heading == nil && matchesHeading(node, "Usage credits") {
			heading = node
		}
	})
	if heading == nil {
		return nil
	}
	credits := &quota.OllamaCredits{Source: "settings_usage_credits"}
	card := headingContainer(heading)
	if card != nil {
		credits.RemainingCents, _ = labelledAmount(card, REMAINING_AMOUNT)
		if credits.RemainingCents == nil {
			// The card no longer says "remaining": it prints one "Total" line for the
			// balance. Read that instead of failing, but keep requiring the used figure
			// below, so a page that lost both labels still reads as no reading rather
			// than as a zero balance.
			credits.RemainingCents, _ = labelledAmount(card, BALANCE_AMOUNT)
		}
		credits.UsedCents, _ = labelledAmount(card, USED_AMOUNT)
		walkElements(card, func(node *html.Node) {
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
