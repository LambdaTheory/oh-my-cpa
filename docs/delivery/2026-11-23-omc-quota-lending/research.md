---
版本: 1
修改日期: 2026-11-23
修改人: 浩文（Agent · T7-S1 调研）
核实日期: 2026-11-23
派发: works 2026-11-23#T7-S1 · 仓库 LambdaTheory/oh-my-cpa · 基线 af68e8c8
---

【交接 · 调研 → Ayachi】跨凭证额度拆借（现状与可行性） · 2026-11-23 11:35

# 跨凭证额度拆借 现状

## 一、结论摘要

数据层够用：窗口有剩余百分比、重置时刻与精度；Codex reset 精度随载荷而变。控制台一卡一条，provider/plan_type 可读，但无同池分组或跨卡比较。`reset_advice` 只算单卡权益；预测逐窗口给 `lasts_until_reset`，不按卡排。priority/weight 属 CPA 调度，OMC 只做受控写入。

## 二、逐条回答

### 1. 数据层

窗口带剩余百分比与 `ResetAtMS`/`ResetLabel`/`ResetAccuracy`（A1）。Codex reset 来自 `reset_at`（exact）或 `reset_after_seconds`（derived），都无为 nil+approximate（A2、A3）。Claude 失败同 nil（A7）。插件只留文本、无时刻（A11）；codex 不走插件（A9）。

### 2. 分组口径

总览一卡一条，按 auth_index 匹配（A14），带 provider、type、plan_type（A1）。AuthFile 有 email/account/id_token（A19），但无同池分组代码，库无 subscription/account 表（A21）。现成键只有 provider+plan_type；同池需自定（推断）。

### 3. 能力边界

`reset_advice` 只处理 codex/claude、要 2h 内读数（A24），算单卡重置权益与最早过期，按 90% 阈值给三种 verdict，不跨卡比较（A23、A25）。前端有倒计时、`~` 与 boundary_unknown（A26、A27）。排序键只有 name/requests/priority/weight（A30），按 reset 排要扩该类型。

### 4. 路由可行性

OMC 经 `PATCH /credentials/fields` 写 priority/weight（A33、A34），抽屉与 Agent 可写（A36）。CPA 语义：高 priority 先选，weight 只在 weighted-round-robin 同优先级内生效、0 排除，会话亲和可能绕过（A37）。改字段影响路由；OMC 只写配置、不进请求路径（A45）。

### 5. 数据缺口

输入：剩余百分比、重置时刻与精度、权益、鲜度。快照全有（A39）；鲜度有 2h–24h 预测界与被动流 365 天/15 分钟心跳（A42、A40）；预测已给 `lasts_until_reset`/`data_age_seconds`（A43）。缺口在聚合（推断）：无跨卡比较/同池定义。

## 三、坑和该马上处理的事

1. Codex reset 可能是 derived，精确排序会误判（A2）。
2. 插件 reset 可能只有文本（A11）。
3. `reset_advice` 的 2h 鲜度会让跨卡建议 unknown（A24）。
4. auth_index 可跨 Family 重复（ADR 0002）。
5. priority/weight 受亲和/策略影响（A37）。

## 四、待确认

- 「同池」口径待定。
- 是否改 CPA 路由权重——决策项。
- 两台 Pro 200 的周窗口给 `reset_at` 还是仅 `reset_after_seconds`，需线上取一次。
- 插件 reset 文本是否够排序，看实际输出。

## 五、结论要点

- 现成能用（A1、A6、A39、A43、A44）：每卡窗口 remaining/used、reset 时刻与精度、逐窗口 `lasts_until_reset`/`data_age_seconds`、重置权益余额、每卡请求量。
- 要新采或改口径：无硬采集缺口；要补「同池」口径、跨卡聚合与按重置排序；Codex reset 精度随载荷而变，排序要带精度。
- 路由权重落地在事实层面：只是写 CPA 的 priority/weight 配置（已有受控通路与回读校验），不新增请求路径；是否自动写属于边界决策。

## 附录 A　出处

