---
type: acceptance-before
slug: 2026-11-17-quota-prediction-usability
status: snapshot
updated: 2026-11-17
source: cpa-buddy MCP（只读）
summary: "2026-11-17 19:36 +08 线上 before：quota_prediction 33 条（2 on_track / 31 unknown），CMC 三 auth windows 为空，OPC 不出现在 quota_list"
---

# 额度预测可用化：线上 before 快照

- **读数时间**：2026-11-17 19:36:22 +08（`quota_prediction.generated_at_ms = 1794915382118`；三个工具在同一分钟内调用）。
- **读取方式**：cpa-buddy MCP 只读工具 `quota_prediction`（全量）、`quota_list`、`model_advice`。**未刷新额度、未调用任何 refresh/write 工具、未读取或管理密钥、未修改生产数据。**
- **脱敏**：credential label 使用 MCP 已掩码的原文；CMC 的 64 位 key hash 只保留前 4 位。**不记录 auth_index、账号原文、密钥。**
- **线上版本背景**：2026-11-17 只读复核线上入口资源哈希，线上实际为 `36653a31`（源 `ba9e2ee7`）；部署时间未从外部核实。本快照是该版本服务端存储的观测，不是上游实时读数。
- **用途**：第一期 after 回放的对照基准；不是「当前额度余额」，也不是上游实时值。

## 1. quota_prediction（全量 33 条）

**总计**：33 条，`omitted = 0`。`on_track` 2 条，`unknown` 31 条，`will_exhaust` 0 条。

31 条 unknown 的最终原因：

| 原因（reasons） | 条数 | 说明 |
|---|---:|---|
| 仅 `stale_reading`（或叠加 `used_percent_below_five`） | 18 | 14 条 antigravity 旧快照 + 2 条 claude 旧快照 + 2 条 codex |
| `boundary_unknown`（叠加 `used_percent_below_five`） | 6 | antigravity 3p 的 5h/weekly × 3 个健康账号 |
| `mid_cycle_reset` | 7 | antigravity Gemini 5h/weekly × 3 个健康账号 + claude `seven_day` |

### 1.1 目标窗口（fresh，A1/A2/A4 要动的）

antigravity 三个健康账号的窗口值与 `quota_list` 中的 Pro 账号一一对应；A/B/C 分别对应 `quota_list` 的 #2/#3/#4，**不写 auth_index**。claude 的「主账号」指 `default_claude_max_5x` 凭证。

