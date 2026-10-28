// Package feishu implements the Oh My CPA Feishu (Lark) bot: outbound
// notifications to one configured group or person plus a long-connection listener
// for help, status, exception and channel queries.
//
// Scope of the first batch (lane C): reuse the bot long connection, answer
// read-only queries from OMC's newest quota data, and denoise notifications so a
// problem is announced once and recovery is not nagged. It deliberately does NOT
// call a language model, does not hold an Agent session, and never triggers an
// upstream quota refresh: a query only reads the report the background
// collectors already produced and reports how fresh it is.
//
// Deployment constraint: run exactly ONE replica. Feishu caps a self-built app
// at 50 concurrent long connections and delivers each event to just one of them
// (cluster delivery, not broadcast). A second replica would make replies and
// card interactions land on a process at random, so a message could be answered
// by an instance that does not hold the newest report.
//
// Authorization boundary: quota notifications and read-only queries address the
// configured recipient. A group permits direct queries and mentions in any joined
// group; a person permits only that person's direct messages. Legacy groups remain
// silent until their exact recipient is confirmed.
// The dedicated OAuth code group keeps its separate existing authorization path.
// Cards contain only display content and one console link.
//
// SECURITY: the app secret is encrypted at rest and never appears in the
// configuration struct, in DTOs, in logs or in error strings. The bot never
// echoes a raw upstream error payload; only scrubbed, human-readable text is
// rendered.
package feishu
