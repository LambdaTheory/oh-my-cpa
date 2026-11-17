---
type: cases
slug: 2026-11-17-quota-prediction-usability
status: draft
updated: 2026-11-17
prd: 无 PRD；依据需求原话与前置调研（见下）
summary: "26 条用例（P0 14 / P1 12），覆盖第一期 A1-A4/C1/C3/C4 与总验、第二期 B1 四条；浏览器 4、接口 8、e2e 14"
---

# 验收用例：额度预测可用化（修滚动窗口与抖动容差 + 补 CMC/OPC 采集）

依据：

- 需求原话（两期验收口径）：`E:/works/Homan-context/runs/2026-11-17-2026-11-17-T8.md` 的「任务说明」。
- 前置调研：`E:/works/Homan-context/inbox/agents/调研/2026-11-17-cpa-额度预测数据调研.md`（§3 现状、§5.1/§5.2/§5.3 修法与收窄口径）。
- 线上 before：`docs/delivery/2026-11-17-quota-prediction-usability/acceptance/before.md`（2026-11-17 19:36 +08，只读 MCP，未刷新）。
- **没有 PRD**。需求原话 + 调研报告是唯一基准；实现计划与代码不作为期望来源。接口字段名沿用当前已发布形状（`quota_prediction` MCP、`GET /api/v1/management/dashboard/predictions`、`GET /api/v1/management/quota`、`model_advice` MCP）。

判定口径：结论只有「通过 / 范围内要修 / 范围外观察」。预期写用户可见结果或可检查的接口字段，不写实现方式。没跑过的项不写通过；一个路子打不开就如实记缺口，最多试三次。

## 测试环境

### 基线与入口

- 基线：`11-17-t5-omc-dev-base` = `origin/dev` `ba9e2ee7`。第一期验收对象是第一期功能分支（PM 提供）；第二期以第一期分支为 base（第一期已合入则改 `dev`）。
- Node 22 / pnpm 11；首次 `pnpm install`。
- Go 1.22+；本机没有 Go 时按项目约定用 Docker 跑，不改仓库配置。
- 浏览器：项目自带的 probe harness（Vite dev server + mock API），不需要真实账号。当前预测面板场景是 `dashboard-quota-prediction`（`scripts/acceptance/probes/dashboardQuotaPrediction.mjs`）。
- 线上只读：cpa-buddy MCP 工具 `quota_prediction`（全量）、`quota_list`、`model_advice`、`quota_observations_list`。**只读**：不调用 refresh/write，不管理或读取密钥，不碰生产配置与真实数据。
- 时间：所有判定用固定时钟/固定观测时间；线上回放以 `before.md` 的读数时间（2026-11-17 19:36:22 +08，`generated_at_ms` 1794915382118）为基准，不用「现在」重算。

### 命令

| 层 | 命令 | 覆盖用例 |
|---|---|---|
| Go 单测 | `go test ./internal/quota ./internal/operations ./internal/api`（或 Docker 等价） | C-01～C-10、C-18 |
| Go 静态 | `go vet ./...` | C-19 |
| 前端逻辑/组件 | `pnpm type-check && pnpm test:logic && pnpm test:components` | C-11、C-13、C-15 的组件断言 |
| 浏览器 probe | `pnpm check:ui --scenario dashboard-quota-prediction`（快速）；完整门 `pnpm verify:probes` | C-11、C-13、C-15 |
| 静态总门 | `pnpm verify:static` | C-19（既有回归；基线已知红项单列） |
| 线上只读 | cpa-buddy MCP | C-06、C-17、C-22～C-24 |

### 夹具与证据

- 浏览器与接口用例使用确定性 fixture：固定 `generated_at_ms`、固定 `observed_at_ms`，不用当前时间；同一 fixture 同时供预测面板和 `quota_list` 使用，保证「面板收起、数据仍在」可核对。
- CMC/OPC 解析用例使用离线上游夹具（JSON）喂给新 reader；线上数值另由 C-24 人眼核对，两者不互相替代。
- 截图、日志、回放输出放 `inbox/agents/验收/2026-11-17-quota-prediction-usability/`，不进 git；报告放 `docs/delivery/2026-11-17-quota-prediction-usability/acceptance/`。
- 每条记录：用例号、命令/点击路径、关键输出或截图文件名。

