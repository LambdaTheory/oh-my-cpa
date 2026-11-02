package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// These pin the second P1: the member route used to PathUnescape unconditionally, which
// is a double decode whenever chi matched the already-decoded URL.Path. The pair that
// made it visible is "a%2Fb" and "a/b" in one group - both are names the add endpoint
// accepts, and the escaped request for the literal "a%2Fb" used to remove "a/b".
//
// Each case asserts the SURVIVORS, not just the status: a wrong removal answers 200 too,
// so a status check alone would pass against the defect.

// memberSurvivors reads the group's members straight from the database, ordered.
func memberSurvivors(t *testing.T, client *http.Client, base, groupID string) []string {
	t.Helper()
	status, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	requireStatus(t, status, payload, http.StatusOK)
	for _, group := range decodeModelGroupList(t, payload).Groups {
		if group.ID == groupID {
			return wireMemberNames(group)
		}
	}
	t.Fatalf("group %s is not listed", groupID)
	return nil
}

// TestModelGroupMemberPathDecodesExactlyOnce covers the names individually: a literal
// percent that is not an escape, a name that looks like an escape sequence, and a name
// that genuinely contains a slash. Each must be removed by its own escaped path.
func TestModelGroupMemberPathDecodesExactlyOnce(t *testing.T) {
	for _, member := range []string{
		"100%",         // a literal percent that is not a valid escape
		"a%2Fb",        // looks like an escape sequence, is not one
		"openai/gpt-5", // a real slash
		"50%off/2025",  // both at once
	} {
		t.Run(member, func(t *testing.T) {
			client, base, _ := startDashboardTestServer(t, nil)
			_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
			group := groupNamed(t, decodeModelGroupList(t, payload), "审查组")

			// The add endpoint takes the name literally, which is what makes it real.
			body := `{"models":["` + strings.ReplaceAll(member, `\`, `\\`) + `","keep"]}`
			status, added := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members", body)
			requireStatus(t, status, added, http.StatusOK)

			escaped := url.PathEscape(member)
			status, removed := modelGroupRequest(t, client, base, http.MethodDelete,
				modelGroupBase+"/"+group.ID+"/members/"+escaped, "")
			requireStatus(t, status, removed, http.StatusOK)

			survivors := memberSurvivors(t, client, base, group.ID)
			if strings.Join(survivors, ",") != "keep" {
				t.Fatalf("removing %q left %v, want only keep", member, survivors)
			}
		})
	}
}

// TestModelGroupMemberPathRemovesTheRightOneOfTwoLookAlikeNames is the exact defect the
// review reproduced: with both "a%2Fb" and "a/b" present, the escaped request for the
// former must remove the former and leave the latter.
func TestModelGroupMemberPathRemovesTheRightOneOfTwoLookAlikeNames(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	group := groupNamed(t, decodeModelGroupList(t, payload), "力工组")

	// Two distinct stored names. Written as raw JSON so the percent signs reach the
	// handler verbatim rather than being reinterpreted by a format verb.
	status, body := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members",
		`{"models":["a%2Fb","a/b"]}`)
	requireStatus(t, status, body, http.StatusOK)
	if got := strings.Join(memberSurvivors(t, client, base, group.ID), ","); got != "a%2Fb,a/b" {
		t.Fatalf("precondition: members = %s", got)
	}

	// The escaped form of the literal "a%2Fb": the percent itself becomes %25.
	escapedLiteral := url.PathEscape("a%2Fb")
	if escapedLiteral != "a%252Fb" {
		t.Fatalf("url.PathEscape(%q) = %q, expected the percent to double", "a%2Fb", escapedLiteral)
	}
	status, body = modelGroupRequest(t, client, base, http.MethodDelete,
		modelGroupBase+"/"+group.ID+"/members/"+escapedLiteral, "")
	requireStatus(t, status, body, http.StatusOK)

	if got := strings.Join(memberSurvivors(t, client, base, group.ID), ","); got != "a/b" {
		t.Fatalf("after removing the literal a%%2Fb the group holds %q, want a/b", got)
	}

	// And the other direction: the slash-bearing name is removed by its own escape.
	status, body = modelGroupRequest(t, client, base, http.MethodDelete,
		modelGroupBase+"/"+group.ID+"/members/"+url.PathEscape("a/b"), "")
	requireStatus(t, status, body, http.StatusOK)
	if survivors := memberSurvivors(t, client, base, group.ID); len(survivors) != 0 {
		t.Fatalf("after removing both, the group still holds %v", survivors)
	}
}
