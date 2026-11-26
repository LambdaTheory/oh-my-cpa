# 额度拆借改版 · 验收报告 r2

- **结论**：本轮 **通过 12 / 不通过 3 / 阻塞 0 / 未测 0**（执行 15 条）。不通过为 N-30、N-51、N-96：不动行没有「现在」、过期卡的补充句写成「最后读数在 1 小时前」（见第 6 节）。整包 97 条的其余 82 条不在本轮范围，沿用 r1 结果（第 7 节）。
- **被测**：分支 `Newoahil/11-26-t6-s8-codex`，依次合入前端最终、PRD v1.0.3、用例终稿后的树；r1 报告保持原样。
- **标准**：`prd.md` v1.0.3（AC 编号，唯一验收标准）；用例 `cases.md` 97 条（N-1～N-97，含 9d744cda 新增 N-97 与改动的 N-31／N-40／N-51／N-88／N-96）；契约 `contract.md`。
- **证据**：临时脚本、日志与截图在 `tmp/`（git 忽略，清单见第 8 节）；截图只留本机、不进 git。

## 1. 合入与被测

| 顺序 | 合并提交 | 来源 | 带入内容 |
| --- | --- | --- | --- |
| 1 | `9683b81f` | `Newoahil/11-26-t6-s4-codex` @ `863b9425` | 前端最终版（16 个文件）：后端最终 `b76faa82`、D1 卡名回退与 D2 池外载荷归一、S7 八条样式文案修复、PRD v1.0.2、演示数据摘要 |
| 2 | （无新提交） | `Newoahil/11-25-t6-s1-claude` @ `5ac64d26` | PRD v1.0.3；`git merge` 报 `Already up to date`，`prd.md` 与 `5ac64d26` 无差异 |
| 3 | `c8bd753b` | `Newoahil/11-26-t6-s5-claude` @ `9d744cda` | 用例终稿：`cases.md` +46／−36，新增 N-97，改动 N-31、N-40、N-51、N-88、N-96 |

- 三次合入只在前端那一次带业务代码；`scripts/acceptance/` 下的 `lending-fixtures.mjs`、`probes/oauthManagement.mjs`、`fake-cpa.mjs`、`lending-experience-env.mjs` **没有冲突**：前端分支已经带着 r1 新增的 Command Code 账单路由、`revCommandCode`／`revCommandCodeFail`、`probeLending(state, now, options)` 第三参、`lendingCardName` 等改动，r1 的夹具与场景都在，本轮没有再动。
- `cases.md` 是唯一冲突文件，按指示取用例分支版本，再把 13.6 整节加回（现行第 1174 行起）：G1/G2 满足、G3 未做、G4 结论、G5/G6 做法、Command Code 形状、v1.0.3 池外口径（含未进池卡、共 6 张）与 N-40 的文字冲突说明。合入后标题数为 97，与用例终稿一致。
- 用例分支只改 `cases.md`（`git diff c8bd753b^1 c8bd753b -- scripts` 为空），测试脚本没有因这次合入变化。

## 2. 环境与构建

- `pnpm build` 成功，SPA 并入单二进制；用 docker `golang:1.27.1` 交叉编译 `tmp/omc-lending-r2.exe`（54,535,168 B，SHA-256 `0648E02B…CF39B05`）。旧的 `tmp/omc-lending.exe` 被运行中的体验环境占用，未覆盖，另存新名。
- 两个体验环境用新二进制重启（旧的两个只停自己起的 node PID，未动别人的进程）：先 `Stop-Process -Id 31804`、`-Id 32144`，应用随 node 退出；再以 `OMCPA_BROWSER_BINARY=tmp/omc-lending-r2.exe` 重启，并各 `--reseed` 一次把「当前时间 + 偏移」重新锚定（2026-11-27 02:10 与 02:11，+08）。
- 环境变量与端口沿用 r1：hero 18641/18642/18643（数据 `tmp/lending-experience-data`，状态 `tmp/lending-experience-env.json`，node PID 39584、app PID 35760）；Command Code 18651/18652/18653（`OMC_LENDING_FIXTURE=revCommandCode`，数据 `tmp/lending-scene-cmdcode-v2`，状态 `tmp/lending-experience-env-18651.json`，node PID 10564、app PID 3248）。二进制运行前设 `TZ=Asia/Shanghai`，所有 `spawn` 带 `windowsHide: true`。
- 体验环境自检 `node scripts/acceptance/lending-experience-env.mjs --check`：**21/21**（`tmp/check-r2.log`）。重启后的数字：A 剩 88%、12/01 09:34 回满（4 天 7 小时后）；B 剩 40%、11/28 20:39 回满（2 天 2 小时后）；池标题「1 张要改」，一句话「把 codex-demo-b 设成 -10；codex-demo-a 不用动」，B 行「去设置」不带数字。

