# 额度拆借一期 · 验收报告（r1）

- 运行：`2026-11-23#T7-S19`（验收会话）；被测基线 `db387d01`，并入改版修复分支 `Newoahil/11-23-t7-s17-codex`（HEAD `08bec60f`，6 个提交）。
- 合并：`git merge --no-ff`，无冲突，合并提交见 `git log` 中 `merge(quota): 并入 S17 复审修复`。
- 用例：`cases.md` 51 条（P0 26 / P1 24 / P2 1）。
- 复审判定（r2）：F-1、F-2、读失败降级、只读边界已通过；本轮补验其 4 条低危修复与 2 处顺手改。

## 结论

- 范围内要修的问题：**0 条**。二期复审提出的 4 条低危与 2 处顺手改在并入 `08bec60f` 后逐条复验通过，见「合并后复验」。
- 51 条用例中，49 条通过、2 条按计划不做（C-32 同账号多凭证、C-34 只读 MCP），没有未测、部分或阻塞。
- 只读边界成立：拆借路径没有写方法（`/management/quota` 写方法 405、`/management/lending` 全部 404），浏览期间未写 CPA `priority` / `weight`，审计只在登录时增加。
- 建议：可以进入发布评审。发布说明只需写清一期不做的两个范围项（同账号多凭证、MCP）。

## 范围与环境

被测对象是额度拆借一期：OAuth 管理页入口、`/omc/quota-lending` 二级页、凭证详情里的同池建议，以及它们共用的 `/management/quota.lending` 数据。

- 构建：`pnpm build`（前端并入单二进制）；`docker run ... golang:1.27.1 go build ./cmd/oh-my-cpa` 交叉编译到 `tmp/omc-lending.exe`。
- 静态与单测：`pnpm test:logic`（90 套通过）、`pnpm test:components`、`pnpm check-docs`（见环境性红项）。
- Go：`go test ./internal/quota ./internal/api ./internal/operations` 在容器内全部 `ok`。
- 浏览器场景：`pnpm check:ui --scenario quota-lending`（真实 Chromium，路由打桩覆盖十余种池状态、缺字段与 `null`），连续多次通过。
- 体验环境：真实二进制 + 进程内 fake CPA + 真实 SQLite，脚本 `scripts/acceptance/lending-experience-env.mjs`，自检 `--check` 16/16 通过。
- 接口与只读：对运行中的二进制直接发请求（登录、读、写方法探测），并从 fake CPA 侧核对上游请求与凭证列表。
- 产物：`acceptance/shots/` 下的截图只是证据，未纳入版本库。

## 用例结果

