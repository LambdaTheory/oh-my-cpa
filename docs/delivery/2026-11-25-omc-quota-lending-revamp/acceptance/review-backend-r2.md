---
type: test-report
slug: 2026-11-25-omc-quota-lending-revamp-backend-r2
status: done
updated: 2026-11-27
verdict: 通过（无范围内要修；O4 为观察）
dispatch: works 2026-11-25#T6-S6
summary: "额度拆借改版后端增量复审：F1～F5 已修；F6 按 PRD v1.0.3 改读法关闭；r4 后 F7、O2、O5 关闭，无范围内要修，只剩观察 O4"
---

# 增量复审：额度拆借改版后端（T6-S6 r2）

**r2 结论：范围内要修 1 条（F6，低危）；F1、F2（后端段）、F3、F4、F5 已修。** 逐个改动都有新用例，我另做了 4 个突变探针，每个只改一处，全部让相关测试变红；自跑 `go test` 三包、`go vet`、`gofmt`、`check-demo` 全绿，`check-docs` 只剩基线那 31 条。F6（r2）已按 PRD v1.0.3 改读法关闭；F7（r3）与 O2、O5 在 r4 关闭。当前无范围内要修，只剩观察 O4，结论以第 10 节为准。

- 审查对象：分支 `Newoahil/11-26-t6-s3-codex`，HEAD `88e57ae4`；本工作区已 `git merge`（合并提交 `46d03c79`），代码与 `88e57ae4` 一致。
- 增量 diff：`git diff 3e07ae4a..88e57ae4`，7 个文件、+194/−54（`internal/quota/lending.go`、`internal/quota/lending_test.go`、`CONTEXT.md`、`docs/architecture.md`、`docs/testing.md`、`contract.md`、`deploy/cloudflare/data/provenance.json`）。
- 标准：PRD **v1.0.2**（分支 `Newoahil/11-25-t6-s1-claude` 的 `758a09ae`，只读，未合并）与澄清清单 `2026-11-26-T6-prd-clarifications.md`；上一份报告的 P1、P2 已由 P-01、P-09 定。`contract.md` 仍只作对照。
- 本次范围：F1～F5（F4=P-01、F5=P-08），以及 P-03、P-09、P-10 的后端部分；不改前端、不连真实 CPA。
- 工作区状态：复审结束时 `git status --short` 除本报告外为空；探针的临时改动都用 `git checkout --` 还原，没有新建探针文件。

## 1. F1～F5 复验

| 项 | 上一版结论 | 本次结论 | 证据 |
|---|---|---|---|
| F1（中）不动卡的原因列表漏服务原因 | 范围内要修 | **已修** | `lendingServiceReasons` 删掉了「窗口不可比较就返回空」的守卫，冷却 / 重授权 / 读取失败 / 耗尽 / <10% 与窗口级原因并列；`window_exhausted` 与 `remaining_below_ten` 仍互斥。新用例 `TestLendingHoldCardListsEveryReason`（过期 + 冷却 + 重授权 → `[needs_reauth, cooldown_active, reading_stale]`，主原因 `needs_reauth`）、`TestLendingNoAdvicePoolUnionsHoldReasons`（整池原因并集）。突变 M3 把旧守卫放回，两条用例都变红 |
| F2（低）文档没跟上口径 | 范围内要修 | **已修（后端段）** | `CONTEXT.md` 删掉一期定义与多余的 `+ ` 前缀，只留一条新定义，并补了分档基准与回满过期；`docs/architecture.md` 与 `CONTEXT.md` 末尾多余空行已清；`docs/testing.md` 的 lending coverage 段改成按档、建议值、读不到、原因并集、池外交集的口径。`PRODUCT.md` 与 `docs/testing.md` 的前端段按 PM 说明属前端分支，**不计未修** |
| F3（低）链式分档与契约边界无用例 | 范围内要修 | **已修** | 新增 `TestLendingTierAnchorsOnTheLatestRefill`（72h / 60h / 45h，与 PRD 的 3.0 / 2.4 / 1.8 天同形，建议 0 / 0 / −10）、`TestLendingAllPrioritiesUnreadableStillSuggests`、`TestLendingOutsideCards` 里的「榜内缺模型清单不列」与「剩余 5% 照列」。M1、M4 证明有牙。残余：池外排除的三条分支（需重新授权 / 读取失败 / 已耗尽）与「本池不动卡不重复列」仍无用例，并入 F6 的修复范围 |
| F4（P-01）分档以本档最晚的卡为基准 | 本次新增 | **已修** | `lendingAssignBoard` 取本档第一张（按回满从晚到早排，即本档最晚）作锚点，`gap < 1 天` 才并入。M1 把它改回「和上一张比」，三张卡立刻连锁并成一档，`TestLendingTierAnchorsOnTheLatestRefill` 变红（`is_balanced=true`、三张全 tier 1） |
| F5（P-08）回满时刻已过的读数算过期 | 本次新增 | **已修** | `analyzeLendingCard` 增加 `ResetAtMS != nil && *ResetAtMS <= now` → `reading_stale`；`TestLendingPastRefillIsStale` 同时覆盖「已过 60 秒」与「恰好到点」，两张都不动、主原因 `reading_stale`、`valid_until` 落到榜内最早的回满。M2 把 `<=` 改成 `<`，「恰好到点」那张立刻变回 suggested（tier 3），用例变红 |