## 3. 门禁与探针

| 检查 | 结果 | 证据 |
| --- | --- | --- |
| 仓库探针 `quota-lending` | **127 PASS / 0 FAIL**（r1 为 118，前端新增 9 条字号与对比度断言） | `tmp/probe-lending-r2.log` |
| 仓库探针 `oauth-management`（一期回归） | **63 PASS / 0 FAIL** | `tmp/probe-oauth-r2.log` |
| 纯逻辑 256 条 | **256/256** | `tmp/logic-r2.log`、`tmp/logic-result.json` |
| Go 契约（`internal/quota` 整包） | `ok`（docker `golang:1.27.1`，不设 TZ） | `tmp/go-quota-r2.log` |
| 聚焦探针（S7 八项 + P2-2～P2-4 + 一轮回归） | **51 PASS / 0 FAIL** | `tmp/probe-r2-known.log` |
| 缺口探针（N-77 d／e／f、N-20 第二组、N-72） | **13 PASS / 0 FAIL** | `tmp/probe-r2-gaps.log` |
| N-77 ①②③（无字段／null／502） | **16 PASS / 0 FAIL** | `tmp/probe-r2-n77.log` |
| r2 用例探针（N-30、N-40、N-51、N-88、N-96、N-97） | 31 PASS / **6 FAIL**，6 条落在 3 个根因上 | `tmp/probe-r2-cases.log` |
| 真机保存链路（N-45、N-59） | 全 PASS：抽屉初值 0、保存一次 200、回读变「不用改」、已还原 priority=0 | `tmp/e2e-save-r2.log` |
| 接口形状与禁止键 | 200；21 个允许字段；无 token/secret/key/email/cookie/password | `tmp/api-lending-r2.json` |
| 安全字段读数 | 两张卡的 lending 数字与 safe-fields 一致（`allEqual: true`） | `tmp/n82-r2.json` |
| 时区 | Asia/Shanghai 09:34 与 UTC 01:34 同一串、相对时间不变，切回恢复 200 | `tmp/lending-proof/timezone-r2.json` |
| 字号与对比度 | 见第 5 节 | `tmp/lending-proof/measure-r2.json` |

- Go 整包在 `TZ=Asia/Shanghai` 下有一条与本轮无关的红：`antigravity_test.go` 期望标签是 UTC 的 `13:00`，本地时区渲染成 `21:00`；不设 TZ 即全绿。本轮只把 Go 当纯逻辑证据，不把它算进验收结论。

## 4. 逐条结果

### 4.1 r1 的已知待复测与 P2-2～P2-4