| provider（账号） | credential（脱敏） | window_id | kind / scope | used% | expected% | lasts | verdict | reasons | samples | coverage_s | max_gap_s | 观测（+08） |
|---|---|---|---|---:|---:|---|---|---|---:|---:|---:|---|
| antigravity A | `ant***@gmail.com.json` | `ag_Claude and GPT models_3p-5h` | model_scoped / group | 0 | — | — | unknown | boundary_unknown, used_percent_below_five | 0 | — | — | 19:32:35（226s） |
| antigravity A | `ant***@gmail.com.json` | `ag_Claude and GPT models_3p-weekly` | model_scoped / group | 0 | — | — | unknown | boundary_unknown, used_percent_below_five | 0 | — | — | 19:32:35（226s） |
| antigravity A | `ant***@gmail.com.json` | `ag_Gemini Models_gemini-5h` | model_scoped / group | 10.577685 | 78.1 | — | unknown | mid_cycle_reset | 16 | 13500 | 900 | 19:32:35（226s） |
| antigravity A | `ant***@gmail.com.json` | `ag_Gemini Models_gemini-weekly` | model_scoped / group | 22.467170 | 19.4 | — | unknown | mid_cycle_reset | 110 | 116160 | 1800 | 19:32:35（226s） |
| antigravity B | `ant***@gmail.com.json` | `ag_Claude and GPT models_3p-5h` | model_scoped / group | 0 | — | — | unknown | boundary_unknown, used_percent_below_five | 0 | — | — | 19:32:35（226s） |
| antigravity B | `ant***@gmail.com.json` | `ag_Claude and GPT models_3p-weekly` | model_scoped / group | 0 | — | — | unknown | boundary_unknown, used_percent_below_five | 0 | — | — | 19:32:35（226s） |
| antigravity B | `ant***@gmail.com.json` | `ag_Gemini Models_gemini-5h` | model_scoped / group | 10.612550 | 78.0 | — | unknown | mid_cycle_reset | 15 | 13500 | 1799 | 19:32:35（226s） |
| antigravity B | `ant***@gmail.com.json` | `ag_Gemini Models_gemini-weekly` | model_scoped / group | 20.191634 | 47.9 | — | unknown | mid_cycle_reset | 233 | 289019 | 1800 | 19:32:35（226s） |
| antigravity C | `ant***@gmail.com.json` | `ag_Claude and GPT models_3p-5h` | model_scoped / group | 0 | — | — | unknown | boundary_unknown, used_percent_below_five | 0 | — | — | 19:33:35（166s） |
| antigravity C | `ant***@gmail.com.json` | `ag_Claude and GPT models_3p-weekly` | model_scoped / group | 0 | — | — | unknown | boundary_unknown, used_percent_below_five | 0 | — | — | 19:33:35（166s） |
| antigravity C | `ant***@gmail.com.json` | `ag_Gemini Models_gemini-5h` | model_scoped / group | 10.376835 | 90.5 | — | unknown | mid_cycle_reset | 18 | 16200 | 1800 | 19:33:35（166s） |
| antigravity C | `ant***@gmail.com.json` | `ag_Gemini Models_gemini-weekly` | model_scoped / group | 16.708875 | 19.0 | — | unknown | mid_cycle_reset | 114 | 114480 | 1800 | 19:33:35（166s） |
| claude 主账号（Max 5x） | `cla***@vinsprite.com.json` | `five_hour` | five_hour / standard | 24 | 75.2 | true | on_track | — | 37 | 13380 | 951 | 19:35:35（46s） |
| claude 主账号 | `cla***@vinsprite.com.json` | `seven_day_fable` | model_scoped / model | 13 | 65.8 | true | on_track | scope_unknown | 223 | 386253 | 98699 | 19:35:35（46s） |
| claude 主账号 | `cla***@vinsprite.com.json` | `seven_day` | weekly / standard | 45 | 65.8 | — | unknown | mid_cycle_reset | 552 | 386253 | 97979 | 19:35:35（46s） |

### 1.2 stale / 旧快照窗口

这些窗口不是本期 A1/A2/A4 的翻转目标；after 回放时应保持 `stale_reading`，不能被伪造成结论。

