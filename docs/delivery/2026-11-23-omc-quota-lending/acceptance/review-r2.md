---
type: test-report
slug: 2026-11-23-omc-quota-lending-r2
status: done
updated: 2026-11-23
verdict: 范围内要修
dispatch: works 2026-11-23#T7-S20
summary: "二级页改版复审：F-1/F-2 修复、读失败降级与只读边界经代码、单测和预修复回放确认；调整后 AC 基本还原；范围内要修 4 条低危（页面固定只读文案缺失、未进池摘要按钮无样式、入口英文单复数、testing.md 归属/场景名），其余通过"
---

# 验收报告 r2：跨凭证额度拆借（二级页改版复审）

**结论：范围内要修 4 条（均低危、非结构性），其余通过。** 本轮针对 S17 的改动做代码审查与调整后 AC 还原度复核，并复验 r1 的 F-1、F-2：两条后端偏差均已修好，新增断言在 `b4a77953` 旧实现上会真实失败（回放见 §3.1）。读失败时总览与详情的 `lending` 均为 `null`，入口、二级页、详情三处按同一条件降级；没有新增写 CPA 的调用，也没有应用 / 写入按钮。未通过的是四处低危：二级页缺少固定的「只提示，不改路由」文案、「未进池」摘要按钮没有样式（原生按钮外观）、入口英文在 1 条建议时写成 `1 suggestions`、`docs/testing.md` 的测试归属与场景名不准确。

- 审查对象：工作区 `Newoahil/11-23-t7-s20-codex`，HEAD `db387d01`，基线 `b4a77953`；16 个提交、40 个文件，含后端修复合并 `0d997a2c`。工作区审查前后 `git status --short` 仅本轮报告文件（已提交）。
- 基准：`docs/delivery/2026-11-23-omc-quota-lending/prd.md`（AC-1～17、AC-14a/b/c、变更记录）、`impl-plan.md`、`docs/architecture.md`、`docs/testing.md`、`AGENTS.md`。r1 只读自 `E:/works/Newoahil__oh-my-cpa/11-23-t7-s16-codex/.../acceptance/review-r1.md`。
- 环境：Windows 11 + Node 24.16.0 / pnpm 11.19.0（engine 写 22.23.2，只有 WARN）；Go 在 `golang:1.27.1` 容器内执行，卷 `omc-go-mod`、`omc-gocache`。
- 界面证据：仅查看 S17 自测截图 `E:/works/Newoahil__oh-my-cpa/11-23-t7-s17-codex/docs/delivery/2026-11-23-omc-quota-lending/acceptance/shots-s17/`（不进 git，只算开发自测）。本轮未起浏览器、未真点，界面真点归 S19。

## 1. 用例与结果

| 用例 | 预期 | 结果 |
|---|---|---|
| C1 limited 池中可服务卡的角色 | 健康卡为 `normal`、无服务性原因；受限卡为 `limited` + 原因 | 通过；旧实现回放失败（`ok card role = limited`） |
| C2 unavailable 池的有效期 | `valid_until_ms` 为空（DTO 省略或 `null`） | 通过；旧实现回放失败（`valid_until = 1763259200000`） |
| C3 快照或冷却读失败 | 详情仍 200，原额度字段保留，`lending` 为 `null` | 通过（`TestQuotaLendingDetailDegradesWhenReadsFail` 两子例） |
| C4 前端三处降级 | 入口隐藏、二级页空态、详情不显示建议块 | 通过（组件/页面/挂载测试均覆盖 null 与缺字段） |
| C5 OAuth 页入口三态 | 有偏用→条数；有均衡→均衡；无池/无法建议→纯文字；缺字段不显示 | 通过（`quota-lending-mount` 断言） |
| C6 OAuth 页不再有面板 | `[data-lending-view="page"]` 为 0，入口是链接 | 通过（代码 + S17 探针自测） |
| C7 二级页直接打开/刷新/返回 | 路由可进；刷新可进；有历史时显示返回，直接打开不显示 | 通过（路由/页面测试 + S17 探针自测）；浏览器真点未由本包执行 |
| C8 未进池折叠 | 收起显示总数与原因计数；展开按原因；再展开列名；N=0 隐藏 | 逻辑通过（逻辑/组件测试）；视觉见 F-B |
| C9 三处同源 | 入口条数、详情与页面共用同一 `lending` 与派生 | 通过（`TestQuotaLendingOverviewAndDetailAgree` + 代码同源） |
| C10 只读边界 | 无应用/写入/切换/消耗按钮；无新增写 CPA 调用 | 通过（代码/组件测试）；浏览器交互计数留给 S19 |
| C11 固定只读文案 | 二级页也应出现「只提示，不改路由」 | **不通过**：见 F-A |
| C12 文档与文案 | 测试归属、场景名、英文单复数准确 | **不通过**：见 F-C、F-D |

