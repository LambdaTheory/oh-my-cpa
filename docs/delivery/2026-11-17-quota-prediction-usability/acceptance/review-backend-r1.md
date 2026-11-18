---
type: review
slug: 2026-11-17-quota-prediction-usability-backend-r1
status: final
updated: 2026-11-17
verdict: 要修
summary: "S1/S2 后端两路审查：A1-A4 全部还原，Go 四包与 vet/gofmt 通过；S1/S2 代码无需返工，1 条跨切片要修（凭据额度页缺 rolling_window 的原因文案）"
---

# 后端切片 S1/S2 审查报告（第 1 轮）

派发：works 2026-11-17#T8-S6（父任务 2026-11-17#T8）
审查对象：`Newoahil/11-17-t8-s6-claude`，基线 `11-17-t5-omc-dev-base`（= `origin/dev` `ba9e2ee7`）
提交：`0979eba6`（S1：A1/A3/A4）、`5ef3df21`（S2：A2），19 个文件，+1063/−71（`git diff --stat 11-17-t5-omc-dev-base...HEAD`）
依据：T8 任务说明（`E:/works/Homan-context/runs/2026-11-17-2026-11-17-T8.md`「任务说明」）、调研 `E:/works/Homan-context/inbox/agents/调研/2026-11-17-cpa-额度预测数据调研.md` §3.1/§3.2/§5.1、S1/S2 进度与交接、S3 派发说明（`runs/2026-11-17-2026-11-17-T8-S3.md` 第 106-130 行）

## 结论

- **A1、A2、A3、A4 均还原，逐条通过**；代码正确性、测试质量、共用路径回归没有发现 S1/S2 自身的「范围内要修」。
- **1 条范围内要修（一般，跨切片）**：R-01，A1 的新 reason 传到凭据额度页后，前端没有对应枚举和文案，滚动窗口的「为什么不估算」说明从有变无。修复位置在前端切片（T8-S4 分支），S1/S2 的 Go 代码不需要返工。
- 另有 5 条编号范围外观察（O-01～O-05）和 1 条日志重复提示（O-06），只记录，不要求本轮改。
- 本报告 `verdict: 要修` 指 R-01 需要在 S7 集成验收前补上；S1/S2 可以按现状进入集成。

## 门禁（自己跑的，不是抄开发报告）

命令与结果（本机无 Go，用热卷 Docker，`golang:1.27.1`，`GOPROXY=off`、`GOFLAGS=-mod=readonly`、`TZ=UTC`）：

```text
go test -count=1 ./internal/quota ./internal/operations ./internal/api ./internal/repository
ok   github.com/oh-my-cpa/oh-my-cpa/internal/quota        0.065s
ok   github.com/oh-my-cpa/oh-my-cpa/internal/operations  191.457s
ok   github.com/oh-my-cpa/oh-my-cpa/internal/api         426.224s
ok   github.com/oh-my-cpa/oh-my-cpa/internal/repository  314.629s

go test -count=1 ./internal/demo
ok   github.com/oh-my-cpa/oh-my-cpa/internal/demo        76.744s

go vet ./...
VET_EXIT=0

gofmt -l <本次改动的 19 个文件>
无输出
```

- 额外跑了 `./internal/demo`（它调用 `WindowCycleRange`），也通过。
- `gofmt -l` 对 `internal/repository/repo_keys_test.go` 有输出，但该文件与基线逐字节相同（`git show 11-17-t5-omc-dev-base:internal/repository/repo_keys_test.go | sha256sum` = 工作区 `sha256sum` = `0138c5e4…`），是基线红项，不算本次问题。
- 本机 Go 四包、demo、vet 全绿；没有出现需要对照基线的失败项。

## 一、需求还原度（A1-A4）

### A1 滚动窗口边界容差且不硬算 ETA —— 通过