| provider | credential（脱敏） | 快照观测（+08） | data_age | window_id | kind / scope | used% | lasts | verdict | reasons |
|---|---|---|---:|---|---|---:|---|---|---|
| antigravity | `ant***@gmail.com.json` | 2026-11-16 19:24:35 | 87106s | `ag_Claude and GPT models_3p-weekly` | model_scoped / group | 0 | — | unknown | stale_reading, used_percent_below_five |
| antigravity | `ant***@gmail.com.json` | 2026-11-16 19:24:35 | 87106s | `ag_Gemini Models_gemini-weekly` | model_scoped / group | 100 | — | unknown | stale_reading |
| antigravity | `ant***@gmail.com.json` | 2026-11-09 16:44:08 | 701533s | 3p-5h / 3p-weekly / gemini-5h / gemini-weekly | model_scoped / group | 全 0 | — | unknown | stale_reading, used_percent_below_five |
| antigravity | `ant***@gmail.com.json` | 2026-11-09 16:49:08 | 701233s | 3p-5h / 3p-weekly / gemini-5h / gemini-weekly | model_scoped / group | 全 0 | — | unknown | stale_reading, used_percent_below_five |
| antigravity | `ant***@gmail.com.json` | 2026-10-20 19:53:28 | 2418173s | `ag_Claude and GPT models_3p-5h` | model_scoped / group | 0 | — | unknown | stale_reading, used_percent_below_five |
| antigravity | `ant***@gmail.com.json` | 2026-10-20 19:53:28 | 2418173s | `ag_Claude and GPT models_3p-weekly` | model_scoped / group | 0 | — | unknown | stale_reading, used_percent_below_five |
| antigravity | `ant***@gmail.com.json` | 2026-10-20 19:53:28 | 2418173s | `ag_Gemini Models_gemini-5h` | model_scoped / group | 15.906173 | — | unknown | stale_reading |
| antigravity | `ant***@gmail.com.json` | 2026-10-20 19:53:28 | 2418173s | `ag_Gemini Models_gemini-weekly` | model_scoped / group | 11.794130 | — | unknown | stale_reading |
| claude | `cla***@vinsprite.com.json` | 2026-10-21 17:09:37 | 2341604s | `five_hour` | five_hour / standard | 100 | — | unknown | stale_reading |
| claude | `cla***@vinsprite.com.json` | 2026-10-21 17:09:37 | 2341604s | `seven_day` | weekly / standard | 17 | — | unknown | stale_reading |
| codex | `cod***@gmail.com-plus (1).json` | 2026-11-16 11:31:35 | 115486s | `weekly` | weekly / standard | 100 | — | unknown | stale_reading |
| codex | `cod***@gmail.com-prolite.json` | 2026-11-13 10:00:02 | 380179s | `weekly` | weekly / standard | 100 | — | unknown | stale_reading |

**对不上的地方（如实记录）**：

- `quota_prediction` 返回 7 组 antigravity 快照，而 `quota_list` 只有 4 个 antigravity 凭证；多出的 2026-11-09 两组和 2026-10-20 一组是更早的保留快照。脱敏 label 对 4 个账号完全相同，**无法用 label 归属到具体凭证**；本节按快照时间原样记录，不猜。
- claude 第二账号（`quota_list` 中 Max 之外的 `default_raven`）在 `quota_prediction` 里只有 2026-10-21 的 stale 快照（`five_hour` 100%、`seven_day` 17%）；但 `quota_list` 同一凭证在 19:30:36 有 fresh 快照（`five_hour` 0%、`seven_day` 41%）。两者不一致，说明预测输出当时没有这枚凭证的 fresh 预测快照；after 回放应以 `quota_observations_list` 的样本为准，并单独核对这条差异。

## 2. quota_list（凭证与窗口现状）

