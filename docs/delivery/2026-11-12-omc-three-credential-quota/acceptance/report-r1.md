---
type: test-report
slug: 2026-11-12-omc-three-credential-quota-r1
status: done
updated: 2026-11-12
verdict: 要修
summary: "27 条用例：完整通过 4、部分通过 9、要修 1、阻塞 1、未测 12；P0 未全部覆盖，本轮不达验收通过"
---

# 测试报告：第 1 轮

**结论**：本轮未达到验收通过条件。可复跑的浏览器场景 `plugin-quota` 在提交 `f7c1c971` 上 30 条断言全部通过，覆盖三家凭证行、CMC 套餐/credits、OPC 三窗口与 note、Ollama cookie 形态、stale 缓存读数、旧宿主 providers 回退；本轮新增的三家失败/缺失夹具未写进场景断言、未跑，C-18 的 429 退避按代码复核不满足用例预期，另有 12 条用例未测。P0 中 C-13、C-15、C-17 未测，C-01/C-03/C-05/C-16/C-26 只覆盖了一部分。

- 测试对象：分支 `Newoahil/11-12-t18-s18-claude`，基线提交 `f7c1c971`；工作区另有本轮的夹具扩展（`scripts/fake-cpa.mjs`、`scripts/acceptance/auth-files.mjs`、`scripts/browser-acceptance.mjs`），随本报告一起提交。
- 环境：Windows 11；`pnpm verify:browser --scenario plugin-quota` 自起 fake CPA + Windows 二进制 + 无头 Chromium，隔离数据目录，只连 127.0.0.1；`TZ=Asia/Shanghai`，`ASSISTANT_NO_SEND=1`。
- 证据目录：`E:/works/Homan-context/inbox/agents/验收/2026-11-12-omc-three-credential-quota/`（`e2e-report.md`、`plugin-quota-run.log`、`fake-cpa-test.log`、`type-check.log`、`runtime/app.log`）。

## 结果

