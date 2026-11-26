# 额度拆借改版 · 验收报告 r4

- **结论**：本轮 **通过 6 / 不通过 0 / 阻塞 0 / 未测 0**（r4 范围：N-100、N-98、N-99、N-30、N-31、N-77）；N-40、N-51、N-96 随同一探针复跑也通过。仓库探针 134＋63、纯逻辑 256、组件 24、环境自检 21/21 全绿。范围内要修 0。
- **被测**：分支 `Newoahil/11-26-t6-s8-codex`，合入前端最终 `4853671c` 与 PRD v1.0.4 `eeeb3720` 后的树；r1～r3 报告保持原样。
- **标准**：`prd.md` v1.0.4（5.6 第 5 行四种写法）；用例 `cases.md` 100 条（N-1～N-100，N-100 为 r4 新增）。
- **证据**：临时脚本、日志与截图在 `tmp/`（git 忽略，清单见第 8 节）；截图只留本机、不进 git。

## 1. 合入与被测

| 顺序 | 合并提交 | 来源 | 带入内容 |
|---|---|---|---|
| 1 | `b32edc4b` | `Newoahil/11-26-t6-s4-codex` @ `4853671c` | 读数过期、回满未到、卡上没有 `observed_at_ms` 时改走 `reading_stale_unknown`，页面与抽屉都不再露出 `{n}`；7 个文件 +36／−1 |
| 2 | `63a16713` | `Newoahil/11-25-t6-s1-claude` @ `eeeb3720` | PRD v1.0.4：只改 `prd.md` 5.6 第 5 行为四种写法与判定顺序，+3／−2 |
| 3 | `580a0cb6` | 本会话 | `cases.md` 对齐 r3 的四处字面差异并新增 N-100，统计同步 100 条 |

- 两次合入都**没有冲突**；前端这次只动 i18n、`lendingAdvice.ts`、组件测试与说明，没有碰 `scripts/acceptance/` 的夹具或探针。PRD 只动 `prd.md`，没有业务代码。
- 用例仍取用例分支版本，13.6 整节保留；标题数 100，无冲突标记。N-100 标明 r4 新增，并写明这是桩层验证（真机采集不会产出读数时刻缺失的卡）。

## 2. 环境与构建

- `pnpm build` 通过（1m1s），`web/dist` 已同步到 `internal/web/dist`。
- 交叉编译带 `TZ=Asia/Shanghai`：`tmp/omc-lending-r4.exe`，54,537,216 B，SHA-256 `95545DB596F1A90CB60485069E52A1BEF3346D4BCFD6331FCC157A911A10C65C`。
- 先停自己起的旧 node（45464、19528），随附的 app（4448、40116）一起退出；没有动别人的进程。新环境各 `--reseed` 一次：

| 场景 | 入口 | 端口（app／CPA／控制） | 数据 | node PID | app PID |
|---|---|---|---|---|---|
| hero | `http://127.0.0.1:18641/omc/quota-lending` | 18641／18642／18643 | `tmp/lending-experience-data` | 19824 | 19816 |
| Command Code | `http://127.0.0.1:18651/omc/quota-lending` | 18651／18652／18653 | `tmp/lending-scene-cmdcode-v2` | 24668 | 5440 |

- 管理密码 `omc-e2e-management-key`；OAuth 页 `…/omc/oauth-management?page_size=48`。启动时 `windowsHide` 由脚本的 `spawn` 固定。
- 环境自检在干净 shell 跑（不继承场景端口）：**21/21**（`tmp/check-r4.log`）。

## 3. 门禁与探针

| 门禁 | 结果 | 证据 |
|---|---|---|
| 体验环境自检 | **21/21** | `tmp/check-r4.log` |
| 纯逻辑 256 条 | **256/256** | `tmp/logic-r4.log` |
| 仓库探针 quota-lending | **134 PASS / 0 FAIL** | `tmp/probe-lending-r4.log` |
| 仓库探针 oauth-management | **63 PASS / 0 FAIL** | `tmp/probe-oauth-r4.log` |
| r4 逐条探针（N-30、N-31、N-40、N-51、N-96、N-98、N-99、N-100） | **57 PASS / 0 FAIL** | `tmp/probe-r4-cases.log` |
| 组件测试 `lending-advice.component.test.tsx` | **24/24**（含 N-100 页面与抽屉同句、无 `{`） | `tmp/component-lending-r4.log` |
| N-77 整条（① 无字段／② null／③ 502） | **16 PASS / 0 FAIL** | `tmp/probe-r4-n77.log` |
| N-77 d／e／f 补充探针 | **13 PASS / 0 FAIL** | `tmp/probe-r4-gaps.log` |