| provider | 凭证（脱敏/编号） | 状态 | observed（+08） | windows | 关键结论 |
|---|---|---|---:|---:|---|
| antigravity | `ant***@gmail.com.json` #1（Free） | cooldown | 11-16 19:24:35 | 2 | `ag_Gemini Models_gemini-weekly` 100%、`capacity_unavailable: "stale"`、`has_mid_cycle_reset: true`；`ag_Claude and GPT models_3p-weekly` 0%、`capacity_unavailable: "stale"` |
| antigravity | `ant***@gmail.com.json` #2（Pro） | healthy | 11-17 19:32:35 | 4 | Gemini 5h/weekly：`capacity_unavailable: "reset_mid_cycle"`，`has_mid_cycle_reset: true`；3p 5h/weekly：`capacity_unavailable: "boundary_unknown"`（值见 §1.1 的账号 A） |
| antigravity | `ant***@gmail.com.json` #3（Pro） | healthy | 11-17 19:32:35 | 4 | 同 #2（值见 §1.1 的账号 B） |
| antigravity | `ant***@gmail.com.json` #4（Pro） | healthy | 11-17 19:33:35 | 4 | 同 #2（值见 §1.1 的账号 C） |
| claude | `cla***@vinsprite.com.json` #1（default_raven） | healthy | 11-17 19:30:36 | 2 | `five_hour` 0%、无 `reset_at_ms`、`reset_accuracy: "approximate"`、`capacity_unavailable: "boundary_unknown"`；`seven_day` 41%，有 capacity（tokens 149658120，error 1.2%） |
| claude | `cla***@vinsprite.com.json` #2（default_claude_max_5x） | healthy | 11-17 19:35:35 | 3 | `five_hour` 24%、`seven_day` 45%，均有 capacity；`seven_day_fable` 13%、`capacity_unavailable: "scope_unknown"` |
| codex | `cod***@gmail.com-plus (1).json` | idle | 11-16 11:31:35 | 1 | `weekly` 100%、`capacity_unavailable: "stale"`、`has_mid_cycle_reset: true`；recommendation 写明「凭据已禁用」 |
| codex | `cod***@gmail.com-prolite.json` | idle | 11-13 10:00:02 | 1 | 同上 |
| commandcode | `commandcode-key-06c9…json` | idle | 11-17 19:36:22（列表时间） | **0** | `windows: []`；recommendation `action: refresh`，原因是暂无实时配额数据 |
| commandcode | `commandcode-key-4f58…json` | idle | 11-17 19:36:22（列表时间） | **0** | 同上 |
| commandcode | `commandcode-key-631c…json` | idle | 11-17 19:36:22（列表时间） | **0** | 同上 |
| copilot | `copilot-madebynight.json` | idle | 11-17 19:36:22 | 0 | 凭据已禁用；不在本需求 |
| copilot | `copilot-newoahil.json` | idle | 11-17 19:36:22 | 0 | 同上 |
| **opencode（OPC）** | — | — | — | **不出现** | `quota_list` 没有任何 `openai-compatibility` 行；OPC 连「窗口为空」都不显示 |

补充：

- CMC 三个 auth 的 `observed_at_ms` 等于本次列表请求时间，不代表读到了上游；`windows: []` 才是现状。
- OPC 的 key 数、`auth_index` 都不在本次快照里；按调研报告，它需要从 `GET /v8/management/config` 的 `api-keys.openai-compatibility[].keys[].auth_index` 获取。本期未读取该树，不记录 auth_index。

## 3. model_advice

`generated_at_ms = 1794915382871`（19:36:22 +08），`omitted = 0`，`recommended = gemini-3.1-flash-image`。

**总数**：33 个模型。`ok` 11 个（4 个 gemini + 7 个 claude），`unknown` 22 个。

| 模型组 | 模型数 | credentials_total | credentials_usable | reasons（原文摘要） |
|---|---:|---:|---:|---|
| `commandcode/*` | 8 | 7 个模型各 3；`commandcode/z-ai/glm-5.3-flash` 1 | 0 | `0 blocked, 3 without valid readings`（最后一个为 1） |
| `op-*`（OPC） | 9 | 各 3 | 0 | `0 blocked, 3 without valid readings` |
| `deepseek/deepseek-v4.1-flash` | 1 | 4 | 0 | `0 blocked, 4 without valid readings` |
| `z-ai/glm-5.3-flash` | 1 | 4 | 0 | `0 blocked, 4 without valid readings` |
| `jev-latest` / `jev-preview` | 2 | 各 1 | 0 | `0 blocked, 1 without valid readings` |
| `step-5-preview` | 1 | 1 | 0 | `0 blocked, 1 without valid readings` |

`ok` 的 11 个模型：

- gemini：`gemini-3.1-flash-image`、`gemini-3.8-flash-high`、`gemini-3.8-flash-tiered`、`gemini-4-hydrogen-xhigh`；各 3 个可用凭证，最紧窗口 weekly 16.708875%，reset 1795404935000。
- claude：`claude-fable-5`、`claude-fable-5-1`、`claude-fable-5-2`、`claude-haiku-5-5`、`claude-opus-5-5`、`claude-sonnet-5`、`claude-sonnet-5-5`；各 2 个可用凭证，最紧窗口 weekly 41%，reset 1795449599518。