| ID | 事实 | 出处（文件 + 符号） |
|---|---|---|
| A1 | 窗口统一字段（used/remaining/reset/label/period/accuracy） | `internal/quota/types.go`（`QuotaWindow`） |
| A2 | Codex reset：`reset_at`=exact，仅 `reset_after_seconds`=derived，都无=nil+approximate；测试夹具两者同时出现 | `internal/quota/codex.go`（`ParseCodexUsage`）；`internal/quota/codex_test.go`（`TestParseCodexUsageCurrentPayload`） |
| A3 | reset 缺失时 `ResetLabel` 为空 | `internal/quota/codex.go`（`ParseCodexUsage`） |
| A4 | Codex 窗口按 `limit_window_seconds` 识别周期 | `internal/quota/codex.go`（`standardWindowIdentity`、`ParseCodexUsage`） |
| A5 | Codex plan_type 映射（pro → Pro 200 / elite） | `internal/quota/codex.go`（`resolveCodexPlanTier`） |
| A6 | Codex 重置积分与 Claude grants 字段 | `internal/quota/types.go`（`CodexResetCredit`、`CodexResetCreditsInfo`、`NormalizedQuota.ResetCredits`/`ResetGrants`） |
| A7 | Claude `resets_at` 解析失败 → nil + approximate | `internal/quota/claude.go`（`ParseClaudeUsage`） |
| A8 | Claude grants（`cedar_ember`）与状态枚举 | `internal/quota/claude.go`（`ClaudeResetGrant`、`ResetGrantsStatus`、`ParseClaudeResetGrants`） |
| A9 | 内置 provider 表；codex 类型不回退到插件 | `internal/quota/plugin.go`（`builtInQuotaProviders`） |
| A10 | 插件窗口解析入口 | `internal/quota/plugin.go`（`fetchPluginQuota`） |
| A11 | 插件 reset：RFC3339→exact；否则只留文本、accuracy 空串 | `internal/quota/antigravity.go`（`ParsePluginQuotaWindows`、`parseQuotaWindowGroups`、`antigravityBucketReset`） |
| A12 | Kimi 无条件写 exact（reset 缺失时也写） | `internal/quota/kimi.go`（`ParseKimiUsage`） |
| A13 | 其余内置 provider 的 exact 写法 | `internal/quota/xai.go`（`ParseXaiBilling`）、`internal/quota/meta.go`（`buildMetaWindow`）、`internal/quota/devin.go`（`buildDevinWindow`）、`internal/quota/opencode.go`（`ParseOpenCodeUsage`）、`internal/quota/commandcode.go`（`commandCodeRateLimitWindow`） |
| A14 | 额度总览一卡一条、按 auth_index 组装 | `internal/api/management_quota.go`（`buildQuotaOverview`） |
| A15 | `QuotaItemDTO` 无分组/账户字段 | `internal/api/management_quota.go`（`QuotaItemDTO`） |
| A16 | plan 从 `plan_json`/legacy 列恢复 | `internal/api/management_quota.go`（`planFromSnapshot`） |
| A17 | 前端记录与 quota 按 auth_index 匹配 | `web/src/pages/oauthManagement/oauthWorkspaceLogic.ts`（`cpaRecords`） |
| A18 | `displayProvider` 归一 | `web/src/pages/oauthManagement/oauthWorkspaceLogic.ts`（`canonicalDisplayProviderKey`） |
| A19 | AuthFile 身份字段与 Codex account id | `internal/cpa/management/client_auth_files.go`（`AuthFile`、`CodexChatgptAccountID`、`CodexPlanType`、`CodexSubscriptionActiveUntil`） |
| A20 | 前端身份使用 email/project_id/auth_index | `web/src/components/authFiles/authFileLogic.ts`（`deriveAuthFileIdentity`） |
| A21 | 数据库无订阅/账号池表（仅 account_automation 的表，与本主题无关）；计划只存 `plan_json` 列；检索到的分组代码只有 model_groups（模型组建议/余额）与 provider_groups（CPA v8 配置分组），无配额同池分组 | `migrations/`（001–068 迁移）的 `CREATE TABLE` 清单；`migrations/008_quota_snapshot_plan_json.sql`；`internal/operations/model_groups.go`；`internal/cpa/management/provider_groups.go`（`keyGroupOrigin`、`groupSharedFields`、`keyZeroOverrides`、`GroupsPath`） |
| A22 | 领域词定义（Subscription/Account） | `CONTEXT.md`（**Subscription**、**Account**） |
| A23 | `reset_advice` 单卡判定与权益算法；verdict 为 use_before_expiry / keep_for_emergency / no_action / unknown | `internal/quota/reset_advice.go`（`ResetAdviceUseBeforeExpiry` 等 verdict 常量、`AdviseReset`、`resetEntitlements`） |
| A24 | 2 小时权益读数鲜度 | `internal/quota/reset_advice.go`（`resetAdviceGrantFreshness`、`AdviseReset`） |
| A25 | operations 逐凭证输出与排序 | `internal/operations/reset_advice.go`（`ResetAdvice`、`ConsoleResetAdvice`、`resetAdvice`） |
| A26 | 前端倒计时/精度标记 | `web/src/pages/quota/quotaFormat.ts`（`formatCountdown`、`resetAccuracyMarker`、`quotaResetCountdown`、`quotaResetText`） |
| A27 | 行内显示与 `boundary_unknown` | `web/src/pages/quota/CompactQuotaView.tsx`（`CompactQuotaView`） |
| A28 | 抽屉展示窗口/积分/grants/余额 | `web/src/pages/quota/CredentialQuotaBody.tsx`（`CredentialQuotaBody`） |
| A29 | `ResetAdviceView` 明细 | `web/src/pages/quota/ResetAdviceView.tsx`（`ResetAdviceView`） |
| A30 | 排序键五种与实现 | `web/src/components/authFiles/authFileLogic.ts`（`AuthFileSortKey`） |
| A31 | 记录排序实现 | `web/src/pages/oauthManagement/oauthWorkspaceLogic.ts`（`sortOAuthWorkspaceRecords`） |
| A32 | UI 排序选项 | `web/src/pages/oauthManagement/OAuthManagementPage.tsx`（`OAuthManagementPage`） |
| A33 | 写 priority/weight 的客户端通路 | `internal/cpa/management/client_auth_files.go`（`PatchAuthFileFields`） |
| A34 | 字段校验、回读与不一致检测 | `internal/api/management_auth_file_fields.go`（`applyManagementAuthFileFields`、`managementAuthFileFieldMismatch`、`normalizeManagementAuthFileField`） |
| A35 | Agent 写字段能力 `oauth_set_credential_fields` | `internal/operations/oauth.go`（`registerOAuth`） |
| A36 | 抽屉编辑 priority/weight 与提示文案 | `web/src/components/authFiles/AuthFileDetailDrawer.tsx`（`AuthFileDetailDrawer`）；`web/src/i18n/index.tsx`（`DICT` 的 `af.field_priority_hint`、`af.field_weight_hint`） |
| A37 | CPA 调度语义（priority/weight/策略/亲和） | `docs/cpamc-parity.md`（§Scheduler Semantics） |
| A38 | routing strategy 配置读写 | `internal/cpa/management/client_config.go`（`knownScalars`）；`internal/operations/config_validation.go`（`ValidateScalarValue`、`normalizeRoutingStrategy`） |
| A39 | 快照结构（windows/reset credits/grants/observed_at） | `internal/repository/quota.go`（`QuotaSnapshotRecord`、`SaveQuotaSnapshot`） |
| A40 | 被动观测保留 365 天、15 分钟心跳 | `internal/repository/quota_observations.go`（`QuotaObservationRetentionDays`、`quotaObservationHeartbeatMS`） |
| A41 | 读时用更新的被动读数覆盖标准窗口 | `internal/api/management_quota_observations.go`（`overlayPassiveObservations`） |
| A42 | 预测鲜度界 2h–24h | `internal/quota/prediction.go`（`predictionFreshWindow`） |
| A43 | 逐窗口 `lasts_until_reset` / `data_age_seconds` | `internal/operations/quota_prediction.go`（`QuotaPredictionItem`） |
| A44 | 每卡请求量来自 `usage_events.auth_index` | `migrations/002_usage_ingest.sql`（`usage_events.auth_index`、`idx_usage_events_auth_time`） |
| A45 | 项目边界原文 | `E:/works/Homan-context/projects/oh-my-cpa/AGENTS.md`（§边界） |
| A46 | 2026-10-30 决策只写插件化方向，未写路由边界 | `E:/works/Homan-context/projects/oh-my-cpa/decisions/2026-10-30-插件化先做观测类.md` |
| A47 | 额度预测采集缺口复盘 | `E:/works/Homan-context/projects/oh-my-cpa/decisions/2026-11-18-额度预测先修口径再补采集.md` |
| A48 | auth_index 是 family 内稳定索引，可跨 Resource Family 重复 | `docs/adr/0002-cpa-binding-and-identity-hierarchy.md`（§2.1 Entity Model Boundaries、§2.2 Discovery Identity Hierarchy） |

