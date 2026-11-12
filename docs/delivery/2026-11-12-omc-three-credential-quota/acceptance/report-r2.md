---
type: test-report
slug: 2026-11-12-omc-three-credential-quota-r2
status: done
updated: 2026-11-13
verdict: 通过
summary: "第 2 轮复测：任务清单内 15 条用例全部通过（C-18 429 退避与窗口内不重发、C-24 自动轮、C-17 批量隔离、C-23 轮询不触发上游等），C-06/C-08/C-09/C-15/C-16/C-19/C-20/C-21/C-22/C-25 补齐；C-10 与 C-13 的自有 Ollama 账号区块因缺 F-OWN-OLLAMA 夹具标阻塞；全量门禁 380 条通过、1 条既有 key-management 断言红，非本需求引入"
---

# 测试报告：第 2 轮

**结论**：本轮任务清单内的用例全部通过。C-18 已复测：插件 429 接入既有退避链路，首次刷新记 `rate_limited` + `http_status=429` + `retry_after_ms`/`retry_at_ms`，退避窗口内第二次刷新直接 429 `rate_limited_backoff`、不再发插件 POST；Go 四包与 `go vet` 通过，浏览器场景新增断言覆盖该路径。C-06/C-08/C-09/C-13/C-15/C-16/C-17/C-19/C-20/C-21/C-22/C-23/C-24/C-25 均已补测；C-10 的插件侧余额显示通过，自有 Ollama 账号侧因缺 F-OWN-OLLAMA 夹具阻塞，C-13 尾部的同一区块也未验证。除该外部前提外，未发现需要在合并前修的业务代码问题。全量默认浏览器套件 380 条通过、1 条失败，失败项是 `key-management` 的既有断言与仓库自有 Key 面板冲突，静态归属可确定与本需求无关（见「范围外观察」）。

- 测试对象：分支 `Newoahil/11-12-t18-s20-claude`，HEAD `acc61e8b`；工作区另有本轮的夹具/场景扩展（`scripts/fake-cpa.mjs`、`scripts/acceptance/plugin-quota.mjs`、`scripts/acceptance/quota-auto-refresh-round.mjs`、`scripts/acceptance/experience-env.mjs`）与 `cases.md` 的 PM 决策措辞，随本报告一起提交。
- 环境：Windows 11；Windows 二进制 `tmp/e2e-win/oh-my-cpa.exe`；fake CPA 与 app 均由验收脚本自起，只用 `127.0.0.1`；`TZ=Asia/Shanghai`，`ASSISTANT_NO_SEND=1`；Go 门禁在 `golang:1.27.1 --network none` 容器内执行。
- 证据目录：`E:/works/Homan-context/inbox/agents/验收/2026-11-12-omc-three-credential-quota/`（`report-r2.md`、`plugin-quota-r2b.log`、`c24-run.log`、`fake-cpa-r2.log`、`browser-suite-r2.log`、`go-gate-r2.log`、`type-check-r2.log`、`test-logic-r2.log`、`test-components-r2.log`、`probe-missing-opc.log`、`experience-quota-page.png`、`experience-run.log`）。

## 结果