- 容差常量与依据：`internal/quota/capacity.go:25-31` 新增 `capacityRollingBoundaryToleranceMS = 2000`，注释写明线上 antigravity 3p `reset = observed + period + 253ms` 的形状，并限定「只接受小的正超差；起点早于观测的普通窗口不变」。
- 判定：`capacity.go:147-158`。`toMS <= fromMS` 且 `fromMS <= toMS + 2s` 时返回 `CapacityReasonRollingWindow`；超出 2s 仍是 `boundary_unknown`。
- 不硬算：`internal/quota/prediction.go:248-254` 对 rolling 直接返回 `lasts_until_reset=null`，不加 ETA/projected/burn rate；`internal/operations/quota_prediction.go:596-635` 对 rolling 不查历史 bucket，只用观测前一个周期查 usage（`capability.go:161-179` 的 `WindowTrailingUsageRange`）。
- 测试钉住：`internal/quota/capacity_test.go:92-126` 覆盖 `0ms / 253ms / 2000ms / 2001ms / −253ms`（最后一条是普通窗口）；`internal/quota/prediction_rolling_test.go:58-81` 断言 rolling 有使用量时不产生 ETA/projected/burn rate；`internal/operations/quota_prediction_rolling_reset_test.go:103-162` 是 operations 层的滚动窗口集成用例。
- 误伤普通窗口：普通固定窗口只有在观测早于周期起点且差值 ≤2s 时才会被认成 rolling；起点早于观测的窗口（本文件的 `−253ms`）继续走固定周期路径。容差不是无限放宽（C-02 的「远超容差」仍是 `boundary_unknown`）。
- 与 S4 用例的口径见 O-05（rolling 且 used=100 时先走事实分支）。

### A2 mid_cycle_reset 只丢触发点前样本、从触发点后重拟合、旧快照兼容 —— 通过

- 持久化字段：`internal/quota/types.go:33-36` 新增 `MidCycleResetAtMS *int64`（`mid_cycle_reset_at_ms,omitempty`）；`internal/api/management_quota.go:414-420` 在落快照时写入最近触发时刻；旧快照没有该字段时为 nil，`HasMidCycleReset` 继续可读。
- 触发点：`internal/quota/capacity_history.go:29-108` 把原来的 bool 入口拆成 `hasAt || hasFlag`，`LastWindowCycleResetAt` 返回整段保留观测里最近一次重置的观测时刻；`HasWindowCycleReset` 的 bool 语义与旧实现等价（旧的六个 true 分支分别落到 `hasFlag` 和 `record`）。
- 预测：`internal/quota/prediction.go:282-301` 的优先级是「本次观测序列检测到的触发点 / operations 从 buckets 检出的触发点（取较晚者）> 快照 `MidCycleResetAtMS` > 旧 bool 降级」；`prediction.go:531-544` 的 `predictionPointsAfterReset` 只保留 `ObservedAtMS > 触发点` 的样本，当前读数始终保留（brief 要求的「其余样本 + 当前读数」）。
- 证据不足如实返回：`prediction.go:298-316` 在重拟合后走原有 `predictionMinimumEvidence`，返回 `fewer_than_three_buckets` / `insufficient_coverage` / `observation_gap`；`mid_cycle_reset` 只作诊断留在 `reasons`，不把结论置空（S3 brief 第 113 行明确要求这样）。
- 旧 bool 降级：`prediction.go:289-296` 在没有触发时刻时保持旧 `mid_cycle_reset` unknown；`internal/quota/prediction_reset_refit_test.go:135-147` 钉住。
- 测试钉住：`prediction_reset_refit_test.go` 的 `TestPredictionRefitsAfterMidCycleReset`（burn rate=5/h、样本=3、覆盖=2h，证明触发前的 10→40→80 没有混进拟合）、`TestPredictionResetAtTailFallsBackToEvidenceReason`（尾部回落 → 样本 1、fewer_than_three_buckets）、`TestPredictionRefitsAfterResetAtMovesEarlier`（只移动 reset_at 也重拟合）、`TestPredictionUsesSnapshotResetInstantWhenObservationsHaveNoTransition`（快照时刻）、`TestPredictionBucketResetOutranksSnapshotResetInstant`（bucket 优先于快照）。
- 触发点精度：buckets 的精确触发时刻来自 `internal/repository/quota_observations.go:423-428,449-452` 新增的全序列 `LAG` 聚合 `LastResetObservedAtMS`，`internal/operations/quota_prediction.go:445-453` 优先使用它；只有「仅跨桶 min 比较触发、桶内没有相邻 >1pp 下落」时才退化为该桶最后一次观测（≤5 分钟粒度，见 O-04）。
- 旧快照 + buckets 重新找到触发点的端到端组合没有单独的 operations 用例；两条路径分别有单测（见「未覆盖/未测项」）。