| 用例 | AC | 类型 | 结果 | 证据 | 说明 |
|---|---|---|---|---|---|
| C-01 三家各自成行 | AC-1 | 浏览器 | 部分通过 | `plugin-quota-run.log` | 三个夹具凭证各为独立行；过滤标签、品牌图标、raw provider key 不显示未断言（S18 夹具已备，场景未扩） |
| C-02 只经 OMC 读额度 | AC-2 | 浏览器 | 通过（自动化间接） | 同上 | 无外联 guard、同源请求无失败、插件 POST body 仅 `{"auth_index":…}`；未做人工 DevTools 域名记录 |
| C-03 CMC 套餐/积分/窗口 | AC-3 | 浏览器 | 部分通过 | 同上 | 行内 `Pro`、`321 credits`（非 `$321`）、80%/55%、raw 窗口名；新鲜态抽屉字段与重置时刻未逐项断言 |
| C-04 OPC 三窗口/无套餐余额 | AC-4 | 浏览器 | 通过 | 同上 | 行内 2 窗口、无 5h/30 days 文案、无 Credit 余额；抽屉 3/3、0%/42%/21%；重置时刻未逐个比对 |
| C-05 Ollama 两形态 | AC-5 | 浏览器 | 部分通过 | 同上 | cookie 形态 `12.34 USD`/60%/80% 通过；api_key 形态（8.75 USD + 空态）S18 夹具已加，未跑 |
| C-06 缺失字段不写 0 | AC-3/4/5 | 浏览器 | 未测 | — | S18 已加 F-MISSING 载荷（CMC 无套餐/余额、Ollama 无窗口、OPC 单 weekly 且无 remainingFraction），场景断言未写未跑 |
| C-07 stale 200 | AC-7 | 浏览器 | 通过 | `plugin-quota-run.log` | 「缓存读数」标记、提示含「插件返回的是上次成功读取…· upstream timeout」、数值保持 321 credits/80%/55%、观测显示「1 小时前」非「刚刚」、抽屉重复标记 |
| C-08 429 无缓存 UI | AC-7 | 浏览器 | 未测 | — | S18 已加固定 429+Retry-After 凭证，场景断言未写未跑 |
| C-09 502 无缓存 UI | AC-7 | 浏览器 | 未测 | — | S18 已加固定 502 凭证，场景断言未写未跑 |
| C-10 Ollama 余额显隐不互相污染 | AC-6 | 浏览器 | 阻塞 | — | 缺 F-OWN-OLLAMA（`ollama:fixture-own`）夹具，无法验证 OMC 自有账号一侧 |
| C-11 窗口 note 可见 | AC-4/7 | 浏览器 | 部分通过 | `plugin-quota-run.log` | Rolling note 可见且标题仍为 `OpenCode Go · Rolling`；「其余两窗口不显示该 note」未断言 |
| C-12 抽屉全量窗口 | AC-4 | 浏览器 | 部分通过 | 同上 | OPC 行 2/抽屉 3 通过；CMC 新鲜态抽屉的 2 窗口与顺序未单独断言 |
| C-13 既有七家回归 | AC-10 | 浏览器 | 未测 | — | 本轮只跑 `plugin-quota`；`auth-files` 夹具开关已改（避免增量凭证挤掉第一页），未跑默认全量套件 |
| C-14 英文界面 | AC-1/3/4/5/7 | 浏览器 | 未测 | — | 未切英文环境 |
| C-15 概览原始 key/无秘密 | AC-1/2 | 接口 | 未测 | — | 代码复核字段与预期一致，但本轮未发请求验证 |
| C-16 单条刷新打对插件 | AC-9 | 接口 | 部分通过 | `plugin-quota-run.log` | 行内刷新恰好一次插件 POST、body 仅 auth_index 已由场景证明；单条响应无 `outcome` 字段（见「范围外观察」），响应内 `observed_at_ms` 未断言 |
| C-17 批量逐条、失败隔离 | AC-9 | 接口 | 未测 | — | 未跑 |
| C-18 429+Retry-After 记限流 | AC-7/9 | 接口 | 要修 | 代码复核（见下节） | 插件 429 未映射为 `quota.RateLimitError`：无 `retry_after_ms`/`retry_at_ms`、无退避，重试窗口内第二次刷新会再次 POST；概览 `observed_at_ms` 无快照时被填为当前时刻 |
| C-19 旧宿主 `/plugins` 匹配 | AC-8 | 接口 | 未测 | — | Ollama 经 `/plugins` 匹配分支未跑 |
| C-20 旧宿主 providers 匹配 | AC-8 | 接口 | 部分通过 | `plugin-quota-run.log` | 场景中 CMC 在 legacy 宿主下走 `GET /credentials/quota/providers` 后正确 POST；每凭证调用次数与 OPC 分支未断言 |
| C-21 匹配不到明确报错 | AC-8 | 接口 | 未测 | — | S18 已加 unmatched 凭证，未跑 |
| C-22 quota_plugin_id 优先 | AC-8 | 接口 | 未测 | — | S18 已加 decoy 插件控制，未跑 |
| C-23 概览/页面轮询不触发上游 | AC-2/9 | 接口 | 未测 | — | 未跑 |
| C-24 自动刷新轮覆盖三家 | AC-9 | 接口 | 未测 | `runtime/app.log`（旁证） | 仅观察到旧 live 环境三十分钟轮次（21:58/22:28/22:58，各 `due:11 refreshed:7 failed:4`），前置不满足用例要求（不是 interval=5、不是全新数据目录、当时为全局 rate-limit 模式），不能算通过 |
| C-25 失败保留旧快照 | AC-7/9 | 接口 | 未测 | — | 未跑 |
| C-26 plugin-quota 场景 | AC-11 | e2e | 部分通过 | `plugin-quota-run.log` | 30 条断言、exit 0（提交 `f7c1c971`）；未含过滤标签、OLK 形态、缺失字段、429/502 UI、英文界面 |
| C-27 fake CPA 单测 | AC-11 | e2e | 通过 | `fake-cpa-test.log`；本轮复跑 | 18/18；本轮扩展夹具后再次 18/18。新增的 OLK/429/502/unmatched/meta/decoy 分支没有新增单测覆盖 |

计数：完整通过 4（C-02、C-04、C-07、C-27），部分通过 9（C-01、C-03、C-05、C-11、C-12、C-16、C-19、C-20、C-26），要修 1（C-18），阻塞 1（C-10），未测 12（C-06、C-08、C-09、C-13、C-14、C-15、C-17、C-21、C-22、C-23、C-24、C-25）。C-28 及以后不在本轮范围。

## 已执行命令

| 命令 | 结果 | 说明 |
|---|---|---|
| `node --test scripts/fake-cpa.test.mjs` | 18/18 通过（2026-11-12 本轮复跑） | 在含本轮夹具扩展的工作区上执行 |
| `pnpm verify:browser --scenario plugin-quota` | 30 checks passed，exit 0（2026-11-12，提交 `f7c1c971`） | 证据 `plugin-quota-run.log`；运行于本轮夹具扩展之前 |
| `pnpm type-check` | exit 0（S17 记录） | 本轮只改 `.mjs`，未复跑 |
| `pnpm build` + `pnpm check:bundle` | 通过（S17 记录） | — |

