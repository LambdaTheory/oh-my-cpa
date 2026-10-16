package feishu

import "time"

// mustTime parses an RFC3339 instant or fails the test.
func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