| 用例 | AC | 类型 | 结果 | 证据 | 说明 |
|---|---|---|---|---|---|
| C-06 缺失字段不写 0 | AC-3/4/5 | 浏览器 | 通过 | `plugin-quota-r2b.log` checks 22–28 | CMC 无套餐/无 `0 credits`；OPC 只保留 Weekly、缺失份额显示 `--`；Ollama api_key 保留 8.75 USD 且空态为「上游已应答但未返回配额窗口」；Ollama cookie 无 groups 显示空态且无进度条；缺失重置时间未写当前时间 |
| C-08 429 无缓存 UI | AC-7 | 浏览器/接口 | 通过 | 同上 checks 62–68 | 首次 429 返回 `rate_limited_backoff` 与未来 `retry_at_ms`；窗口内第二次 429 同码且不重发 POST；行头「已限流」、`HTTP 429`、可重试时间、「上次成功读数：未知」，无伪造百分比/额度 |
| C-09 502 无缓存 UI | AC-7 | 浏览器/接口 | 通过 | 同上 checks 44–45 | 固定 502 刷新返回失败状态、无额度数字；行头「刷新失败 · HTTP 502」与「上次成功读数：未知」，空态「最近一次读取配额失败」，无 %/credits |
| C-10 Ollama 余额显隐不互相污染 | AC-6 | 浏览器 | 阻塞（插件侧通过） | 同上 checks 19–20、25–26 | 插件凭证 `12.34 USD` / `8.75 USD` 均显示；缺 F-OWN-OLLAMA（`ollama:fixture-own`，快照来源非 `settings_balance`）夹具，自有账号侧无法验证，见「未覆盖与外部前提」 |
| C-13 既有七家不回归 | AC-10 | 浏览器 | 部分通过 | `browser-suite-r2.log`、`experience-quota-page.png` | 默认套件 380 条通过，含 auth-files 场景全部断言；额度页可见 Claude/Antigravity/Codex/xAI/Kimi/Devin/Meta 与三家新凭证并存，Meta 在「跳过 4 个」里注明不支持实时刷新；1 条既有 `key-management` 断言红与本需求无关（见「范围外观察」）；OMC 自有 Ollama 账号区块同 C-10 阻塞 |
| C-15 概览原始 key/无秘密 | AC-1/2 | 接口 | 通过 | `plugin-quota-r2b.log` checks 29–34 | 三家 raw provider key、`refresh_supported`、套餐/余额按形状返回；窗口标签不泄露 bucket id，OPC rolling/monthly 未被套上虚构周期；响应无凭证秘密字段 |
| C-16 单条刷新打对插件 | AC-9 | 接口 | 通过 | 同上 checks 35–37 | 单条刷新返回 `status:"ok"` 与本次 `observed_at_ms`；恰好 1 个插件 POST、body 仅 `auth_index`；概览快照被新观测替换 |
| C-17 批量逐条、失败隔离 | AC-9 | 接口 | 通过 | 同上 checks 46–49 | `[CMC, " CMC ", OPC, 502, Ollama]` 去重后 4 条有序结果、逐条 outcome；4 个插件 POST；失败项无零值快照、无 `last_success_at_ms` |
| C-18 429+Retry-After 记限流 | AC-7/9 | 接口/浏览器 | 通过 | 同上 checks 62–68；`go-gate-r2.log`；`c24-run.log` | 首次 429：`outcome=rate_limited`、`http_status=429`、`retry_after_ms`≈120000、`retry_at_ms` 为未来；无 `last_success_at_ms`、无快照/额度；窗口内第二次刷新拒绝且不重发；Go 单测覆盖插件 429 映射。`observed_at_ms` 子项按 2026-11-13 PM 决策改为断言 `last_success_at_ms=0`（未改业务代码，`cases.md` 已同步措辞） |
| C-19 旧宿主 `/plugins` 匹配 | AC-8 | 接口 | 通过 | 同上 check 61 | legacy 宿主下 Ollama 经 `GET /v8/management/plugins` 匹配 `ollama-quota`，未走 providers 注册表 |
| C-20 旧宿主 providers 匹配 | AC-8 | 接口 | 通过 | 同上 checks 55–59 | legacy 宿主下 CMC 经 `GET /credentials/quota/providers` 解析，POST body 仅 auth index，读数正常渲染 |
| C-21 匹配不到明确报错 | AC-8 | 接口 | 通过 | 同上 checks 50–52 | unmatched 凭证返回明确错误、0 个插件 POST；拒绝前确实查过 plugins 与 providers；未写入快照 |
| C-22 `quota_plugin_id` 优先 | AC-8 | 接口 | 通过 | 同上 checks 53–54 | 带 `quota_plugin_id` 的凭证直达 `ollama-quota`，decoy 插件收不到 POST，也不再请求 providers |
| C-23 概览/页面轮询不触发上游 | AC-2/9 | 接口 | 通过 | 同上 checks 38–39 | 关掉自动策略后静置 62 秒：只有 `GET /v8/management/credentials`，无插件 POST、无 provider 读取；已存观测时间不变 |
| C-24 自动刷新轮覆盖三家 | AC-9 | 接口/调度 | 通过 | `c24-run.log` | 全新数据目录 + `interval_minutes:5` + 135 秒：三条插件凭证各恰好 1 次 POST，第二次 tick 无重复；Meta 无尝试、无快照、无上游请求；轮次日志 `due:14 refreshed:11 failed:3` |
| C-25 失败保留旧快照 | AC-7/9 | 接口/浏览器 | 通过 | `plugin-quota-r2b.log` checks 40–43 | CMC 502 后保留 T1/321 credits/原观测时间；`last_refresh` 记 `failed` + HTTP 502；行头「刷新失败 · HTTP 502」并标出上次成功读数 |
| C-26 plugin-quota 场景 | AC-11 | e2e | 通过 | `plugin-quota-r2b.log` | 74/74 条断言、exit 0；首跑因 C-06 稀疏快照覆盖 C-15 预期失败 2 条，补一次恢复性批量刷新后复跑全绿（见下） |
| C-27 fake CPA 单测 | AC-11 | e2e | 通过 | `fake-cpa-r2.log` | 18/18；含本轮新增的 `setCredentialDisabled` 控制 |