| 用例 | 结论 | 证据 |
| --- | --- | --- |
| C-01 行内剩余与倒计时 | 通过 | probe base：88%、4.3 天；`shots/10-probe-state-base.png` |
| C-02 推算时刻带 ~ 与精度 | 通过 | probe derived：`~`、参考（推算重置）；Go `TestProjectLendingAdviceReferencePrecision` |
| C-03 只有文字/无时刻 | 通过 | probe textOnly：保留上游文字、时刻未知；`shots/14-probe-state-text-only.png` |
| C-04 缺读数说明缺哪种 | 通过 | probe noLong：写明没有长窗口，数字为 `—`；`shots/16-probe-state-no-long.png` |
| C-05 快重置标记与余量高低 | 通过 | 体验环境 `--check`：B 行 `data-fast-reset=low`、文案「快重置 · 余量低」，A 行没有；Go 两个 fast-reset 单测 |
| C-06 周期未知与剩余缺失不硬判 | 通过 | `test:logic`：周期未知不打快重置标记、不算长窗口；probe `missingVsZero`：剩余缺失给「—」和「没有剩余百分比」，不写成 0% |
| C-07 按距自然重置排序 | 通过 | 实机选「距自然重置最近」后行序 b(11/26 00:43)→a(11/28 05:41)，与 `/management/quota` 各卡最早长窗口逐一对应；两张 5 小时窗口重置时刻相同，若误用短窗口会退回名称序；`test:logic` 另证无长窗口沉底、短窗口不决定顺序 |
| C-08 排序被记住与重置筛选 | 通过 | 实机：刷新后仍为 `sort=reset-asc` 且顺序不变；点「重置筛选」清掉参数、回到名称序 a→b；`shots/45-oauth-sort-reset-asc-light-zh-1440.png` |
| C-09 偏用 A、少用 B | 通过 | 体验环境 `--check`（A prefer / B avoid / 日均 20.4 与 19.0）；probe base；Go `TwoSeats` |
| C-10 快重置但余量高 | 通过 | Go `FastResetWithHighRemainingPrefersTheFastSeat`；`test:logic` 快重置阈值 |
| C-11 差值不到 1 点/天 | 通过 | Go `BalanceThreshold`；`test:logic` 1 点阈值 |
| C-12 不足 1 小时按 1 小时 | 通过 | Go `DailyAvailablePercentClampsUnderAnHour`；`test:logic` 同断言 |
| C-13 建议窗口取最低长窗口 | 通过 | Go `SelectsTheLowestLongWindow`；`test:logic` 只取长标准窗口 |
| C-14 窗口类型不同整池无法建议 | 通过 | probe windowMismatch；`shots/17-probe-state-window-mismatch.png`；Go `WindowTypeMismatch` |
| C-15 池块数字、理由、有效期一致 | 通过 | probe base：有效期文案、理由含两张卡与「先偏用」 |
| C-16 缺重置时刻 | 通过 | probe textOnly：unavailable + 逐卡原因；Go `MissingResetTimeBlocksThePool` |
| C-17 读数过期降级 | 通过 | probe stale：`读数已过期`、不给有效期；`shots/15-probe-state-stale.png` |
| C-18 缺长窗口不可比较 | 通过 | probe noLong：整池 unavailable，该卡无百分比 |
| C-19 受限卡不参与排名 | 通过 | probe restricted：两张照常排名、第三张 limited 带原因；`shots/19-probe-state-restricted.png` |
| C-20 可服务不足两张 | 通过 | probe limited：健康卡保持 normal、受限卡带原因、池写明无法给结论；`shots/18-probe-state-limited.png` |
| C-21 推算重置按 ~ 与参考 | 通过 | probe derived：`~`、参考标记、门槛内均衡；Go `ReferencePrecision` |
| C-22 与重置权益建议分区 | 通过 | 详情抽屉独立分块，拆借块只谈偏用/少用；`shots/24-detail-quota-lending-link-en-1440.png` |
| C-23 三处同源 | 通过 | probe：详情与页同池同角色；入口计数与池数一致；`shots/22-probe-detail-pool-link.png` |
| C-24 同池判定 | 通过 | Go `UnknownPlanNeverPools`、`SingleSeatDoesNotFormAPool`；probe 未进池「套餐未知」 |
| C-25 无同池/全不可比较空状态 | 通过 | probe empty、single 与四种 unavailable；`shots/42-state-empty-zh-postmerge.png` |
| C-26 无写动作、固定只读标签 | 通过 | probe：页面按钮无应用/保存/切换/消耗，输入框为 0；标签在标题块下方；`shots/31-lending-page-light-zh-postmerge-1440.png` |
| C-27 不写 CPA priority / weight | 通过 | 浏览前后 fake CPA 凭证列表 priority=1、weight=1 不变；上游只有 GET 与只读 `requests/api-call` |
| C-28 不落库、不推送、不改采集 | 通过 | 登录 +1 条 auth 审计，浏览 OAuth→二级页→详情→返回不增；全程无写请求 |
| C-29 时区与页面时钟重算 | 通过 | 体验环境固定 `Asia/Shanghai`；probe 等 31 秒文本推进、`/management/quota` 请求数不变（页面按 0.1 天粒度，30 秒不一定跨档） |
| C-30 窄屏单列、不只靠颜色 | 通过 | probe 760/390：`scrollWidth == innerWidth`、卡片同一列；角色有文字；`shots/34-lending-page-dark-zh-postmerge-760.png`、`shots/35-lending-page-dark-zh-postmerge-390.png` |
| C-31 旧网关与缺 plan_type | 通过 | 缺字段/`null` 显示降级空态（probe）；未知套餐永不进池（Go + 未进池分组） |
| C-32 同账号多凭证 | 范围外 · 一期不做 | impl-plan §3.9「同一账号与模型可用性（本期留白）」：没有可靠账号 id，本期按不同卡参与比较，风险记在 §8 |
| C-33 池按最紧迫重置排序 | 通过 | 真实后端两套餐夹具：team（12 小时）排在 pro（2 天 7 小时）前，尽管 team 凭证后创建；页面按该顺序渲染；`shots/46-lending-two-pools-order-dark-zh-1440.png` |
| C-34 只读 MCP 不泄露 | 范围外 · 一期不做 | 一期没有新增 MCP：`internal/agent` 与 `internal/mcpbridge` 无 lending 引用，能力注册表没有拆借工具 |
| C-35 总览窗口字段齐全 | 通过 | 实读 `/management/quota`：window 含 id/label/kind/scope/used/remaining/reset_at_ms/period_hours/reset_accuracy/usage |
| C-36 建议字段与只读性 | 通过 | 实读：pools 含 state/valid_until_ms/observed_at_ms/fresh_until_ms/is_reference/reason_codes/cards；写方法不可用 |
| C-37 降级路径原因字段 | 通过 | Go 缺时刻/过期/窗口不符/可服务性四个单测；probe 逐卡显示原因 |
| C-38 未鉴权 401 | 通过 | 未登录 GET quota 与详情均 401，写方法 401 |
| C-39 详情与列表同源 | 通过 | Go `DetailMatchesOverview`；probe 详情同池同角色 |
| C-40 精度字段不被改写 | 通过 | Go `ReferencePrecision`；实读 `reset_accuracy` 原样（derived/exact） |
| C-41 缺失值与零值可区分 | 通过 | probe `missingVsZero`：剩余 0 显示「0%」，缺失显示「—」并写「没有剩余百分比」，两者不混同；`shots/47-lending-missing-vs-zero-zh.png` |
| C-42 建议路径没有写方法 | 通过 | 已登录 POST/PUT/PATCH/DELETE `/management/quota` 全 405；`/management/lending` 全方法 404 |
| C-43 路径、标题与侧栏高亮 | 通过 | probe 与实机：`/quota-lending` 与带尾斜杠都高亮 OAuth 管理；`shots/32-lending-trailing-slash-nav-light-zh-1440.png` |
| C-44 返回上一页 | 通过 | probe：应用内进入显示一个返回；新标签直接打开不显示 |
| C-45 直接打开、刷新、前进后退 | 通过 | probe：直接打开、刷新、前进后退都渲染出整池 |
| C-46 旧后端/读取失败降级 | 通过 | probe：`lending: null` 显示「暂时读不到拆借建议」，空池显示「暂无可拆借的同池凭证」；`shots/41-state-null-zh-postmerge.png`、`shots/44-state-null-light-zh-postmerge.png`、`shots/42-state-empty-zh-postmerge.png` |
| C-47 入口位置与不增高度 | 通过 | probe：入口是页头内 `<a>`，加入前后页头高度差 ≤1px；`shots/30-oauth-entry-light-zh-postmerge-1440.png` |
| C-48 入口三态与缺字段 | 通过 | probe：advised/balanced/plain 三态，缺字段与 `null` 不显示；`shots/23-probe-oauth-entry-advised.png` |
| C-49 未进池折叠与计数 | 通过 | probe：收起只显示总数与三组计数，展开三组、再展开见 3 条（2 个不重复名字）；`shots/20-probe-unpooled-folded.png`、`shots/21-probe-unpooled-expanded.png` |
| C-50 体验场景两张卡 | 通过 | 脚本自检 16/16：A 偏用 20.4%/天、B 少用 19.0%/天、差 1.40；`shots/02-lending-hero-light-zh-1440.png`、`shots/03-lending-hero-dark-zh-1440.png` |
| C-51 审查 §8 复审清单 | 通过 | 8 点都有断言：文字型重置时刻、过期、窗口不符、受限池、只读标签、详情整池链接、未进池折叠、入口三态 |