与 P-xx 直接相关的后端检查：

| 澄清 | 结论 | 证据 |
|---|---|---|
| P-03 读不到优先级 | **通过（后端部分）** | H 只取读得到的卡，全部读不到时 H=0；`alreadyOrdered` 要求榜内全部读得到；读不到的卡给建议、`priority` 与 `needs_change` 缺失；`needs_change_count` 只数 `needs_change=true`。新用例 `TestLendingAllPrioritiesUnreadableStillSuggests`（0 / −10、计数 0、`is_balanced=false`） |
| P-09 池外的卡的定义 | **通过（v1.0.3 改读法）** | 交集、排除项、<10% 照列、缺模型清单不列都已实现；v1.0.3 把「未进池的卡」改为要列、本池的卡（榜内与不动）不列，实现与此一致，F6 关闭（见第 5 节与第 9 节） |
| P-10 停用的卡 | **通过（后端部分）** | 停用凭证仍进 `unpooled`、原因 `credential_disabled`，本次未改；「凭证已停用」的中文表述属前端 |

## 2. 新增测试的牙

每个探针只改 `internal/quota/lending.go` 一处，跑完 `git checkout -- internal/quota/lending.go` 还原；每次还原后 `git status --short` 为空。

| 探针 | 改动 | 跑的命令 | 结果 |
|---|---|---|---|
| M1 | 分档锚点改回「和上一张比」（`tiers[last][0]` → 本档最后一张） | `go test -run 'TestLendingTierAnchorsOnTheLatestRefill\|TestLendingTierGapUsesOneDay\|TestLendingRanksByLatestRefillNotDailyAllowance' ./internal/quota` | **变红**：`TestLendingTierAnchorsOnTheLatestRefill` 报 `IsBalanced:true`、三张全 `Tier:1` |
| M2 | 回满过期判断 `<=` → `<` | `go test -run TestLendingPastRefillIsStale ./internal/quota` | **变红**：`card exact = {... Role:suggested Tier:3 ...}`, want hold with `reading_stale` |
| M3 | 恢复旧守卫「`window == nil \|\| !comparable` 就返回空」 | `go test -run 'TestLendingHoldCardListsEveryReason\|TestLendingNoAdvicePoolUnionsHoldReasons\|TestLendingServiceabilityReasons' ./internal/quota` | **变红**：`reasons = [reading_stale]`（应三条）；`pool reasons = [reading_stale no_long_window]`（应四条） |
| M4 | 同档边界 `gap < 1 天` → `<=` | `go test -run 'TestLendingTierGapUsesOneDay\|TestLendingTierAnchorsOnTheLatestRefill' ./internal/quota` | **变红**：`exactly_one_day_splits` 报 `is_balanced = true, want false` |

白盒行为探针（不改实现，只观察；追加到 `internal/quota/lending_test.go` 末尾，跑完 `git checkout -- internal/quota/lending_test.go` 还原）：