## 用例

### 第一期 · A1 滚动窗口边界

#### C-01 滚动窗口（reset = 观测时刻 + 周期）不再判 boundary_unknown
- AC：A1 · 类型：e2e（Go 单测） · 优先级：P0
- 前置：固定时钟。构造一个窗口：period = 5h 或 168h，`reset_at = observed_at + period + 253ms`（观测落在边界前），`used_percent` 非 0，且周期内有流量（用于隔离 A4，不让零使用短路）。
- 步骤：
  1. 通过预测入口（单测或开发提供的回放入口）跑该窗口。
  2. 读 `verdict`、`reasons`、`expected_used_percent`、`lasts_until_reset`、`exhaustion_eta_ms`。
- 预期：
  - `reasons` 不含 `boundary_unknown`，`verdict` 不因边界而 unknown。
  - 该窗口按滚动窗口处理：不给 `exhaustion_eta_ms` / `lasts_until_reset` 的确定结论，不伪造 expected 起点；`used_percent` 原样保留。
  - 把边界差改成同一量级（例如 1 秒）结果相同；改成远超容差（例如 1 小时）时仍判 `boundary_unknown`（见 C-02），说明容差不是无限放宽。

#### C-02 大的边界差仍判 boundary_unknown（防误判滚动）
- AC：A1 · 类型：e2e（Go 单测） · 优先级：P1
- 前置：`reset_at = observed_at + period + 1h`（或 `reset_at` 早于观测时刻远超一个周期），period 已知。
- 步骤：跑同一预测入口。
- 预期：`verdict = unknown`，`reasons` 含 `boundary_unknown`；不被当成滚动窗口硬算出 expected/ETA。

### 第一期 · A2 周期内重置

#### C-03 周期内重置后只丢触发前样本，从触发点后重新拟合
- AC：A2 · 类型：e2e（Go 单测） · 优先级：P0
- 前置：固定时钟。构造某窗口的完整历史：前段 used 上升；中段出现一次超过容差的下跌（或 `reset_at` 位移）作为触发点；后段从低点重新上升，后段样本满足最小证据（至少 3 个桶、覆盖至少 30 分钟）。
- 步骤：
  1. 跑预测。
  2. 读 `verdict`、`reasons`、`sample_count`、`coverage_seconds`、`expected_used_percent`、`burn_rate_per_hour`。
- 预期：
  - `verdict` 为 `on_track` 或 `will_exhaust`（有结论）；`mid_cycle_reset` 可作为诊断保留在 `reasons`，但不得作为唯一最终原因；重拟合后证据足够时必须给出 `lasts_until_reset` 结论。**预期调整（2026-11-17 PM 决定）**
  - `sample_count` / `coverage_seconds` 只反映触发点之后的样本，明显小于全部历史。
  - expected / burn rate / ETA 用触发后样本拟合；触发前的旧点不参与。

#### C-04 触发后证据不足时 unknown，原因是历史不足而非 mid_cycle_reset
- AC：A2 · 类型：e2e（Go 单测） · 优先级：P1
- 前置：同 C-03，但触发点后样本少于最小证据（例如只有 1 个桶、覆盖 10 分钟）。
- 步骤：跑预测。
- 预期：
  - `verdict = unknown`；`reasons` 含 `history_insufficient`（或等价的历史不足原因），不含 `mid_cycle_reset`。
  - `sample_count` / `coverage_seconds` 给出触发后的实际数量。
  - 不伪造 ETA、expected 或 lasts 结论。

