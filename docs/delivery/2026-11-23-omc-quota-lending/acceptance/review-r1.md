---
type: test-report
slug: 2026-11-23-omc-quota-lending-r1
status: done
updated: 2026-11-23
verdict: 范围内要修
dispatch: works 2026-11-23#T7-S16
summary: "一期（显示层+建议层）两路审查：Go 四包与前端门禁全绿，17 条 AC 中 15 条通过、2 条带低危偏差（受限池角色过宽、unavailable 池仍返回 valid_until_ms）；面板位置类结论已被 17:54 范围调整取代，另附新结构复审清单"
---

# 验收报告 r1：跨凭证额度拆借（一期：显示层 + 建议层）

**结论：范围内要修 2 条（均为低危、非结构性），其余通过。** 代码路与 PRD 路都跑过：`go test ./internal/quota ./internal/api ./internal/demo` 三包全绿（api 185.8s、demo 63.4s）；前端 `test:logic` 90 套、`test:components` 332 测试/43 文件全绿；静态门禁 `type-check` / `lint:antd` / `check-i18n` / `check-css-modules` / `check:motion` / `check:style-tokens` / `check:feedback` / `check:demo` 全绿；受影响浏览器场景 `oauth-management`、`mobile-console`、`oauth-workspace-scale` 三条通过。两条低危偏差都不是「面板在哪、长什么样」，而是数据口径：受限池里可服务的卡被标成「受限」，以及 `unavailable` 池仍带 `valid_until_ms`（契约写「为空」）。面板位置与入口形态相关的偏差按 17:54 范围调整标为**已被取代**，不记缺陷。

- 审查对象：工作区分支 `Newoahil/11-23-t7-s16-codex`，HEAD `b4a77953`，与 `Newoahil/11-23-t7-s13-codex` 同一提交；diff 基线 `11-23-t7-dev-base`（`af68e8c8`），28 个提交、4 处 merge（P1 后端 / P2 前端 / 用例 / 文档引用修复）。
- 验收基准：基线 `docs/delivery/2026-11-23-omc-quota-lending/prd.md` 的 AC-1～17、`impl-plan.md`（§2 契约、§3 口径、§9 AC 对照）、`cases.md`（42 条）。策略模型 `strategy-model.md` 只用于判断二期边界（它不在本分支，见 `git log --all` 的另一提交）。
- 环境：Windows 11 + Node 24.16.0 / pnpm 11.19.0（仓库 engine 写 22.23.2，pnpm 只报 WARN，未影响结果）；Go 门禁在 `golang:1.27.1` 容器内执行；体验环境 `http://127.0.0.1:18422/omc/oauth-management?page_size=48`（demo 数据，密码 `omc-e2e-management-key`），跑的是同一份代码的二进制。
- 证据目录（**不进 git**）：`C:/Users/lhw/omc-acceptance/2026-11-23-t7-s16/shots/`，截图文件名见「界面证据」；临时契约探针 `C:/Users/lhw/omc-scratch/lendprobe/`（`replace` 指向本工作区，只 import `internal/quota`，不改任何业务代码）。
- 工作区状态：审查前后 `git status --short` 均为空；`node_modules/`、`tmp/`、`.playwright-mcp/` 均在 `.gitignore` 内（`git check-ignore -v` 已核）。

## 1. 已执行命令与结果