| 用例 | 结果 | 证据 |
| --- | --- | --- |
| N-18（原 P1-1／P1-2 同屏读数） | 通过 | 两张卡 0 / −10；一句话点名 B 设成 −10、A 不用动；「去设置」不带数字（`tmp/probe-r2-known.log`） |
| N-25（S7 P2-5 页头顺序） | 通过 | 标签与标题同一 `terminal-page-heading`，标题 top 88、标签 top 91、副标题 124，标签在标题右侧（同上） |
| N-28（S7 P1-1／P1-2 字号） | 通过 | 主屏无 12px 及以下；卡名 16、一句话 14、剩余 14、回满 14、现在 16、建议 22；次要说明与标签 13；建议数字大于卡名（同上） |
| N-70（S7 P2-1 详情块） | 通过 | 详情块不含「只提示，不改路由」整句，六块内容、完整名称、「在额度拆借页查看整组」都在（同上） |
| N-75（S7 P1-3 对比度） | 通过 | 正文 7.09:1～8.02:1；浅色最低 6.33:1、深色 6.74:1；数字与像素抽样见第 5 节 |
| P2-2 确认 | 通过 | 一句话写「credential-keep-1、credential-keep-2 不用动」，不写成计数 |
| P2-3 确认 | 通过 | 同档两行都写「第 1，并列」 |
| P2-4 确认 | 通过 | 看依据写「低 10、20……」与「见下面的名单」，没有「排到所有没设数字的卡之后」 |

### 4.2 N-77 整条

| 子项 | 结果 | 证据 |
| --- | --- | --- |
| ① 响应无 `lending` | 通过 | 拆借页「暂时读不到拆借建议」，与「暂无可拆借的同池凭证」分开；保留只读边界；无未捕获错误（`tmp/probe-r2-n77.log`） |
| ② `lending: null` | 通过 | 同 ① |
| ③ 额度请求 502 | 通过 | 拆借页回到一期失败态并给「重试」；OAuth 页无入口；详情无拆借块（同上） |
| ④ 卡条目无 `display_name` | 通过 | 整池照常渲染，卡名回退文件名 `codex-demo-a`／`codex-demo-b`，不抛错（`tmp/probe-r2-gaps.log`） |
| ⑤ 卡条目无「现在的优先级」 | 通过 | 现在写「—」，行尾「现在的数字读不到」，不判要改／不用改 |
| ⑥ 无池外名单字段 | 通过 | 不显示池外提醒，池与一句话照常 |
| 回归 | 通过 | ①②③ 全程 16 条断言全过；`quota-lending` 127 条、`oauth-management` 63 条同时全绿 |

### 4.3 用例终稿新增与改动

| 用例 | 结果 | 证据 |
| --- | --- | --- |
| N-31 十种原因 | 通过 | 收起行 10 个原因各计 1；展开 10 行，各有短标签与补充、都不给建议数字；H1 建议 0、H2 −10（`tmp/probe-r2-known.log`） |
| N-40 池外口径 | 通过（按 PRD） | 载荷 6 张时提醒写 6 张、名单 6 行，含未进池卡与低于 10% 的卡；本池的不动卡不列；行内写「现在是 50（默认）／0（默认）」（`tmp/probe-r2-cases.log`）。行内「归属」显示 `codex`，与 PRD 5.7「属于哪个池或哪一家」一致；用例文字「归属写未进池」与 PRD 冲突，见第 6 节 |
| N-51 回满时刻不晚于当前时刻 | **不通过** | a、d 通过：B 建议 −10、有效到 B 的回满；刷新后 B 排第一、A −10。b 的收起行「暂无建议／2 张都不动／读数过期 1／额度类型不同 1」与无有效期都对，**但过期卡的补充句写成「最后读数在 1 小时前，先刷新额度」**，与用例「回满时刻不晚于当前时刻」不符；1 分钟的读数被写成 1 小时（`tmp/lending-proof/r2-n51-expired.png`） |
| N-88 同池判定与未进池汇总 | 通过 | 只有 Pro 池两张；未进池 3 张（套餐未知 1、同套餐不足两张 2）；展开后三张卡名可达；没有 plus／team 池（`tmp/probe-r2-cases.log`） |
| N-96 过期卡不动、榜重算 | **不通过** | 三档时 2 张要改、Z −20；到点后 Z 离开卡行、1 张要改、一句话「把 Y 设成 −10；X 不用动」、另有 1 张不动、不动行「读数过期 1」都对；**Z 的不动行没有「现在 −10」**（同上） |
| N-97 本池卡不列池外 | 通过 | 提醒 2 张，名单只有 team-1／team-2；A、B 只在卡行，C、D 只在不动行（同上） |