本轮未执行：`pnpm test:logic`、`pnpm test:components`、默认全量 `pnpm verify:browser`（既有回归）、Go 四包与 `go vet`（Docker 配方）、C-24 独立全新数据目录运行、浏览器真测截图与体验环境。这些都是明确的覆盖缺口，不记为通过。

## 范围内要修

1. **C-18 插件 429 未接入既有退避链路。** `internal/cpa/management/client.go:214` 把插件 429 变成 `*management.HTTPError`；`internal/api/quota_refresh_status.go` 只有 `errors.As(err, *quota.RateLimitError)` 才记 `rate_limited`，因此插件 429 被记为 `outcome:"failed"`、`http_status:429`，没有 `retry_after_ms`/`retry_at_ms`，`quota.Backoff` 也不会拦截（`internal/quota/backoff.go`）。批量刷新第二次调用会再次发出插件 POST。建议把「插件路由返回 429 + Retry-After」映射为 `quota.RateLimitError`，复用既有退避；再补 C-18 的接口断言。
2. **C-18 观测时间语义。** `internal/api/management_quota.go:858` 在无快照时把 `observed_at_ms` 填为 `nowMS`，而 C-18 要求「仍为 0/旧值」。`last_success_at_ms` 保持 0，抽屉显示「上次成功读数：未知」，所以 C-08/C-09 的界面语义不受影响；但 API 与本用例冲突。若产品接受「以 last_success=0 表达从未成功」，可把这一子项降为范围外观察并在用例中改写。

## 范围外观察

1. **S16 的 stale 排序观察：判断为范围外，不需要修。** 插件 stale 载荷把 `observed_at_ms` 回填为插件声明的更早读取时刻，`GetLatestQuotaSnapshots` 按 `observed_at_ms DESC` 取最新；若同一凭证已有更新的成功快照，行会展示那份更新、更可信的读数，而不是更旧的缓存读数。AC-7 要求的是「展示的读数若来自插件缓存必须标记」，不是「每次插件返回 stale 都必须覆盖更新的成功快照」；`last_refresh` 仍记录本次 stale 失败。建议在需求文档中把该语义写死，避免下一轮再被当成缺陷。
2. **C-16 的 `outcome` 字段是用例与实现的表述差异。** 单条刷新返回 `{status:"ok", quota}`（`internal/api/management_quota.go:178`），没有 `outcome`；前端单条路径只消费 `quota`。功能上「打对插件、写回观测时间」可满足，建议改用例措辞而不是改接口；若产品要求统一响应，再作为 API 变更另立。
3. **C-25 的「旧读数有标记」目前由 `last_refresh` 失败标签 + 「上次成功读数」行表达。** 当保留的旧快照本身是新鲜读数时，不会出现「缓存读数」标记（该标记只来自插件 stale payload）。是否需要一个独立的「旧读数」徽标，需产品确认。
4. **本轮夹具扩展尚未被场景消费。** `scripts/fake-cpa.mjs` 已增加 OLK（余额无窗口）、固定 429、固定 502、unmatched、meta、decoy 与 missing 模式，`auth-files.mjs` 增加关闭增量凭证的开关；这些改动随报告提交，但没有对应的 `plugin-quota.mjs` 断言，除 fake CPA 单测外的行为未验证。

## 未覆盖与外部前提

- 浏览器真测截图、体验链接与体验卡：本轮未产出（未启动体验环境）。
- C-24 需要独立的全新 `OMCPA_DATA_DIR` + `interval_minutes:5` + `ok` 模式运行约 135 秒，未做。
- 真实 CPA / 真实凭证：需 CPA ≥ v8.0.25（带 `quota_plugin_id`）与 ollama-quota 插件 v0.3.0，三家真实 key/cookie；线上插件未安装、N1112-28 未决，本轮按要求未连接任何线上环境，均标为外部前提。
- C-10 需要 F-OWN-OLLAMA 夹具（OMC 自有 Ollama 账号），现有 fake CPA 不提供。

## go.sum 说明

`go.sum` 的未提交改动是先前 Go 构建/模块缓存拉取时新增的若干 `h1:` 校验行（cloud metadata、errcheck、goldtool、goldmark、crypto、term），与本需求的验收无关，也没有 Go 代码改动需要它们。已用 `git checkout -- go.sum` 还原，报告提交中不含 `go.sum`。

## 边界声明

未改业务代码（后端/前端）；未推送、未合 dev、未对外发请求；只连本地 fake CPA 与 127.0.0.1；未读 `private/`；未发通知、未改 PM 运行记录。改动仅限验收脚本与夹具：`scripts/fake-cpa.mjs`、`scripts/acceptance/auth-files.mjs`、`scripts/browser-acceptance.mjs`，以及本报告。