C-01/C-03/C-04/C-05/C-07/C-11/C-12 的既有断言在本轮扩展后的场景中继续执行并通过（同一 `plugin-quota-r2b.log`），R1 中这些用例的「部分通过」项（过滤标签、raw key、抽屉窗口、余额形态）已由 C-01/C-15 等新增断言补齐。C-14（英文界面，P2）不在本轮任务清单内，未测；C-28 及以后不在本轮范围。

计数：通过 16（C-06、C-08、C-09、C-15、C-16、C-17、C-18、C-19、C-20、C-21、C-22、C-23、C-24、C-25、C-26、C-27），部分通过 1（C-13），阻塞 1（C-10）。

## 已执行命令

| 命令 | 结果 | 说明 |
|---|---|---|
| `pnpm type-check` | exit 0 | `type-check-r2.log` |
| `pnpm test:logic` | 89 suites passed，exit 0 | `test-logic-r2.log` |
| `pnpm test:components` | 40 files / 305 tests passed，exit 0 | `test-components-r2.log` |
| `node --test scripts/fake-cpa.test.mjs` | 18/18，exit 0 | `fake-cpa-r2.log`，夹具最终版复跑 |
| `pnpm verify:browser --scenario plugin-quota` | 74/74，exit 0 | `plugin-quota-r2b.log`（`plugin-quota-r2.log` 为首跑失败记录） |
| `node scripts/acceptance/quota-auto-refresh-round.mjs` | 6/6，exit 0 | `c24-run.log`，135 秒真实调度等待 |
| `pnpm verify:browser`（默认全量） | 380 PASS / 1 FAIL，exit 1 | `browser-suite-r2.log`；失败项见「范围外观察」第 2 条，其余含 auth-files 场景全部通过 |
| Docker `go test ./internal/quota ./internal/cpa/management ./internal/api ./internal/operations && go vet …` | 4 包 ok、vet 无输出，exit 0 | `go-gate-r2.log`；`golang:1.27.1 --network none` |
| `pnpm build` | exit 0 | `pnpm-build.log` |

## 范围内要修

无。本轮未发现需要在本需求合并前修改的业务代码；C-18 的观察时间子项已按 PM 决策改用例断言（`last_success_at_ms=0`/从未成功），未改代码。

## 范围外观察

