---
type: contract
slug: 2026-11-25-omc-quota-lending-revamp
status: agreed
updated: 2026-11-26
summary: "额度拆借第二版的响应契约：每张卡的建议优先级、现在的优先级、要改与不用改、主名称、前置条件，池的建议与不动的卡，池外同模型卡"
---

# 契约：额度拆借第二版（只读）

对应 PRD：`docs/delivery/2026-11-25-omc-quota-lending-revamp/prd.md`（AC-6～12、14b、15、19～21、23～25）。一期契约继续有效，本文只写新增和改动的字段；一期已有字段的语义没有提到的部分不变。

范围：复用 `GET /management/quota` 与 `GET /management/quota/{authIndex}` 的 `lending` 对象，不新增路由、不新增 CPA 调用、不写 CPA。总览与详情由同一份纯计算结果投影，逐字段同源。

## 1. 字段总览

### 1.1 `lending`（对象，可缺失）

| 字段 | 类型 | 必有 | 含义 |
|---|---|---|---|
| `generated_at_ms` | int64 | 是 | 页面时钟；算法与推导都以它起算 |
| `pools` | array | 是 | 池列表；按 `valid_until_ms` 升序，没有建议的池排在最后，再按 `id` |
| `unpooled_count` | int | 是 | 未进任何池的凭证数（一期口径不变） |
| `unpooled` | array | 是 | 未进池凭证（一期口径不变） |

`lending` 缺失或为 `null` 的降级行为与一期相同：快照或冷却读失败时不投影，页面显示一期的降级空状态，不白屏。

### 1.2 `pools[]`

一期字段 `id`、`provider`、`plan_type`、`plan_label`、`window_kind`、`observed_at_ms`、`fresh_until_ms`、`is_reference`、`reason_codes`、`cards` 保留，语义按下表；`state` 的取值集合改变。

| 字段 | 类型 | 必有 | 含义 |
|---|---|---|---|
| `id` | string | 是 | `provider + ":" + plan_type`，与一期一致 |
| `provider` | string | 是 | 归一化 provider |
| `plan_type` | string | 是 | 归一化 plan_type |
| `plan_label` | string | 否 | 展示用套餐名；取池内按名称排序第一张卡 |
| `state` | string | 是 | `advised`：池内有榜（≥ 2 张参与卡）；`no_advice`：池内没有榜（一期 `limited` / `unavailable` 不再返回） |
| `window_kind` | string | 否 | 出榜的额度类型；没有榜时为空 |
| `valid_until_ms` | int64 | 否 | 建议有效期 = 榜内最早的回满时刻；没有榜时为空 |
| `observed_at_ms` | int64 | 否 | 池内最早读数时刻，用于「依据读数」 |
| `fresh_until_ms` | int64 | 否 | 池内最早鲜度截止；沿用一期的鲜度界 |
| `is_reference` | bool | 是 | 榜内任一卡回满时刻为 `derived` / `approximate` |
| `is_balanced` | bool | 是 | 榜内只有一档（回满时间差不到 1 天），即「差不多」 |
| `needs_change_count` | int | 是 | 榜内 `needs_change == true` 的卡数；入口文案的 N 是所有池的这个数之和 |
| `hold_count` | int | 是 | 池内 `role == "hold"` 的卡数 |
| `reason_codes` | string[] | 是 | `no_advice` 时池内不动卡的全部原因并集，按 5.6 词表顺序；`advised` 时为空数组 |
| `cards` | array | 是 | 池内每张卡一行，见 1.3 |
| `outside_cards` | array | 是 | 池外同模型的卡，见 1.4；没有时为空数组 |

### 1.3 `cards[]`

一期字段 `auth_index`、`name`、`window_id`、`window_kind`、`remaining_percent`、`reset_at_ms`、`reset_accuracy`、`observed_at_ms`、`fresh_until_ms`、`days_to_reset`、`daily_available_percent`、`is_comparable` 保留。`days_to_reset` / `daily_available_percent` 仍按一期口径计算，但**不再参与排序**，前端可不显示。