## 2. 已执行命令与结果

| 命令 | 结果 |
|---|---|
| `docker golang:1.27.1 go test ./internal/quota` | 通过 `ok quota 0.027s` |
| 同上 `go test ./internal/api` | 通过 `ok api 140.972s` |
| 同上 `go test ./internal/demo` | 通过 `ok demo 36.749s` |
| 同上 `go test ./internal/api -run TestQuotaLending -v` | 5 个契约测试全 PASS（含 DTO 有效期与读失败降级） |
| `pnpm type-check` | 通过，`tsc --noEmit` |
| `pnpm test:logic` | 通过，`90 suites passed in 11.05s` |
| 相关组件测试（lending-advice / quota-lending-page / quota-lending-mount） | 通过，3 文件 20 测试 |
| 相关组件测试（credential-quota / reset-advice） | 通过，2 文件 47 测试 |
| `pnpm check-i18n` / `check-css-modules` / `check:demo` | 通过（4333 键 / 519 消费点 / 155 responses, 19 routes） |
| `pnpm lint:antd` / `check:motion` / `check:style-tokens` / `check:feedback` | 通过 |
| `pnpm check-docs` | 红（既有）：31 条全部在未改动的 `2026-11-12-omc-three-credential-quota/impl-plan.md` 等 11-12 文档，本包 diff 为 0 |
| 预修复回放：`b4a77953` 源码 + 当前 `lending_test.go`，`go test ./internal/quota -run "LowRemainingSeat|MissingResetTime"` | **失败**，错误正是 F-1/F-2 的断言 |

## 3. 代码审查

### 3.1 F-1 / F-2 复验（确认修好）

- F-1：`internal/quota/lending.go` 的 `buildLendingPool` 在 `len(serviceable) < 2` 分支只给有 `serviceReasons` 的卡 `limited`，其余可比卡保持 `normal`。`internal/quota/lending_test.go` 的 `TestProjectLendingAdviceLowRemainingSeatIsLimited` 新增 `ok` 卡 `normal` 且无原因的断言。预修复回放失败：`ok card role = limited, want normal`。
- F-2：同一函数在 `state == unavailable` 时清空 `validUntil`；`TestProjectLendingAdviceMissingResetTimeBlocksThePool` 断言 `ValidUntilMS == nil`，`TestQuotaLendingUnavailablePoolOmitsValidUntil` 同时断言 DTO 字段为空或 `null`。预修复回放失败：`unavailable pool declared valid_until = 1763259200000`。
- 结论：两条都与 `impl-plan.md` §2.4/§2.2 的契约一致；新增断言不是「断言没有麻烦」，而是能在旧实现上失败。

### 3.2 读失败与 null

- `internal/api/management_quota.go` 的 `quotaCardStates` 返回 `lendingComplete`；快照或冷却任一读失败即置 false，总览与详情都不投影，返回 `lending: null`。详情仍返回原 `quota`、`history`、`model_quotas`，由 `TestQuotaLendingDetailDegradesWhenReadsFail` 的 `quota_snapshots` / `error_events` 两子例证明。
- 前端：入口只在 `quotaQuery.data?.lending` 为真时渲染；二级页 `LendingAdviceView` 对 `null` 与字段缺失都走 `data-lending-absent` 空态；详情 `CredentialQuotaBody` 用 `{lending && ...}` 同时隐藏建议块和整池链接。三处条件一致，测试分别覆盖。

### 3.3 只读与越界

- `git diff --name-status b4a77953..HEAD` 无 `internal/api/handler.go`、无 `migrations/`、无 `internal/cpa/` 写路径；`management_quota.go` 只改只读投影。对 diff 搜索写方法 / `priority` / `weight`，只命中文档与测试夹具。
- `LendingAdviceView` 的按钮只有未进池折叠控件；二级页头只有返回按钮；入口与详情链接都是 `Link`。没有应用、写入、切换路由或消耗重置权益的控件。
- `ResetAdviceView`、`internal/quota/reset_advice.go`、`internal/operations/reset_advice.go` 在本 diff 中零改动；`TestQuotaLendingPreservesResetAdvice` 与 reset-advice 组件测试通过，AC-13 保持。