- 没有用 `pnpm check:ui`，5181 未碰；字号与对比度本轮不重量。

## 4. 逐条结果

### 4.1 N-100 读数时刻缺失

- **通过**。桩层喂一张读数过期、回满时刻未到（+1 天）、没有 `observed_at_ms` 的不动卡，页面展开行写「codex-demo-e／读数过期／不动／读数时间不明，先刷新额度」；凭证详情抽屉写「这张卡不动：读数过期／读数时间不明，先刷新额度」。两处句子严格相等，两处文字都不含 `{` 或 `}`。
- 5 条断言全过：页面句子、页面无占位符、抽屉句子、抽屉无占位符、两处严格相等。截图 `tmp/lending-proof/r4-n100-hold-row.png`、`r4-n100-drawer.png`；日志 `tmp/probe-r4-cases.log` 的 N-100 段。
- 这条只验展示兜底：真机采集不会产出读数时刻缺失的卡，用例里已写明是桩层验证。

### 4.2 N-98、N-99、N-30、N-31、N-40 回归

- **N-98 四种写法全过**：回满在过去、回满恰好等于生成时刻都写「回满时刻已到…」；26 小时写「最后读数在 26 小时前，先刷新额度」；30 分钟写「…30 分钟前…」；回满已到且读数 1 分钟走第一种写法，不写「1 小时前」。与 PRD v1.0.4 的判定顺序一致。
- **N-99 三组全过**：C 的低剩余句、D 的 26 小时句、回满已到的 1 分钟句，页面与抽屉各自正则提取后严格相等；抽屉没有把 1 分钟写成 1 小时。
- **N-30 通过**：收起行「不动 2 张／读数过期 1／余量低于 10% 1」；C「周限额剩 6%，留作应急，不参与排名」，D「最后读数在 26 小时前，先刷新额度」，都不出现「建议设成」。
- **N-31 通过**：10 个原因各计 1，H1 0、H2 −10，R1～R10 无建议；R5 26 小时、R9 周限额 6%。
- **N-40 通过**：6 张池外卡一行一张，含 `codex-demo-o7`；O6 写 provider `codex`，Team 三张写池名 `Team 300`；O4／O5／O8／O9 与本池 C 不列。N-38 的同一归属写法也按池名对齐。
- N-51、N-96 随同一探针复跑也通过：到点卡的补充句、榜重算、Z 不出现建议数字都保持 r3 的结果。

### 4.3 N-77 整条

- **通过**：① 卡缺 `display_name`、② 该字段为 null、③ 配额接口 502，共 **16 PASS / 0 FAIL**；OAuth 页不崩、入口不出现、详情抽屉不画拆借块、没有未捕获页面错误。
- N-77 d／e／f 补充探针 **13 PASS / 0 FAIL**。
- 探针进程在跑完后会留下 dev server 不退出；本轮停掉了自己遗留的两个（18654 的 26772、18656 的 22348），没有动别人的进程。

## 5. 用例的四处字面对齐

- 只改 `cases.md`，没有改实现与 PRD；提交 `580a0cb6`。
- **标点**：小时、分钟分支统一成「，先刷新额度」——N-30 的 D、N-31 的 R5、N-49、N-98 的 b／c 都改了；回满已到那一支保留「读数已不准；先刷新额度」的分号，因为实现与 PRD 都是分号。
- **窗口名**：N-30 的 C、N-31 的 R9 从「每周额度」改成「周限额」，与页面既有词 `quota.window_weekly` 一致。
- **池外归属**：N-38、N-40 与第 13 节的表改成「池名或 provider」，不再要求 `Codex · Team 300` 这种拼接；Team 三张写 `Team 300`，O6 写 `codex`。
- **O7 夹具**：第 13 节的名字从 `codex-demo-team-3` 改成实际夹具 `codex-demo-o7`，当前值从「未设」改成「0（默认）」；池、剩余 6%、回满 +3 天不变。
- N-100 已写进 `cases.md` 并标 r4 新增；统计改为 100 条（P1 45、浏览器 71、可自动化 96）。第 14 节 R2-2／R2-3 的历史记录保留原措辞，不回改当初的反馈。