| 字段 | 类型 | 必有 | 含义 |
|---|---|---|---|
| `auth_index` | string | 是 | 凭证索引 |
| `name` | string | 是 | 凭证文件名（与一期一致） |
| `display_name` | string | 是 | 主名称，与 OAuth 管理页同一张卡的大字一致：有账号名（`email`，没有时 `project_id`）用账号名，没有才用 `name`；都为空时退回 `auth_index`。去掉与 `auth_index` 相同的值、`auth_index` / UUID 前缀，与前端 `deriveAuthFileIdentity` 的口径一致 |
| `role` | string | 是 | `suggested`：有建议数字；`hold`：不动。一期 `prefer` / `avoid` / `normal` / `balanced` / `limited` / `unavailable` 不再返回 |
| `tier` | int | 否 | 榜内档位，从 1 起；`role == "suggested"` 时有值，`hold` 时没有 |
| `priority` | int | 否 | 这张卡在 CPA 里现在的优先级；`priority_known == false` 时缺失 |
| `priority_known` | bool | 是 | 现在的优先级是否读得到；CPA 的列表把未设与显式 0 都表示为 0（写 0 会删属性），所以 0 视为「默认」，不是读不到 |
| `priority_is_default` | bool | 是 | `priority_known == true` 且 `priority == 0`；前端据此写「默认」 |
| `can_edit_priority` | bool | 是 | 这张卡的优先级能不能在控制台改：与 OAuth 管理页的 `canEditFile` 同口径——非 `runtime_only`、文件名在名册内唯一；兼容条目的虚拟条目为 `false`。本功能不写，只用它决定是否显示「去设置」 |
| `suggested_priority` | int | 否 | 建议设成的优先级；`role == "suggested"` 时有值，`hold` 时没有。整数，可为负 |
| `needs_change` | bool | 否 | 建议与现在不同；`hold` 时或 `priority_known == false` 时缺失（不判断要改还是不用改） |
| `primary_reason` | string | 否 | `hold` 时的主原因，按 5.6 词表取第一个；`suggested` 时缺失 |
| `reason_codes` | string[] | 是 | `hold` 时该卡全部原因，按 5.6 词表顺序；`suggested` 时为空数组 |

`cards` 顺序：有建议的卡按 `tier` 升序，再按 `display_name`、`auth_index`；不动的卡排在有建议的卡之后，按 `display_name`、`auth_index`。

### 1.4 `outside_cards[]`

池外提醒（AC-25）。同一项在 `pools[]` 内，只有该池的 `state == "advised"` 时可能非空；`no_advice` 池恒为空数组。

| 字段 | 类型 | 必有 | 含义 |
|---|---|---|---|
| `auth_index` | string | 是 | 池外凭证索引 |
| `name` | string | 是 | 凭证文件名 |
| `display_name` | string | 是 | 主名称，口径同 1.3 |
| `provider` | string | 是 | 该凭证的 provider |
| `pool_id` | string | 否 | 该凭证属于另一个池时，那个池的 `id` |
| `pool_label` | string | 否 | 另一个池的展示名；没有时前端用 `provider` |
| `priority` | int | 否 | 该卡现在的优先级；读不到时缺失 |
| `priority_known` | bool | 是 | 是否读得到现在的优先级 |
| `priority_is_default` | bool | 是 | 0 且读得到，即默认 |

**「同一个模型」的定义**：取该池榜内参与卡都能供的模型（模型 ID 交集）；池外的卡只要供其中任一个模型就列出；交集为空或榜内有卡没有模型清单时，不列任何池外卡。池外名单包含别的池的卡与未进池的卡（它们在 CPA 里同样和本池的卡竞争同一个模型），不含本池的卡——榜内的与不动的都不重复列在这里。停用的、不可用（`unavailable`）、冷却中、需要重新授权、最近一次读取失败、已耗尽的卡不列；剩余低于 10% 的卡照列（CPA 仍会把请求发给它）。

数据来源：名册里 `management.AuthFile.Models`（CPA 的凭证列表已带时零额外请求）。由 `quota.OpenCodeAuthFiles` 合成的兼容条目没有模型清单，本版不列；不为此新增 CPA 调用或缓存。