第二期判据：`commandcode/*`（CMC 插件 3 个 auth）与 `op-*`（OPC）在 before 里全部是 0 个可用凭证、`N without valid readings`。`deepseek/…`、`z-ai/…` 是 Command Code 兼容条目的模型（调研 §3.7），**不在需求点名的「CMC 三个 auth 与 OPC key」范围内**；第二期如果这些也随之恢复，记为范围外观察，不当作 B1 的通过条件。

## 4. 与调研报告的对账

| 调研结论（2026-11-17） | 本次 before | 是否一致 |
|---|---|---|
| antigravity 3p 窗口永远 `boundary_unknown`，`reset − observed − period ≈ 253ms` | 3 个健康账号 × 2 个 3p 窗口 = 6 条 fresh `boundary_unknown`；毫秒差本次未逐条重算 | 定性一致；毫秒差沿用调研的算术复核 |
| antigravity weekly 105–229 样本被 `mid_cycle_reset` 拦 | 本次 110 / 233 / 114 样本，均 `mid_cycle_reset`；233 超出调研上限 | 一致（233 为新观测） |
| claude `seven_day` 543 样本被 `mid_cycle_reset` 拦 | 552 样本 / 386253s / max_gap 97979s，`mid_cycle_reset` | 一致（样本增长 9 条） |
| claude `five_hour` 可用（sample 25 / coverage 8669 / burn 7.3086） | 37 样本 / 13380s / burn 6.3979，`on_track` | 一致（读数变化，结论相同） |
| CMC 3 个 auth `windows: []` | 3 个 auth 全为 `windows: []` | 一致 |
| OPC 不出现在 `quota_list` | 没有 openai-compatibility 行 | 一致 |
| codex 两个凭证禁用 | `quota_list` recommendation 写明「凭据已禁用」；预测只有 stale 周窗口 | 一致（未另调 `oauth_credentials_list`） |
| claude 第二账号 `five_hour` used=100 → `lasts_until_reset=false` | 预测只有 2026-10-21 的 stale 快照 used=100；`quota_list` fresh 读数是 used=0、`boundary_unknown`（无 reset） | **不一致**：该凭证状态已变化；after 回放以 `quota_observations_list` 为准，并单列这条差异 |

## 5. 缺口与未实测

- 本快照来自 OMC 存储的只读观测，**不是上游实时值**；`stale_reading` 的窗口尤其不能当当前余额。
- 未读取 `auth_index`、密钥、账号原文；因此 4 个 `ant***@gmail.com.json` 账号只能用 `quota_list` 顺序 + 窗口值区分，不能用稳定 ID 归属。
- OPC 完全不可观测：`quota_list` 无行，预测无条目；本期无法给出 OPC 的 before 数值。
- CMC 上游真实数值未读：没有调用 `requests/api-call`，只记录 `windows: []`。
- 本次没有逐条重算 253ms 的边界差；A1 的复现以调研报告的算术复核为准，after 对比以「verdict 是否翻转 / `boundary_unknown` 是否消失」为准。
- `quota_prediction` 返回 33 条、`omitted = 0`；默认上限 100 未截断。

## 6. 给 after 的对照键

- 主键：`provider` + 脱敏 credential label + `window_id` + `window_scope`（+ `window_model`）。
- antigravity 四个账号 label 相同，用 `window_id` + `used_percent` + `sample_count` 组合区分 A/B/C；旧快照用 `observed_at_ms` 的快照时间区分。
- 对比字段固定为：`verdict`、`reasons`、`expected_used_percent`、`lasts_until_reset`、`exhaustion_eta_ms`、`sample_count`、`coverage_seconds`、`max_gap_seconds`。
- after 必须由**同一只读路径**（cpa-buddy MCP）和**同一份观测**生成；不刷新、不改生产数据、不读密钥。