| 探针 | 观察 |
|---|---|
| 两张卡，一张刚回满 | `state=no_advice`、`holds=2`、`valid_until=nil`；刚回满的卡 `hold / reading_stale`，另一张也 `hold / window_type_mismatch`（见第 5 节） |
| 三张卡，带「近似」时刻的一张刚回满 | `state=advised`、`reference=false`、`holds=1`、`valid_until=+172,800,000ms`（跳到下一张的回满） |
| 同上，但那张还差 1 分钟才回满 | `state=advised`、`reference=true`、`holds=0`、`valid_until=+60,000ms` |
| 最高优先级的卡 1 分钟后回满（100 / 50 / 40） | 回满前：H=100，建议 100 / 90 / 80，`needs_change_count=3`；回满后：H=50，建议 50 / 40，`needs_change_count=2`，那张 `hold / reading_stale` |

## 3. 门禁复跑

| 命令 | 结果 | 关键输出 |
|---|---|---|
| `go test -count=1 ./internal/quota ./internal/api ./internal/demo`（golang:1.27.1 容器） | 通过 | `ok internal/quota 0.055s` / `ok internal/api 316.634s` / `ok internal/demo 90.400s` |
| `go vet ./...` | 通过 | 无输出 |
| `gofmt -l internal/quota internal/api internal/demo` | 通过 | 无输出 |
| `node scripts/check-demo.mjs` | 通过 | `the demonstration dataset is current: 155 responses covering 20 console routes` |
| `node scripts/check-docs.mjs` | 红（基线既有） | `missing-path=9 retired-reference=0 line-reference=22`，31 条全部指向未改动的 `docs/delivery/2026-11-12-omc-three-credential-quota/impl-plan.md`；本需求目录 0 条 |

补充：演示数据只改了 `provenance.json` 的 `inputs_digest`，因为 `internal/quota/lending.go` 在 `scripts/demo-inputs.mjs` 的输入清单里；155 条响应里没有 `reading_stale`、也没有非空的 `outside_cards`，所以响应本身不变，`check-demo` 通过。

## 4. 回满已过算过期的副作用

先给结论：**没有发现必须改的副作用**；下面四条都是读得出来的行为变化，前三条已由探针确认，最后一条是推断，记在这里供前端和运营知道。

1. **两张卡的池会在有人回满的那一刻短暂变成「暂无建议」。** 一张卡到点变过期后，池里只剩一张参与卡，不够成榜，`state=no_advice`、`valid_until` 为空，另一张也被标 `window_type_mismatch`（「额度类型不同」的措辞局限，P-04 已记为已知限制）。等下一次额度采集把新窗口读回来就恢复。属于 AC-23 写的「榜随之重算」，不是缺陷，但前端空状态会在这段间隙出现。
2. **`is_reference` 会从 true 变 false。** 探针里带「近似」时刻的卡在榜上时 `reference=true`；它一回满变过期、退出榜，剩下都是精确时刻，`reference=false`。契约把 `is_reference` 定义为「榜内任一推算/近似」，所以这是对的；只是页面上「参考」标记会在回满那一刻消失。
3. **`valid_until` 会跳到下一张的回满时刻。** 探针里从 +1 分钟直接跳到 +2 天。也就是说建议不会停留在已经过期的截止时间上；页面按 `valid_until` 到点重取时会拿到新的、更晚的截止时间。
4. **H 会跟着榜内最大优先级一起变，整池建议数字瞬间下移。** 探针（优先级 100 / 50 / 40，最高那张 1 分钟后回满）：回满前 H=100 建议 100 / 90 / 80；回满后它退出榜，H=50，建议 50 / 40，`needs_change_count` 从 3 变 2。下一次采集读回新窗口后又会回来。这是 D4「只往下压」与 P-08 的组合结果，PRD 也写了「到时先后会翻，要再改一次」，但一晚上回满好几次的池会让数字来回跳，运营要有预期。
5. **如果上游在回满后仍返回旧的回满时刻，刷新也去不掉「读数过期」。** 这种卡会一直不动，直到 CPA 自己把窗口滚到下一个周期。没有连真实 CPA 验证这种情形出现的概率，只按代码路径记一笔。