#### C-05 已持久化的旧 mid_cycle_reset 标记可恢复
- AC：A2 · 类型：e2e（Go 单测） · 优先级：P1
- 前置：构造一个已存储的快照，带旧版本写入的 `has_mid_cycle_reset = true`（没有触发时刻）；同时提供触发之后的观测。
- 步骤：加载旧快照后跑预测。
- 预期：
  - 触发后证据足够时给出 `on_track` / `will_exhaust` 结论，不因旧标记永久 unknown。
  - 若实现选择重建或丢弃旧标记，行为可复核且不把旧标记改写成 `boundary_unknown`。
  - 旧快照的 `observed_at` / `reset_at` 不被改写。

#### C-06 线上 before 里的 mid_cycle_reset 窗口不再只因该原因 unknown
- AC：A2 · 类型：接口（线上只读回放） · 优先级：P0
- 前置：`before.md` 记录的 claude `seven_day` 552 样本 / 386253s，antigravity weekly 110 / 233 / 114 样本；用同一份只读观测（`quota_observations_list` 导出）和同一固定时钟回放新算法。
- 步骤：
  1. 对上述窗口逐一跑回放。
  2. 把每个窗口的 `verdict`、`reasons`、`sample_count`、`coverage_seconds` 写进 after 表。
- 预期：
  - 每个窗口要么变成 `on_track` / `will_exhaust`，要么 `unknown` 的原因是 `history_insufficient` 并给出触发后样本数。
  - `mid_cycle_reset` 不再作为最终 unknown 的唯一原因。
  - 不能得出上述任一结果的窗口，逐个写明原因（缺口），不判通过。

### 第一期 · A3 跨桶重置触发点日志

#### C-07 跨桶重置触发时日志能定位到具体比较和数值
- AC：A3 · 类型：e2e（日志） · 优先级：P1
- 前置：构造三类触发各一次：相邻桶 used 下跌 > 容差；相邻桶 `reset_at` 位移 > 容差；单桶内 min/max `reset_at` 位移 > 容差（或 `ResetDetected`）。
- 步骤：
  1. 跑预测并捕获服务日志（测试注入 logger，或本地起服务抓日志）。
  2. 逐条核对触发记录。
- 预期：
  - 每个触发至少一条日志，含 provider、脱敏 credential label、`window_id`、触发分支（相邻 used / 相邻 reset / 桶内 reset / 当前读数）、前桶 `last_used` 与当前桶 `min_used`（或两个 reset 值）、差值、桶时间范围。
  - 日志能回答「哪一个桶对、跌了多少（或位移多少）」。
  - **预期调整（PM 2026-11-17 19:50 决策，复制时同步）**：`auth_index` 允许出现在服务日志（与既有 `scheduler.go` / `automation` / `management_quota.go` 日志一致），它是内部分级排障标识；只要求不含密钥、账号原文。
  - 不含密钥、账号原文。

#### C-08 容差内的相邻回落不触发，也不盲改既有容差
- AC：A3 · 类型：e2e（Go 单测） · 优先级：P1
- 前置：相邻桶 used 回落 0.75pp（小于现有 1.0pp 容差），且无 reset 位移。
- 步骤：跑预测，检查 `mid_cycle_reset` 与跨桶重置判定。
- 预期：
  - 不触发跨桶重置，不写 `mid_cycle_reset`；预测行为与改动前一致。
  - 若实现调整了容差，必须附线上触发点证据并在交付说明中写明；不能静默放宽或收紧。

### 第一期 · A4 零流量零使用

#### C-09 used=0 且周期 traffic=0 直接 on_track
- AC：A4 · 类型：e2e（Go 单测） · 优先级：P0
- 前置：窗口 `used_percent = 0`，周期内 `usage.requests = 0`（或等价 traffic = 0）；reset 边界可以未知（boundary_unknown 场景），也可以带 mid-cycle 标记。
- 步骤：跑预测。
- 预期：
  - `verdict = on_track`；`reasons` 不含 `boundary_unknown` / `mid_cycle_reset`。
  - 不给出 `will_exhaust`，`lasts_until_reset` 若返回不得为 `false`。
  - 不为零使用伪造 expected / ETA / burn rate。

