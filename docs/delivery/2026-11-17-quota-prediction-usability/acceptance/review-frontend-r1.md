---
type: review-report
slug: 2026-11-17-quota-prediction-usability-frontend-r1
status: done
updated: 2026-11-17
verdict: 要修
summary: "S3 前端两路审查（C1/C3/C4）：预测面板主路径、计数、i18n 与浏览器真测均通过；1 条范围内要修——凭据额度页遇到 capacity_unavailable: rolling_window 时整条容量说明静默消失，另有 1 处浏览器证据缺口和 1 条范围外观察"
---

# 前端审查报告：第 1 轮（切片 S3 · C1/C3/C4）

**结论：要修。** 预测面板的 C1/C3/C4 行为与方案一致，判定字段全部取自后端 console-only 语义，计数与渲染口径正确，门禁自跑通过，浏览器真测看到 rolling 行、空闲合并行和过期折叠区。**1 条范围内要修（一般）**：凭据额度页对 S1 新增的 `capacity_unavailable: "rolling_window"` 没有类型/文案映射，整条容量说明会静默消失，这是相对 S1 之前 `boundary_unknown` 可见文案的回归。另有 1 处浏览器证据缺口、1 条范围外观察，都不阻塞合并。

- 审查对象：`Newoahil/11-17-t8-s4-codex` 的 `39fb5415`（本 worktree HEAD；父提交 `0979eba6` 为 S1，含 S1+S3，未推送）。
- 基准：父运行记录 `runs/2026-11-17-2026-11-17-T8.md`「第一期」C1/C3/C4 与验收口径（需求没有另出 PRD），验收用例 `docs/delivery/2026-11-17-quota-prediction-usability/cases.md` C-11～C-16，S1 交接的字段语义（`E:/works/Newoahil__oh-my-cpa/11-17-t8-s1-codex/.agent-进度.md`），S3 交接与自测（`E:/works/Newoahil__oh-my-cpa/11-17-t8-s4-codex/.agent-进度.md`）。
- 方法：代码审查 + 需求还原度两路；门禁与浏览器探针自己重跑，不引用开发报告。
- 证据目录（不进 git）：`tmp/review-s5/`（`evidence.md`、各门禁日志、`capacity-probe.log`、两张截图）。

## 范围内要修

### F1（一般）凭据额度页遇到 `capacity_unavailable: "rolling_window"` 时静默省略容量说明

- 位置：
  - `web/src/types/quota.ts:58`：`QuotaCapacityUnavailableReason` 联合类型没有 `'rolling_window'`。
  - `web/src/pages/quota/quotaCapacity.ts:28-40`：`REASON_KEYS` 没有 `rolling_window` 键；`:92-93` 用 `REASON_KEYS[reason]` 取值，缺键得到 `undefined`，`note` 变成 `null`。
  - `web/src/pages/quota/QuotaProgressBar.tsx:117`：`capacityReading` 为 null 时整条容量行不渲染。
  - i18n 缺少对应文案：`web/src/i18n/index.tsx:3598-3611` 的 `quota.capacity_reason_*` 系列，以及 `web/src/i18n/locales/ms.ts`、`locales/zh-Hant.ts`。
- 问题：S1 让滚动窗口的 `WindowCycleRange` 返回 `rolling_window`（`internal/quota/capacity.go:154`），它会经 `attachWindowCapacity` 写进凭据页的 `capacity_unavailable`（`internal/api/management_quota_capacity.go:36-38`）。前端联合类型与 reason map 都没跟上，结果是**不报错、不显示原文，而是整条容量说明消失**。
- 回归对照：同一窗口在 S1 之前返回 `boundary_unknown`，凭据页会显示「重置时间不精确，暂不估算」。我用真实 `describeQuotaCapacity` 跑了三种 reason（临时探针 `web/tmp/review-capacity.test.ts`，未入库）：
  ```
  boundary_unknown => {"note":"quota.capacity_reason_boundary_unknown", ...}
  stale            => {"note":"quota.capacity_reason_stale", ...}
  rolling_window   => null
  ```
  完整输出在 `tmp/review-s5/capacity-probe.log`。