| 命令 | 结果 | 关键输出 |
|---|---|---|
| `docker golang:1.27.1 go test ./internal/quota ./internal/api ./internal/demo` | 通过 | `ok quota 0.259s` / `ok api 185.814s` / `ok demo 63.399s` |
| 同上 `-run "Lending\|DailyAvailable\|LongStandard" -v` | 通过 | 19 个 `TestProjectLendingAdvice*`、`TestDailyAvailablePercentClampsUnderAnHour`、`TestLongStandardWindow`（7 子例）全 PASS |
| `pnpm test:logic` | 通过 | `90 suites passed in 15.37s`（含 `test-quota-lending`、`test-oauth-sort`、`test-auth-file-logic`） |
| `pnpm test:components` | 通过 | `Test Files 43 passed` / `Tests 332 passed` / 174.84s |
| `pnpm type-check` | 通过 | `tsc --noEmit` exit 0 |
| `pnpm lint:antd` | 通过 | `{total:0, deprecated:0, a11y:0, usage:0, performance:0, skipped:0}` |
| `pnpm check-i18n` | 通过 | `Total defined keys in DICT: 4328`，无缺键 |
| `pnpm check-css-modules` | 通过 | `516` 个消费点全部解析 |
| `pnpm check:motion` / `check:style-tokens` / `check:feedback` | 通过 | 三条 exit 0 |
| `pnpm check:demo` | 通过 | `the demonstration dataset is current: 155 responses covering 18 console routes` |
| `pnpm check:ui --scenario oauth-management` | 通过 | `1 scenario(s) passed in 36.3s` |
| `pnpm check:ui --scenario mobile-console` | 通过 | `1 scenario(s) passed in 27.1s` |
| `pnpm check:ui --scenario oauth-workspace-scale` | 通过 | `1 scenario(s) passed in 20.8s` |
| `pnpm test:fast --base 11-23-t7-dev-base` | 见说明 | 前端组件与 API 集成段 PASS；`repository self-tests` 恰 10 条红，全部是任务书点名的既有环境红（`tar` 打包、符号链接、fixed waits），非本包 |
| `pnpm check-docs` | 红（既有） | 全部 31 条命中都在**未改动**的 `docs/delivery/2026-11-12-omc-three-credential-quota/impl-plan.md`；该文件在本分支 `git diff --stat` 为空；对 `2026-11-23` 交付目录的命中数为 **0** |

`test:self` 的 10 条红已逐条看过，与任务书点名的一致：`fixed waits in tests only ever decrease`、`release integrity rejects missing…`、`tar and zip archives extract…`、`native builder embeds once…`、`prepare is isolated…`、`source whitelist and inventories…`、`publication refuses remote races…`、`deployment history accepts a pull-request merge…`、`publication refuses a concurrent first branch…`、`original total evidence survives decode…`；其中两条失败的 `Error: Command failed: tar --force-local …` 直接说明是 Windows `tar` 缺参，不是本包引入。

## 2. 代码审查

### 2.1 范围内要修

**F-1（低）受限池把可服务的卡也标成「受限」，且该卡没有任何原因**

- 复现（纯投影，不碰业务代码）：用 `C:/Users/lhw/omc-scratch/lendprobe` 造两张同套餐卡 —— `low` 剩 5% / 30 天后重置，`ok` 剩 80% / 2 天后重置 —— 调 `quota.ProjectLendingAdvice`，输出：
  ```
  state=limited poolReasons=[]
    card=low role=limited reasons=[remaining_below_ten] days=30 daily=0.2
    card=ok  role=limited reasons=[]                   days=2  daily=40
  ```
- 偏差：`impl-plan.md` §2.4 把 `role=limited` 定义为「`limited` 池中**不能承接当前流量**的卡」，`ok` 这张剩 80%、2 天后重置的卡显然能承接；AC-8 也要求「要列出受限卡和原因」，而它被标「受限」却没有一条原因。这一点是用代码读出来的、**界面没实测**：按 `LendingAdviceView` 的 `lendingRoleTone('limited') → warn` 与 `{card.reason_codes.length > 0 && …}` 条件，该卡只会渲染一个红色「受限」标签、下面没有原因行。
- 根因：`buildLendingPool` 在 `len(serviceable) < 2` 分支里把 `roles` 全量填成 `LendingRoleLimited`，没有区分「有 `serviceReasons` 的卡」和「可服务但凑不满两张的卡」。
- 建议：只给 `len(serviceReasons[index]) > 0` 的卡 `limited`；可服务卡保留一个中性角色（`normal`）。池状态仍是 `limited`，`stateTag` 走 `quota.lending.state_limited`（「无法给出偏用结论」）不受影响。前端 `serviceableCount()` 只数 prefer/avoid/normal/balanced，改成 `normal` 后仍然计 1，标题不会误报「2 张可拆借」。
- 现有测试未覆盖：`TestProjectLendingAdviceLowRemainingSeatIsLimited` 只断言「没有 prefer/avoid」，`ok` 卡的角色恰好落在断言盲区。

**F-2（低）`unavailable` 池仍返回 `valid_until_ms`，与契约「为空」不符**