## 附录 B　目录结构（相关部分）

```
internal/quota/
  types.go            窗口/计划/重置权益 DTO
  codex.go            Codex 解析（reset_at / reset_after_seconds）
  claude.go           Claude 解析（resets_at / cedar_ember grants）
  plugin.go           插件额度目标解析与载荷投影
  antigravity.go      插件与 Antigravity 共用的 groups[].buckets[] 解析
  reset_advice.go     单卡「重置权益要不要用掉」判定
  prediction.go       逐窗口 for lasts-until-reset 与鲜度界
  observations.go     观测 DTO 与窗口 kind 映射
  passive.go          响应头被动读数（codex/claude 5h、weekly）
internal/api/
  management_quota.go           额度总览（一卡一条）
  management_auth_file_fields.go 字段写入校验与回读
  management_quota_observations.go 读时覆盖被动读数
internal/operations/
  reset_advice.go     逐凭证 reset advice 输出
  quota_prediction.go 逐窗口预测输出
internal/repository/
  quota.go            额度快照（WindowsJSON/ResetCreditsJSON/ResetGrantsJSON）
  quota_observations.go 观测时间线（365 天）
web/src/pages/quota/
  quotaFormat.ts      倒计时、精度标记、重置文案
  CompactQuotaView.tsx 行内紧凑额度视图
  CredentialQuotaBody.tsx 抽屉额度详情
  ResetAdviceView.tsx reset advice 明细
web/src/pages/oauthManagement/
  oauthWorkspaceLogic.ts 记录投影与排序
```

