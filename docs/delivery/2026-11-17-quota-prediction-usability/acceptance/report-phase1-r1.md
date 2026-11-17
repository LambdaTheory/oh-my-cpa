---
type: acceptance-report
slug: 2026-11-17-quota-prediction-usability-phase1-r1
status: final
updated: 2026-11-17
verdict: 通过
summary: "第一期集成验收：第一期 19 条用例全部执行（P0 全过），第二期 6 条按未部署不测、范围 1 条通过；Go 四包/vet/前端门禁/浏览器 probe 全绿；线上只读回放 5 个窗口 unknown→有结论、6 个 3p 窗口不再 boundary_unknown、2 个仍 history_insufficient；0 条代码要修，2 条非代码事项需 PM 决断"
---

# 额度预测可用化 · 第一期集成验收（第 1 轮）

- 派发：works 2026-11-17#T8-S8（父任务 2026-11-17#T8），工作目录 E:/works/Newoahil__oh-my-cpa/11-17-t8-s8-claude，分支 `Newoahil/11-17-t8-s8-claude`。
- 验收对象：本分支 = S14 分支 `Newoahil/11-17-t8-s7-codex`（含 S1 `0979eba6`、S3 前端 `39fb5415`、S14/F1 `b1e4bed8`）+ 本地合入的 S2 后端 `Newoahil/11-17-t8-s3-codex`（`5ef3df21`）。合并提交 `581d5955`，无冲突。
- 基线：`11-17-t5-omc-dev-base` = `origin/dev` `ba9e2ee7`。
- 依据：需求原话（`E:/works/Homan-context/runs/2026-11-17-2026-11-17-T8.md`「任务说明」）、前置调研、验收用例 `cases.md`、before 快照 `acceptance/before.md`。
- 线上 before 时钟：2026-11-17 19:36:22 +08（`generated_at_ms = 1794915382118`）。本报告所有「after」都用同一时刻、同一批只读观测回放，**不是部署后实测**。

## 结论

**通过。代码要修 0 条。**

- 第一期 19 条用例（C-01～C-19）全部执行，其中 P0 全部通过；第二期 6 条（C-20～C-25）按「未部署」记未测；范围核对 1 条（C-26）通过。
- 门禁全部自跑通过：Go 四包测试、`go vet ./...`、前端 type-check / test:logic / test:components / check-i18n / lint:antd / check-css-modules、`pnpm build`、浏览器 probe。`check-docs` 红：基线既有 35 条，本次按派发要求原样复制的 S5/S6 报告新增 44 条（43 处行号 + 1 处缺失路径），本报告自身新增 0 条（见「需 PM 决断」）。
- 线上只读回放 headline：**5 个窗口从 unknown 变为有结论**（antigravity A/B 的 Gemini 5h/weekly + claude `seven_day`）；**6 个 antigravity 3p 窗口不再 `boundary_unknown`**（转为 `rolling_window`，不硬给 ETA）；**2 个 C 账号 Gemini 窗口转为 `history_insufficient`（触发后 1 个样本）**；claude `five_hour` 的 `on_track` 不回归（37 样本 / 13380s / burn 6.3979，与 before 完全一致）。
- 另有 2 条非代码事项需 PM 在合入前决断：C-03 用例字面与 S2 brief 的 `mid_cycle_reset` 诊断保留口径冲突；复制的审查报告触发 `check-docs` 行号引用规则。
- A3 的线上触发点已定位：**本轮回放观察到的跨桶触发全部是误判**（1.0pp 整数步进的浮点边界、计数小幅回落、以及周期起点 reset 时刻后移 17–344s）。按需求 A3 本期不盲改容差，只留证据与建议，见「A3 触发点定位」。

## 环境与提交