#### C-10 used=0 但周期 traffic>0 不短路
- AC：A4 · 类型：e2e（Go 单测） · 优先级：P1
- 前置：窗口 `used_percent = 0`，但周期内 `usage.requests > 0`（上游报 0、实际有流量）。
- 步骤：跑预测。
- 预期：
  - 不因 `used=0` 直接判 `on_track`；仍按 A1/A2 的边界与历史规则判断。
  - 结果可以是 `on_track`（边界可算且证据足）或 `unknown`（边界/历史不足），但原因不能写成「零流量」。
  - A4 的条件是「零流量 + 零使用」两者都成立。

### 第一期 · C1 rolling 窗口口径

#### C-11 滚动窗口只显示当前 used + 下次 reset，不参与 ETA/lasts
- AC：C1 · 类型：浏览器 · 优先级：P0
- 前置：`dashboard-quota-prediction` 场景注入一个已识别的滚动窗口（used 非 0，reset 每次等于 now + period，无固定周期），另加一个普通 5h 窗口作对照。
- 步骤：
  1. 运行 `pnpm check:ui --scenario dashboard-quota-prediction`；或本地起 OMC 后打开 `/dashboard`。
  2. 展开该 provider。
- 预期：
  - 滚动窗口行显示当前 used% 和下次 reset 时间；不显示 ETA、不显示「Cannot last」/「Lasts until reset」的结论、不显示 expected/projected。
  - 面板汇总（`data-testid="prediction-summary"`）不把该行计为 undecidable / cannot last。
  - 对照的普通窗口行为不变。

#### C-12 滚动窗口的接口字段不参与预测
- AC：C1 · 类型：接口 · 优先级：P1
- 前置：同一 fixture 经 `GET /api/v1/management/dashboard/predictions`（或 MCP `quota_prediction`）返回。
- 步骤：检查该窗口的字段。
- 预期：
  - `used_percent` 有值、`reset_at_ms` 有值。
  - `expected_used_percent`、`lasts_until_reset`、`exhaustion_eta_ms`、`projected_percent_at_reset`、`projected_tokens_at_reset`、`burn_rate_per_hour` 为 null。
  - `reasons` 不含 `boundary_unknown`；不被计入会耗尽的窗口。

### 第一期 · C3 零流量零使用窗口

#### C-13 零流量+零使用窗口不在预测面板单独成行
- AC：C3 · 类型：浏览器 · 优先级：P0
- 前置：`dashboard-quota-prediction` fixture 含 3 个零流量零使用窗口（例如 antigravity 3p × 3 个账号）+ 2 个正常窗口；另含 1 个 `used=0` 但有流量的窗口（负例）。
- 步骤：
  1. 运行 `pnpm check:ui --scenario dashboard-quota-prediction`。
  2. 展开全部 provider，数行与汇总。
- 预期：
  - 3 个零流量零使用窗口不以独立预测行出现；如果合并，只出现一行汇总，并给出数量（例如「3 个窗口空闲」），可展开或可读。
  - `used=0` 但有流量的窗口仍作为独立行出现（不被误收）。
  - 汇总计数不把隐藏的窗口算进 undecidable。

#### C-14 收起的窗口在额度页仍可见
- AC：C3 · 类型：接口 · 优先级：P1
- 前置：同一 fixture 同时提供给 `GET /api/v1/management/quota` 和凭据页。
- 步骤：
  1. 查 `quota_list` 中这些窗口。
  2. 打开额度页/凭据抽屉。
- 预期：这些窗口仍出现在 `quota_list` 与凭据页，`used_percent` / `reset_at_ms` 等字段不丢；只是预测面板不展示。没有数据被删除或改写成 0。

### 第一期 · C4 stale 窗口