1. **稀疏成功读数被概览标成 `stale`。** OPC 返回「只有 Weekly、无 `remainingFraction`」的载荷时，刷新本身成功（`quota_status:"idle"`、`observed_at_ms` 为本次时刻），但概览项在 6–9ms 后变成 `status:"stale"`，前端会显示「数据过期」。复现：`docs/delivery/2026-11-12-omc-three-credential-quota/acceptance/probe-missing-opc.mjs`（运行日志 `probe-missing-opc.log`）输出 `OK state ... sparse state {"status":"stale","observed_age_ms":6,"last_refresh":{"outcome":"success"}}`。根因：`internal/api/management_quota.go` 的 `classifyQuotaSignal` 把 `idle` 归入 Unknown，`internal/api/quota_signal_projection.go` 的 `applyQuotaCollectionSignal` 再把 Unknown 覆盖成运输错误；窗口数 > 0 时置 `stale`。该行为未被 C-06 或任何 AC 要求（C-06 只要求缺失字段不写 0、不造占位），且分类代码早于本需求；判为范围外，建议另立跟进：把 `idle` 视为 healthy/unknown 不当运输失败，或在成功刷新后不覆盖快照状态。
2. **`key-management` 的「one container」断言过期。** 默认全量套件唯一失败：`key management renders one container rather than nested frames — cards=2 nested=0`。检查写于 `b9cf688d`（2026-09-18），要求 `.keys-page` 只有一个 `.ant-card`；仓库自有 Key 面板 `RepoKeysPanel`（#117，`95183b9c`）在基线上新增了第二个 `Panel`，本需求未改该页。判定为既有断言与后续功能冲突，不是本需求回归；建议更新断言或把范围限定到客户 Key 面板，不在本需求内修。
3. R1 的三条观察继续有效：stale 载荷按 `observed_at_ms` 排序展示更新的成功快照；单条刷新响应使用 `status` 而非 `outcome`；C-25 的「旧读数」由失败标签 + 上次成功读数行表达。

## 未覆盖与外部前提

- **C-10 / C-13 自有 Ollama 账号区块**：缺 F-OWN-OLLAMA 夹具（`auth_index:"ollama:fixture-own"`，快照来源非 `settings_balance`）。现有 fake CPA 不提供 OMC 自有账号的登录态与快照；要补需先给 app 造一个 OMC 自有 Ollama 账号（真实 ollama.com 登录），或在 fixture/数据目录里直接种入该账号与旧快照。未验证「自有账号余额不被放开、也不被三家插件凭证挤掉」，不写成通过。
- **C-14 英文界面（P2）**：不在本轮任务清单内，未切换 English 做截图核对；新文案的英文键存在性由前端组件/逻辑测试间接覆盖。
- **真实 CPA / 真实凭证**：需 CPA ≥ v8.0.25（带 `quota_plugin_id`）与 ollama-quota 插件 v0.3.0、三家真实 key/cookie；线上插件安装（N1112-28）未决，本轮按要求未连接任何线上环境，均标为外部前提。
- **真实体验**：体验环境用 fake CPA 的固定夹具，不代表真实上游读数。

## 体验环境

- 打开：<http://127.0.0.1:18322/omc/oauth-management?page_size=48>，密码 `omc-e2e-management-key`（登录后即额度页；自动刷新策略已关闭，页面不会自己变化）。
- 截图：`docs/delivery/2026-11-12-omc-three-credential-quota/acceptance/experience-quota-page.png`（同一张已复制到 inbox 证据目录）。
- 三个体验亮点：
  1. **三家插件凭证各成一行**：Command Code 显示 `Pro` + `321 credits` + 5 小时 80%/周 55%；Ollama cookie 显示 `12.34 USD` + 5 小时 60%/周 80%；OpenCode Go 显示三窗口（Rolling 0% 触发「配额已耗尽」是既有语义），行内只显示最短两个窗口、抽屉看全量。
  2. **失败不造数**：刷新固定 429 凭证显示「已限流 · HTTP 429 · 可重试时间」和「上次成功读数：未知」，窗口内再点刷新不会重发；502 显示「刷新失败 · HTTP 502」并保留旧读数；稀疏载荷只显示 `--`/空态，不写 0。
  3. **与既有七家并存**：Claude/Antigravity/Codex/xAI/Kimi/Devin/Meta 仍在同一页，Meta 被明确跳过实时刷新；自动刷新轮在 interval 模式下每个到期凭证只读一次、Meta 不参与（由 C-24 脚本验证，体验环境为保持页面静止已关闭策略）。
- 关闭：`Stop-Process -Id 19152,49304`（app 与 node 控制器；若 PID 已变，读 `tmp/experience-env.json` 的 `appPid`/`nodePid`）。状态文件 `tmp/experience-env.json`，日志 `tmp/experience-app.log`。

## 边界声明

未改业务代码（后端/前端）；未推送、未合并、未发布；未对外发请求，只连本地 fake CPA 与 `127.0.0.1`；未读 `private/`；未发通知、未改 PM 运行记录。改动仅限验收脚本、夹具、`cases.md` 措辞与本报告。