### A3 跨桶重置诊断日志可定位触发点、不盲改容差 —— 通过

- 容差未改：`internal/operations/quota_prediction.go:29-30` 的 `quotaPredictionResetTolerancePercent = 1.0`、`quotaPredictionResetToleranceMS = 1000` 与基线一致；`capacityResetTolerancePercent` 也未改。
- 信号：`quota_prediction.go:400-508` 把四种判定（相邻桶 used 下跌、相邻桶 reset_at 位移、桶内 reset 位移、当前读数回落）结构化为 `predictionResetSignal`，保留前后桶、两侧 used/reset、差值。
- 日志：`quota_prediction.go:545-568`，消息 `quota prediction mid-cycle reset detected`，字段含 `auth_index`、`provider`、`window_kind/id/scope/model`、`check`、`trigger_at_ms`、前后桶 `StartMS`、`previous_last_used_percent`、`current_min_used_percent`、`used_difference`、两侧 reset 值、`reset_difference_ms`。`auth_index` 是 PM 2026-11-17 19:50 决策保留的排障标识（决策记录在 T8 运行记录），日志不含密钥或账号原文。
- 测试：`internal/operations/quota_prediction_rolling_reset_test.go:19-101` 同时钉住信号里被比较的值和日志文本。
- 说明：日志随每次预测读取触发（dashboard/MCP 轮询时会重复出现同一条重置），属于排障便利而非缺陷；若上线后觉得吵，可后续按窗口+触发时刻去重。

### A4 used=0 且周期 traffic=0 直接 on_track —— 通过

- 判定：`internal/quota/prediction.go:231-238`，条件是 `used=0` + period 有效 + `predictionUsageWasRead`（`UsageReason=="" && Usage!=nil`）+ `Usage.Requests==0`，返回 `lasts_until_reset=true`、`evidence=fact`，不再进入边界/样本拟合；顺序在过期读检查之后、used≥100 之前。
- 真的查了周期 usage：`internal/operations/quota_prediction.go:588-596` 对 rolling、以及 `boundary_unknown + period 已知 + used=0` 的窗口，用观测前一个周期查 usage（`WindowTrailingUsageRange`）；边界已知的窗口沿用 `[fromMS,toMS)` 的原有查询。边界未知时仍不产生假起点（`WindowStartMS` 保持 nil）。
- used=0 有流量不短路：`prediction.go:234` 要求 `Requests==0`；`internal/quota/prediction_rolling_test.go:40-56,83-118` 覆盖「有流量 → rolling/boundary unknown，不判 fact」和「没读过 usage → 不判 fact」；`internal/operations/quota_prediction_rolling_reset_test.go:103-220` 用真实 usage 事件区分 0 / 1 / nil。
- 对照用例：C-09（boundary_unknown + period 已知）、C-10（有流量不短路）都通过；S1 的追加派发（19:46）也覆盖到了。

## 二、代码正确性专项（派发审查重点 1）