#### C-15 capacity_unavailable = "stale" 的窗口在预测面板收起
- AC：C4 · 类型：浏览器 · 优先级：P0
- 前置：fixture 含 codex stale 窗口（`used_percent = 100`、`capacity_unavailable: "stale"`、`reading_stale`）和一个正常窗口。
- 步骤：
  1. 运行 `pnpm check:ui --scenario dashboard-quota-prediction`。
  2. 展开对应 provider。
- 预期：
  - 默认展开的预测列表里没有该 stale 窗口的独立行。
  - 如果以折叠区/汇总出现，必须标记为 stale/unavailable，并可展开核对；不显示伪造的 expected/ETA/lasts。
  - 正常窗口不受影响。

#### C-16 stale 窗口在接口里仍可识别，不被当成普通 unknown
- AC：C4 · 类型：接口 · 优先级：P1
- 前置：同一 fixture 经 predictions API 和 `quota_list`。
- 步骤：检查该窗口的返回字段。
- 预期：
  - 预测接口仍返回可识别的 stale 信息（`reading_stale` 或等价的 reasons/`capacity_unavailable`）；`quota_list` 仍带 `capacity_unavailable: "stale"`。
  - 不被计成 `on_track`，也不被错误标成 `boundary_unknown`；面板收起不改变数据层结论。

### 第一期 · 总验收

#### C-17 线上只读数据回放：列出 unknown → 有结论的窗口
- AC：第一期总验「用线上只读数据回放新算法，列出哪些窗口从 unknown 变成有结论，附 before/after 对比」 · 类型：接口（线上只读回放） · 优先级：P0
- 前置：
  - `before.md` 的 33 条 before（2026-11-17 19:36:22 +08）。
  - 回放入口由开发提供（Go 测试或命令行工具，接受导出的观测 JSON，不改实现）；同一份观测、同一固定时钟。
- 步骤：
  1. 用 before 的观测跑新算法。
  2. 生成 after 表：provider、脱敏 label、window、before verdict/reasons/samples、after verdict/reasons/samples、是否翻转。
  3. 与 `before.md` 逐窗口对上。
- 预期：
  - 6 个 fresh antigravity 3p 窗口（used=0、`boundary_unknown`）变为 `on_track`（A4），或至少不再 `boundary_unknown`（A1）。
  - 6 个 antigravity Gemini 5h/weekly + claude `seven_day`（`mid_cycle_reset`）分别变为有结论，或 `history_insufficient` 且给出触发后样本数。
  - 18 个 `stale_reading` 窗口：进入回放范围的保持 stale，不伪造结论；未进入回放范围的（例如旧凭证快照）在 after 表标注「未回放」和原因。
  - claude `five_hour` 的 `on_track`（37 样本 / 13380s / burn 6.3979）不回归。
  - 报告明确列出「哪些窗口从 unknown 变成有结论」；无法回放的窗口逐个标缺口，不整体判通过。

#### C-18 新增行为有单测：滚动窗口、抖动容错、零流量
- AC：第一期总验「新增行为要有单元测试（滚动窗口、抖动容错、零流量）」 · 类型：e2e · 优先级：P0
- 前置：第一期实现完成。
- 步骤：
  1. 跑 `go test ./internal/quota ./internal/operations ./internal/api`。
  2. 核对测试名与断言：滚动窗口（C-01）、抖动/周期内重置（C-03 或 C-08）、零流量（C-09）。
- 预期：
  - 三类行为各有可指名的测试，全部通过。
  - 既有预测测试不回归；失败项按范围外观察记录，不混进「本次通过」。

#### C-19 静态门与既有预测场景回归
- AC：第一期总验「既有行为不回归」 · 类型：e2e · 优先级：P1
- 前置：第一期实现完成。
- 步骤：
  1. `pnpm verify:static`。
  2. `pnpm check:ui --scenario dashboard-quota-prediction`；有构建产物时再跑 `pnpm verify:probes`。
- 预期：
  - 静态门通过；基线已知红项单列（如 `test-oauth-sort` 的 requests-desc 是既有失败，dev 基线以交付时核对为准），不算本次回归。
  - 既有预测面板场景断言通过；新增断言不放松既有断言。