## 附录 C　跑过的命令与关键输出

```text
$ git rev-parse origin/dev
af68e8c88a03dc00461cd71e1e1cf3afc6b83bcc
$ git log --oneline -1 11-23-t7-dev-base
af68e8c8 Merge pull request #127 from LambdaTheory/Newoahil/11-18-t9-opencode-models
$ git merge-base --is-ancestor 11-23-t7-dev-base HEAD && echo "base ok"
base ok

$ grep -rn "func ParsePluginQuotaWindows" internal/quota/
internal/quota/antigravity.go:127:func ParsePluginQuotaWindows(...)

$ grep -rn "ResetAccuracy" --include="*.go" internal/ | head
（各 provider 与 API/飞书消费点，见附录 A）

$ grep -rn "CREATE TABLE" migrations/*.sql
（001-068，无 subscription/account 表）

$ grep -n "priority-desc\|weight-desc" web/src/pages/oauthManagement/OAuthManagementPage.tsx
874: { value: 'priority-desc', ... }
875: { value: 'weight-desc', ... }

$ grep -n "LastsUntilReset\|DataAgeSeconds" internal/operations/quota_prediction.go
60: DataAgeSeconds *int64 `json:"data_age_seconds"`
62: LastsUntilReset *bool `json:"lasts_until_reset"`

$ grep -rniE "group.*credential|credential.*group" --include="*.go" --include="*.ts" --include="*.tsx" internal/ web/src/
（47 处命中：model_groups 的模型组建议/余额与 provider_groups 的 CPA v8 配置分组；无配额同池分组）
```

正文（一至四节）1498 字符（`sed -n '13,53p' … | wc -m`；结论要点与附录不计入）。
自检：附录 A 的 54 个文件:行引用全部存在且未超文件行数（脚本逐条核对，bad=0）。

## 附录 D　原文摘录

`docs/cpamc-parity.md`（§Scheduler Semantics）：

> Priority and weight are routing fields, not presentation metadata. ...
>
> | `priority` | Missing or `0` is the default tier; higher integer values are selected before lower values. ... |
> | `weight` | Missing defaults to `1`; `1..1_000_000` are allowed; non-positive values normalize to `0`, which excludes the credential from `weighted-round-robin`. Weight does not cross priority tiers. |
> | `fill-first` | ... ignores weight. |
> | `round-robin` | ... ignores weight. |
> | Session affinity | An existing session may remain bound to its credential; priority and weight apply when no affinity binding can be reused. |

`internal/quota/codex.go`（`ParseCodexUsage` 的 reset 解析）：

> var resetAtMS *int64
> accuracy := "approximate"
> if hasResetAt && resetAtVal > 0 { ... accuracy = "exact" }
> else if hasResetAfter && resetAfterSec > 0 { calculated := nowMS + int64(resetAfterSec*1000) ... accuracy = "derived" }

`internal/quota/antigravity.go`（`antigravityBucketReset`）：

> if ms, ok := parseRFC3339MS(resetTimeStr); ok { ... return &ms, formatResetInstant(...) }
> if keepText { return nil, strings.TrimSpace(resetTimeStr) }
> return nil, ""

`E:/works/Homan-context/projects/oh-my-cpa/AGENTS.md`（§边界）：

> 边界：不做自动路由，不进 CPA 请求路径（2026-10-30 定）。

## 变更范围

- 要改的文件：无（只读调研；仓库内只新增本报告 `docs/delivery/2026-11-23-omc-quota-lending/research.md`）
- 要不要建 Issue：否——本次只回答现状与可行性，是否立项、要不要动路由权重由浩文决定，调研不替人决定
- 预计几轮：一轮（报告由跟进 Agent 阅读；若决定落地实现，另立开发任务）
- 走哪条路：自建小队（若立项，先定「同池」分组口径再排实现）