- 影响：线上 antigravity 3p 这类滚动窗口（本需求 A1 要修的目标形态）在凭据页不再说明「为什么不估算额度大小」。`docs/architecture.md:3968` 与 ADR 0119 修正案里「凭据额度页保留原说明」的表述对这个新值暂不成立。
- 建议：
  1. `web/src/types/quota.ts` 的联合类型加 `'rolling_window'`；
  2. `web/src/pages/quota/quotaCapacity.ts` 的 `REASON_KEYS` 加 `rolling_window: 'quota.capacity_reason_rolling_window'`；
  3. `web/src/i18n/index.tsx` 加 `'quota.capacity_reason_rolling_window': ['滚动窗口没有固定周期，暂不估算', 'Rolling window has no fixed period, so no estimate']`，并补 `locales/ms.ts`、`locales/zh-Hant.ts`（`pnpm check-i18n` 会校验）；
  4. 加一条最小测试：`describeQuotaCapacity({ capacity_unavailable: 'rolling_window' }, …)` 返回非 null 且带该 key。
  - 归属：这三个文件不在 S3 的文件清单里，是否并入 S2 分支（A2 后端）或单开小切片由跟进决定；不管归谁，合入 dev 前要有一条修复。
- 复现：对任一带 `capacity_unavailable: "rolling_window"` 的 `QuotaWindow` 调 `describeQuotaCapacity`（或打开凭据额度页/抽屉看该窗口），容量行整体缺失。

## 逐条结论

### 审查重点

| # | 审查点 | 结论 | 依据 |
|---|---|---|---|
| 1 | 判定字段来自后端真实语义，不靠前端猜 | 通过 | 滚动用 `is_rolling_window`（`web/src/components/dashboard/quotaPredictionLogic.ts:23`），空闲用 `usage_requests === 0`（`:32-37`），过期用 `reading_stale`（`:61-63`）；字段由 `PredictionAccounting` 直接投影（`internal/quota/prediction.go:158-166`、`internal/api/management_dashboard_predictions.go:12-14,65`），前端没有任何 `period_hours == null` / `used_percent == 0` 单独推断滚动的写法。 |
| 2 | 计数与渲染把收起行排除 | 通过 | `summarizePredictions` / `summarizeUnpushed` 只计 `risk`（`quotaPredictionLogic.ts:77-93,124-134`）；`visibleGroups` 丢掉只剩空闲的 provider 组（`QuotaPrediction.tsx:194`）；滚动行不进「撑不到」计数，探针汇总文案为 `1 windows cannot last until reset · 2 undecidable`。 |
| 3 | 可访问性、排序、诊断 details、i18n | 通过 | 新增折叠都用原生 `details/summary`（`QuotaPrediction.tsx:150-163,265-273`），CSS 有 `:focus-visible`（`QuotaPrediction.module.css`）；探针验证诊断可用键盘 Enter/Space 开合且不关闭 provider 组；`pnpm check-i18n` 通过（DICT 4264 keys）。 |
| 4 | 测试真钉住渲染/过滤/计数 | 通过 | 新增 4 条组件测试（`web/tests/quota-prediction-collapse.component.test.tsx`）钉 rolling 行形态、空闲合并与空组不渲染、`usage_requests` 缺失/非零负例、stale 折叠与计数；新增 6 条逻辑测试（`web/tests/quota-prediction.logic.contract.test.ts`）钉 row kind 优先级、分区、汇总、footer、结论。既有 `quota-prediction*.test.tsx` 未被放松，全量 315 条通过。 |
| 5 | 自跑门禁 | 通过 | 见下「门禁自跑」；类型检查、i18n、logic、components、浏览器探针全部通过。 |
| 6 | S3 交接的 `rolling_window` 凭据页遗留 | **范围内要修** | 见 F1。 |
| 7 | 需求还原度 C1/C3/C4 | 通过 | 见下「AC 还原度」。 |

### AC 还原度