### 4.4 r1 通过、与前端改动相邻的抽查

| 用例 | 结果 | 证据 |
| --- | --- | --- |
| N-30 不动行内容 | **不通过** | 收起「不动 2 张／读数过期 1／余量低于 10% 1」与「不动」标签都对；**展开的两行没有「现在 99」「现在 −5」**，C 的补充句是「留作应急，不参与排名」、没有用例要求的窗口与剩余（`tmp/lending-proof/r2-n30-hold-row.png`）。r1 判通过时用的是仓库探针，那组断言只查计数与标签，没有查用例里的「只写现在」 |
| N-45／N-59 保存链路 | 通过 | 抽屉初值 0 不是建议值；保存一次 200；回读「不用改」、池标题与一句话翻转；已把 priority 还原 0（`tmp/e2e-save-r2.log`） |
| 页头 | 通过 | 见 N-25 |
| 详情块 | 通过 | 见 N-70 |
| 字号 | 通过 | 见 N-28 |

## 5. 字号与对比度

独立测量脚本 `tmp/measure-r2.mjs`：按可见叶子文本节点读计算字号与前景／背景对比度（支持 `color(srgb …)` 与半透明叠层），另对关键行按渲染像素做一次抽样（元素截图取众数色为背景、0.5% 分位为文字色），浅色／深色 × 1440／390 各一遍。数字在 `tmp/lending-proof/measure-r2.json`，四张截图 `r2-{light,dark}-{1440,390}.png`。

| 位置 | 浅色 1440 | 浅色 390 | 深色 1440 | 深色 390 |
| --- | --- | --- | --- | --- |
| 页面标题 | 22px | 18px | 22px | 18px |
| 副标题（读数时刻） | 13px / 8.02:1 | 13px / 8.02:1 | 13px / 7.30:1 | 13px / 7.30:1 |
| 池标题 | 14px | 14px | 14px | 14px |
| 一句话 | 14px | 14px | 14px | 14px |
| 卡名 | 16px | 16px | 16px | 16px |
| 剩余／回满时间 | 14px / 7.20:1 | 14px / 7.19:1 | 14px / 7.06:1 | 14px / 7.06:1 |
| 现在 | 16px | 16px | 16px | 16px |
| 建议数字 | 22px / 7.09:1 | 22px / 7.09:1 | 22px / 6.74:1 | 22px / 6.74:1 |
| 次要说明（剩余／现在／建议设成） | 13px / 8.02:1 | 13px / 8.02:1 | 13px / 7.30:1 | 13px / 7.30:1 |
| 「默认」 | 13px / 5.68:1 | 13px / 7.58:1 | 13px / 6.40:1 | 13px / 7.21:1 |
| 标签（只提示／周限额） | 13px / 10.15:1 | 13px / 10.15:1 | 13px / 9.49:1 | 13px / 9.49:1 |
| 「1 张要改」「要改」 | 13px / 6.33:1 | 13px / 6.33:1 | 13px / 8.61:1 | 13px / 8.61:1 |
| 「去设置」 | 13px / 7.09:1 | 13px / 7.09:1 | 13px / 6.74:1 | 13px / 6.74:1 |

- **最小字号 13px**，与前端自报一致；主屏没有 12px 及以下的可见文字（含 390 宽与深色，展开不动行后也复查过）。
- **对比度**：本轮可见文本（29 个叶子节点，排除 `aria-hidden` 装饰符）浅色最低 **6.33:1**（「1 张要改」「要改」的 `color-mix` 前景）、深色最低 **6.74:1**（「去设置」与建议数字），正文 7.09～8.02:1；前端自报「浅色最低 5.02、深色 6.74」在深色上与本轮完全一致，浅色上本轮的集合里没有低于 6.33 的节点（他们的 224 个节点含其它探针状态）。
- 像素抽样与计算值吻合：副标题 8.02／7.30、次要说明 8.02／7.30、回满时间 7.20／7.06、标签 10.15／9.49，「默认」浅色抽样 5.68（像素分位受抗锯齿影响，计算值 8.02）。
- 390 宽与 1440 宽的同一元素字号一致（仅页面标题按既有规则降到 18px），没有横向滚动。