### 1.5 详情 `lending`

一期字段 `generated_at_ms`、`pool`、`unpooled_reason_codes` 保留。`pool` 与总览的 `pools[]` 同一结构、逐字段相同；不在任何池时为 `null`。`unpooled_reason_codes` 仍按一期返回未进池原因。

## 2. 计算口径（与 PRD 5.2 一一对应）

1. **成池**：同 provider + 同 plan（一期口径）。停用凭证、plan 缺失或 `unknown` 的凭证仍进 `unpooled`；同套餐只有一张凭证时仍进 `unpooled`（`pool_too_small`）。同套餐 ≥ 2 张凭证即形成池，即便一张都排不出来（`state == "no_advice"`）。一期的「没有长窗口就不成池」改为池内不动。
2. **参与卡**：建议窗口读数完整（有剩余、有具体回满时刻）、没过期（回满时刻不晚于当前时刻、含恰好到点，按过期处理）、没有冷却 / 需要重新授权 / 读取失败 / 已耗尽、剩余 ≥ 10%。条件与一期一致；不满足只让这张卡不动，不再让整池无法建议。
3. **一个池一个榜**：参与卡按建议窗口类型分组，取卡数最多的一组；并列时取组内最早回满时刻更早的一组。榜内不足两张时池 `no_advice`。
4. **先后**：榜内按回满时刻从晚到早；每张卡与所在档最晚的那张相差严格小于 1 天（86,400,000 毫秒）时并入该档，否则另起一档，因此同一档里任意两张都差不到 1 天。回满时刻为推算 / 近似时照排，`is_reference = true`。
5. **建议值**：H = 榜内**读得到**的现在优先级的最大值（都读不到或都是 0 时 H = 0）。第 1 档建议 H，第 n 档建议 H − 10 × (n − 1)，同档同值；建议值都不高于 H。
6. **不用改**：
   - 榜内只有一档（`is_balanced = true`）：读得到的卡建议等于现在、`needs_change = false`；读不到的卡建议 H，不给 `needs_change`。
   - 榜内多于 1 档：榜内每张卡的优先级都读得到、且前面档的每张卡现在都严格大于后面档的每张卡时，所有卡建议等于现在、`needs_change = false`。只要有一张读不到，就不算排对，读得到的卡按第 5 条给建议值。
7. **现在读不到**：`priority_known = false`，不给 `needs_change`；建议数字照给。
8. **原因词表**：`needs_reauth` → `cooldown_active` → `reading_failed` → `window_exhausted` → `reading_stale` → `reset_time_unknown` → `remaining_unknown` → `no_long_window` → `remaining_below_ten` → `window_type_mismatch`。`primary_reason` 取该卡原因中在这个顺序里最靠前的一个。参与卡不在榜内时记 `window_type_mismatch`（词表第 10 条「额度类型与本池的榜不同，或同类只有 1 张」）。
9. **有效期**：`valid_until_ms` = 榜内最早的回满时刻；`no_advice` 池为空。
10. **入口计数**：`needs_change_count` 与 `hold_count` 由同一份池数据给出；前端不要另算。

## 3. 兼容与降级

| 情况 | 行为 |
|---|---|
| `lending` 缺失或 `null` | 页面显示一期降级空状态；不白屏（AC-14a） |
| 现在的优先级读不到（兼容条目 / 旧网关） | `priority_known = false`，`priority` 缺失，前端写「—」，不判断要改 |
| 账号名拿不到 | `display_name` 退回文件名；文件名为空退回 `auth_index` |
| 模型清单缺失 | 该池的 `outside_cards` 为空，不影响建议值 |
| 池内一张都排不出 | `state = "no_advice"`，`valid_until_ms` 为空，卡行全部 `hold` |

## 4. 不做

- 不新增路由、不新增 CPA 调用、不写 priority / weight、不落库、不推送、不产生历史。
- 不改变 `reset_advice` 的字段与语义。
- 不新增模型清单的逐卡读取或缓存；数据只来自名册已有字段。