- 复现（同一探针）：`unsigned` 卡只有 `reset_label` 文字、`signed` 卡正常，整池 `state=unavailable`：
  ```
  state=unavailable validUntil=0x… reasons=[reset_time_unknown]
  validUntil set on unavailable pool -> 1800259200000 (3.00 days)
  ```
- 偏差：`impl-plan.md` §2.2 字段表写 `valid_until_ms`「建议有效期 = 池内最早的自然重置；**`unavailable` 时为空**」。现在它从「可比较的那张卡」拿了一个值。
- 影响面：前端已兜住 —— `deriveLendingView` 在 `state==='unavailable'` 时不改 `valid_until_ms`，但 `LendingAdviceView` 只在组件测试里用 `valid_until_ms: undefined` 构造，界面靠 `quota.lending.valid_none`（「等数据补齐后再比较」）兜底；真实后端在 unavailable 池会给到值，任何直接读 DTO 的消费方（导出、将来的二级页、将来的 MCP）都会拿到一个无法兑现的「有效期」。
- 建议：`buildLendingPool` 末尾按 `state` 决定是否赋 `ValidUntilMS`（`unavailable` 置 nil），或在 `projectLendingPool` 里按 `State` 清零；补一条 DTO 断言（现在 `quota_lending_test.go` 的 `assertLendingKeys` 只核字段名）。
- 也可反向处理：如果产品认为「受限但仍可参考」需要有效期，那就改契约文字而不是改代码 —— 二选一，别让文档与实现各说一套。

### 2.2 通过的部分（要点）

- **纯投影边界干净**：`ProjectLendingAdvice` 只吃 `LendingCardInput` + `nowMS`，不读仓库、不调上游、不写状态；`LongStandardWindow` / `DailyAvailablePercent` 与前端 `isLongStandardWindow` / `deriveLendingView` 规则同源（`scope=standard`、`period_hours>=24`、1 小时下限、1 个百分点/天门槛）。
- **总览与详情同源**：`getCredentialQuotaDetail` 与 `buildQuotaOverview` 共用 `quotaCardStates` + `buildQuotaCard` + `lendingCardInputs`，只有投影函数不同（`DetailFor` vs `projectLendingOverview`）。实测两张卡的 DTO 逐字节相同（见 §3 AC-15）。
- **只读**：diff 里没有新增路由（`internal/api/handler.go` 不在改动集），没有 `reset_advice` / `operations` / `prediction` / 迁移的改动；对 `/management/quota` 发 `POST/PUT/PATCH/DELETE` 全 405，对不存在的 `/management/lending` 全 404。
- **状态词表接得对**：`needs_reauth` 取自既有的 `NormalizedQuota.Recommendation.Status`（`internal/quota/recommendation.go` 的既有取值），没有新造词；`Recommendation` 是值类型，`lendingCardInputs` 里直接取 `.Status` 无空指针风险。
- **降级不猜**：不可比较即整池 `unavailable`，逐卡带原因；`limited` 不给偏用；从来不退回「按剩余百分比排」。
- **demo 种子**：`internal/demo/quota.go` 写入 plan 字段后，demo 里真的出现了一个 3 卡池（live 验证），`check:demo` 通过。
- **越界**：改动集中在 `internal/quota`、`internal/api`、`internal/demo`、`deploy/cloudflare/data`（P1）与 `web/src`、i18n、`scripts/*.ts`（P2），符合任务书的分包口径；没有把二期内容混进来。

### 2.3 一处行为变化（记录，不要求改）

详情接口过去对 `BatchCorrelatedCooldowns` / `GetLatestQuotaSnapshots` 的错误是**吞掉**（`if err == nil` 才用），现在统一走 `quotaCardStates`，这两个读失败会返回 500；`GetFeishuAccountSignals` / `GetQuotaRefreshAttempts` 在基线就已经是「读失败即 500」。这属于「让坏状态说出来」的方向，与仓库 Tier 3 的偏好一致，但确实把详情页在快照读失败时的可用性从「降级显示」变成了「整页失败」。如果要保守，可以在 `quotaCardStates` 里对这两项保留 `err == nil` 的宽松读，并把该取舍写进注释。**不列为要修**，请开发按自己的判断决定。

## 3. PRD 还原度（AC-1～17）