- 边界容差常量与依据：见 A1；2000ms 约为线上 253ms 的 8 倍，且只在 `toMS <= fromMS` 的窄形状生效，普通窗口和「起点早于观测」不受影响。
- 滚动信号稳定、不误伤普通窗口：见 A1；`WindowCycleRange` 的其它调用方（`quota_scheduler.go:198`、`demo/quota.go:42`、`management_quota.go:413`、`management_quota_capacity.go:35,70`、`capacity_history.go:121,142,153`）都只判断 `reason == ""` / `reason != ""`，rolling 只是把非空原因从 `boundary_unknown` 换成 `rolling_window`，分支行为不变。
- A4 是否真的查了周期 usage、是否把「used=0 有流量」也判 on_track：见 A4，两者都通过。
- A2 触发点与丢弃范围：见 A2；丢弃范围是严格 `<= 触发点`，当前读数例外保留（brief 明确「用其余样本 + 当前读数」）。
- 重拟合后 reason：见 A2，`fewer_than_three_buckets` 等如实返回，`mid_cycle_reset` 只作诊断。
- 旧快照降级：见 A2，单测覆盖。
- 快照持久化字段是否只在内部：`MidCycleResetAtMS` 只加在 `QuotaWindow` 上，`quota_prediction` 的输出结构（`QuotaPredictionItem`）没有这个字段；capability 契约测试也确认 `usage_requests`、`is_rolling_window` 不泄漏（`internal/api/management_dashboard_predictions_test.go:144-153`）。但 `QuotaWindow` 同时是 `quota_list` 的 windows 线上形状（`internal/operations/quota.go:21-32,53-75`），所以 `mid_cycle_reset_at_ms` 会作为新增可选字段出现在 `quota_list`/`quota_refresh` 的 windows 里，详见 O-03。

## 三、测试质量（派发审查重点 3）

- 新增测试不是只跑通：断言用到具体数值和时间点（burn rate 5、10；样本 3/1；覆盖 2h；触发时刻 5h；日志逐字段文本）。
- 没有删测试：`git diff --name-status` 只有 M/A，没有 D。
- 没有放宽断言：`TestDashboardPredictionAccountingPreservesCapabilityWireContract` 增加了 console-only 字段不泄漏的断言；`TestWindowCapacityResetEvidenceSurvivesSnapshotRetention` 增加了 `MidCycleResetAtMS == nowMS-119000` 的断言；`quota_prediction_test.go` 的当前快照重置用例把 SampleCount 从 5 改为 1 并加了 `fewer_than_three_buckets`/`history_insufficient` 断言（行为改为「当前读数作为新周期起点」，是收紧而非放宽）。
- 唯一被替换的断言是 `TestPredictionAccountingWithholdsUnavailableAndJustResetEstimates` 删除 mid_cycle_reset 分支，另加「mid_cycle_reset 不再阻挡 accounting」的显式断言；这对应 S3 brief 第 114 行的要求，是行为变更的显式记录，不是掩盖失败。
- 滚动、抖动容错/重置、零流量三类都有覆盖：滚动见 A1；重置/抖动见 A2 与 `capacity_history_test.go:107-130`、`repository/quota_observations_test.go:299-301`；零流量见 A4。

## 四、回归（派发审查重点 4）

- `EstimateWindowCapacity`（`internal/quota/capacity.go:202-240`）未改；凭据容量路径仍然对 mid-cycle reset 保守拒绝。`HasWindowCycleReset` 的 bool 语义与旧实现等价；`PreviousWindowObservation` 未改，`internal/quota/capacity_history_test.go` 既有用例全部通过。
- `predictionHasReset` 改成返回最近触发点，调用方只有 `PredictWindow`（`grep` 确认），没有别的包依赖旧 bool 签名。
- `calculatePredictionAccounting` 不再因 `HasMidCycleReset`/`mid_cycle_reset` 提前放弃容量/expected，这是 S3 brief 第 114 行明确要求的行为同步，不是顺手扩展。
- 共用 `WindowCycleRange` 的路径里，只有凭据额度页的 `capacity_unavailable` 值从 `boundary_unknown` 变成 `rolling_window`，见 R-01。
- S2 提交改到了拆分单未列出的 `internal/repository/quota_observations.go`（新增只读 LAG 聚合与 `LastResetObservedAtMS` 字段），是 A2 精确定位触发点所必需，改动是新增而非改写既有语义，并有 repository 测试；不判越界为要修。