## 5. F6：池外名单的成员范围（已按 PRD v1.0.3 改读法，不再是偏差）

- r2 当时的标准（v1.0.2）：5.7 与 AC-25 写「未进池的卡……不重复列在池外名单里」。实现把 `unpooled` 也列进池外名单，所以记成 F6。
- v1.0.3 改的是读法：池外名单包含别的池的卡**与未进池的卡**（未进池的卡在 CPA 里同样和本池的卡竞争同一个模型），不含本池的卡——榜内的与不动的都不重复列。实现本来就是「排除本池、其余合格卡全列」，因此**逻辑与响应形状不用改**。
- 契约：`f45bf1cf` 已在 §1.4 写明成员范围，与 5.7 一致（逐条对照见第 9 节）。
- 用例：`f45bf1cf` 断言 `unpooled` 与 `low-outside` 列在池外、`same-pool-hold` 在池内 hold 且不重复列；后者的强度见第 9 节。
- 演示数据不受影响：155 条响应里 `outside_cards` 全为空，`check-demo` 通过。
- **结论：F6 已按 PRD v1.0.3 改读法，不再是偏差。**

## 6. 问题清单

| ID | 结论 | 严重度 | 一句话 | 证据位置 |
|---|---|---|---|---|
| F6 | 已关闭（v1.0.3 改读法） | — | 池外名单包含别的池与未进池的卡、不含本池的卡，这就是 r3 的标准，不再是偏差 | `internal/quota/lending.go` 的 `attachLendingOutsideCards`；`TestLendingOutsideCards`；PRD 5.7 / AC-25 |
| O1 | 已关闭（v1.0.3 写明边界） | — | PRD 4.2 / 5.2 / 5.6 / AC-8 / AC-23 已统一为「不晚于当前时刻（含恰好到点）」 | `analyzeLendingCard`；`contract.md` §2.2；`TestLendingPastRefillIsStale` |
| O2 | 已关闭（r3 补三条例用，r4 隔离本池排除） | — | 需重新授权 / 读取失败 / 已耗尽都有用例；同池不动卡改用只过期的卡，去掉 `inPool` 就变红 | `lendingOutsideEligible`；`TestLendingOutsideCards` |
| O3 | 部分关闭（见 F7） | — | f45bf1cf 新增多档部分读不到的用例；H 规则有牙，「一张读不到就不判已排对」那半没有牙 | `lendingAssignBoard`；`TestLendingSameTierTreatsUnreadablePriorityAsNoChange` |
| F7 | 已关闭（b76faa82） | — | 多档部分读不到的用例加了「原始值 1」子例，忽略 `PriorityKnown` 时该子例会红 | `TestLendingPartialPriorityKnowledgeBreaksOrdering` |
| O5 | 已关闭（b76faa82） | — | 契约 §1.4 已删掉「PRD 5.7 未写死」的过期括注 | `contract.md` §1.4 |
| O4 | 范围外观察 | 低 | 池外名单不套用 P-08 的过期口径：回满时刻已过、但仍有余量的池外卡照样列出。AC-25 没要求排除，属主池与池外口径不一致 | `lendingOutsideEligible` |

## 7. 未核实与缺口

- 没有起界面：本轮仍是后端增量，P-03 的「行尾写现在的数字读不到」「N 张要改」文案、P-09 的折叠名单渲染都归前端切片。
- 没有连真实 CPA、真实账号或线上数据；`unavailable`、`models`、`reset_at_ms` 的真实返回形状仍只按契约与假网关核对。
- 「未进池的卡要列在池外名单」这条，前端是否已按 v1.0.3 渲染未验证；F6 已关闭，剩下的是前端怎么呈现。
- O4 与第 4 节第 5 条都没有真实环境复现，只按代码路径判断。
- 本轮未跑 `pnpm verify` / `test:fast` / `check:ui`：范围是后端，未改 `web/`。

## 8. 环境与清理

