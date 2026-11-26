# 额度拆借改版 · 验收报告 r3

- **结论**：本轮 **通过 8 / 不通过 0 / 阻塞 0 / 未测 0**（执行 r3 范围内 8 条：N-30、N-31、N-40、N-51、N-96、N-77、N-98、N-99）。另跑 51 条 r1／r2 回归断言、23 条组件测试、134＋63 条仓库探针、256 条纯逻辑与 21 项体验环境自检，全绿。用例字面与实现用词有 4 处差异，见 6.2，不改变 PRD 判定。
- **被测**：分支 `Newoahil/11-26-t6-s8-codex`，合入前端最终 `f314c9b3` 与用例终稿 `b9e89937` 后的树，另加本会话新增的 N-99 用例；r1、r2 报告保持原样。
- **标准**：`prd.md` v1.0.3（AC 编号，唯一验收标准）；用例 `cases.md` 99 条（N-1～N-99，N-99 为 r3 新增）；契约 `contract.md`。
- **证据**：临时脚本、日志与截图在 `tmp/`（git 忽略，清单见第 8 节）；截图只留本机、不进 git。

## 1. 合入与被测

| 顺序 | 合并提交 | 来源 | 带入内容 |
|---|---|---|---|
| 1 | `cfea511f` | `Newoahil/11-26-t6-s4-codex` @ `f314c9b3` | 前端最终：不动行补充句按 PRD 5.6 写回满已到、分钟、额度类型与剩余，抽屉复用同一句，旧函数删除。10 个文件 +260／−34 |
| 2 | `478c53a0` | `Newoahil/11-26-t6-s5-claude` @ `b9e89937` | 用例终稿：`cases.md` +64／−23，97 → 98 条，新增 N-98，改动 N-30、N-31、N-40、N-96 等 |
| 3 | `1362f618` | 本会话 | 新增 N-99（抽屉不动块与页面同句），只改 `cases.md` 一处；统计行同步为 99 条 |

- PRD 仍是 r2 合入的 `5ac64d26`（v1.0.3），本轮无新提交，未重复合并。
- 两次合入都**没有冲突**：前端分支的 `lending-fixtures.mjs` 自动合入，我的 O7 夹具仍在（`auth-outside-o7`、假 CPA 的 `codex-demo-o7.json`）；`cases.md` 取用例分支版本，13.6 整节自动保留（现行第 1209 行），不需要手工重加。
- 合入后标题数 98，加 N-99 后 99；无冲突标记，13.6 的 G1～G6 与 v1.0.3 池外口径都在。合入只在前端那次带业务代码，用例分支只改 `cases.md`。

## 2. 环境与构建

- `pnpm build` 通过（58.17s），`web/dist` 已同步到 `internal/web/dist`。
- 交叉编译：`docker run … -e GOOS=windows -e GOARCH=amd64 -e CGO_ENABLED=0 -e TZ=Asia/Shanghai golang:1.27.1 go build -trimpath -ldflags "-s -w"`，产物 `tmp/omc-lending-r3.exe`，54,537,216 B，SHA-256 `13DC3675BCFBBD2B374CCA6F23608BF2EDF6F9183D0195093AB00822F8E4503A`。再带 TZ 编一次到 `tmp/omc-lending-r3-tz.exe`，字节数与哈希完全相同，两个体验环境跑的就是这份二进制。
- 两个体验环境先停自己起的旧 node（39584、10564），随附的 app（35760、3248）一起退出；没有动别人的进程。新环境各 `--reseed` 一次：

| 场景 | 入口 | 端口（app／CPA／控制） | 数据 | node PID | app PID |
|---|---|---|---|---|---|
| hero | `http://127.0.0.1:18641/omc/quota-lending` | 18641／18642／18643 | `tmp/lending-experience-data` | 45464 | 4448 |
| Command Code | `http://127.0.0.1:18651/omc/quota-lending` | 18651／18652／18653 | `tmp/lending-scene-cmdcode-v2` | 19528 | 40116 |

- 管理密码 `omc-e2e-management-key`；OAuth 页 `…/omc/oauth-management?page_size=48`。启动时 `windowsHide` 由脚本的 `spawn` 固定。
- 环境自检 `node scripts/acceptance/lending-experience-env.mjs --check`：**21/21**（`tmp/check-r3.log`）。

## 3. 门禁与探针