| 项 | 值 |
|---|---|
| 合并后 HEAD | `581d5955`（merge commit，父 `b1e4bed8` + `5ef3df21`） |
| 合并方式 | 本地 `git merge --no-edit Newoahil/11-17-t8-s3-codex`，无冲突；未推送 |
| Node / pnpm | Node v24.16.0（仓库期望 22.23.2，仅 WARN）/ pnpm 11.19.0 |
| Go | 本机无 Go，按项目约定用 Docker `golang:1.27.1`，`GOPROXY=off`、`GOFLAGS=-mod=readonly`、`TZ=UTC` |
| 浏览器 | 项目自带 probe harness（Vite dev server + 无头 Chromium + mock API），端口 5181/5182 |
| 线上只读 | cpa-buddy MCP `quota_prediction` / `quota_list` / `quota_observations_list` / `model_advice`；未刷新、未写、未读密钥 |

## 收口文档

本次把以下文档提交到本分支（内容只读，未改）：

- `docs/delivery/2026-11-17-quota-prediction-usability/cases.md`（S4 产出；按 PM 2026-11-17 19:50 决策把 C-07 的预期改为允许日志含 `auth_index`、只禁止密钥/账号原文，已在用例内标注「预期调整」）。
- `docs/delivery/2026-11-17-quota-prediction-usability/acceptance/before.md`（S4 产出）。
- `docs/delivery/2026-11-17-quota-prediction-usability/acceptance/review-backend-r1.md`（S6 后端审查会话产出）。
- `docs/delivery/2026-11-17-quota-prediction-usability/acceptance/review-frontend-r1.md`（S5 前端审查会话产出）。

上面两份审查报告由 S5/S6 审查会话产出，S7 收口时复制；本验收报告由 S8 执行并汇总。两份审查报告的结论（后端 A1–A4 通过、前端 C1/C3/C4 通过、F1/R-01 已在 S14 修复）本轮复核仍然成立；F1 的修复已在 `b1e4bed8` 中确认存在（类型联合、`REASON_KEYS`、三份 i18n 与 `scripts/test-quota-capacity.ts` 的用例）。

## 门禁（自己跑的，不抄开发报告）

### 后端

```text
docker run --rm --network none -v <worktree>:/src:ro -v omc-go-mod:/go/pkg/mod -v omc-gocache:/root/.cache/go-build \
  -w /src -e GOPROXY=off -e GOFLAGS=-mod=readonly -e TZ=UTC -e ASSISTANT_NO_SEND=1 -e OMCPA_ENV_FILE=/dev/null \
  golang:1.27.1 go test -count=1 ./internal/quota ./internal/operations ./internal/api ./internal/repository
ok  internal/quota        0.023s
ok  internal/operations   231.504s
ok  internal/api          196.123s
ok  internal/repository   265.139s
```

- `go vet ./...` 退出 0。
- `gofmt -l` 输出 5 个本分支未修改的文件：`cmd/oh-my-cpa/main.go`、`internal/automation/lifecycle_test.go`、`internal/fontasset/fontasset_test.go`、`internal/release/stargazers_test.go`、`internal/repository/repo_keys_test.go`；`git diff` 确认均不在本分支改动内，属基线既有。临时回放测试的 gofmt 输出已随脚本删除。

### 前端

| 命令 | 结果 |
|---|---|
| `pnpm --dir web type-check` | 退出 0 |
| `pnpm test:logic` | 89 suites passed |
| `pnpm test:components` | 42 suites / 315 tests passed |
| `pnpm check-i18n` | 退出 0，DICT 4265 keys |
| `pnpm lint:antd` | 0 findings |
| `pnpm check-css-modules` | 514 consumers，全部解析 |
| `pnpm build` | 退出 0，web/dist 同步到 `internal/web/dist` |
| `pnpm check-docs` | **失败**：基线既有 35 条 + 复制报告新增 44 条（本报告自身 0 条，见「需 PM 决断」） |

### 浏览器