## AC 覆盖

| AC | 结论 | 主要证据 |
| --- | --- | --- |
| AC-1 | 通过 | C-01～C-04 |
| AC-2 | 通过 | C-05 |
| AC-3 | 通过 | C-07 |
| AC-4 | 通过 | C-08 |
| AC-5 | 通过 | C-24、C-49 |
| AC-6 | 通过 | C-13、C-14 |
| AC-7 | 通过 | C-09～C-12 |
| AC-8 | 通过 | C-19、C-20、C-31 |
| AC-9 | 通过 | C-15 |
| AC-10 | 通过 | C-14、C-16～C-18 |
| AC-11 | 通过 | C-21（及 C-02 显示面） |
| AC-12 | 通过 | C-26、C-27、C-42 |
| AC-13 | 通过 | C-22 |
| AC-14 | 通过 | C-25 |
| AC-14a | 通过 | C-43～C-46 |
| AC-14b | 通过 | C-47、C-48 |
| AC-14c | 通过 | C-49 |
| AC-15 | 通过 | C-23、C-39 |
| AC-16 | 通过 | C-29 |
| AC-17 | 通过 | C-28 |

## 问题清单

### 范围内要修

无。

### 范围外观察（按严重度）

- **P2 · 入口对「均衡 + 不可比较」只报均衡**：一池均衡、另一池窗口类型不同时，入口写「额度拆借 · 均衡」，不可比较的那池没有痕迹。这符合 AC-14b 的三态定义，但读者容易以为两池都健康；二期可考虑入口显示不可比较计数或改用中性词。证据：probe `mixed` 断言。
- **P2 · 390 宽度下页面 h1 不出现**：窄屏只看到面包屑与副标题，只读标签与正文正常。是否为既有窄屏规则需要产品确认，本包未改动公共页头。
- **P3 · 排序空键只有单测覆盖**：体验环境两张卡都有长窗口，界面上看不到「没有长窗口的排最后」；该断言由 `test:logic` 的 `test-oauth-sort.ts` 承担，要眼见需要一条无长窗口的夹具。