- 本轮没有执行任何删除类命令；没有新建探针文件。突变探针只改 `internal/quota/lending.go`，行为探针追加到 `internal/quota/lending_test.go` 末尾，两者都用 `git checkout --` 还原；每次还原后 `git status --short` 为空。
- 本报告是唯一新增文件；`git status --short` 在提交前只有它。
- 前一轮 `Remove-Item` 被平台拦下、改用 `git clean -f` 的经过仍记在 `review-backend.md` 第 9 节，本轮没有新的拦截。
- 环境：Windows 11；本机无 Go，门禁在 Docker `golang:1.27.1`，挂载本工作区与 `omc-gomod` / `omc-gocache` 缓存卷；未起长期服务，未占用 18631–18634 端口，未碰 5181。

## 9. r3 确认（f45bf1cf / PRD v1.0.3）

对象：`Newoahil/11-26-t6-s3-codex` 的 `f45bf1cf`（只动契约与测试），已合入本工作区（`2323f0f2`）；标准是 PRD v1.0.3（`5ac64d26`，只读）。**结论：F6 关闭；新增 1 条测试缺口 F7（低）。**

### 9.1 ① 契约 §1.4 与 PRD v1.0.3 5.7

| 5.7 / AC-25 的读法 | 契约 §1.4 | 一致 |
|---|---|---|
| 池外含别的池的卡与未进池的卡（别的套餐、别的 provider） | 明写「包含别的池的卡与未进池的卡」 | 是 |
| 不含本池的卡（榜内与不动都不重复列） | 明写「不含本池的卡——榜内的与不动的都不重复列在这里」 | 是 |
| 模型取榜内参与卡的交集，供任一个即算 | 同 | 是 |
| 交集为空或榜内缺模型清单时不列 | 同 | 是 |
| 停用 / 不可用 / 冷却 / 重授权 / 读取失败 / 耗尽不列，<10% 照列 | 同 | 是 |
| 模型清单只取名册已有字段 | 同 | 是 |

小瑕疵（O5）：§1.4 仍写「（PRD 5.7 未写死，本版采用）」，但 PRD 从 v1.0.2 起已把交集口径写进 5.7；建议删掉这个括注。

### 9.2 ② 用例断言与牙

| 断言 | 位置 | 有牙？ |
|---|---|---|
| 未进池的卡列在池外名单 | `TestLendingOutsideCards` 的 `unpooled`、`low-outside`（都是 plan_type=unknown） | 与 v1.0.3 一致；若按旧读法排掉未进池卡，名单只剩 2 张，用例会红（未单独做这个突变） |
| 本池不动的卡不重复列 | 新增 `same-pool-hold`，断言池内 `hold`、池外不在 | 弱：这张卡同时是冷却卡，单看它分不清「本池排除」与「冷却排除」 |
| 需重新授权不列 | 新增 `reauth` | 是（P1 红） |
| 读取失败不列 | 新增 `failed` | 是（P2 红） |
| 已耗尽不列 | 新增 `exhausted`（status=exhausted） | 是（P3 红） |
| 多档 + 一张读不到：H 只取读得到的 | `TestLendingPartialPriorityKnowledgeBreaksOrdering` | 是（P5 红：H 变 999） |
| 同一用例的「一张读不到就不判已排对」 | 同上 | **否**（P6 绿）：那张卡的原始优先级是 999，本来就过不了排序检查，两条规则区分不开 → F7 |

| 探针 | 改动（只改 `internal/quota/lending.go` 一处） | 结果 |
|---|---|---|
| P1 | `lendingOutsideEligible` 去掉 `NeedsReauth` | **红**：名单多出 `reauth` |
| P2 | 去掉 `ReadingFailed` | **红**：多出 `failed` |
| P3 | 去掉 `Status != "exhausted"` | **红**：多出 `exhausted` |
| P4 | 去掉 `inPool[card.AuthIndex] ||` | **红**：多出榜内卡 `a`、`b`；`same-pool-hold` 仍不出现，印证上面的弱断言 |
| P5 | 建议值的 H 循环去掉 `PriorityKnown` 跳过 | **红**：`card a suggestion = 999, want 20` |
| P6 | `allKnown` 忽略 `PriorityKnown` | **绿**：用例不覆盖这条规则 |