## 6. 问题清单

### 6.1 范围内要修（3 项，都在不动行）

| 编号 | 严重度 | 用例 | 现象与证据 |
| --- | --- | --- | --- |
| R2-1 | P1 | N-30、N-96 | 展开的不动行没有「现在」：C 行只有「codex-demo-c／余量低于 10%／不动／留作应急，不参与排名」，D 行只有「读数过期／不动／最后读数在 26 小时前」；用例要求「只写现在（99、−5）」，N-96 要求 Z「现在显示 −10」。PRD 5.6「这些卡不给建议数字，现在是几还是几」支持显示这个数字。位置：`LendingAdviceView.tsx` 的 `HoldRows` 只渲染卡名、原因标签、「不动」、补充句（截图 `tmp/lending-proof/r2-n30-hold-row.png`） |
| R2-2 | P2 | N-30 | 余量低于 10% 的补充句是「留作应急，不参与排名」，没有用例与 PRD 5.6 例子里的「每周额度剩 6%」。同一行已经能读到原因标签，缺的是把窗口与剩余写进补充句 |
| R2-3 | P1 | N-51 | 回满时刻不晚于当前时刻的卡，补充句写成「最后读数在 1 小时前，先刷新额度」：①没有写用例要求的「回满时刻不晚于当前时刻」；②`HoldRows` 的 `Math.max(1, Math.round(age / 3.6e6))` 把 1 分钟前的读数写成「1 小时前」，读数本身是准的。截图 `tmp/lending-proof/r2-n51-expired.png`（池收起行「暂无建议／2 张都不动／读数过期 1／额度类型不同 1」都是对的） |

三项都是前端展示层，后端判定（哪张卡不动、原因、榜重算）本轮全部符合：Go `TestLendingPastRefillIsStale`、`TestLendingOutsideCards`、`TestLendingHoldCardListsEveryReason`、`TestLendingNoAdviceWhenOnlyOneParticipant`、`TestLendingTierAnchorsOnTheLatestRefill` 全过，整包 `internal/quota` 也绿。

### 6.2 用例问题（产品不改）

| 编号 | 用例 | 现象 |
| --- | --- | --- |
| R2-4 | N-40 | 用例写「O6（未进池的卡也列，归属写「未进池」）」，PRD 5.7 写「点开列出卡名、属于哪个池或哪一家、现在的优先级」。实现按 PRD 走：未进池卡的归属显示 provider（实测 `codex-demo-o6` 行写 `codex`）。建议把用例改成「归属写它所属的池或 provider」 |
| R2-5 | N-40 | 夹具 `rev-outside-full` 只带 5 张池外卡（team-1、team-2、compat-1、O3、O6），缺用例里的 O7（Team 300、剩 6%）。本轮在探针载荷里补了一张 O7 才凑足 6 张并验到「低于 10% 照列」的渲染；包含／排除规则由后端 `TestLendingOutsideCards` 覆盖（未进池、低于 10% 计入；停用、冷却、不可用、需重新授权、读取失败、耗尽、模型不重叠、本池卡不计）。建议补进夹具，免得下一轮又要在探针里手工加 |

### 6.3 范围外观察

- `→` 箭头是 `aria-hidden="true"` 的装饰符，浅色 2.87:1、深色 2.42:1（S7 P1-3 里的「2.87:1」就是它，r1 记的也是同一个数）。它不在 N-75 的正文清单里、也不承载文字信息，本轮不计入不通过；若要严格按 3:1 的非文本对比度处理，可以顺手加深一档。
- 深色标签的前景色是 `color(srgb …)`（`color-mix` 的结果）。本轮 Chromium 渲染正常；更老的 WebView 不支持 `color-mix` 时会回退到别的颜色，属既有实现方式，不在本轮范围。

## 7. 未测与范围