- `pnpm check:ui --scenario dashboard-quota-prediction --workers 1`：1/1 通过（14.67s）。覆盖 C-11（rolling 行不显示 Expected/ETA、汇总不计入）、C-13（空闲窗口合并为一行并计数、只含空闲的 provider 组不渲染）、C-15（stale 行折叠到 Expired/unavailable 区）。
- 补充场景（临时脚本，不进 git）：C-13 的精确夹具 = 3 个空闲窗口 + 2 个正常窗口 + 1 个 used=0 但有流量的负例。8 条断言全部 PASS：3 个空闲窗口合并成一行且计数为 3；空闲行收起时不出现在预测行；used=0 有流量的行仍作为独立行出现；汇总只计 2 条 undecidable。
  - 定位记录：该负例第一次断言失败，原因是检查时 Claude provider 组仍处于收起状态（选择器取可见性），不是实现把行收起；打开 provider 组后同一检查 PASS。断言保留，未删改用例。
- `pnpm verify:browser:p0` / `pnpm verify:probes` 未运行：二者会 `go build` 出真实二进制，本机无 Go 工具链；按派发说明用项目浏览器验收路径（probe + build）替代，属未覆盖项。

## 逐条用例

结论只有：通过 / 范围内要修 / 范围外观察 / 未测。