## 合并后复验

并入 `Newoahil/11-23-t7-s17-codex`（HEAD `08bec60f`，6 个提交：读失败空态、尾斜杠高亮、英文单复数、testing 文档、未进池按钮样式、只读标签）。`git merge --no-ff` 无冲突；改动面是 `web/src/pages/quota`、`web/src/i18n`、`docs/testing.md` 与两个组件测试，与本会话的 `scripts/acceptance` 写集不重叠。

合并后重新 `pnpm build`、交叉编译，并在 18431 上新起环境；`pnpm check:ui --scenario quota-lending` 通过（新增断言后 84～95 秒），体验环境 `--check` 16/16。以下逐条是在新二进制上的真实点击与 DOM 断言。

| 检查点 | 结果 | 证据 |
| --- | --- | --- |
| 二级页标题块下方有固定标签「只提示，不改路由」，且不在公共页头内 | 通过 | DOM：标签在 `[data-lending-view="page"]` 内、`top=164`，页头 `bottom=140`，`head.contains(tag)=false`；`shots/31-lending-page-light-zh-postmerge-1440.png`、`shots/33-lending-page-dark-zh-postmerge-1440.png`、`shots/34-lending-page-dark-zh-postmerge-760.png`、`shots/35-lending-page-dark-zh-postmerge-390.png` |
| 「未进池」摘要按钮与分组按钮同款（不是浏览器灰按钮），深浅色与窄屏 | 通过 | probe 比较计算样式：背景 `rgba(0,0,0,0)`，颜色/字体/边框与分组按钮逐项相等，light、dark、390 三处都成立；`shots/38-unpooled-light-zh-postmerge.png`、`shots/39-unpooled-dark-zh-postmerge.png`、`shots/40-unpooled-dark-zh-postmerge-390.png` |
| 入口英文：1 条 `1 suggestion`，多条 `N suggestions`，中文不变 | 通过 | 实机（英文）：`Quota lending · 1 suggestion`；两池夹具：`Quota lending · 2 suggestions`；中文实机仍是「额度拆借 · 1 条建议」；`shots/36-oauth-entry-dark-en-postmerge-1440.png`、`shots/43-oauth-entry-two-pools-en-postmerge.png` |
| 带尾斜杠 `/omc/quota-lending/` 侧栏仍高亮 OAuth 管理 | 通过 | 实机访问 `/omc/quota-lending/`：`.ant-menu-item-selected` 为「OAuth 管理」，标题仍是 OAuth 管理；`shots/32-lending-trailing-slash-nav-light-zh-1440.png` |
| `lending: null` 与「确实没有池」文案分开 | 通过 | probe：`null` →「暂时读不到拆借建议」，空池 →「暂无可拆借的同池凭证」，两句互斥；`shots/41-state-null-zh-postmerge.png`、`shots/44-state-null-light-zh-postmerge.png`、`shots/42-state-empty-zh-postmerge.png` |
| `docs/testing.md` 入口三态归属与场景名 | 通过 | 文本核对：入口三态归 `web/tests/quota-lending-mount.component.test.tsx`，场景名为 `quota-lending` |