| 门禁 | 结果 | 证据 |
|---|---|---|
| 体验环境自检 | **21/21** | `tmp/check-r3.log` |
| 纯逻辑 256 条 | **256/256** | `tmp/logic-r3.log` |
| 仓库探针 quota-lending | **134 PASS / 0 FAIL**（含前端新增的补充句断言） | `tmp/probe-lending-r3.log` |
| 仓库探针 oauth-management | **63 PASS / 0 FAIL** | `tmp/probe-oauth-r3.log` |
| r3 逐条探针（N-30、N-31、N-40、N-51、N-96、N-98、N-99） | **52 PASS / 0 FAIL** | `tmp/probe-r3-cases.log` |
| 组件测试 `lending-advice.component.test.tsx` | **23/23**（含 N-98 a～d 与抽屉三句） | `tmp/component-lending-r3.log` |
| N-77 整条（① 无字段／② null／③ 502） | **16 PASS / 0 FAIL** | `tmp/probe-r3-n77.log` |
| N-77 d／e／f 补充探针 | **13 PASS / 0 FAIL** | `tmp/probe-r3-gaps.log` |
| r1／r2 回归断言（S7 八项、P2-2～P2-5、N-37／N-46／N-67／N-89 等） | **51 PASS / 0 FAIL** | `tmp/probe-r3-known.log` |
| 真机保存链路（N-45、N-59） | **9 PASS**，保存后已还原 priority=0 | `tmp/e2e-save-r3.log` |

- 没有用 `pnpm check:ui`，5181 端口未碰。字号与对比度本轮不重量（只改文案，PM 已明确）。

## 4. 逐条结果

### 4.1 N-30、N-31、N-51、N-96、N-98

| 用例 | 结果 | 实测 |
|---|---|---|
| N-30 不动折叠行 | **通过** | 收起行「不动 2 张／读数过期 1／余量低于 10% 1」；展开 C「codex-demo-c／余量低于 10%／不动／周限额剩 6%，留作应急，不参与排名」，D「codex-demo-d／读数过期／不动／最后读数在 26 小时前，先刷新额度」；两行都没有「建议设成」。截图 `tmp/lending-proof/r3-n30-hold-row.png` |
| N-31 十种原因 | **通过** | 收起行「不动 10 张」，10 个原因标签各计 1；H1 建议 0、H2 −10；R1～R10 都没有建议数字；R5 写 26 小时分支，R9 写周限额与 6%。截图 `r3-n31-reasons.png` |
| N-51 回满时刻到点 | **通过** | a：A 0、B −10；b（R+10 小时整）：池「暂无建议／2 张都不动／读数过期 1／额度类型不同 1」，不写建议有效期；B 写「回满时刻已到（不晚于当前时刻），读数已不准；先刷新额度」，不写「1 小时前」，没有建议；d：新读数后 B 第一、A 第二。截图 `r3-n51-expired.png` |
| N-96 到点后重算 | **通过** | a：三档、2 张要改、Z −20；b：Z 离开卡行，标题「1 张要改」，一句话「把 codex-demo-y 设成 -10；codex-demo-x 不用动」＋「另有 1 张不动」；Z 在不动行写回满已到句，不写「1 小时前」，不出现「建议设成」。截图 `r3-n96-expired.png` |
| N-98 补充句二选一 | **通过** | 页面级独立喂数：a 回满在过去与恰好等于生成时刻都写「回满时刻已到…」；b 26 小时写「最后读数在 26 小时前，先刷新额度」；c 30 分钟写「…30 分钟前…」；d 回满已到且读数 1 分钟走 a 的写法，不写「1 小时前」。仓库组件套件 23/23 覆盖同一张表 |

- 这 5 条的判定按 PRD 5.6、AC-8、AC-24：分支、数字、是否给建议、是否写「1 小时前」都对。中文标点与窗口名和用例字面不完全一致（「，」对「；」、「周限额」对「每周额度」），见 6.2。

### 4.2 N-40 池外 6 张

- **通过（PRD 5.7、AC-25）**：提醒行写「这个模型还有 6 张池外的卡也在供货」，名单 6 行：`codex-demo-team-1`、`codex-demo-team-2`、`compat-demo-1`、`compat-demo-o3`、`codex-demo-o6`、`codex-demo-o7`；每张只出现一次。
- 计入：低于 10% 的 O7 照列；未进池的 O6 也列。不计：停用的 O5、耗尽的 O8、冷却中的 O9、模型不重叠的 O4，以及本池不动的 C。
- 归属：Team 池三行（team-1、team-2、O7）显示 `Team 300`，`compat-demo-1` 与 O3 显示 `openai-compat-demo`，O6 显示 `codex`。PRD 5.7 写「属于哪个池或哪一家」，当前实现满足；用例字面写「Codex · Team 300」，见 6.2。
- 夹具差异：用例第 13 节把 O7 叫 `codex-demo-team-3`、现在未设；我的夹具叫 `codex-demo-o7`、现在 0（默认）。纳入／排除规则验到了，名字与当前值按实际夹具记录，见 6.2。

