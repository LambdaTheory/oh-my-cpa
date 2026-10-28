package feishu

import "regexp"

const (
	RecipientGroup  = "group"
	RecipientPerson = "person"
)

var RECIPIENT_CHAT_ID = regexp.MustCompile(`^oc_[A-Za-z0-9_-]+$`)

// RecipientKind accepts only the two identifiers supported by the notification
// contract. Blank and malformed values never authorize sends or queries.
func RecipientKind(value string) string {
	if RECIPIENT_CHAT_ID.MatchString(value) {
		return RecipientGroup
	}
	if OAUTH_OPEN_ID.MatchString(value) {
		return RecipientPerson
	}
	return ""
}

func canAnswerRecipient(recipient string, in Inbound) bool {
	switch RecipientKind(recipient) {
	case RecipientGroup:
		return in.IsDirect() || in.ChatType == "group" && in.BotMentioned
	case RecipientPerson:
		return in.IsDirect() && in.SenderOpenID == recipient
	default:
		return false
	}
}