| AC | 结论 | 依据（一手证据） |
|---|---|---|
| C1 rolling 不参与 ETA/lasts，只显示当前 used + 下次 reset | 通过 | 行渲染：`QuotaPrediction.tsx:95,111-130` 对 `kind === 'rolling'` 隐藏 Expected/Deviation/ETA，改用「Rolling window」标签和 `line_rolling` 文案；结论 `quotaPredictionLogic.ts:150-163` 滚动先于 lasts 判定。浏览器截图 `tmp/review-s5/collapsed-classes.png`：`gravity@example.com` 行只有 `Actual 40.0%`、`Rolling window (no fixed period) · Reset time …`，没有 Expected/Deviation/ETA。接线字段：`internal/quota/prediction.go:247-251`（返回前不拟合，`lasts_until_reset`/ETA/projected 为 null），`predictionExpectedUsedPercent` 需要 `WindowStartMS`，滚动时为 nil。 |
| C3 零流量+零使用窗口收起/合并 | 通过 | 判据 `used_percent === 0 && usage_requests === 0`（`quotaPredictionLogic.ts:32-37`），面板级一行 `Idle windows hidden: N`（`QuotaPrediction.tsx:152-165`），可展开看凭据/窗口；只剩空闲窗口的 provider 组不渲染（`:194`）。浏览器截图 `tmp/review-s5/idle-expanded.png`：两个空闲窗口合并在 Step 组后的一行，展开后列出 `idle@example.test`、`openrouter@example.test`。`used=0` 但有流量/未读取的负例由组件测试钉住（`quota-prediction-collapse.component.test.tsx:124-134`）。 |
| C4 stale 窗口在预测面板收起 | 通过（按真实语义实现） | 需求原文写 `capacity_unavailable: "stale"`，但预测响应里不存在该值：`PredictionAccounting.CapacityUnavailable` 只会被赋成 `reset_mid_cycle` / `invalid_observation` / `arithmetic_unsafe` / `usage_unavailable` / `scope_unknown` / `low_usage` / `no_traffic`（`internal/quota/prediction.go:414-446`），预测里的过期标记是 `reading_stale`（`:385-392`）。S3 按 `reading_stale` 折叠（`quotaPredictionLogic.ts:39-44`、`QuotaPrediction.tsx:265-273`），并按 provider 放进「Expired / unavailable (N)」，主列表与 cannot/unknown 计数都不含它。截图同 `collapsed-classes.png`（Claude 组的折叠区）。 |

### 需求没要求但一并做了的行为（均合理，不算偏差）

- `predictionConclusion` 把 stale 判断提前到 lasts 之前（`quotaPredictionLogic.ts:150-153`），让过期行不再显示「撑不到/能撑到」，与 C4 的「不伪造结论」一致。
- 发送按钮的禁用条件从 `groups.length === 0` 改为 `allRisk.length === 0`（`QuotaPrediction.tsx:217`）：没有风险行时推送本来也只会得到空 findings，禁用是更准确的行为。
- 文档（`docs/architecture.md`、`docs/agent-capabilities.md`、ADR 0119 修正案）与浏览器探针 fixture 的同步更新，均属 PM 已记录的决定和真测需要，不算越界。

## 代码审查记录

- **正确性**：`partitionPredictions` 对每行只归一类，优先级 stale > idle > rolling > risk（`quotaPredictionLogic.ts:39-44`），与 C4「过期优先」和 C3「有流量不误收」的用例一致；`summarizePredictions`、`summarizeUnpushed` 只计 `risk`；面板 `summary` 用 `allRisk`、`footer` 仍按 `risk+stale` 统计，数字与探针/组件测试断言一致。
- **一致性**：新增报警/折叠样式沿用 `--muted`、`--text-sm`、`--accent` 等既有 token；provider 组、诊断 details 的原结构未动；i18n 三份词典同步。
- **可维护性**：纯判定抽在 `quotaPredictionLogic.ts`，组件只做分组与渲染；测试直接钉函数与 DOM，没有放宽既有断言。
- **越界**：没有改后端、没有改 `internal/api/management_dashboard_predictions.go`（字段已由 embedded `PredictionAccounting` 投影，无需改）。改动文件都在 S3 清单内，加 PM 指定的文档和真测探针。
- **回归**：既有 `quota-prediction.component.test.tsx`、`quota-prediction-baseline.component.test.tsx` 未改动且全量通过；探针既有断言只按新 fixture 调整了计数（`li[data-lasts="no"]` 2→1 是因为 antigravity 从复制样本的陈旧风险行改成滚动行，不是放水）。

## 门禁自跑（在 `39fb5415` 上重跑）