| 用例 | 结论 | 证据 |
|---|---|---|
| C-01 滚动窗口不再 boundary_unknown | 通过 | 线上回放：3p-5h / 3p-weekly × 3 账号，`WindowCycleRange` 返回 `rolling_window`，`reasons` 无 `boundary_unknown`；`lasts_until_reset` / `expected` / `exhaustion_eta_ms` / `projected` / `burn_rate` 全为 null。单测 `TestWindowCycleRangeRecognisesRollingBoundary`（0ms / 253ms / 2000ms 通过，2001ms 仍 `boundary_unknown`） |
| C-02 大边界差仍 boundary_unknown | 通过 | 同上的 2001ms 子用例；`TestWindowCycleRange` 既有用例 |
| C-03 重置后只丢触发前样本并重拟合 | 通过（附口径冲突） | 单测 `TestPredictionRefitsAfterMidCycleReset`（样本 3、覆盖 2h、burn 5/h、前段 10→40→80 未混入）；线上回放 claude `seven_day` 552 桶 → 触发后 323 样本 / 282779s。附注：实现按 S2 brief 把 `mid_cycle_reset` 作为诊断保留在 `reasons`，与用例字面「不含 mid_cycle_reset」冲突（见「需 PM 决断」） |
| C-04 触发后证据不足如实 unknown | 通过 | 单测 `TestPredictionResetAtTailFallsBackToEvidenceReason`（样本 1、`fewer_than_three_buckets`）；线上回放 antigravity C 的两个 Gemini 窗口：`history_insufficient`、触发后 1 个样本 |
| C-05 旧 mid_cycle_reset 标记可恢复 | 通过（有条件） | 线上旧快照确实只有 bool、无 `mid_cycle_reset_at_ms`。新代码在「下一次刷新写快照」后从观测历史算出触发时刻（A/B 的 4 个 Gemini 窗口翻转）；claude `seven_day` 仅凭桶信号即可重拟合。条件：旧快照的**首次**预测仍 unknown（after-A 列），需一次刷新 |
| C-06 线上 mid_cycle_reset 窗口不再只因该原因 unknown | 通过 | 见 after/B 表：A/B 的 4 条 → on_track/will_exhaust；claude `seven_day` → on_track；C 的 2 条 → `history_insufficient`（触发后 1 样本）。无窗口以 `mid_cycle_reset` 作为唯一最终原因 |
| C-07 跨桶重置日志可定位 | 通过 | 单测 `TestPredictionResetDiagnosticLogContainsLocatorFields` 逐字段断言 provider、`auth_index`、`window_id`、`window_kind`、`window_scope`、`check`、前后桶起止、前后 used、差值、前后 reset、reset 差；`auth_index` 按 PM 决策保留，不含密钥/账号原文 |
| C-08 容差内回落不触发、不盲改容差 | 通过（附观察） | `TestPredictionBucketsHaveResetConditions` 的 within tolerance（49 vs 50）、`TestPredictionCurrentReadingResetSignal` 的 exact tolerance（49 vs 50）不触发；容差常量 `quotaPredictionResetTolerancePercent=1.0` / `ToleranceMS=1000` 未改。线上回放发现 1.0pp 整数步进因浮点残留触发，记入 A3 观察 |
| C-09 used=0 且 traffic=0 直接 on_track | 通过 | `TestPredictWindowRollingZeroTrafficIsFact`、`TestPredictWindowZeroTrafficFactRequiresAUsageRead`、operations 层 `TestQuotaPredictionRollingWindowZeroTrafficAndTraffic`、`TestQuotaPredictionUnknownResetUsesTrailingTrafficForZeroUse` |
| C-10 used=0 但 traffic>0 不短路 | 通过 | `TestPredictWindowRollingZeroUsageWithTrafficDoesNotShortcut`、`TestQuotaPredictionRollingWindowZeroTrafficAndTraffic` 的 1 次请求分支 |
| C-11 滚动窗口只显示 used + 下次 reset | 通过 | probe 断言 + 截图：Antigravity 行只有 Actual 40.0%、Rolling window、reset；无 Expected/ETA；面板汇总 1 cannot last · 2 undecidable（滚动行不计入） |
| C-12 滚动窗口接口字段不参与预测 | 通过 | 线上回放 3p 窗口：`used_percent=0`、`reset_at_ms` 有值，六项预测字段全 null；`is_rolling_window=true`；`unavailable_reason=rolling_window` |
| C-13 零流量+零使用窗口不单独成行 | 通过 | probe（2 个空闲窗口合并一行）+ 临时补充场景（精确 3 个空闲 + 2 正常 + used=0 有流量负例）全部 PASS；截图 `C-13-idle-collapsed.png` / `C-13-idle-expanded-3windows.png` |
| C-14 收起的窗口在额度页仍可见 | 通过 | 线上 `quota_list`（21:45 只读）仍列出 3p 的 5h/weekly 窗口（used 0、reset 有值）；预测面板的收起只发生在投影层，`storedPredictionQuotas` 不删窗口 |
| C-15 stale 窗口在预测面板收起 | 通过 | probe 断言 `Expired / unavailable (1)` 默认关闭、stale 行不可见、打开后可见；截图 `C-11-C-15-collapsed.png` |
| C-16 stale 窗口在接口里仍可识别 | 通过 | `quota_list` 线上 codex 两个窗口 `capacity_unavailable: "stale"`；预测侧 `reading_stale` / `reading_failed` 由 probe 的 stale 行与配置断言验证；未被计成 on_track |
| C-17 线上回放列出 unknown→有结论 | 通过 | 见「before/after 回放」：5 条有结论、6 条不再 boundary_unknown、2 条 history_insufficient、1 条不回归 |
| C-18 新增行为有单测 | 通过 | 滚动：`TestWindowCycleRangeRecognisesRollingBoundary`、`TestPredictWindowRollingUsageDoesNotFitAFakeCycle`；重置/抖动：`prediction_reset_refit_test.go` 全部用例；零流量：`prediction_rolling_test.go` 的 4 条 + operations 2 条。四包全量通过 |
| C-19 静态门与既有预测场景回归 | 通过（附门禁说明） | type-check / logic / components / i18n / antd / css 通过；Go 测试 + vet + gofmt 通过；`dashboard-quota-prediction` probe 通过。`pnpm verify:static` 整体因 `check-docs` 红，其中基线 34 条、复制报告新增 45 条，见「需 PM 决断」 |
| C-20～C-25 第二期 CMC/OPC 采集 | 未测 | 第二期实现不在本分支；未部署、未读取 CMC/OPC 上游，不判通过 |
| C-26 只改 OMC、不碰 CPA fork 与生产 | 通过 | `git diff --name-only 11-17-t5-omc-dev-base...HEAD` 仅 `internal/`、`web/`、`scripts/`、`docs/`；无 CPA fork、`private/`、生产配置或密钥 |