### 第二期 · B1 CMC/OPC 采集

#### C-20 CMC 解析：3 个 auth 的窗口映射
- AC：B1-1 · 类型：e2e（离线夹具） · 优先级：P0
- 前置：CMC 上游夹具含 `windowLimits.fiveHour/weekly` 的 `used/cap/exceeded/resetAt`、`credits.monthlyCredits`、`subscriptions.currentPeriodEnd`；3 个 auth。
- 步骤：用离线夹具跑新 reader 的单测或本地 fake 上游。
- 预期：
  - 每个 CMC auth 得到 5h、weekly 窗口（以及 monthly，若订阅字段存在）。
  - `used_percent` 来自 used/cap；reset 来自 `resetAt`，`reset_accuracy = "exact"`；`period_hours` 为 5 / 168。
  - monthly 的分母若来自人工 plan 表，标注为估算/人工表，不冒充上游精确值；缺失字段不写 0。
  - 不把 3 个 auth 合并成一条。

#### C-21 OPC 解析：rolling / weekly / monthly 映射
- AC：B1-1 · 类型：e2e（离线夹具） · 优先级：P0
- 前置：OPC 上游夹具含 `usage.{rolling,weekly,monthly}{percent,resetsAt}`。
- 步骤：用离线夹具跑新 reader 的单测或本地 fake 上游。
- 预期：
  - 每个启用的 OPC key 得到三个窗口；`percent` 映射 used；`resetsAt` → `reset_at_ms`，`reset_accuracy = "exact"`。
  - weekly / monthly 的 `period_hours` 为 168 / 720；rolling 的 `period_hours` 为 null，不硬套 5h。
  - 缺失窗口不补 0，不编造 reset。

#### C-22 线上 quota_list 出现 CMC 三 auth 与 OPC key 的 windows
- AC：B1-1 · 类型：接口（线上只读 MCP） · 优先级：P0 · **部署后**
- 前置：第二期已部署（prebuilt + Dokploy，浩文执行）；cpa-buddy MCP 只读；不刷新。
- 步骤：
  1. 调 `quota_list`。
  2. 对 CMC 三个 auth 与 OPC 的启用 key 逐个核对 windows。
- 预期：
  - commandcode 三个 auth 各自出现 windows（至少 5h + weekly；有月窗口则按 C-20 的标注）。
  - OPC 的启用 key 出现 windows（数量以线上 `api-keys` 树实际为准；至少 1 个）。
  - 窗口数与上游返回一致，不使用 0 值填充；没有读取密钥，没有 refresh。
- 缺口：未部署前记「未实测」，不判通过；第二期验收报告必须写明部署版本/时间依据。

#### C-23 model_advice 里 CMC/OPC 模型不再是 without valid readings
- AC：B1-2 · 类型：接口（线上只读 MCP） · 优先级：P0 · **部署后**
- 前置：同 C-22。
- 步骤：
  1. 调 `model_advice`（全量）。
  2. 看 `commandcode/*` 与 `op-*` 模型的 verdict、reasons、`credentials_usable`、`tightest_window`。
- 预期：
  - `commandcode/*` 模型不再是 `0 blocked, N without valid readings`；`credentials_usable > 0`，`tightest_window` 有值。
  - `op-*` 模型同样。
  - gemini / claude 模型的排序与推荐不回归（`recommended` 仍为有读数模型）。
  - 其它本来就没有额度能力的模型（如 jev、step）仍标 unknown，属范围外观察。

#### C-24 真实数值与上游页面/接口人眼对一次
- AC：B1-3（并覆盖 B1-4 的「未实测」标注要求） · 类型：浏览器 + 接口 · 优先级：P0 · **部署后**
- 前置：有 CMC 真实 auth（线上已有 3 个）；OPC 有真实 key 时做。
- 步骤：
  1. 在同一分钟内读取 OMC `quota_list` 的 used/remaining 与 reset。
  2. 对照 CMC 上游页面/接口（或插件 quota-usage 页面）的同一窗口。
  3. OPC 同上；没有真 key 时记录「无真 key，未实测」。