每个探针跑完都 `git checkout -- internal/quota/lending.go` 还原，`git status --short` 为空。

### 9.3 ③ 门禁

| 命令 | 结果 |
|---|---|
| `go test -count=1 ./internal/quota ./internal/api ./internal/demo` | 通过：`ok internal/quota 0.035s` / `ok internal/api 165.881s` / `ok internal/demo 44.273s` |
| `go vet ./...`、`gofmt -l internal/quota internal/api internal/demo` | 通过，无输出 |
| `node scripts/check-demo.mjs` | 通过：`the demonstration dataset is current: 155 responses covering 20 console routes` |

### 9.4 结论与后续

- F6：**已按 PRD v1.0.3 改读法，不再是偏差**；实现与响应形状不动，契约与用例已对齐。
- F7（新，范围内要修，低）：`TestLendingPartialPriorityKnowledgeBreaksOrdering` 只守住「H 只取读得到的」，没守住「只要有一张读不到就不判已排对」。建议把那张读不到卡的原始优先级改成有序的值（例如 1）另加一条用例，或另建一个子例；999 的那条留给 H。
- O4 仍在：池外名单不套用回满过期口径，AC-25 未要求，保持观察。
- O5（新，观察，低）：契约 §1.4 的「PRD 5.7 未写死」括注已过期，可顺手删掉。
- 本轮没有用任何删除命令；只改了这一个报告文件，探针改动都用 `git checkout --` 还原。

## 10. r4 确认（b76faa82）

对象：`Newoahil/11-26-t6-s3-codex` 的 `b76faa82`（单个提交，只动 `internal/quota/lending_test.go` 与 `contract.md`；`lending.go` 零改动，已按 `git show --stat` 核对），已合入本工作区（`5038efab`）。**结论：F7 关闭；O2 的弱断言关闭；O5 关闭；本轮无新增范围内要修，r2～r4 的要修项清零，只剩观察 O4。**

### 10.1 ① F7：多档 + 一张优先级读不到

用例现在有两条子例：读不到的卡原始优先级 999（守 H）与 1（守「不判已排对」）。P6 突变（`allKnown` 忽略 `PriorityKnown`，只改 `lending.go` 一处）结果：`reported_value_would_break_the_order`（999）仍绿，`reported_value_would_look_already_ordered`（1）**红**——`card b suggestion = 5, want 10`。说明「只要有一张读不到就不判已排对」现在有牙，F7 关闭。

### 10.2 ② 本池不动卡不重复列

同池不动卡改成 `same-pool-stale`：只把 `ObservedAtMS` 放到 25 小时前（读数过期），不冷却、不重授权。P4 突变（去掉 `inPool[card.AuthIndex] ||`）让 `TestLendingOutsideCards` **红**，名单里同时多出榜内卡 `a`、`b` 与 `same-pool-stale`；这条断言现在能单独隔离「本池排除」，不再依赖冷却这个间接条件。

### 10.3 ③ O5：契约括注

`contract.md` §1.4 的「（PRD 5.7 未写死，本版采用）」已删，其余定义未动；与 PRD v1.0.3 5.7 的逐条对照仍见 9.1，一致。

### 10.4 ④ 门禁

| 命令 | 结果 |
|---|---|
| `go test -count=1 ./internal/quota ./internal/api ./internal/demo` | 通过：`ok internal/quota 0.046s` / `ok internal/api 241.927s` / `ok internal/demo 60.257s` |
| `go vet ./...`、`gofmt -l internal/quota internal/api internal/demo` | 通过，无输出 |
| `node scripts/check-demo.mjs` | 通过：`the demonstration dataset is current: 155 responses covering 20 console routes` |

### 10.5 结论

- 范围内要修：0 条。F6（r2）、F7（r3）、O2 与 O5 全部关闭。
- 范围外观察：O4 仍在（池外名单不套用回满过期口径，AC-25 未要求排除）。
- 本轮没有用任何删除命令；只改本报告；两个突变都只改 `internal/quota/lending.go` 一处并用 `git checkout --` 还原，`git status --short` 为空。