## before/after 回放

### 方法

- before：`acceptance/before.md`，2026-11-17 19:36:22 +08，只读 MCP。
- 观测：`quota_observations_list` 只读导出，按窗口身份分片拉取到本地临时目录（不进 git）：
  - antigravity：3 个健康账号 × 4 个窗口，覆盖各自 weekly 周期起点 → 19:36:22；最大采样间隔 900s 左右，无 > 905s 缺口。
  - claude：`seven_day`（`56b5…` 账号）从 reset−168h 起；上游本身存在 27.2h 与 13.3h 两段无观测（与 before 的 `max_gap=97979s` 吻合），不是导出缺口。
- 回放：临时 Go 测试（跑完删除）读取导出 JSON，按 `repository.ListQuotaObservationBuckets` 的同一语义聚成 5 分钟桶（同刻 active 优先、桶内 min/last、桶内相邻回落、全序列 LAG 重置标记），再走 `quota.WindowCycleRange` → `predictionObservationsFromBuckets` → `predictionBucketsResetSignal` / `predictionCurrentReadingResetSignal` → `quota.PredictWindow`。
- 两列 after：
  - **after-A（旧快照首次预测）**：直接使用 before 时的线上快照（旧版本写入，`has_mid_cycle_reset=true` 但没有触发时刻）+ 本次观测。这是部署后第一次预测会看到的状态。
  - **after-B（新快照写入后）**：同批观测上先按新代码的快照写入路径（`quota.HasWindowCycleReset` / `quota.LastWindowCycleResetAt`）重新计算标记与触发时刻，再跑预测。这是部署后下一轮刷新起的稳态。history 以观测序列代替快照序列，是近似。
- 没有把真实观测原始 JSON 提交进仓库；报告只保留窗口标识与结论字段。

### 3p 滚动窗口（A1/A4）

| 账号 | 窗口 | before | after（A/B 相同） | 翻转 |
|---|---|---|---|---|
| A/B/C ×3 | `ag_Claude and GPT models_3p-5h` | unknown，`boundary_unknown` + `used_percent_below_five`，0 样本 | unknown，`rolling_window` + `used_percent_below_five`；lasts/expected/ETA/projected/burn 全 null | 不再 boundary_unknown |
| A/B/C ×3 | `ag_Claude and GPT models_3p-weekly` | 同上 | 同上 | 同上 |

说明：这 6 个窗口 used=0 且周期已知。真实生产路径会再用观测前一个周期的 usage 判断 A4；本次允许的只读工具里没有对应窗口的历史流量数据，因此回放不伪造 usage，停在对 A1 的验证（C-17 允许的分支「至少不再 boundary_unknown」）。A4 本身由 C-09/C-10 的单测与 operations 集成用例覆盖。

### Gemini 窗口（A2）与 claude（不回归）

| 账号 | 窗口 | before（19:36） | after-A（旧快照） | after-B（新快照写入后） |
|---|---|---|---|---|
| A | gemini-5h | unknown `mid_cycle_reset`，16 样本 / 13500s | unknown `history_insufficient`，0 样本 | **on_track**，15 样本 / 12600s，expected 78.1 |
| A | gemini-weekly | unknown `mid_cycle_reset`，110 / 116160s | unknown `history_insufficient`，0 样本 | **will_exhaust**，109 / 115260s，expected 19.4 |
| B | gemini-5h | unknown `mid_cycle_reset`，15 / 13500s | unknown `history_insufficient`，0 样本 | **on_track**，14 / 12600s，expected 78.0 |
| B | gemini-weekly | unknown `mid_cycle_reset`，233 / 289019s | unknown `history_insufficient`，0 样本 | **on_track**，232 / 288119s，expected 47.9 |
| C | gemini-5h | unknown `mid_cycle_reset`，18 / 16200s | unknown `history_insufficient`，1 样本 | 同 A（1 样本） |
| C | gemini-weekly | unknown `mid_cycle_reset`，114 / 114480s | unknown `history_insufficient`，1 样本 | 同 A（1 样本） |
| claude Max5x | `five_hour` | **on_track**，37 / 13380s / gap 951s / burn 6.3979 / expected 75.2 | **on_track**，完全一致 | **on_track**，完全一致 |
| claude Max5x | `seven_day` | unknown `mid_cycle_reset`，552 / 386253s / gap 97979s | **on_track**，323 / 282779s / gap 97979s | **on_track**，同 A |