复审报告里的两条观察：

- **一池均衡、另一池不可比较时入口显示什么**：显示「额度拆借 · 均衡」。已用 `mixed` 夹具断言，并记入「问题清单 · 范围外观察」。
- **读失败的空态文案**：见上表第 5 行，`null` 与空池分开；截图为浅色/深色各一张。

合并后同时跑过：`pnpm test:logic`（90 套）、`pnpm test:components`、`go test ./internal/quota ./internal/api ./internal/operations`（全 `ok`）。

同一轮补测：C-07/C-08 排序（实机选择、刷新、重置筛选三态）、C-33 两池顺序（真实 API + 页面渲染）、C-41 缺失与零值（probe），结果见用例结果。

## 环境性红项

这些红项在本次验收前就存在，或来自本机环境，不是额度拆借引入的：

- `pnpm check-docs` 失败：`docs/delivery/2026-11-12-omc-three-credential-quota/impl-plan.md` 有 9 条 MISSING-PATH，同目录还有 22 条绝对行号引用（分布在 `cases.md` 与 `impl-plan.md`）。本包新增文档没有绝对行号，也没有新增缺失路径。
- `pnpm` 引擎警告：仓库要求 node `22.23.2`，本机为 `24.16.0`；所有套件仍正常跑完。
- 主机没有可用的 Go 工具链，Go 测试固定在 `golang:1.27.1` 容器里跑；容器内三个包全部 `ok`。
- 探针曾出现一次竞态：点「在额度拆借页查看整池」后立刻数池块，页面尚未挂载。已改为等待池块出现（元素等待，不是固定 sleep），没有加重试或放宽断言。

## 体验环境

真实二进制 + 进程内 fake CPA + 真实 SQLite，场景是「浩文两张卡」：`codex-demo-a.json`（Pro 200，剩 88%，4 天后重置）与 `codex-demo-b.json`（剩 40%，2 天后重置，快重置 · 余量低）。

- 入口：`http://127.0.0.1:18431/omc/oauth-management?page_size=48`
- 二级页：`http://127.0.0.1:18431/omc/quota-lending`
- 管理密钥：`omc-e2e-management-key`
- 端口：应用 18431、fake CPA 18432、控制口 18433（`/status`、`/requests`、`/reseed`）
- 状态文件：`tmp/lending-experience-env.json`（含 node/app PID、数据目录、二进制路径）
- 启动：`node scripts/acceptance/lending-experience-env.mjs`
- 自检：`node scripts/acceptance/lending-experience-env.mjs --check`（用 18441/18442 私有端口，不动常驻环境）
- 重新种场景：`node scripts/acceptance/lending-experience-env.mjs --reseed`（把两张卡的重置时刻重新锚到当前时刻）
- 两池场景：`OMC_LENDING_FIXTURE=twoPools OMC_LENDING_APP_PORT=18451 OMC_LENDING_CPA_PORT=18452 OMC_LENDING_CONTROL_PORT=18453 OMC_LENDING_DATA_DIR=tmp/lending-two-pools-data node scripts/acceptance/lending-experience-env.mjs`（状态文件按端口命名，不会覆盖常驻环境；用完结束其 node 进程）
- 关闭：结束状态文件里的 node 进程（当前为 27224），应用 39312 会随之退出；也可在任务管理器结束 `omc-lending.exe`
- 注意：启动脚本会给应用注入 `TZ=Asia/Shanghai`，手工起二进制时要一并带上，否则时报时区读取失败