### 3.4 挂载模块与 AppLayout

- `web/src/pages/quota/lendingMount.ts` 集中了 `QUOTA_LENDING_PATH`、`QUOTA_LENDING_NAV_KEY`、标签键和 `hasPreviousHistoryEntry`；`App.tsx`、`routePreload.ts`、入口、详情链接都引用它，导航改版时确实只需改这一处配置。
- `AppLayout.tsx` 只把 `location.pathname` 换成 `lendingNavKeyForPath(location.pathname)` 再匹配；其他路径原样返回，现有路由的侧栏高亮不受影响。当前二级页在截图中确实高亮 OAuth 管理。

### 3.5 范围内要修

**F-A（低）二级页缺少固定的「只提示，不改路由」文案。** `LendingAdviceView` 的 `page` 分支不渲染 `readOnly` 标签，只在页底输出较长的「建议只读…」说明；旧面板在标题旁有该标签（`git show b4a77953:web/src/pages/quota/LendingAdviceView.tsx`）。PRD §4.4 屏幕项 1 与 `impl-plan.md` §5.1 第 10 条要求固定写这句；`web/tests/lending-advice.component.test.tsx` 反而断言 page 模式没有该标签，等于把偏差固定成了测试。复现：S17 截图 `03-lending-1440-zh.png` 页面标题旁无标签；`06-credential-detail-zh.png` 详情有。建议在二级页标题旁渲染同一 `Tag`，或在页底说明中原样带上这句。

**F-B（低）「未进池」摘要按钮没有样式。** `LendingAdviceView.module.css` 只有 `.unpooled-summary, .unpooled-group { min-width: 0; }`，没有给 `.unpooled-summary` 做按钮重置；它渲染成浏览器原生按钮（灰色底、边框、默认字体/对齐），和下面的 `.unpooled-group-toggle` 不一致。复现：S17 截图 `03-lending-1440-zh.png`、`04-lending-unpooled-expanded-zh.png` 中「未进池 7 张」是灰色矩形，文字对比度偏低。建议让 `.unpooled-summary` 复用 `.unpooled-group-toggle` 的 `display:flex`、`border:0`、`background:none`、`padding:0`、`font:inherit`、`cursor:pointer`、`text-align:start` 等规则。

**F-C（低）`docs/testing.md` 的测试归属与场景名不准确。** 文中说 `web/tests/lending-advice.component.test.tsx` 拥有「入口三态」，实际入口测试在 `web/tests/quota-lending-mount.component.test.tsx`（`LendingAdviceEntryLink` 的三种状态与缺字段都在那里）；同段又说浏览器几何在「`lending` and OAuth management scenarios」，仓库没有 `lending` 场景，实际 id 是 `quota-lending`（`scripts/acceptance/scenarios.mjs`）。建议把入口三态归到 mount 测试，并把场景名改为 `quota-lending`。

**F-D（低）入口英文在 1 条建议时是 `1 suggestions`。** `web/src/i18n/index.tsx` 的 `quota.lending.entry_advised` 英文固定为 `Quota lending · {n} suggestions`；`quota-lending-mount` 测试实际断言了 `Quota lending · 1 suggestions`。中文「1 条建议」不受影响，但英文单复数错误。建议改用单复数两键、调用处按 n 选择，或改成不可数的 `{n} advice`。

## 4. PRD 还原度（只核调整过与新增条）

