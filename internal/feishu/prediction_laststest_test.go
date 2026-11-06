package feishu

// Three-state conclusions used by digest tests. A pointer keeps "unknown"
// distinct from "will not last".
var (
	cannotLast   = false
	lastsToReset = true
)