| 命令 | 结果 | 日志 |
|---|---|---|
| `pnpm --dir web type-check` | exit 0 | `tmp/review-s5/type-check.log` |
| `pnpm check-i18n` | exit 0，DICT 4264 keys | `tmp/review-s5/check-i18n.log` |
| `pnpm test:logic` | 89 suites passed | `tmp/review-s5/test-logic.log` |
| `pnpm test:components` | 42 files / 315 tests passed | `tmp/review-s5/test-components.log` |
| `pnpm check:ui --scenario dashboard-quota-prediction --workers 1` | 1 scenario passed（14.1s） | `tmp/review-s5/check-ui.log` |
| Docker `golang:1.27.1`：`go test -run TestDashboardPredictionAccountingPreservesCapabilityWireContract ./internal/api` | ok（0.474s）：dashboard 带 `usage_requests`、console 字段不泄进 capability | 控制台输出（见 `evidence.md`） |

组件测试说明：第一次全量跑在 `web/tests/account-automation.component.test.tsx:201` 失败一次（与本切片无关的 AntD 弹窗时序），同一文件单独跑 15/15 通过、随后两次全量 315/315 通过，判为既有 flake。

## 浏览器真测

- 路径：`pnpm check:ui --scenario dashboard-quota-prediction --workers 1`（Vite dev server + 无头浏览器 + mock API，不需要真实凭证）。
- 看到的界面（我自己跑出的截图）：
  - `tmp/review-s5/collapsed-classes.png`：Antigravity 组一行滚动窗口只显示 `Actual 40.0%`、`Rolling window (no fixed period)` 与重置时间；Claude 组折叠区 `Expired / unavailable (1)` 关着时主列表只有正常行。
  - `tmp/review-s5/idle-expanded.png`：空闲一行 `Idle windows hidden: 2 (zero traffic and zero usage this cycle)` 展开后列出两个窗口；OpenRouter 组因只剩空闲行不渲染；面板汇总只含风险行。
- 探针还断言了空闲行默认收起、stale 行默认不可见、诊断键盘开合、provider 顺序与桌面/手机布局几何；全部通过。
- 凭据页的 `rolling_window` 状态没有浏览器夹具，使用真实函数运行探针取证（见 F1）；这是本轮唯一的界面证据缺口。

## 范围外观察（不要求修）

1. `account-automation.component.test.tsx` 的 AntD 弹窗交互在全量并发下偶发超时（本轮出现 1 次，文件单独跑与后续全量均通过）。与本切片无关，建议后续单独处理测试稳定性。
2. 浏览器探针 fixture 没有 `used=0` 且 `usage_requests > 0` 的负例行（该负例只在 jsdom 组件测试里钉住）。建议 S7 集成验收时把它加进 fixture，补上浏览器证据。

## 未覆盖 / 交给后续

- **C-14（收起的窗口在额度页仍可见）**：本切片没有改后端数据层，`attachWindowCapacity` 只加字段、不删窗口（`internal/api/management_quota_capacity.go`），额度页展示逻辑未动；实际线上数据核对留给 S7。
- **C-12/C-16 的接口字段**：由 S1 实现，我用代码路径和 dashboard wire 契约测试核对，没有跑真实线上接口；线上 before/after 回放属 S7。
- **真实滚动窗口的端到端**：浏览器探针用 mock fixture，未接线上 antigravity 观测；滚动判定本身的单测在 S1 侧（`internal/quota/prediction_rolling_test.go`、`internal/operations/quota_prediction_rolling_reset_test.go` 及 `capacity_test.go` 的补充），不在本切片重复验证。

## 证据清单

| 类型 | 路径 |
|---|---|
| 本报告 | `docs/delivery/2026-11-17-quota-prediction-usability/acceptance/review-frontend-r1.md` |
| 日志与截图（不进 git） | `tmp/review-s5/`：`evidence.md`、`type-check.log`、`check-i18n.log`、`test-logic.log`、`test-components.log`、`check-ui.log`、`capacity-probe.log`、`collapsed-classes.png`、`idle-expanded.png` |
| 开发自测证据（git 内） | `docs/delivery/2026-11-17-quota-prediction-usability/acceptance/s3-ui-probe.md` 及两张 png |