| AC | 结果与证据 |
|---|---|
| AC-5 | 通过。`unpooled_count` 与 `unpooled` 只由同 provider + plan_type 成池；`UnpooledSection` 始终显示总数与原因计数，凭证名在第二层折叠；`groupLendingUnpooled` 按原因分组，测试覆盖不丢不重。 |
| AC-9 | 通过。`PoolBlock` 显示有效期、每卡剩余/距重置/日均与一句理由；`lending-advice` 组件测试断言三列数字和理由。 |
| AC-10 | 通过。`deriveLendingView` 对 `fresh_until_ms` 过期降级 `unavailable` + `reading_stale`；后端不可比较/窗口不同也保持 `unavailable`，逐卡原因；`test-quota-lending.ts` 覆盖不退回百分比排序。 |
| AC-11 | 通过。`daysText` 对 `derived` / `approximate` 加 `~`，池头 `is_reference` 显示「参考」；组件测试覆盖。 |
| AC-12 | 通过（代码/组件层）。二级页、入口、详情无应用/写入/切换/消耗控件；`LendingAdviceView` 只有折叠按钮，无写入口；后端无新增写路由。浏览器交互计数未由本包执行。 |
| AC-13 | 通过。`ResetAdviceView` 与原后端零改动；reset-advice 组件测试断言两区块分开、顺序与语义不变。 |
| AC-14 | 通过。`lending` 存在但无池时二级页显示空态并带未进池汇总；`lending` 为 null/缺字段时显示降级空态，不白屏。 |
| AC-14a | 通过（代码/测试层）。路径常量、标题、返回按钮、直接打开/刷新、侧栏高亮均由 `lendingMount.ts` 与路由测试覆盖；浏览器真点留给 S19。尾斜杠高亮见 §5。 |
| AC-14b | 通过。OAuth 页不再渲染面板；入口是页头统计行末尾的 `Link`，三态与缺字段不显示由 mount 测试覆盖；S17 截图显示入口在统计行内且不占独立区块。 |
| AC-14c | 逻辑通过。默认折叠、摘要按原因计数、展开按原因、再展开列名、N=0 隐藏均有组件/逻辑测试；摘要按钮视觉缺陷见 F-B。 |
| AC-15 | 通过。入口、页面、详情共用同一 `lending`；详情由 `lendingDetailFor` 从同一次总览投影选出，API 测试 `reflect.DeepEqual` 总览池与详情池；页面与入口共用 `deriveLendingView`。 |
| AC-16 | 通过（代码/纯逻辑层）。页面使用 `useVisibleNow` 与 `deriveLendingView` 重算；`QuotaLendingPage` 复用 `['management-quota']` 查询且 `refetchInterval` 与 OAuth 页一致（60 秒），没有新增逐卡请求。 |
| AC-17 | 通过。无迁移、无 `SaveQuotaSnapshot`/通知调用进入 lending 路径；`internal/quota` 投影为纯函数；diff 无 `internal/operations`、预测或迁移改动。 |

## 5. 缺口与范围外观察

- **未做的验证**：本包未起浏览器、未真点；S17 的 `quota-lending` 探针与截图只算开发自测，不能写成「界面通过」。未重跑全量 `pnpm test:components`（332 测试）与 `pnpm verify`；已跑与拆借相关的 5 个组件文件与静态门禁。
- **F-2 的 DTO 级预修复回放**：只在 `internal/quota` 层用当前测试文件回放旧实现；DTO 级测试在当前代码通过，旧 handler 未单独回放（字段由同一 `buildLendingPool` 产生，结论一致）。
- **尾斜杠**：`/omc/quota-lending/` 仍能渲染页面（路由匹配忽略尾斜杠），但 `lendingNavKeyForPath` 只精确匹配 `/quota-lending`，此时侧栏会回落到仪表盘。AC 未规定尾斜杠；建议顺手把 pathname 规整后再匹配。
- **混合池入口态**：一池 balanced、另一池 unavailable 时，`lendingEntryState` 因 `states.includes('balanced')` 显示「均衡」，并非严格「所有池都均衡」。AC-14b 没有定义混合态；记录不改。
- **空态文案**：读取失败返回 null 与「确实没有池」共用 `No lendable pool yet`，用户无法区分是数据没读到还是没有可拆借池；AC-14a 只要求非白屏，暂不列为要修。
- **既有问题（非 S17 引入）**：balanced 池的 `poolReason` 会在所有可比较卡中取最早重置者，若最早的是 limited 卡，理由句可能把不能服务的卡写成「最先重置」；该函数在本 diff 中未改，记为观察。
- **测试闸门**：`scripts/ui-impact.test.mjs` 为 `lendingMount.ts` 收窄了页面发现规则（只认 `.tsx`）。当前所有页面都是 `.tsx`，且有自测；但将来若出现 `.ts` 页面组件，需要重新审视这一规则。

## 6. 结论

F-1、F-2 确认修好，读失败降级与只读边界成立，调整后的 AC-5、9–16、14a/b/c 基本还原；4 条低危要修（F-A 页面固定只读文案、F-B 未进池摘要按钮样式、F-C 测试文档归属/场景名、F-D 英文单复数）。证据在本文件、上述命令输出、S17 截图（只算开发自测）与 `internal/api` / `internal/quota` 的新增测试。界面真点与 30 秒时钟推进仍归 S19，未跑过的不写成「通过」。