## 问题清单

### R-01（范围内要修 · 一般 · 回归 · 修复归属前端切片）

- 位置：`internal/quota/capacity.go:147-158` 产出 `rolling_window`；`internal/api/management_quota_capacity.go:35-37` 把它直接写进 `window.CapacityUnavailable`；前端（T8-S4 分支工作区 `E:/works/Newoahil__oh-my-cpa/11-17-t8-s4-codex`）的 `web/src/types/quota.ts:58-68` 枚举没有 `rolling_window`，`web/src/pages/quota/quotaCapacity.ts:28-40` 的 `REASON_KEYS` 也没有这个键，`:92-93` 查不到时 `note=null`。
- 复现：对任一带 period 的滚动窗口（线上 antigravity 3p：`reset = observed + period + 253ms`）调 `GET /api/v1/management/quota` 或打开凭据额度页抽屉。改动前该窗口 `capacity_unavailable="boundary_unknown"`，抽屉显示「重置时间不精确，暂不估算」；改动后值为 `rolling_window`，抽屉不再显示任何原因说明（有 recorded 用量时只显示用量，没有时整行不出现）。
- 影响：数据层行为不变（仍然不估算），但用户失去了「为什么不估算」的说明；`quota_list`/`quota_refresh` 的外部调用方也会看到一个新的枚举值。
- 建议：在 S4 分支补 `QuotaCapacityUnavailableReason` 的 `rolling_window` 和对应 i18n（例如「滚动窗口没有固定周期，暂不估算」）；若不想动前端，后端可在 `attachWindowCapacity` 把 rolling 映射回 `boundary_unknown`（不推荐，语义更差）。S1/S2 的 Go 代码不需要改。

## 范围外观察

- **O-01 用例口径冲突（C-03 vs S3 brief）**：`docs/delivery/.../cases.md` C-03 期望有结论时 `reasons` 不含 `mid_cycle_reset`；S3 派发说明第 113 行明确要求「`mid_cycle_reset` 只作为诊断保留在 reasons 里」。实现按 S3 brief 保留。S7 集成验收前需要 PM 统一口径（建议按 brief 调整 C-03，或把诊断移到 console-only 字段），否则 C-03 会判失败。
- **O-02 字面 rolling（无 period）窗口未被标记**：antigravity 插件里 `window="rolling"` 的窗口 `PeriodHours=nil`（`internal/quota/antigravity.go:123-126` 与 `antigravity_test.go:309-334,383-391`），`WindowCycleRange` 在 `capacity.go:129-131` 先因 period 缺失返回 `boundary_unknown`，不会走到 rolling 判定；前端 C1 只认 `is_rolling_window`（S4 分支 `web/src/components/dashboard/quotaPredictionLogic.ts:23-25`）。若 C1 也要覆盖这类窗口，需要后端/前端另补信号；当前 A1 只覆盖「period 已知、reset=now+period」的形态。
- **O-03 新增快照字段会出现在 quota_list 输出**：`MidCycleResetAtMS` 定义在 `QuotaWindow`（`types.go:36`），而 `operations.Quota.Windows` 就是 `quota_list` 的输出形状（`internal/operations/quota.go:21-32,53-75`），因此 `mid_cycle_reset_at_ms` 会作为新增可选字段出现在 `quota_list`/`quota_refresh` 的 windows 中；`quota_prediction` 契约不受影响。若 PM 要严格执行「新增字段只在内部快照或 console-only」，需要在 quota_list 投影里剥离；当前做法与既有 `has_mid_cycle_reset` 的暴露方式一致。
- **O-04 触发点精度退化**：只有「跨桶 last→min 比较」触发、而桶内没有相邻 >1pp 下落时，`triggerAt` 会退化为该桶最后一次观测（`internal/operations/quota_prediction.go:438-508`，`triggerAt` 在 445-453），最多多丢一个 5 分钟桶的样本；不会把触发点之后的样本留下，也不会把旧周期样本混进新周期。生产 543/105-229 样本级别的窗口影响可以忽略。
- **O-05 rolling 且 used=100 的边界情形**：`prediction.go:239-254` 的顺序是 used≥100 事实先于 rolling 判定，所以一个 100% 的滚动窗口仍会返回 `lasts_until_reset=false`、`exhaustion_eta_ms=observed`。C1 说滚动窗口「不参与 ETA/lasts」，C-12 期望这些字段为 null；若 S7 的夹具包含 100% 滚动窗口，需要确认这是否符合预期（事实级「现在已耗尽」是否也算「参与 lasts」）。
- **O-06 日志重复**：A3 日志在每次预测读取时只要检测到重置就打印（dashboard/MCP 轮询会重复），不影响正确性；上线后若日志过密，可按「auth_index+window+trigger_at_ms」做一次去重。