## 6. 问题清单

### 6.1 范围内要修

- **无**。N-100 与全部回归通过；四处用例字面差异已在 `cases.md` 对齐，没有需要改实现或 PRD 的项。

### 6.2 用例与夹具

- r3 报告 6.2 的四处差异已全部落在 `cases.md`：标点、窗口名、池外归属、O7 夹具名字与当前值。没有遗留的用例与实现对不上。
- 第 14 节 R2-2／R2-3 仍保留 r2 当时的原话（「每周额度」、分号），这是反馈记录，不是断言；本轮没有回改历史。
- 真机采集产不出「读数时刻缺失」的卡，N-100 在用例里明确标了桩层验证；组件测试也覆盖了同一条，避免只靠临时探针。

### 6.3 范围外观察

- **无新增**。本轮只改缺失读数时刻的兜底句、用例字面与 PRD 读法；相邻页面、池外名单、抽屉与保存链路都没有新问题。字号、对比度与 `pnpm check:ui` 按要求未做。

## 7. 未测与范围

- r4 范围执行 **6 条**：N-100、N-98、N-99、N-30、N-31、N-77；未测 0。另 N-40、N-51、N-96 随同一探针复跑通过，不计入这 6 条。
- 整包 100 条的其余 91 条不在 r4 范围，沿用 r1～r3 的结果；本轮代码只改一个兜底分支，组件 24 条与 r4 探针 57 条压住了相邻行为。
- 没有做的事：没有重跑全量浏览器目录、没有跑 `check:ui`、没有重量字号与对比度、没有改业务代码、没有推送远端。

## 8. 环境与清理

- 入口：hero `http://127.0.0.1:18641/omc/quota-lending`，Command Code `http://127.0.0.1:18651/omc/quota-lending`；密码 `omc-e2e-management-key`。
- 关闭：`Stop-Process -Id 19824`（hero）、`Stop-Process -Id 24668`（Command Code）。两个 node 退出时随附的 app（19816、5440）一起退出，不要单独杀 app。两个环境保持运行。
- 自建产物（都在本机、git 忽略，未用删除命令）：
  - 二进制：`tmp/omc-lending-r4.exe` 54,537,216 B；r3 的 `tmp/omc-lending-r3.exe`、`tmp/omc-lending-r3-tz.exe` 仍保留。
  - 数据：`tmp/lending-experience-data`（4.8 MB）、`tmp/lending-scene-cmdcode-v2`（4.8 MB）；状态 `tmp/lending-experience-env.json`、`tmp/lending-experience-env-18651.json`。
  - 截图：`tmp/lending-proof/r4-n100-hold-row.png`（40 KB）、`r4-n100-drawer.png`（151 KB）；目录共 47 个文件、4.1 MB。
  - 日志：`tmp/check-r4.log`、`logic-r4.log`、`probe-lending-r4.log`（15.9 KB）、`probe-oauth-r4.log`（5.7 KB）、`probe-r4-cases.log`（8.6 KB）、`component-lending-r4.log`、`probe-r4-n77.log`、`probe-r4-gaps.log`。
  - 脚本：`tmp/probe-r4-cases.mjs`（22 KB）、`tmp/patch-r4-cases.txt`、`tmp/patch-r4-report-*.txt`。

## 9. 最终 HEAD

- **被测树（最终集成树）**：`580a0cb6`。其中前端最终为 `4853671c`（经 `b32edc4b` 合入），PRD v1.0.4 为 `eeeb3720`（经 `63a16713` 合入），`580a0cb6` 只改用例并加 N-100；报告不改变被测树。
- **本分支头**：本节所在提交，父提交 `ab7b54cd`；四个报告提交只改 `docs/delivery/2026-11-25-omc-quota-lending-revamp/acceptance/report-r4.md`。验收判定以被测树 `580a0cb6` 为准。
