package feishu

import "strings"

// helpText is the reply to a help request or an unrecognized query. It is plain
// Chinese and states that a query is read-only.
const helpText = `用法：
- @我 额度 / 状态：整体额度与结论
- @我 codex|claude|gemini|gemini-cli|antigravity|ollama：只看某个渠道（gemini 与 gemini-cli 均指 Gemini CLI）
- @我 异常 / 告警：只看异常项
- @我 帮助：显示本提示
查询只读最近一次采集数据，不会触发上游刷新。`

// intent is the parsed meaning of an @-mention or private-chat message. Parsing
// is rule-based on purpose: no language model is involved, so the answers stay
// predictable and cheap, and no query reaches the Agent.
type intent struct {
	kind     intentKind
	provider string
}

type intentKind int

const (
	intentHelp     intentKind = iota // usage hint
	intentUnknown                    // unrecognized: reply with a short hint
	intentStatus                     // overall status
	intentAbnormal                   // only the abnormal items
	intentProvider                   // filtered to one channel
)

// providerVocab maps accepted aliases to display channels, in a fixed priority
// order. Order matters when a query names several channels.
var providerVocab = []providerAlias{
	{"codex", "codex"},
	{"claude", "claude"},
	{"gemini-cli", "gemini-cli"},
	{"gemini", "gemini-cli"},
	{"antigravity", "antigravity"},
	{"anti", "antigravity"},
	{"ollama", "ollama"},
}

type providerAlias struct {
	alias    string
	provider string
}

var helpWords = []string{"帮助", "help", "怎么用", "用法", "指令"}

var statusWords = []string{"额度", "quota", "状态", "status", "用量", "余额", "余量", "情况", "怎么样"}

var abnormalWords = []string{"异常", "告警", "警告", "问题", "故障", "掉线", "失效", "alerts", "alert", "abnormal", "warning"}

// parseIntent classifies a message. The caller has already stripped the @mention
// token, so text is the human query.
func parseIntent(text string) intent {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return intent{kind: intentStatus}
	}
	for _, word := range helpWords {
		if matchWord(t, word) {
			return intent{kind: intentHelp}
		}
	}
	for _, word := range abnormalWords {
		if matchWord(t, word) {
			return intent{kind: intentAbnormal}
		}
	}
	// A channel filter is read before generic status words so "codex 额度" stays
	// scoped to Codex.
	for _, alias := range providerVocab {
		if matchWord(t, alias.alias) {
			return intent{kind: intentProvider, provider: alias.provider}
		}
	}
	for _, word := range statusWords {
		if matchWord(t, word) {
			return intent{kind: intentStatus}
		}
	}
	return intent{kind: intentUnknown}
}

// matchWord matches CJK words by substring (they have no latin word boundaries)
// and latin words on a boundary, so "quota" does not fire inside "quotax".
func matchWord(text, word string) bool {
	if isASCII(word) {
		return containsToken(text, word)
	}
	return strings.Contains(text, word)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// containsToken reports whether token appears in text bounded by non-letter
// characters.
func containsToken(text, token string) bool {
	index := 0
	for {
		i := strings.Index(text[index:], token)
		if i < 0 {
			return false
		}
		start := index + i
		end := start + len(token)
		if boundary(text, start-1) && boundary(text, end) {
			return true
		}
		index = start + 1
		if index >= len(text) {
			return false
		}
	}
}

func boundary(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	c := s[i]
	return !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'))
}

// stripMentions removes the "@_user_N" placeholders Feishu injects into message
// text, so they do not pollute intent matching.
func stripMentions(text string) string {
	fields := strings.Fields(text)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.HasPrefix(field, "@_") {
			continue
		}
		out = append(out, field)
	}
	return strings.Join(out, " ")
}