- 「是否只用了重置后样本」：claude `seven_day` 共 552 个 5 分钟桶，触发点（11-14 12:56 +08）之后保留 323 个，前 229 个被丢弃；A/B 的 Gemini 窗口在 after-B 中触发点位于周期起点附近，保留 109/110、232/233、15/16、14/15 个桶。C 的 2 个窗口触发点在最后一个桶，触发后仅 1 个样本。
- after-A 与 after-B 的差别只在 Gemini 4 条：旧快照没有触发时刻，桶信号又不认为「reset 时刻后移」是重置，所以第一次预测仍按旧 bool 保守 unknown；下一次刷新把 `mid_cycle_reset_at_ms` 写进快照后即可重拟合。

### unknown → 有结论清单

1. antigravity A · gemini-5h：unknown → on_track（15 样本）
2. antigravity A · gemini-weekly：unknown → will_exhaust（109 样本）
3. antigravity B · gemini-5h：unknown → on_track（14 样本）
4. antigravity B · gemini-weekly：unknown → on_track（232 样本）
5. claude Max5x · seven_day：unknown → on_track（323 样本）

另：6 个 3p 窗口从 `boundary_unknown` 转为 `rolling_window`（C-17 允许的「至少不再 boundary_unknown」）。

### 仍未变 / 未回放的窗口

- antigravity C · gemini-5h / gemini-weekly：仍 unknown，原因是 `history_insufficient` 且触发后 1 个样本；见 A3 的「最新桶小幅回落」误判。
- claude `five_hour`：保持 on_track，无回归。
- before 里的 18 条 `stale_reading` 窗口：不在本期 A1/A2/A4 的翻转目标内，未回放；它们在 before 中就是旧快照，回放会让它们继续 stale，不据此判通过。
- 第二期 CMC 三 auth 与 OPC：不出现 windows / 不在 quota_list，属第二期，未测。
- before 记录的两处对不上（antigravity 7 组快照 vs 4 个凭证、claude 第二账号快照不一致）本轮未改变；after 只用观测序列，不解决快照归属问题。

## A3 触发点定位

同一批 live 观测回放 `predictionBucketsResetSignal` / `predictionCurrentReadingResetSignal` 的结果：

| 窗口 | 实际触发的 check 与次数 | 最新触发（+08） | 跌幅 | reset 差 | 判断 |
|---|---|---|---:|---:|---|
| antigravity A/B · gemini 5h/weekly | 桶信号 0 次 | — | — | — | 快照 bool 来自「周期起点 reset 时刻后移」：A weekly +26s、A 5h +344s、B weekly +17s、C weekly +299s；旧快照无时刻，桶信号只认 reset 后移之外的形状（不认后移），故定位不到 |
| claude Max5x · seven_day | `bucket_reset_detected` ×6 + `bucket_used_drop` ×4 | 11-14 12:56:17 | 1.0000000000000036pp | 315ms | 整数百分比 30→29 的步进；严格 `> 1.0` 被浮点残留击穿。非真实重置 |
| antigravity C · gemini-5h | `bucket_used_drop` ×1 | 11-17 19:33:35 | 1.564pp | 0 | 计数回落、reset 未动；非真实重置 |
| antigravity C · gemini-weekly | `bucket_used_drop` ×3 | 11-17 19:33:35 | 1.876pp | 0 | 同上；另两次 11-16 19:36（1.119pp）、11-16 13:00（1.490pp） |