### 4.3 N-99 抽屉与页面同句

- **通过**。同一张读数过期的不动卡，页面展开行与 OAuth 凭证详情抽屉写同一句；三组都逐字比对：

| 场景 | 页面展开行 | 详情抽屉 | 结果 |
|---|---|---|---|
| C：余量低于 10% | 周限额剩 6%，留作应急，不参与排名 | 同左 | 逐字相同 |
| D：读数 26 小时前 | 最后读数在 26 小时前，先刷新额度 | 同左 | 逐字相同 |
| 回满已到、读数 1 分钟 | 回满时刻已到（不晚于当前时刻），读数已不准；先刷新额度 | 同左 | 逐字相同，且两处都不写「1 小时前」 |

- 页面与抽屉都调 `lendingHoldRowNote`，取数上下文分别是概览的 `generated_at_ms` 与详情的 `generated_at_ms`；这次没有出现两处写法分叉。

### 4.4 N-77 与 r1／r2 回归抽查

- **N-77 整条通过**：① 卡缺 `display_name`、② 该字段为 null、③ 配额接口 502，三种载荷各 4～6 条断言，共 **16 PASS / 0 FAIL**；OAuth 页不崩、入口不出现、详情抽屉不画拆借块、没有未捕获页面错误。
- N-77 d／e／f 的补充探针 **13 PASS / 0 FAIL**（`tmp/probe-r3-gaps.log`）。
- r1／r2 的 51 条回归断言全绿：S7 的 P1-1～P2-5（字号与对比度不在本轮重量范围，断言仍按 DOM 与文案）、N-37 无不动行、N-46 看依据、N-67 抽屉里的不动卡、N-89 单卡重置建议与拆借分离、池外与并列等。
- 真机保存链路 N-45／N-59：抽屉初值 0、保存一次 200、回读「不用改」、池标题与一句话翻转，最后已还原 priority=0（`tmp/e2e-save-r3.log`）。

## 5. N-99 的证据

- 探针用 `rev-hidden` 的 C、D 两张真不动卡，加一张自造的「回满已到、读数 1 分钟前」卡；先读额度拆借页展开行，再打开 OAuth 凭证详情抽屉读同一张卡的不动块。页面与抽屉的句子各自取正则匹配结果后做严格相等比较，不用「包含」糊过去。
- 三组证据：低剩余（C）、26 小时（D）、回满已到且 1 分钟（自造）。截图 `tmp/lending-proof/r3-n99-drawer-c.png`、`r3-n99-drawer-expired.png`；日志 `tmp/probe-r3-cases.log` 的 N-99 段。
- 组件层同一张表另有 3 条：回满已到、1 分钟读数、低剩余，均在 `web/tests/lending-advice.component.test.tsx`，本次 23/23 通过。页面与抽屉共用 `lendingHoldRowNote`，本轮删掉了抽屉里的旧写法。

## 6. 问题清单

### 6.1 范围内要修

- **无**。r2 不通过的 N-30、N-51、N-96 本轮转绿；N-31、N-40、N-98、N-99 通过；R2-1（不动行写「现在」）按 PM 决定不修，用例已不再要求。

### 6.2 用例与夹具的字面差异