- 预期：
  - used 百分比与上游一致（同一时刻误差 ≤ 1pp）；reset 时间误差 ≤ 2 分钟。
  - 不一致时记录上游实际值、读数时刻和差异，不自行解释成通过。
  - 没有真 key 的部分在验收报告与相关文案中明确标「未实测」；不能把 C-20/C-21 的夹具结果当作线上实测。

#### C-25 采集成本与调度兜底
- AC：B1-1（成本约束） · 类型：接口 · 优先级：P1
- 前置：本地 fake 上游记录请求；或线上服务日志（只读）。
- 步骤：
  1. 触发一轮 CMC/OPC 刷新（本地 fixture，或等待线上自动轮次）。
  2. 数每个凭证的上游请求次数与间隔。
- 预期：
  - 每个 CMC auth 每轮 1–3 次上游请求（credits / subscriptions），每个 OPC key 每轮 1 次。
  - 按 idle 2h / normal 30min 的分层调度兜底；单凭证失败不拖垮其他凭证，不无限重试。

### 范围核对

#### C-26 只改 OMC，不碰 CPA fork 与生产
- AC：需求边界「不改 CPA fork、不改 CPA 上游采集」 · 类型：e2e（diff 核对） · 优先级：P1
- 步骤：
  1. 看第一期/第二期分支的 `git diff --stat <base>...<branch>` 与 PR 文件列表。
- 预期：
  - 改动只在 OMC 仓库的 `internal/`、`web/`、测试与文档；没有 CPA fork 仓库、`private/`、生产配置或密钥。
  - 第二期 B3（OPC 换 opencode-go 插件）不在 diff 里。
  - 没有真实数据写操作或生产部署脚本。

## AC 覆盖

| AC | 用例 |
|---|---|
| A1 滚动窗口边界 | C-01、C-02、C-17 |
| A2 周期内重置 | C-03、C-04、C-05、C-06、C-17、C-18 |
| A3 跨桶重置日志 | C-07、C-08 |
| A4 零流量零使用 | C-09、C-10、C-17、C-18 |
| C1 rolling 窗口口径 | C-11、C-12 |
| C3 零流量零使用窗口收起 | C-13、C-14 |
| C4 stale 窗口收起 | C-15、C-16 |
| 第一期总验：线上回放 before/after | C-17 |
| 第一期总验：滚动/抖动/零流量单测 | C-01、C-03、C-08、C-09、C-18 |
| B1-1 CMC/OPC quota_list 出现 windows | C-20、C-21、C-22、C-25 |
| B1-2 model_advice 不再 without valid readings | C-23 |
| B1-3 真实数值人眼核对 | C-24 |
| B1-4 未实测部分如实标注 | C-24（缺口分支）、C-22 / C-23 的前置 |
| 边界：不改 CPA fork | C-26 |

## 不测的

- 第二期 B3（OPC 换 opencode-go 插件）：需求明确不在本需求内，方案里提一句即可。
- CPA fork 与 CPA 上游采集的改动：本需求明确不改；验收只核对 OMC 分支 diff（C-26）。
- 生产部署、Dokploy 操作：红线，由浩文执行；验收只核对部署后的线上只读结果。
- codex 数据质量：两个凭证都禁用，线上没有可评读数；A5（`passiveWindowKind` 放开非 300/10080 长度）不在本需求列出的 A1-A4 内，不测。
- CMC 月额度的厂商 `planAllowances` 表本身：只验证不冒充精确值，不测表是否覆盖所有 plan。
- 像素级视觉、动画、暗色/手机布局：除非 C1/C3/C4 的收起交互在手机上有特殊行为，本期不展开。
- 飞书预测摘要的内容变化：不在本期 AC。
- 真实账号写操作、登录、验证码、密钥读取：明确禁止。
- 未部署前的第二期线上行为：记「未实测」，不用本地假数据冒充线上。