判断：**本轮回放观察到的触发全部不是真实重置。**

- 类 1（claude）：整数百分比步进 1.0pp，浮点残差 `1.0000000000000036 > 1.0` 触发；reset 差只有几百毫秒，属上游重置时刻的正常抖动。
- 类 2（antigravity A/B Gemini）：触发来自快照历史的绝对位移规则（`|prev−current| > 1s`），位移发生在周期起点、方向是向后（reset 变晚），used 没有下跌；这不是「周期内重置」，而是周期边界时刻的对齐抖动。
- 类 3（antigravity C Gemini）：相邻桶 used 小幅回落 1.1–1.9pp，reset 完全没动；是上游计数修正/取整，不是重置。它发生在最新桶，按 A2 的重拟合规则把之前的样本全部丢掉，只剩 1 个样本，因此这两个窗口仍 unknown。

建议容差（A3 本期不改，留给后续切片）：

1. used 回落容差不要贴着 1.0pp：要么加浮点 epsilon（例如 `> tolerance + 1e-9`），要么把阈值提到 3–5pp，或要求下一次读数继续低（确认）。
2. reset 位移只认「变早」且幅度 ≥ 5 分钟；周期起点附近的向后位移不计。
3. 快照历史规则加一条：没有 used 下跌时，不把「reset 变晚」记为 mid-cycle reset；或者把它记为 boundary 调整，不设在当前观测时刻作触发点。
4. 如果保留「最新触发点」语义，建议在触发点只比最后一个桶早一个桶时，不要把它当作可重拟合的起点（否则必然 history_insufficient）。

## 范围内要修 / 需 PM 决断

**范围内要修（代码）：无。** 第一期 A1–A4/C1/C3/C4 的行为、单测、浏览器与门禁未发现需要开发返工的问题；F1/R-01 已在 S14 修复并复核。

以下 2 条不是代码缺陷，但会影响合入口径或门禁，需要 PM 决断：

1. **C-03 口径冲突**：用例字面要求重拟合后 `reasons` 不含 `mid_cycle_reset`，S2 brief 明确要求保留该诊断；实现按 brief 保留。建议照 C-07 的方式更新用例措辞（例如「`mid_cycle_reset` 可作诊断保留，但不得作为唯一最终原因」），或把诊断移到 console-only 字段。本轮不改用例，C-03 按行为判通过。
2. **check-docs 新增红**：按派发要求原样复制的 S5/S6 报告新增 44 条（后端 30 处行号 + 1 处缺失路径、前端 13 处行号），触发仓库 AGENTS.md §7「非归档文档不得引用绝对行号」；基线另有 35 条（`docs/delivery/2026-11-12-omc-three-credential-quota/` 等）。合入前需决定：改写报告引用（与「不要改内容」冲突）、把验收报告加入归档白名单、或接受该门禁红。

## 范围外观察

- **A3 的三类误判**：上文已列。按需求 A3 本期不盲改；建议在后续切片带线上触发点证据一起改容差。
- **旧快照首次预测的过渡态**：Gemini 4 条在部署后第一次预测仍是 unknown，等下一次刷新写入 `mid_cycle_reset_at_ms` 才翻转。建议部署后复核时至少等一个刷新周期再看 `quota_prediction`。
- **C-13 的浏览器夹具差异**：项目自带 probe 用 2 个空闲窗口，没有 used=0 有流量的负例；本次用临时补充场景补齐并全部通过。建议后续把该负例并入正式 probe 夹具，减少每次验收另写临时场景。
- **Node 版本**：本机 v24.16.0，仓库期望 22.23.2；只出现 pnpm engine WARN，门禁全绿，未影响结果。
- **check-docs 基线红**：`docs/delivery/2026-11-12-omc-three-credential-quota/` 的 34 处与本需求无关，属既有。
- **capability 契约**：`mid_cycle_reset_at_ms` 会作为 `quota_list` 的可选字段出现（S6 审查的 O-03），与既有 `has_mid_cycle_reset` 的暴露方式一致；`quota_prediction` 契约不受影响。