| AC | 结论 | 证据 |
|---|---|---|
| AC-1 行内剩余% + 距重置 + `~` + 时刻未知 | 通过 | 截图 `03-narrow-700-zh.png`：`~距重置 1天23小时后 · 11/25 16:09 · 参考`（推算）与 `距重置 1小时21分后 · 11/23 18:09`（精确）并列；`snapshot@example.test` 行 `~7m left … · Reference`。`quotaResetCountdownLabel` 对「只有文字」分支返回 `quota.reset_time_unknown_label`（保留上游原文 + 标时刻未知），由组件/逻辑测试覆盖；**文字型时刻未在界面上构造过**，见 §5 |
| AC-2 快重置 + 余量高/低（文字不只靠色） | 通过 | live DOM 抽样：`5小时` 75% → `Fast·high`，`每周` 40% → `Fast·low`，`每周` 50% → `high`（50% 边界落在高侧），`xai` 192h 窗口 70% → `high`；标记同时带 `title`（`Fast reset · plenty left` / `…low`）与颜色，`period_hours` 缺失不打标 |
| AC-3 按最早长窗口重置升序，无长窗口排最后 | 通过 | 选 `Natural reset (soonest)` 后 URL 变 `?sort=reset-asc`；页面顺序 `claude → xai → owner(fixture-auth) → virtual-runtime → snapshot → e2e(antigravity)`，与用 `/management/quota` 自己算出的长窗口最早 `reset_at_ms` 升序**逐项一致**（`2026-11-24T08:09:32.419Z`、`…420Z`、`2026-11-25T08:09:32.418Z`、…），其余 14 张无长窗口的卡排在最后 |
| AC-4 排序被记住 + 重置筛选回名称序 | 通过 | 直接打开 `?page_size=48&sort=reset-asc`，首行仍是 `claude-fixture.json`（排序生效并持久）；点 `Reset filters` 后 URL 回 `?page_size=48`，首行变 `e2e-project`，接下来按名称 A-Z |
| AC-5 同 provider + 同 plan_type 成池；未知不进；不足两张不显示；未参与汇总 | 通过 | live `pools=1`（`codex:pro`），`unpooled_count=16`，原因分布 `13 × plan_type_unknown`、`2 × pool_too_small`、`1 × pool_too_small+no_long_window`；面板底部逐张列出 `xxx.json（套餐未知，不进任何池）` 等 |
| AC-6 只比 ≥24h 标准窗口；取最低；类型不同不给建议 | 通过 | 后端 `LongStandardWindow` 与前端 `isLongStandardWindow` 同规则；`TestProjectLendingAdviceSelectsTheLowestLongWindow`、`…WindowTypeMismatch`、`…WindowTypeMismatchSurvivesAnUnreadableCard` 全 PASS；live 池 `window_kind=weekly` |
| AC-7 日均可用、偏用/少用、1pp/天门槛、1 小时下限 | 通过 | `TestProjectLendingAdviceTwoSeats`、`…FastResetWithHighRemainingPrefersTheFastSeat`、`TestProjectLendingAdviceBalanceThreshold`（恰好 1.0 分先后、0.9 均衡）、`TestDailyAvailablePercentClampsUnderAnHour` 全 PASS；live 三卡 20.6%/天 全部 `balanced` |
| AC-8 停用不计入；冷却/重授权/读取失败/耗尽/<10% 不参与排名但列原因 | **范围内要修（F-1）** | 六个服务性原因子例全 PASS（`ServiceabilityReasons`）；停用卡进 `unpooled` 记 `credential_disabled`（`DisabledSeatIsExcluded` PASS）。**但** `limited` 池把可服务卡也标 `limited` 且无原因，违反「列出受限卡和原因」，见 F-1 |
| AC-9 建议块给有效期、每卡窗口/剩余/距重置/日均/理由 | 通过（契约另一条见 F-2） | 截图 `01-overview-zh.png`：`建议有效到 11/25 16:09 · 1天23小时后`，每卡三列 `40% / ~2.0 天 / 20.2%/天`，理由句点名前两张卡；API 每卡带 `window_kind`。**每卡不单独渲染「建议窗口」列**（池标题带 `周限额`），原型同样如此，见 §6 |
| AC-10 缺时刻/过期/缺长窗口/类型不同 → 整池无法建议并逐卡写原因 | 通过 | `…MissingResetTimeBlocksThePool`、`…StaleReadingBlocksThePool`、`…WindowTypeMismatch*` 全 PASS；组件测试 `degrades an unreadable pool to unavailable…` 断言 `Advice unavailable` + `Not comparable` + 具体原因；live 未构造该状态，见 §5 |
| AC-11 推算/近似带 `~` 与「参考」；门槛内均衡 | 通过 | live `is_reference=true`、`Reference (estimated reset)` 标签、三卡 `~2.0 天`；`TestProjectLendingAdviceReferencePrecision` PASS |
| AC-12 只读：无应用/写入/切换/消耗按钮 | 通过 | live 面板内 `button/[role=button]/a[href]/input/select/textarea` 计数 **0**（总览与详情各测一次）；固定文案 `Advice only, routing unchanged`；写方法 405/404 |
| AC-13 重置权益建议语义/位置/阈值/动作不变，分区不混文案 | 通过 | `web/src/pages/quota/ResetAdviceView.tsx`、`internal/quota/reset_advice.go`、`internal/operations/reset_advice.go` 在本分支 `git diff --stat` 全空；`CredentialQuotaBody` 里 `ResetAdviceView` 与 `LendingAdviceView` 并列分区；`reset-advice.component.test.tsx` 新增「两区块分开、顺序与语义不变」断言；拆借区固定写「不写 priority/weight、不消耗重置次数或授权」 |
| AC-14 无同池/不可比较时空状态 + 未参与数量与原因 | 通过 | `states the pool conditions and the unpooled breakdown when there is no pool` 断言 `No lendable pool yet` + `Not in any pool: 2` + 两条原因计数；live 有池时底部仍给 `未参与本页建议：16 张` 逐条明细 |
| AC-15 列表与详情同一份建议 | 通过 | 同一 `auth_index` 在总览与详情的卡片 JSON **逐字节相同**（`role/window_kind/remaining_percent/reset_at_ms/reset_accuracy/observed_at_ms/fresh_until_ms/days_to_reset/daily_available_percent/reason_codes/is_comparable`）；live 详情抽屉 `In the codex · Pro 200 · Weekly limit pool (3 credentials)` + `本卡` 标记 + 三列数字与列表一致 |
| AC-16 时区一致；倒计时/建议随页面时钟走；不额外请求上游 | 通过 | 在距上一次配额轮询 20 秒内取样 30 秒：三行倒计时从 `3h 4m / 4m / ~4m` 推进到 `3h 3m / 3m / ~3m`，`~` 与 `· Reference` 保留，期间**没有** `/management/quota` 请求（只有既有的 `reset-announcements` 与 `healthz`）；`refetchInterval: 60_000` 是基线就有的页面轮询，本分支未改（`git diff` 中无该字段） |
| AC-17 不改采集/预测/单卡状态/通知；不落库不推送 | 通过 | 无迁移文件；`internal/operations/`、`internal/quota/prediction.go`、`reset_advice.go` 零改动；`lending` 只在 `buildQuotaOverview` / `getCredentialQuotaDetail` 里由已加载的状态投影，路径内无 `SaveQuotaSnapshot` / 通知调用 |