三个看点：OAuth 页头的「额度拆借 · 1 条建议」、二级页的两张卡与「偏用/少用」、以及「未进池」两级的折叠。场景里只有两张可成池的卡，未进池分组要在探测场景（`pnpm check:ui --scenario quota-lending`）里看。

## 截图清单

路径相对本目录下的 `shots/`，截图未纳入版本库。

合并前（体验环境，自检 16/16）：

- `01-oauth-entry-light-zh-1440.png`：OAuth 页头入口，浅色中文，不增高。
- `02-lending-hero-light-zh-1440.png`、`03-lending-hero-dark-zh-1440.png`：两张卡的偏用/少用，浅色与深色。
- `04-lending-hero-dark-en-1440.png`、`06-lending-hero-dark-en-760.png`、`07-lending-hero-dark-en-390.png`：英文与窄屏。
- `05-oauth-entry-dark-en-premerge-1440.png`：合并前英文入口 `1 suggestions`（对照用）。

探测场景（真实 Chromium，路由打桩）：

- `10-probe-state-base.png`、`11-…-760`、`12-…-390`、`13-…-en`：基准池与窄屏、英文。
- `14-…-text-only.png`、`15-…-stale.png`、`16-…-no-long.png`、`17-…-window-mismatch.png`、`18-…-limited.png`、`19-…-restricted.png`：六种降级与受限状态。
- `20-probe-unpooled-folded.png`、`21-probe-unpooled-expanded.png`：未进池两级折叠。
- `22-probe-detail-pool-link.png`：详情里的整池链接。
- `23-probe-oauth-entry-advised.png`：入口 advised 态。
- `24-detail-quota-lending-link-en-1440.png`：详情抽屉的拆借分区与整池链接（英文）。

合并后（新二进制）：

- `30-oauth-entry-light-zh-postmerge-1440.png`：入口中文「1 条建议」。
- `31-lending-page-light-zh-postmerge-1440.png`、`33-lending-page-dark-zh-postmerge-1440.png`：只读标签在标题块下方，浅色与深色。
- `32-lending-trailing-slash-nav-light-zh-1440.png`：带尾斜杠地址仍高亮 OAuth 管理。
- `34-lending-page-dark-zh-postmerge-760.png`、`35-lending-page-dark-zh-postmerge-390.png`：760/390 单列。
- `36-oauth-entry-dark-en-postmerge-1440.png`：英文 `1 suggestion`。
- `37-lending-page-dark-en-postmerge-1440.png`：英文二级页。
- `38-unpooled-light-zh-postmerge.png`、`39-unpooled-dark-zh-postmerge.png`、`40-unpooled-dark-zh-postmerge-390.png`：摘要按钮与分组按钮同款，浅色、深色、390。
- `41-state-null-zh-postmerge.png`、`44-state-null-light-zh-postmerge.png`：`lending: null` 的「暂时读不到拆借建议」，深色与浅色。
- `42-state-empty-zh-postmerge.png`：确实没有池的空态。
- `43-oauth-entry-two-pools-en-postmerge.png`：两池时 `2 suggestions`。
- `45-oauth-sort-reset-asc-light-zh-1440.png`：OAuth 页选「距自然重置最近」后的行序。
- `46-lending-two-pools-order-dark-zh-1440.png`：真实后端两池按最紧迫重置排序。
- `47-lending-missing-vs-zero-zh.png`：剩余 0% 与缺失「—」并列，不混同。
- `48-probe-pool-order.png`：页面按载荷顺序渲染两池。

## 缺口与未测

一期范围内没有未测项。两项按计划不做：

- C-32 同账号多凭证：impl-plan §3.9「同一账号与模型可用性（本期留白）」，没有可靠账号 id，本期按不同卡参与比较。
- C-34 只读 MCP：一期没有新增 MCP 工具，`internal/agent` 与 `internal/mcpbridge` 都没有 lending 引用。

仍只有单测覆盖、界面未直接复现的一条：C-07 里「没有长窗口或没有重置时刻的排最后」——体验环境两张卡都有长窗口。断言在 `scripts/test-oauth-sort.ts` 与 `scripts/test-quota-lending.ts`，要眼见需要一条无长窗口的夹具。