## 未覆盖/未测项（如实记录，不当作通过）

- 线上 before/after 回放（`quota_observations_list` 的 33 条 before 跑新算法）不在本轮，属 S7 集成验收；本报告只验证算法与单测/集成测试。
- 浏览器/前端 C1/C3/C4 的真实渲染不在本轮，属 S5（前端审查）与 S7（集成验收）；本报告只在需要说明跨切片影响时读了 T8-S4 分支的代码。
- 旧快照 `has_mid_cycle_reset: true`（无时刻）+ 本次 buckets 能重新找到触发点的端到端 operations 组合没有单独用例；单元层「旧 bool 保持 unknown」和「buckets 触发重拟合」两条路径分别有测试。
- 「buckets 触发点与快照 `MidCycleResetAtMS` 同时存在且快照更晚」的组合没有测试；实现按 brief 的优先级使用 buckets，未做 max 合并。
- A3 的生产侧验证（用线上日志确认跨桶触发对与数值）要等部署后看 `quota prediction mid-cycle reset detected`，本轮只验证了日志字段的本地测试。
- S1/S2 未推送、未开 PR；本报告提交到当前分支后也不推送，由 PM 在 S7/S8 统一处理。

## 附：逐条核对索引

| AC | 结论 | 主要证据 |
|---|---|---|
| A1 滚动窗口边界 | 通过 | `capacity.go:25-31,147-158`；`prediction.go:248-254`；`capacity_test.go:92-126`；`prediction_rolling_test.go:58-81`；operations 集成用例 `quota_prediction_rolling_reset_test.go:103-162` |
| A2 mid-cycle 重拟合 | 通过 | `types.go:33-36`；`capacity_history.go:29-108`；`prediction.go:282-301,531-544`；`management_quota.go:414-420`；`prediction_reset_refit_test.go`；`management_quota_capacity_test.go:314` |
| A3 触发点日志 | 通过 | `quota_prediction.go:400-568`；`quota_prediction_rolling_reset_test.go:19-101`；容差常量 `quota_prediction.go:29-30` 未改 |
| A4 零流量零使用 | 通过 | `prediction.go:231-238,679-685`；`quota_prediction.go:588-635`；`prediction_rolling_test.go:18-118`；operations 集成用例 `quota_prediction_rolling_reset_test.go:103-220` |
| 回归 | 通过（R-01 跨切片另计） | `capacity.go:202-240` 未改；`capacity_history_test.go` 全绿；四包 + demo 测试全绿 |
| 测试质量 | 通过 | 无删测试；新增断言更严；`git diff --name-status` 无 D |

## 复核命令（可直接重跑）

```powershell
docker run --rm --network none -v "E:/works/Newoahil__oh-my-cpa/11-17-t8-s6-claude:/src:ro" -v "omc-go-mod:/go/pkg/mod" -v "omc-gocache:/root/.cache/go-build" -w /src -e GOPROXY=off -e GOFLAGS=-mod=readonly -e TZ=UTC -e ASSISTANT_NO_SEND=1 -e OMCPA_ENV_FILE=/dev/null golang:1.27.1 go test -count=1 ./internal/quota ./internal/operations ./internal/api ./internal/repository
```