## 未测 / 未覆盖

- C-20～C-25（第二期 CMC/OPC 采集）：实现不在本分支，未部署，未测。
- `pnpm verify:browser:p0` / `pnpm verify:probes`：需要 `go build`，本机无 Go；用 `pnpm build` + `check:ui` 场景替代。真实产物的全量浏览器门禁未跑。
- 生产部署后的线上实测：生产部署是红线，本期不做；本报告的 after 是只读观测回放，不是部署后实测。
- 18 条 `stale_reading` 窗口：未回放（非本期翻转目标）。
- A4 的线上 usage 分支：允许的只读工具不含按窗口的历史流量，3p 窗口只验证到 A1；A4 由单测/operations 用例覆盖。
- `pnpm verify:static` 的 Go 部分：用 Docker 等价命令（四包 + vet + gofmt）覆盖，未跑完整脚本。

## 部署后需复核

1. 等一个刷新周期后调 `quota_prediction`，确认 A/B 的 Gemini 4 条出现 on_track/will_exhaust，且 `mid_cycle_reset_at_ms` 已写入快照。
2. 看服务日志的 `quota prediction mid-cycle reset detected`：确认实际线上触发的 `check`、跌幅、reset 差是否与 A3 表一致；若仍是 1pp / 几百毫秒级，按建议容差另开切片。
3. 复核 antigravity C 的两个窗口是否仍因最新桶回落而 history_insufficient；若刷新后触发点前移，应能给出结论。
4. 复核 claude `seven_day` 的 on_track 与 burn（本次 0.1147/h、projected 51.59%）是否与实际上游趋势一致。
5. `check-docs` 的处理决定落定后，再确认 `verify:static` 的最终状态。

## 证据清单（不进 git）

本地临时目录 `tmp-acceptance/`（`tmp-*` 已被仓库忽略）：

- `replay-results.json`：14 个窗口的 after-A / after-B 完整字段。
- `replay-final.log`：回放命令、退出码与原始输出。
- `tmp_replay_test.go.txt`：临时回放测试的副本（原文件已从 `internal/operations/` 删除，未提交）。
- `check-ui-probe.log`、`c13-run.log`：浏览器 probe 与 C-13 补充场景日志。
- `shots/`：`C-11-C-15-collapsed.png`、`C-11-C-13-desktop-expanded.png`、`C-11-mobile.png`、`C-13-idle-collapsed.png`、`C-13-idle-expanded-3windows.png`。
- `tmp-go-test.log`、`tmp-go-vet.log`、`tmp-go-focused.log`、`tmp-gofmt.log`、`tmp-fe-*.log`：门禁日志副本。

原始观测导出 `tmp-replay/`（含 `ag-*`、`claude-*`、`pre-range.json`）只用于本次回放，不提交。

## AC 覆盖

| AC | 用例 | 结论 |
|---|---|---|
| A1 滚动窗口边界 | C-01、C-02、C-17 | 通过 |
| A2 周期内重置 | C-03、C-04、C-05、C-06、C-17、C-18 | 通过（C-03 附口径冲突） |
| A3 跨桶重置日志 | C-07、C-08 | 通过；误判证据见 A3 一节 |
| A4 零流量零使用 | C-09、C-10、C-17、C-18 | 通过（线上回放只验证到 A1 分支） |
| C1 rolling 窗口口径 | C-11、C-12 | 通过 |
| C3 零流量零使用窗口收起 | C-13、C-14 | 通过 |
| C4 stale 窗口收起 | C-15、C-16 | 通过 |
| 第一期总验：线上回放 before/after | C-17 | 通过 |
| 第一期总验：滚动/抖动/零流量单测 | C-01、C-03、C-08、C-09、C-18 | 通过 |
| B1-1～B1-4 第二期 | C-20～C-25 | 未测（未部署） |
| 边界：不改 CPA fork | C-26 | 通过 |
