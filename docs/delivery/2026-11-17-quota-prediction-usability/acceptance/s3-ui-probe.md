# S3 dashboard prediction UI probe

Run: 2026-11-17, worktree `E:/works/Newoahil__oh-my-cpa/11-17-t8-s4-codex`.

```bash
pnpm check:ui --scenario dashboard-quota-prediction --workers 1
```

Result: `1 scenario(s) passed in 9.2s`; the scenario's checks covered the four-field
risk rows, rolling-window presentation, the collapsed idle line, the folded stale
section, refresh, diagnostics and the confirmed Feishu send.

## What the fixture exercises

- Antigravity: a rolling window (`is_rolling_window: true`) shows only `Actual` and
  the next reset. The row has no `Expected`, `Deviation`, exhaustion ETA or
  lasts-until-reset tag, and the panel summary does not count it.
- Ollama and OpenRouter: `used_percent: 0` with `usage_requests: 0` are merged into
  one panel line. The OpenRouter group disappears because it has no other row, and
  the line expands to both covered windows.
- Claude: a `reading_stale: true` row is folded into `Expired / unavailable (1)`;
  it stays out of the main list and out of the cannot/unknown counts.
- Codex, Claude and Step keep ordinary risk rows and the existing dashboard order.

## Evidence

- `s3-collapsed-classes.png`: rolling row, ordinary risk rows, and the open expired
  section containing the stale row.
- `s3-idle-expanded.png`: the idle line expanded to the two covered windows and the
  footer counting only risk/stale rows.

## C4 field

The prediction response does not emit `capacity_unavailable: "stale"`. `stale` is a
credential-capacity reason; prediction accounting marks an expired reading with
`reading_stale: true`. The panel uses that flag to fold the row. No backend
projection change was needed.