- **标点**：小时／分钟分支，用例写「最后读数在 26 小时前；先刷新额度」（分号），实现与 i18n 写「，先刷新额度」（逗号）。影响 N-30 的 D、N-31 的 R5、N-98 b／c、N-99 的 D。PRD 5.6 只规定句子内容，未规定这个标点；英文文案用分号。建议用例按现有中文文案改成逗号，或产品统一改文案，二选一。
- **窗口名**：用例写「每周额度剩 6%」，实现复用一期词「周限额」（`quota.window_weekly`），实际是「周限额剩 6%，留作应急，不参与排名」。影响 N-30 的 C、N-31 的 R9、N-99 的 C。PRD 5.6 的例子写「每周额度」，AC-24 不锁字符串；语义与「带额度类型与剩余」的要求都满足。建议用例改用「周限额」，或在 PRD／一期词表里统一。
- **池外归属**：用例 N-40 要求 Team 池三张写「Codex · Team 300」；实现按 PRD 5.7「属于哪个池或哪一家」只写池名 `Team 300`，未进池的 O6 写 provider `codex`。r2 已按 PRD 判过 `codex` 这一半；本轮同一口径。建议用例接受「池名或 provider」，不要要求两者拼接。
- **夹具 O7 的名字与当前值**：用例第 13 节叫 `codex-demo-team-3`、现在未设；我的夹具叫 `codex-demo-o7`、现在 0（默认），O7 的池、剩余 6%、回满 +3 天与用例一致。建议下一轮把第 13 节的表改成夹具实际名字，或把夹具改名并对齐 `priority_known=false`。本轮按实际夹具记录，不据此判失败。
- N-99 已写进 `cases.md` 并标明 r3 新增；15.3 的统计同步为 99 条（P1 44、浏览器 70、可自动化 95），只改了 `cases.md` 一处。

### 6.3 范围外观察

- **无新增**。本轮没有发现与不动行、抽屉、池外名单无关的新缺陷。深色主题、390 宽、字号与对比度按要求不重量；检查过程中未碰 `pnpm check:ui` 与 5181。

## 7. 未测与范围

- r3 范围执行 **8 条**：N-30、N-31、N-40、N-51、N-96、N-77、N-98、N-99；范围内未测 0。
- 整包 99 条的其余 91 条不在 r3 范围，沿用 r1（90 通过）与 r2（12 通过／3 不通过，3 条本轮已转绿）的结果；本轮代码只改不动行补充句与抽屉复用，另用 51 条 r1／r2 回归断言、23 条组件测试压住相邻行为。
- 没有做的事：没有重跑全量浏览器目录、没有跑 `pnpm check:ui`、没有重量字号与对比度、没有改业务代码、没有推送远端。

## 8. 环境与清理

- 入口：hero `http://127.0.0.1:18641/omc/quota-lending`，Command Code `http://127.0.0.1:18651/omc/quota-lending`；密码 `omc-e2e-management-key`。
- 关闭：`Stop-Process -Id 45464`（hero）、`Stop-Process -Id 19528`（Command Code）。两个 node 退出时随附的 app（4448、40116）一起退出，不要单独杀 app。两个环境保持运行，等下一步通知。
- 自建产物（都在本机、git 忽略，未用删除命令）：
  - 二进制：`tmp/omc-lending-r3.exe` 54,537,216 B；`tmp/omc-lending-r3-tz.exe` 同字节同哈希（第二次带 TZ 构建，保留备查）。
  - 数据：`tmp/lending-experience-data`（4.8 MB）、`tmp/lending-scene-cmdcode-v2`（4.8 MB）；状态 `tmp/lending-experience-env.json`、`tmp/lending-experience-env-18651.json`。
  - 截图：`tmp/lending-proof/r3-n30-hold-row.png`（55 KB）、`r3-n31-reasons.png`（103 KB）、`r3-n51-expired.png`（24 KB）、`r3-n96-expired.png`（43 KB）、`r3-n99-drawer-c.png`（174 KB）、`r3-n99-drawer-expired.png`（160 KB）；目录共 45 个文件、3.9 MB。
  - 日志：`tmp/check-r3.log`、`logic-r3.log`、`probe-lending-r3.log`（15.8 KB）、`probe-oauth-r3.log`（5.7 KB）、`probe-r3-cases.log`（7.7 KB）、`component-lending-r3.log`、`probe-r3-n77.log`、`probe-r3-gaps.log`、`probe-r3-known.log`（20.1 KB）、`e2e-save-r3.log`。
  - 脚本：`tmp/probe-r3-cases.mjs`（19.9 KB）、`tmp/patch-r3-n99.txt`、`tmp/patch-r3-report-*.txt`。

## 9. 最终 HEAD

- **被测树（最终集成树）**：`1362f618`。其中前端最终为 `f314c9b3`（经 `cfea511f` 合入），用例终稿为 `b9e89937`（经 `478c53a0` 合入），`1362f618` 只加 N-99；报告不改变被测树。
- **本分支头**：本节所在提交，父提交 `ea900e7c`；它和前三个报告提交只改 `docs/delivery/2026-11-25-omc-quota-lending-revamp/acceptance/report-r3.md`。验收判定以被测树 `1362f618` 为准。