小结：**15 条通过、2 条带低危偏差（AC-8 的 F-1、F-2 归到契约而非 AC 本身）**。没有「PRD 没写但加了功能」的越界；q01（写权重）确实没做，`priority/weight` 没有任何写入路径。

## 4. 只读断言（单独复核）

- 界面：面板内可交互元素 0；`data-lending-read-only` 标签常驻；详情同样为 0。
- 接口：`POST/PUT/PATCH/DELETE /omc/api/v1/management/quota` → 405；`/omc/api/v1/management/lending` → 404；未登录 `GET` → 401（两条路径都测）。
- 代码：`git diff --name-status 11-23-t7-dev-base..HEAD` 中无 `internal/api/handler.go`、无 `migrations/`、无 `internal/cpa/` 的写路径改动。
- `reset_advice`：详情 JSON 里该字段仍由 `consoleResetAdvice` 投影（`target.ResetAdvice = advice[authIndex]`），字段名与位置未动；本次抽样凭证 `reset_advice` 为 `null`（该卡没有权益读数），与改动前行为一致。

## 5. 缺口（没构造出来，因此不写「通过」）

以下状态只有 Go 单测 / 组件测试 / 纯逻辑测试覆盖，**界面上没有造出真实数据**，取证到此为止：

1. **只有上游文字的重置时刻**（AC-1 第三分支、AC-10 的 `reset_time_unknown`）：demo 与 e2e 夹具里所有长窗口都带 `reset_at_ms`，没能在页面上看到「时刻未知」那条文案。
2. **读数过期**（`reading_stale` 降级，AC-10、AC-16）：需要把快照 `observed_at_ms` 推到鲜度界之外，e2e 数据是「刚刚采样」，未构造。
3. **窗口口径不同**（`window_type_mismatch`，AC-6、AC-10）：没有「同一 provider 同套餐、两张卡分别给周窗口和日窗口」的夹具。
4. **`limited` 池（单卡可服务）**：live 池是三卡均衡，界面上的「受限」分支只走组件测试。
5. 根因：`scripts/fake-cpa.mjs` **没有 F-LEND-\* 系列夹具**（`rg F-LEND scripts/` 为空），`cases.md` 里那些「F-LEND-BASE / F-TEXT-ONLY / F-STALE / F-NO-LONG / F-WINDOW-MISMATCH` 目前只能靠单测与手写 mock 表达。另外 `codex.pro` 在默认 e2e 夹具里只有一个 pro 席位，无法成池，所以浏览器层必须靠 demo 数据才有池可看。
6. `pnpm test:fast --base` 的 `repository self-tests` 10 条红为既有环境红，未逐条深入（任务书已排除）。

## 6. 范围外观察（只记录，不要求改）

- **二期**：自动化 L2/L3（人工确认后写 priority/weight、窄场景自动）、订阅衔接与延长、同供应商多凭证池口径扩展、把模型可用性纳入池、同账号排除、MCP `lending_advice` —— 全部未在一期出现，符合任务书「不要把二期缺失当一期要修」。
- **同账号排除**：仓库里其实有 `quota.resolveCodexAccountID`（`internal/quota/service.go`），一期没用它。PRD 写的是「如能确认是同一账号，直接排除…判不准时按不同卡参与」，属可接受的本期留白，`impl-plan.md` §3.9 已写明。
- **AC-9 每卡「建议窗口」列**：面板只在池标题给 `周限额`，每卡卡片上不带窗口类型列；原型 `02-拆借建议-三种状态.html` 同样没有，属产品形态选择而非漏做。
- **`check-docs` 红**：31 条命中全在未改动的 2026-11-12 交付文档（缺路径 9、行号引用 22），与本分支无关，但会让 `pnpm verify` 在本机一直是红的；要不要清，建议单独开一条文档整理任务。
- **`limited` 池的 `remaining_below_ten` 卡**：仍会展示它的 `days/daily`（`is_comparable=true` 所以有值）。PRD 只要求「不参与偏用排名」，未禁止展示数字，故不算偏差；但与 F-1 一起看时，建议统一「受限卡展示什么」的规则。

## 7. 17:54 范围调整的影响

浩文 17:54 要求把「额度拆借建议」整块搬到独立二级页、OAuth 页只留页首统计行一个轻入口，已在同一基线 `b4a77953` 上另派切片。对本报告的处置：

- **不记缺陷**：面板在 OAuth 页「列表页顶部固定一块」的形态、「建议块标题/依据/只读标签」在列表页的具体排布、详情抽屉里的区块位置 —— 这些都被新结构调整取代，本报告 §3 中涉及位置的描述只作为旧结构证据保留。
- **照旧有效**：行内显示（倒计时、`~`、`参考`、快重置高/低）、排序与记忆、后端 `lending` 契约、详情角色小块的数据（与列表同源）、只读断言、`reset_advice` 不变。**F-1、F-2 都在这一侧**，不受搬迁影响（F-1 是角色语义，F-2 是 DTO 字段），新结构同样会踩到。
- 一期 AC 若在新分支上改口径（AC-5、9–11、12–14、15、16 改位置与口径，新增 AC-14a/b/c），以新分支 `prd.md` 为准重判；本报告只对基线 AC 负责。

## 8. 新结构复审清单（供派复审用）

按我这次踩过的坑排的优先级，5–8 点：

1. **只读范围扩到二级页**：新页面内 `button/[role=button]/a[href]/input/select/textarea` 计数必须为 0（或只剩导航类返回按钮），「只提示，不改路由」固定文案必须在二级页也出现；`POST/PUT/PATCH/DELETE` 到任何新路径 404/405。这次旧结构测到 0，新结构要重测，因为整块组件被搬走时最容易顺手加一个「应用」按钮。
2. **三处同源**：页首统计行入口、二级页列表、凭证详情的角色小块，同一 `auth_index` 的 `role / remaining_percent / days_to_reset / daily_available_percent / valid_until_ms / reason_codes` 必须逐字段相同；建议直接对比 `lendingDetailFor` 与二级页 `deriveLendingView` 的输出（这次详情与列表是逐字节相同，新结构要保住这一点）。
3. **页面时钟**：二级页停留 30 秒以上，倒计时与日均可用要推进，且**不能**因为搬迁而引入对 `/management/quota` 的额外轮询（当前 `refetchInterval` 是基线 60 秒，搬迁不应改它）；`~` 与 `· 参考`要在时间推进后仍在。
4. **旧后端降级**：`lending` 字段缺失时（旧网关）二级页要有空态条而不是空白页或报错；这次旧结构是 `data-lending-absent="true"` 单行条，新结构要确认入口本身在无 `lending` 时如何呈现（统计行是否还该出现入口）。
5. **直接打开与刷新**：`/omc/<二级页路径>` 直接访问、F5、以及带 `?sort=reset-asc` 的深链都要能进；base path `/omc` 前缀不能写死（离线嵌入、`/omc` 子路径是 Tier 1）。这次排序记忆靠 URL 参数，搬迁后要确认参数仍被读写。
6. **侧栏高亮与返回**：二级页在侧栏要落在一个明确条目上（新增还是挂在 OAuth 下），返回 OAuth 页与浏览器 Back 都要正常；`overlay-back` 场景在受影响清单里，值得跑一次。
7. **未进池折叠**：新结构说「未进池折叠」，要确认折叠**不吞掉** `unpooled_count`（AC-14 要求数量与原因必须可见），展开后每张卡的原因文案仍与 `lendingReasonLabel` 的键一一对应。
8. **F-1 / F-2 复验**：`limited` 池里可服务卡不能再挂「受限」且无原因；`unavailable` 池的 `valid_until_ms` 要么为空、要么契约文字同步改。

## 9. 界面证据（旧结构，截图不进 git）

目录 `C:/Users/lhw/omc-acceptance/2026-11-23-t7-s16/shots/`：

| 文件 | 内容 |
|---|---|
| `01-overview-zh.png` | 列表页建议面板（中文）：`codex · Pro 200 · 周限额`、`均衡使用`、`参考（推算重置）`、`建议有效到 11/25 16:09 · 1天23小时后`、三卡三列、底部 16 张未参与逐条原因、只读说明 |
| `02-detail-drawer-zh.png` | 详情抽屉：`属于 codex · Pro 200 · 周限额 池（同套餐 3 张）`、`本卡`、数字与列表一致、下面接 `重置积分到期时间` 与窗口行（快重置 · 余量高/低） |
| `03-narrow-700-zh.png` | 700px 窄屏：行内 `~距重置 … · 参考`、`快重置 · 高 75%`、`快重置 · 低 55%` |
| `03b-narrow-panel-zh.png` | 700px 窄屏面板单列：卡片纵向堆叠，无横向溢出 |
| `04-overview-en.png` | 英文全量：`Recomputed from the … reading · follows the page clock`、`Use evenly`、`Reference (estimated reset)`、`Not in any pool: 16 …`、`This advice is read-only …` |
| `05-detail-drawer-en-live.png` | 本轮复查时新截：英文详情抽屉，`In the codex · Pro 200 · Weekly limit pool (3 credentials)`、`Advice valid until 11/25 16:09 · 1d 22h left` |

## 10. 建议重点看的三处

1. `01-overview-zh.png` 底部「未参与本页建议：16 张」那段 —— 16 张逐条原因是否读得下去（AC-5 / AC-14 的实际观感）。
2. `02-detail-drawer-zh.png` 里「属于 … 池（同套餐 3 张）」到「重置积分到期时间」这一段 —— 两种建议分区后的可读性（AC-13 / AC-15）。
3. `03-narrow-700-zh.png` 的行内标记 —— `快重置 · 高/低` 与 `~距重置` 在窄屏是否还分得清（AC-1 / AC-2 的真实密度）。