- 本轮执行 **15 条**：12 通过、3 不通过、0 阻塞、0 未测（r2 范围内）。
- 整包 97 条中其余 **82 条本轮没有重跑**，沿用 r1：其中 81 条 r1 通过，1 条（N-77）r1 不通过、本轮整条重测后转通过。需要全量重跑时再排一轮。
- 本轮没有做：`pnpm verify`／`pnpm check:ui`（5181 与他人共用，全程没占用）、真实账号与真实网关的写操作、生产数据读取。Go 整包在 `TZ=Asia/Shanghai` 下的那一条既有红见第 3 节，是时区把 UTC 标签渲染成本地时刻，与本轮改动无关。

## 8. 环境与清理

- **hero（浩文两张卡）**：`http://127.0.0.1:18641/omc/quota-lending`，OAuth 入口 `http://127.0.0.1:18641/omc/oauth-management?page_size=48`，管理口令 `omc-e2e-management-key`；假 CPA 18642、控制口 18643；数据 `tmp/lending-experience-data`。关闭：`Stop-Process -Id 39584`，应用（PID 35760）随 node 退出。
- **Command Code 场景**：`http://127.0.0.1:18651/omc/quota-lending`，入口 `.../oauth-management?page_size=48`，同一口令；假 CPA 18652、控制口 18653；数据 `tmp/lending-scene-cmdcode-v2`。关闭：`Stop-Process -Id 10564`（app PID 3248 随 node 退出）。
- 重新锚定时间：`node scripts/acceptance/lending-experience-env.mjs --reseed`（hero，控制口 18643）；Command Code 场景加 `OMC_LENDING_CONTROL_PORT=18653`。
- 本轮停掉的进程都是自己起的：旧体验环境 node `31804`、`32144`；调试时泄漏的两个 vite 子进程 `19712`（18660）、`36236`（18661）。没有动别人的进程，也没有对 5181 做任何事。
- 没有执行删除类命令，也没有被平台拦下。自建产物保留，清单如下（都给的是本机路径与大小）：

| 产物 | 大小 |
| --- | --- |
| `tmp/omc-lending-r2.exe` | 52.0 MB（SHA-256 `0648E02B…CF39B05`） |
| `tmp/lending-experience-data` | 4.8 MB（含 WAL） |
| `tmp/lending-scene-cmdcode-v2` | 4.8 MB（含 WAL） |
| `tmp/lending-proof`（r2 截图与 JSON，7 张 PNG＋`measure-r2.json` 等） | 3.3 MB |
| r2 脚本与日志（`tmp/probe-r2-cases.mjs`、`probe-r2-n77.mjs`、`measure-r2.mjs`、`*-r2.log`、`api-lending-r2.json`、`n82-r2.json`、`e2e-save-r2.log` 等 24 个文件） | 约 150 KB |
| r1 的 `tmp/omc-lending.exe` 与两个数据目录 | 保留未动 |

- 截图：`tmp/lending-proof/r2-light-1440.png`、`r2-dark-1440.png`、`r2-light-390.png`、`r2-dark-390.png`、`r2-n30-hold-row.png`、`r2-n51-expired.png`、`r2-hold-row.png`。

## 9. 最终 HEAD

- 分支：`Newoahil/11-26-t6-s8-codex`，分支头是本轮报告的最后一次提交（含本节）；此前两次报告提交为 `46bbf9f7`（骨架与合入记录）、`6c48f083`（逐条结果、字号对比度、问题清单）。
- **被测代码树**：`c8bd753b` —— 前端最终 `863b9425`（含后端 `b76faa82`、D1/D2、S7 八条修复、PRD v1.0.2、演示数据摘要）＋用例终稿 `9d744cda`（97 条）；PRD v1.0.3 `5ac64d26` 已在其中。报告提交只改 `docs/delivery/**/acceptance/report-r2.md`，不动业务代码。
- r1 报告与 r1 的提交保持原样；r1 的二进制 `tmp/omc-lending.exe` 与两个数据目录未动。






