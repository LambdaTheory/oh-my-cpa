---
type: report
slug: 2026-11-23-omc-capabilities-nav-report-r1
status: final
verdict: 通过
subject: Newoahil/11-24-t9-s5-codex @ 5d3e5b4e（整件需求的 PR 头，含 S4 按 S7 审查补修的 4 个提交）
baseline: 11-24-t9-dev-base（dev 90a8d8ff）
standard: prd.md（定稿，唯一基准）；cases.md（64 条，P0 40、P1 17、P2 7）
updated: 2026-11-24
summary: "左侧导航「能力」分组的集成测试报告（第 1 轮）：64 条用例全部通过（P0 40 条），范围内要修 0 条，门禁独立复跑全绿，体验环境已备好"
---

# 集成测试报告：左侧导航新增「能力」分组（r1）

测试对象：分支 `Newoahil/11-24-t9-s5-codex`，头提交 `5d3e5b4e`（本报告所在分支 `Newoahil/11-24-t9-s8-claude` 合并了它，产品代码与它逐字节相同，只多了审查报告、验收脚本、两个环境脚本里各一行改动和本报告）；基线 `11-24-t9-dev-base`（dev `90a8d8ff`）。
依据：同目录上一级的 `prd.md`（定稿）与 `cases.md`（只按用例执行，不改用例）。
派发标识：works 2026-11-23#T9-S8，派自主会话 1d0e5f19。

## 1. 结论

**通过。** 没有需要开发修的问题（范围内要修 0 条）。

- **被测对象**：PR 头 `Newoahil/11-24-t9-s5-codex`，含 S4 按 S7 审查补修的 4 个提交，头 `5d3e5b4e`。本分支在其上只多了两份审查报告、验收脚本、两个环境脚本里各一行 `windowsHide` 和本报告；`web`、`internal`、`cmd`、`migrations`、`deploy` 与依赖文件相对 PR 头的差异为空，测的就是 PR 头的产品代码。
- **用例**：64 条，通过 64、失败 0、阻塞 0、未测 0。P0 40 条全部通过，P1 17 条、P2 7 条也全部通过。每条用例的逐项结果见第 4 节。
- **AC**：AC-1～AC-29 除 PRD 标为不适用的 AC-26 外，28 条都有用例并全部通过（第 6 节）。
- **门禁**：独立复跑的门禁在头上全绿；凡红的都在基线同条件复现，归基线既有或环境性（第 3、7 节）。基线的 `verify:browser` 有 1 个红，头上转绿（3.2）；基线的 `check:bundle` 红（`lendingMount` 被首屏拉进来），S4 已修，头上通过。
- **没有新增的请求与写操作**：新页读的正是旧面板读的四样，没有多一条；全控制台的写操作 H 与 B 逐页相同（4.5）。
- **范围外的观察**共 8 项，都不要求本需求修，列在 5.2，供 PM 决定要不要另开事项。
- **缺口**写在 9.1：只测了 Edge 一款浏览器、手机宽度是模拟而不是真机、真实环境造不出非零用量与审计平台推送（这两项只在桩层对照）、没有在基线上跑 Go 门禁。
- **体验环境**保留到 2026-11-25 00:00 自动到期，卡片与关闭命令见第 10 节。
- **密钥扫描**：被跟踪的树扫描干净；证据目录里首次命中的 7 条是假环境造数生成的密钥形状字符串，已打码，不入库（3.1）。
- **本轮对工作区的改动**：除新增验收脚本与本报告外，只改了两个环境脚本里各一行 `windowsHide: true`（PM 临时授权，单独提交，见 9.3）。

## 2. 环境与方法

### 2.1 被测对象与基线

- 被测：分支 `Newoahil/11-24-t9-s5-codex`。派发时的头是 `6c83fbfc`；开工中 S4 按 S7 的审查补修，新头 `5d3e5b4e`（4 个提交，只改测试、文档与注释；`git diff 6c83fbfc..5d3e5b4e` 在 `web/src` 下只有两处注释）。本分支 `Newoahil/11-24-t9-s8-claude` 已并入 S6、S7 两份审查报告（合并 `f82b6ebd`、`d0e3656c`）和 S4 的新提交（合并 `ed48fe6b`），最终一轮测试用合并后的源码重新构建的二进制。
- 基线：`11-24-t9-dev-base`（dev `90a8d8ff`），在系统临时目录用 `git worktree add --detach` 取出，装依赖、构建，用完 `git worktree remove`。
- 两端二进制都是「`pnpm build` 把控制台同步进 `internal/web/dist`，再用容器 golang:1.27.1 交叉编译 `GOOS=windows`」，运行时 `TZ=Asia/Shanghai`。头 SHA-256 `19BD632A3DFC…ED95F58EA8E1C`，基线 `B40DE177F3A7…B776C2381`。第一轮探索用的是 `6c83fbfc` 源码构建的二进制，最终一轮全部脚本在合并后源码的二进制上整套重跑，结果以最终一轮为准。

### 2.2 环境

- 本机 Windows 11，Node 24.16.0（仓库钉 22.23.2，所有命令都有引擎告警，`verify:toolchain` 非严格模式只告警不失败），pnpm 11.19.0，Edge 158.0.4370.33，playwright-core 1.62.1，Docker 29.5.3；本机没有 go，Go 编译与测试一律走容器。
- 自托管环境用仓库已有的 `lending-experience-env.mjs`（进程内假 CPA 加构建出的二进制），不连生产、不用真实账号，只监听 127.0.0.1。两对环境各自从同一份数据目录起：先在基线上经真实接口造数（待审批 2、其中 1 条重新启用；待交付 1；生效 2；停用 3：轮换、滥用、泄露各 1）并存入隐藏设置 S1，停下，把数据目录复制给其余环境，再各自起来。第一对（B1、H1）服务只读与常规用例，第二对（B2、H2）留给 C-23 的写对写对照。演示模式的 H、B 各一个二进制进程。
- 端口只用 18481–18499：H1 18481–18483，B1 18484–18486，演示 H 18487、演示 B 18488，H2 18494–18496，B2 18497–18499；C-35 的全新数据目录实例临时用 18489、18490、18492；体验环境 18491–18493，演示体验 18489。`pnpm check:ui` 固定占 5181；`verify:browser` 自带的服务由系统分配临时端口，只监听 127.0.0.1。起之前都用 `Get-NetTCPConnection -State Listen` 核过空闲，没有碰 18421–18433、18441、18442、18461–18479。

### 2.3 测试路径与原因

- **自动化（Edge 无头加 playwright-core）**：全部可自动化的用例写成脚本，放在 `scripts/acceptance/capabilities-nav/`，脚本里标用例号，一条命令 `run-all.ps1` 整套重跑；没有固定等待（只用 `harness.mjs` 的条件等待），新增文件在 `fixed-waits` 棘轮里是 0 处。原因：并排对照要用请求拦截造桩数据（DS-RK）与挂起偏好读取来拉长加载占位、要固定时钟让两端渲染同一时刻、要模拟 390 宽手机、要抓每一个请求，内置浏览器没有等价的请求拦截，所以这一批走 accept-test 兜底顺序的最后一条。
- **Orca 内置浏览器（真点加截图）**：内置浏览器能打开、能点（`orca tab create`、`snapshot`、`click`、`screenshot` 一次成功，没有走到兜底）。用它把主要 P0 用户路径真点了一遍并截图：登录、展开栏、点「仓库自有密钥」、点「密钥管理」再点页底指路、点「额度拆借」、点「模型组」、收起栏、深浅色，另在基线上截了展开栏与带面板的密钥管理页作对照，也点开了演示体验环境的仓库自有密钥页；截图名带 `orca`，会话记录在 `net/s8/orca-session.log`。Orca 的设备模拟（`orca set device`）没有让页面切到手机布局，所以手机宽度的点击与落页只用 Edge 脚本测（如 C-14、C-22、C-36、C-37、C-40、C-42、C-54）。
- **命令类**：门禁、单元与组件测试、文档检查、`check:ui`，输出落在 `net/s8/` 下，不进 git。
- 判定口径：每条用例的每个检查点单独记录，一条用例只有所有检查点都过才记通过；失败先在基线复现。没有测到的写缺口，不写通过。

### 2.4 约束与例外

- 不改业务代码、PRD、`cases.md`；不推送、不开 PR、不切分支；截图与读数文件都不 `git add`。除新增的验收脚本和报告外，PM 在中途临时授权改动两个已有的环境脚本（见第 9 节），只加了两行。
- 只用假数据：仓库名 `example/...`，假 CPA 的管理密钥 `omc-e2e-management-key` 是测试夹具常量，不是真实密钥。造数生成的密钥与令牌长得像真密钥，证据里一律打码后才落盘，处理与扫描结果见 3.1。

## 3. 基础门禁（独立复跑）

所有命令都在本分支的工作区（头，含 S4 补修的新提交）里独立复跑，输出落在 `net/s8/` 下；凡红的先在基线复跑同一条，基线同红的归第 7 节。`check:ui --all` 与最早的一批静态门禁跑在 `6c83fbfc` 源码上，S4 之后的 4 个提交不涉及产品逻辑，合并后又整套重跑了 `pnpm verify` 的前端一路与 `check:bundle`。

| 命令 | 头 | 基线 | 判定 |
|---|---|---|---|
| `pnpm install --frozen-lockfile` | 成功 | 成功 | 通过 |
| `pnpm build && pnpm check:bundle` | 构建成功；`check:bundle` 退出码 0，`Loading boundaries and anomaly ceilings: PASS`；入口 522.95 KiB（gzip 176.20）、首屏脚本 2016.71 KiB | 退出码 1：`web/src/pages/quota/lendingMount.ts is eager through assets/index-….js` | 通过；S4 修了基线既有的红，独立确认 |
| `pnpm type-check` | 退出码 0 | — | 通过 |
| `pnpm test:logic` | 90 个套件全过 | — | 通过 |
| `pnpm test:components` | 49 个文件 365 个用例全过（含 S4 新补的测试） | — | 通过 |
| `pnpm lint:antd`、`pnpm check-css-modules`（521 个使用方）、`pnpm check:style-tokens`、`pnpm check:motion`、`pnpm check:feedback`、`pnpm check-brand`、`pnpm verify:workflow` | 全部退出码 0 | — | 通过 |
| `pnpm check-i18n`、`pnpm test:i18n` | 头 4339 个键，无缺失；基线 4336 个键（头多出 3 个新键） | 同样通过 | 通过 |
| `pnpm check:demo` | 通过：155 条响应，覆盖 20 条控制台路由 | — | 通过 |
| `pnpm check:ui --all`（端口 5181） | 81 个场景全过，569.6 秒，退出码 0 | S7 在基线上单跑 `quota-lending` 3 次全过 | 通过（`6c83fbfc` 源码）；`quota-lending` 场景在头上另单跑通过 |
| `node scripts/check-docs.mjs` | 9 条缺失路径、22 条行号引用 | 逐行相同的 9 与 22 | 基线既有 |
| `pnpm verify`（合并后的头，209 秒） | 退出码 1，三路里：前端一路通过（204.6 秒），`go` 一路红（本机没有 go，`spawnSync go ENOENT`），`repository` 一路红（`check-docs` 的基线红挡住后面的链条） | 同 | 环境性与基线既有，见第 7 节；`&&` 链因此没有走到密钥扫描，下一行单跑 |
| `pnpm verify:secrets:worktree`（gitleaks 8.24.3） | 被跟踪的树在干净副本里扫描干净（退出码 0）；含证据目录的工作区首次扫出 7 条 `generic-api-key`，全在没入库的证据文件里，是假环境造数生成的密钥形状字符串，已打码，见 3.1 | — | 通过 |
| Go：`go vet ./...`、`TZ=UTC go test ./...`（容器 golang:1.27.1） | 全过，42 个包 ok | — | 通过（Go 在本需求里零改动） |
| `pnpm test:self`、`pnpm test:fast --base 11-24-t9-dev-base` | 见第 7 节 | 见第 7 节 | 基线既有、环境性 |
| `pnpm verify:browser`（构建出的二进制加假 CPA，本机用容器编出的使用量造数程序，设 `TZ=Asia/Shanghai`） | 381 项确定性检查全过，退出码 0，100 秒 | 380 过 1 败：「密钥管理页只有一个容器」`cards=2 nested=0`，退出码 1 | 通过；基线的这 1 个红被本需求的搬页消除，见 3.2 |

### 3.1 密钥扫描与证据脱敏

- **命中的是什么**：在我的工作区（含没入库的证据目录）跑 `pnpm verify:secrets:worktree`，首次扫出 7 条 `generic-api-key`，全在证据文件 `setup-summary.json` 里。那份文件是造数脚本把假环境的应答原样记下来的，里面是一次性审批令牌这类密钥形状的字符串。它们是假环境造数生成的，不是真泄露；证据目录没有被 Git 跟踪，也不进 PR。
- **怎么处理**：`gitleaks.toml` 没改，没加 allowlist，没放宽任何规则。证据里这类值一律打码：`sk-` 开头的完整密钥、审批链接里的令牌、字段名为 token、plaintext、ciphertext 等的值、32 位十六进制串，都保留前 6 位，其余用星号补齐。落盘的脚本先打码再写：`scripts/acceptance/capabilities-nav/lib.mjs` 里的 `maskSecrets` 管住 `writeJson`、每个脚本的结果文件和控制台输出，造数脚本 `00-setup.mjs` 不再记录应答，只记步骤名。含那 7 条的旧文件已删除，由重新造数后的新文件取代。新增的验收脚本里没有任何生成出来的完整密钥写进被跟踪的文件（对脚本目录用 `sk-`、32 位十六进制、私钥头与令牌字段名逐项搜过，命中的只有处理令牌的代码和 `stubtoken` 这类桩值）。
- **证据目录重扫**：打码之后，`gitleaks dir` 对整个 acceptance 目录（含 net、shots）0 条，`node scripts/secret-scan.mjs --worktree` 在含证据目录的工作区里也是 `no leaks found`（日志 `secret-scan-worktree-with-evidence.log`）；另用正则找 `sk-` 开头的长串、审批链接里的令牌与 32 位十六进制串，文本文件里 0 处。打码时还发现更早几轮的结果文件与运行日志里留着审批链接的令牌（43 位，gitleaks 没报），一并打了码。
- **被跟踪的树单独扫**：用 `git worktree add --detach` 取出提交后的干净副本（副本里没有 `acceptance/net` 与 `acceptance/shots`），在副本里运行 `node scripts/secret-scan.mjs --worktree`（同一份钉死版本的 gitleaks 8.24.3，用 `GITLEAKS_BIN` 指过去），结论 `no leaks found`，退出码 0；输出存在 net/s8 下的 `secret-scan-tracked-tree.log`，里面记了扫描时的提交号。结论：被跟踪的树扫描干净；证据目录里的命中是假数据且已打码，不入库。

### 3.2 verify:browser 的两点说明

- **要设 `TZ`**：第一次没设时，构建出的二进制起不来，日志是 `load timezone: cannot identify server timezone; set TZ to an IANA name: invalid_timezone`，浏览器验收等不到服务；基线的二进制同样如此（`gate-base-verify-browser-no-tz.log`）。这是 Windows 上 Go 认不出本机时区名，cases.md 的 C-51 本来就写了「时区用例需设 `TZ`」。设 `TZ=Asia/Shanghai` 后，头 381 项检查全过，其余自托管环境脚本也都是这样起的。
- **要先编两个二进制**：`verify:browser` 用环境变量 `OMCPA_BROWSER_BINARY` 与 `OMCPA_SEED_USAGE_BINARY` 指定构建出的控制台二进制与使用量造数程序；本机没有 go，第一次直接跑报 `spawnSync go ENOENT`。用容器 golang:1.27.1 把两个程序都交叉编成 Windows 可执行文件（头与基线各一份）后才跑起来，这一步是环境准备，不改任何仓库文件。
- **基线的 1 个红**：基线上 380 过 1 败，败的是 `key management renders one container rather than nested frames`，读到 `cards=2`。这一项数密钥管理页里的卡片，期望 1 张；基线上仓库自有密钥面板在同一页里，是第 2 张。头上面板搬走，读到 `cards=1`，通过。所以这是基线上本就存在的红，面板搬走后自然转绿；头上没有改过这条检查（C-52 的清单里没有它），两端的检查总数同为 381 项。

## 4. 逐条用例结果

### 4.1 用例结果总表

64 条用例：通过 64，失败 0，阻塞 0，未测 0；其中 P0 共 40 条，全部通过，每条 P0 都有截图或命令输出作证据。「怎么测的」里的脚本在 `scripts/acceptance/capabilities-nav/` 下，截图在 acceptance 目录的 shots 下，读数与日志在 net/s8 下（均不入库，索引见第 11 节）。

| 用例 | 级别 | 标题 | 结果 | 怎么测的 | 证据 |
| --- | --- | --- | --- | --- | --- |
| C-01 | P0 | 展开栏的分组数量、顺序与各组条目 | 通过 | 脚本 01-structure（17 个检查点）；加 Orca 内置浏览器真点（2 张） | 截图 4 张：C-01-B-expanded-zh.png、C-01-H-expanded-zh.png、C-01-orca-B-expanded-zh.png 等 |
| C-02 | P0 | 四种语言下的「能力」分组标题，现场切换 | 通过 | 脚本 01-structure（20 个检查点） | 截图 3 张：C-02-H-rail-en.png、C-02-H-rail-ms.png 等 |
| C-03 | P1 | 四种语言目录齐全（命令） | 通过 | 命令 pnpm check-i18n、pnpm test:i18n（头与基线各一次） | 头 4339 个键、基线 4336 个键，两条命令在两端都退出码 0；日志 gate-head-check-i18n.log、gate-head-test-i18n.log、gate-base-check-i18n.log、gate-base-test-i18n.log |
| C-04 | P0 | 「能力」标题样式与相邻分组一致 | 通过 | 脚本 01-structure（52 个检查点） | 截图 12 张：C-04-en-dark-capabilities-title.png、C-04-en-dark-observe-title.png 等 |
| C-05 | P0 | 「能力」组内的成员与顺序 | 通过 | 脚本 01-structure（3 个检查点） | 截图 1 张：C-05-H-capabilities-zh.png |
| C-06 | P0 | 条目名称在四种语言、四个位置上一致 | 通过 | 脚本 02-presentation（24 个检查点） | 截图 2 张：C-06-H-settings-panel-en.png、C-06-H-settings-panel-ms.png |
| C-07 | P0 | 点击条目打开对应页面 | 通过 | 脚本 02-presentation（7 个检查点）；加 Orca 内置浏览器真点（1 张） | 截图 3 张：C-07-H-model-groups-zh.png、C-07-H-repo-keys-zh.png、C-07-orca-H-repo-keys-zh.png |
| C-08 | P0 | 固定条目的图标两两不同 | 通过 | 脚本 01-structure（7 个检查点） | 截图 4 张：C-08-H-collapsed-icons.png、C-08-icon-api-keys.png 等 |
| C-09 | P0 | 「运行」组不再有模型组 | 通过 | 脚本 01-structure（3 个检查点） | 截图 1 张：C-09-H-operate-zh.png |
| C-10 | P0 | 密钥管理页只剩网关客户端密钥列表 | 通过 | 脚本 06-apikeys（15 个检查点）；加 Orca 内置浏览器真点（1 张） | 截图 3 张：C-10-B-api-keys-zh.png、C-10-H-api-keys-zh.png、C-10-orca-B-api-keys-with-panel-zh.png |
| C-11 | P1 | 密钥管理页上半块的操作结果与 B 一致 | 通过 | 脚本 06-apikeys（16 个检查点） | 截图 4 张：C-11-B-after-alias.png、C-11-B-after-refresh.png 等 |
| C-12 | P0 | 页底指路提示：存在、文字、可点 | 通过 | 脚本 06-apikeys（5 个检查点）；加 Orca 内置浏览器真点（2 张） | 截图 4 张：C-12-H-api-keys-bottom-zh.png、C-12-H-landed-repo-keys-zh.png、C-12-orca-H-api-keys-zh.png 等 |
| C-13 | P1 | 列表为空、读取失败、读取未完成时提示仍在 | 通过 | 脚本 06-apikeys（9 个检查点） | 截图 3 张：C-13-H-empty-zh.png、C-13-H-failed-zh.png 等 |
| C-14 | P2 | 页底提示的四种语言与手机宽度 | 通过 | 脚本 06-apikeys（24 个检查点） | 截图 2 张：C-14-H-api-keys-bottom-ms-390.png、C-14-H-api-keys-bottom-zh-390.png |
| C-15 | P0 | 说明句、搜索与状态筛选（F1、F2） | 通过 | 脚本 07-repokeys-parity（66 个检查点） | 截图 2 张：C-15-B-search-stopped-zh.png、C-15-H-search-stopped-zh.png |
| C-16 | P0 | 密钥列表与详情（F6、F7） | 通过 | 脚本 07-repokeys-parity（70 个检查点） | 截图 4 张：C-16-B-RK-1-detail-30d-zh.png、C-16-B-RK-5-detail-zh.png 等 |
| C-17 | P0 | 待审批、审阅、批准与驳回、一次性审批链接（F4、F9） | 通过 | 脚本 07-repokeys-parity（94 个检查点） | 截图 4 张：C-17-B-AP-2-review-zh.png、C-17-B-approval-link-zh.png 等 |
| C-18 | P0 | 待交付与代取密文（F5） | 通过 | 脚本 07-repokeys-parity（23 个检查点） | 截图 2 张：C-18-B-ciphertext-dialog-zh.png、C-18-H-ciphertext-dialog-zh.png |
| C-19 | P0 | 代 agent 提交申请（F3） | 通过 | 脚本 07-repokeys-parity（77 个检查点） | 截图 4 张：C-19-B-proxy-form-zh.png、C-19-B-proxy-result-zh.png 等 |
| C-20 | P0 | 停用、申请重新启用、查看明文（F8） | 通过 | 脚本 07-repokeys-parity（151 个检查点） | 截图 4 张：C-20-B-after-stop-zh.png、C-20-B-reveal-dialog-zh.png 等 |
| C-21 | P1 | 审计平台推送状态（F10） | 通过 | 脚本 07-repokeys-parity（14 个检查点） | 截图 4 张：C-21-B-audit-off-zh.png、C-21-B-audit-on-zh.png 等 |
| C-22 | P1 | 加载中、读取失败、列表为空与手机宽度（F11） | 通过 | 脚本 07-repokeys-parity（24 个检查点） | 截图 8 张：C-22-B-empty-zh.png、C-22-B-failed-zh.png 等 |
| C-23 | P0 | 真实层：同数据、同操作的对照 | 通过 | 脚本 08-repokeys-real（80 个检查点） | 截图 6 张：C-23-B-audit-zh.png、C-23-B-final-zh.png 等 |
| C-24 | P0 | 新页的标题、浏览器标签页标题与直接打开 | 通过 | 脚本 04-pages（24 个检查点） | 截图 4 张：C-24-H-repo-keys-en.png、C-24-H-repo-keys-ms.png 等 |
| C-25 | P1 | 统一页头与页头刷新只重读本页数据 | 通过 | 脚本 07-repokeys-parity（5 个检查点） | 截图 2 张：C-25-H-after-refresh-zh.png、C-25-H-api-keys-header-zh.png |
| C-26 | P0 | 既有地址的直接打开、刷新、前进后退 | 通过 | 脚本 03-routes（67 个检查点） | 截图 4 张：C-26-H-api-keys.png、C-26-H-model-groups.png 等 |
| C-27 | P1 | 服务端对各地址的直接请求 | 通过 | 脚本 03-routes（13 个检查点） | 读数文件 |
| C-28 | P0 | 八个页面的高亮、面包屑与标签页标题 | 通过 | 脚本 03-routes（32 个检查点）；加 Orca 内置浏览器真点（1 张） | 截图 9 张：C-28-H-api-keys.png、C-28-H-model-groups-add.png、C-28-orca-H-model-groups-zh.png 等 |
| C-29 | P1 | 被隐藏的条目用地址打开，面包屑仍写出分组 | 通过 | 脚本 03-routes（16 个检查点） | 截图 4 张：C-29-H-hidden-api-keys.png、C-29-H-hidden-model-groups.png 等 |
| C-30 | P1 | 查询参数不影响高亮与面包屑 | 通过 | 脚本 03-routes（4 个检查点） | 截图 1 张：C-30-H-quota-lending-query.png |
| C-31 | P2 | 四种语言下的面包屑 | 通过 | 脚本 03-routes（24 个检查点） | 截图 3 张：C-31-H-breadcrumb-model-groups-ms.png、C-31-H-breadcrumb-quota-lending-ms.png 等 |
| C-32 | P0 | 审批链接页的返回入口改指新页 | 通过 | 脚本 04-pages（18 个检查点） | 截图 4 张：C-32-B-apply-footer-zh.png、C-32-B-apply-landing-zh.png 等 |
| C-33 | P1 | 审批链接的格式、有效期与批准驳回行为不变 | 通过 | 脚本 04-pages（12 个检查点） | 截图 1 张：C-33-H-approved.png |
| C-34 | P0 | 改动前已存的隐藏设置继续生效 | 通过 | 脚本 05-hidden（15 个检查点） | 截图 5 张：C-34-B-S1-rail.png、C-34-H-S0-rail.png 等 |
| C-35 | P0 | 新页面默认可见 | 通过 | 脚本 05-hidden（6 个检查点） | 截图 2 张：C-35-H-fresh-data-rail.png、C-35-H-S6-panel.png |
| C-36 | P0 | 锁定项仍锁定 | 通过 | 脚本 05-hidden（10 个检查点） | 截图 2 张：C-36-H-locked-rows.png、C-36-H-S3-drawer-390.png |
| C-37 | P0 | 「能力」整组隐藏与留一项 | 通过 | 脚本 05-hidden（11 个检查点） | 截图 6 张：C-37-H-S4-collapsed.png、C-37-H-S4-drawer-390.png 等 |
| C-38 | P1 | 其他分组整组隐藏的行为不变 | 通过 | 脚本 05-hidden（7 个检查点） | 截图 1 张：C-38-H-S7-collapsed.png |
| C-39 | P0 | 设置面板按新分组列出，计数包含新条目 | 通过 | 脚本 05-hidden（10 个检查点） | 截图 3 张：C-39-B-panel.png、C-39-H-panel-all-shown.png 等 |
| C-40 | P2 | 设置面板的四种语言与手机宽度 | 通过 | 脚本 05-hidden（16 个检查点） | 截图 2 张：C-40-H-panel-phone-ms.png、C-40-H-panel-phone-zh-Hant.png |
| C-41 | P0 | 收起栏的图标顺序、数量与名称 | 通过 | 脚本 02-presentation（7 个检查点）；加 Orca 内置浏览器真点（1 张） | 截图 3 张：C-41-B-collapsed-zh.png、C-41-H-collapsed-zh.png、C-41-orca-H-collapsed-zh.png |
| C-42 | P0 | 手机抽屉与展开栏同一份内容 | 通过 | 脚本 02-presentation（7 个检查点） | 截图 3 张：C-42-H-after-tap-quota-lending-390.png、C-42-H-after-tap-repo-keys-390.png 等 |
| C-43 | P0 | 加载占位的分组数、条数与高度 | 通过 | 脚本 02-presentation（5 个检查点） | 截图 4 张：C-43-B-loaded.png、C-43-B-placeholder.png 等 |
| C-44 | P1 | 加载占位：演示模式 | 通过 | 脚本 10-demo（4 个检查点） | 截图 4 张：C-44-B-demo-loaded.png、C-44-B-demo-placeholder.png 等 |
| C-45 | P0 | 演示模式的侧栏 | 通过 | 脚本 10-demo（8 个检查点） | 截图 4 张：C-45-B-demo-rail-expanded.png、C-45-H-demo-collapsed.png 等 |
| C-46 | P0 | 演示模式里仓库自有密钥页可打开并显示演示数据 | 通过 | 脚本 10-demo（3 个检查点）；加 Orca 内置浏览器真点（1 张） | 截图 3 张：C-46-B-demo-repo-keys-zh.png、C-46-H-demo-repo-keys-zh.png、C-46-orca-H-demo-repo-keys-zh.png |
| C-47 | P0 | 演示模式下写操作与拆页前一样被拒（F12） | 通过 | 脚本 10-demo（36 个检查点） | 截图 3 张：C-47-B-demo-controls-zh.png、C-47-H-demo-apply-page-zh.png 等 |
| C-48 | P1 | 演示模式下密钥管理页的行为不变 | 通过 | 脚本 10-demo（5 个检查点） | 截图 1 张：C-48-H-demo-api-keys-zh.png |
| C-49 | P0 | 打开新页与密钥管理页发出的数据请求，对照拆页前 | 通过 | 脚本 09-network（11 个检查点） | 截图 3 张：C-49-B-api-keys-with-panel-zh.png、C-49-H-api-keys-zh.png 等 |
| C-50 | P0 | 整个控制台没有新增写操作 | 通过 | 脚本 09-network（4 个检查点） | 截图 2 张：C-50-B-settings-panel-after-actions-zh.png、C-50-H-settings-panel-after-actions-zh.png |
| C-51 | P0 | 既有自动化全部通过 | 通过 | 命令：头上逐条跑；verify:browser 在基线同条件复跑对照 | type-check、test:logic（90 套件）、test:components（49 文件 365 用例）、lint:antd、check-i18n、check-css-modules、check:style-tokens 全过；check:ui --all 81 个场景全过；verify:browser 381 项检查全过（须设 TZ，见 3.2）。基线同命令 380 过 1 败，败的是「密钥管理页只有一个容器」，头上已消除，见 3.2 |
| C-52 | P1 | 既有断言的改动清单 | 通过 | 人工：git diff 90a8d8ff..头 逐条归类 | 见 4.2 的归类表；没有 (f) 类行为改动，没有删除断言而无等价替代，没有放宽、没有 skip |
| C-53 | P1 | 其余页面的功能与外观与 B 一致 | 通过 | 脚本 11-visual（22 个检查点） | 截图 12 张：C-53-B-group-detail-zh.png、C-53-group-add-B.png 等 |
| C-54 | P2 | 深色、手机与四种语言下的新分组与新页 | 通过 | 脚本 11-visual（14 个检查点）；加 Orca 内置浏览器真点（2 张） | 截图 10 张：C-54-dark-en-desktop-menu.png、C-54-dark-en-desktop-repo-keys.png、C-54-orca-H-dark-rail-zh.png 等 |
| C-55 | P2 | 键盘操作与无障碍名称 | 通过 | 脚本 02-presentation（4 个检查点） | 截图 1 张：C-55-H-keyboard-opened-repo-keys.png |
| C-56 | P2 | 新页按需加载，不拖慢首屏 | 通过 | 脚本 11-visual（3 个检查点） | 读数文件 |
| C-57 | P0 | 额度拆借挂入：位置、高亮、面包屑、标签页标题与页内标题 | 通过 | 脚本 04-pages（8 个检查点）；加 Orca 内置浏览器真点（1 张） | 截图 3 张：C-57-B-quota-lending-zh.png、C-57-H-quota-lending-zh.png、C-57-orca-H-quota-lending-zh.png |
| C-58 | P0 | 额度拆借的四种语言名称 | 通过 | 脚本 04-pages（20 个检查点） | 截图 1 张：C-58-H-quota-lending-ms.png |
| C-59 | P1 | 从 OAuth 管理页头的入口进入 | 通过 | 脚本 04-pages（6 个检查点） | 截图 1 张：C-59-H-after-entry-click.png |
| C-60 | P2 | 额度拆借页的直接打开、刷新、前进后退（烟测） | 通过 | 脚本 04-pages（4 个检查点） | 读数文件 |
| C-61 | P0 | 额度拆借一期既有验证作为证据 | 通过 | 命令：check:ui --scenario quota-lending、test:components、test:logic、lending-experience-env.mjs --check | c61.log：场景 1 个全过，自检 16/16 checks passed；test:components 49 个文件 365 个用例、test:logic 90 个套件全过 |
| C-62 | P0 | 文档同步：写死的分组名、面板位置、术语 | 通过 | 命令加人工读每处改动（头与基线各跑一遍） | c62-head-final.txt、c62-base.txt；改动处逐行读过，见 4.3 |
| C-63 | P1 | 文档里带数字的统计 | 通过 | 命令加人工 | c62-head-final.txt 的计数部分；见 4.3 |
| C-64 | P0 | 文档检查 | 通过 | 命令：PR 头改动的 14 份 Markdown 逐份 check-docs --file，加头与基线各一次全量 | 见 4.3 末的 C-64 小节：14 份全部 0/0/0/0；全量头 9 与 22、基线 9 与 22，新增 0 |

### 4.2 C-52：既有断言的改动清单

范围：`git diff 11-24-t9-dev-base..Newoahil/11-24-t9-s5-codex` 里 web/tests、scripts/acceptance 与 scripts 下的测试脚本。改动的既有文件 8 个，新增的测试文件 4 个（`api-keys-page`、`app-layout-navigation`、`repo-keys-page`、`shell-loading` 的组件测试，都是新增用例，不在归类范围内）。被删的断言行共 19 行，全部落在下表里；没有 `skip`、`only`、`todo`，没有放宽阈值（逐行搜过新增行）。

| 文件 | 被改或被删的既有断言 | 归类 | 与 PRD 的对应 |
|---|---|---|---|
| `navigation.component.test.tsx` | 「运行」组的条目清单去掉 `/model-groups` | (a) 模型组换组 | AC-7；同文件新增了六组顺序、能力组成员与顺序、演示模式下能力组仍在的用例 |
| `sidebar-nav.component.test.tsx` | 设置面板的条目清单：模型组从运行组挪到「审计」之后，并多出「仓库自有密钥」「额度拆借」 | (a) 加 (e) 条目清单随六组顺序 | AC-19；同文件新增三个用例（密钥管理被隐藏时新页仍可见、搬走的条目隐藏态保持、额度拆借计入隐藏数） |
| `quota-lending-mount.component.test.tsx` | 删除 6 条断言：`QUOTA_LENDING_NAV_KEY` 等于 `/oauth-management`，以及 `lendingNavKeyForPath` 对拆借地址（含末尾斜杠）返回 OAuth 管理、对 `/model-groups` 原样返回；用例名与 import 随之改成菜单文案键 | (d) 撤掉对「OAuth 管理」高亮的借用 | AC-27；等价替代更强：断言文案键 `nav.quota_lending`，新增一个用例断言拆借地址解析到「能力」组里自己的条目，面包屑与标签页标题读同一个条目名，末尾斜杠也一样 |
| `quota-lending-page.component.test.tsx` | 只改 import 路径：常量模块从 `pages/quota/lendingMount` 搬到 `types/quotaLendingMount` | (d) 的配套（模块搬家，没有断言改动） | AC-27；S4 为让页面按需加载把常量搬出该模块，`check:bundle` 因此由红转绿 |
| `repo-key-display.component.test.tsx` | 挂载的路由由 `/api-keys` 改为 `/repo-keys`（2 处，面板在新页上读写） | (b) 仓库自有密钥换页 | AC-10 |
| `scripts/ui-impact.test.mjs` | 常量模块路径随搬家改名（3 行）；另新增一个用例：改 RepoKeysPage 会选中手机整站扫描 | (d) 的配套（路径改名） | AC-27、AC-25 |
| `scripts/acceptance/probes/modelGroups.mjs` | 「侧栏把模型组放在模型广场旁边」改为「侧栏把模型组归在能力之下」，并读所在分组标题等于 `Capabilities` | (a) | AC-7；断言更严 |
| `scripts/acceptance/probes/oauthManagement.mjs` | `/quota-lending/` 末尾斜杠地址的高亮断言：「OAuth 条目保持高亮」改为「额度拆借条目高亮，且不落在 OAuth、仪表盘」 | (d) | AC-27；保留了「不落到仪表盘」 |

(c) 加载占位：没有既有断言被改，占位的组数与条数由新增的 `shell-loading` 组件测试断言。(e) 的另一半（分组名清单变六组）：`docs`、`DESIGN.md` 里的分组名清单不是测试断言，归 C-62。

**(f) 类与其他需要单独说明的改动：没有 (f) 类断言改动。** 下面几处不是既有断言的改写，列出来供对账：

- `oauthManagement.mjs` 另在读 OAuth 页头条目前加了一次就绪等待（等 `[data-lending-entry]` 出现）。它不改断言文字与阈值，也不是 skip；页头条目随额度读取落地而不是随凭证行，这一点 C-53 第 3 步在两端各等到条目出现后逐项比对，文字与三态相同。
- `check-ui-plan.mjs`、`probes/mobileConsole.mjs`、`scripts/check-demo.mjs`、`scripts/demo-readiness.mjs` 只新增登记（新页进手机整站扫描、进演示覆盖检查与演示就绪清单），没有改动或删除已有条目。
- `repo-key-apply.component.test.tsx` 只新增一个用例：审批链接页的返回链接指向 `/repo-keys`。

结论：每条被改动的既有断言都归入 (a)～(e)，改后的写法与 PRD 对应 AC 一致；没有删掉断言而无等价替代，没有放宽，没有新增 skip。C-52 通过。

### 4.3 文档类用例：C-62、C-63、C-64

**C-62**（头与基线各跑一遍用例里的三条命令，并读每处命中的上下文；用例命令里的 `docs\guides\*.md` 在两端都不存在，去掉后重跑，见 9.2）：

| 命令 | 基线 | 头 | 读后结论 |
|---|---|---|---|
| 1. 只写四个分组名 | 2 处：`docs/design.md` 的导航分组一节与 `DESIGN.md` 的导航侧栏条目，都写「Operate / Gateway / Observe / Control」 | 0 处 | 头里两处改成六组，顺序为 Operate、Gateway、Observe、Capabilities、Plugins、Control；`docs/design.md` 同一段里插件组的那句随之换了写法，仍是「含管理、商店与各插件自己的页，排在控制之前」 |
| 2. 仓库自有密钥面板写在密钥管理页上 | 1 处：`docs/agent-capabilities.md` 说明控制台一侧的那句，写「面板在 API keys 页」 | 0 处 | 这一句改成「仓库自有密钥页，地址 `/repo-keys`」，句子本身通顺；另有三处同类改动一并读过：`docs/repo-key-client.md` 的停用与重发入口改指该页，`docs/testing.md` 补了新页的组件测试归属与密钥管理页「只留网关列表加指路」，`PRODUCT.md` 的页面清单写明新页在「能力」组、与模型组和额度拆借并列、密钥管理页指向它 |
| 3. 导航分组词条 | 无 | `CONTEXT.md` 有 Navigation Group 词条 | 词条写明它只是侧栏标题，不是 Capability（OMC 暴露给 agent 的声明式操作），并说明「在分组间移动页面只改侧栏的画法，不改地址、隐藏态与行为」；「侧栏可见性」词条补了「条目按路由键存，不按分组存，所以页面换组后隐藏或可见的状态保持」，整组全隐藏则标题不显示的句子在 `docs/design.md` 里原样保留，与新分组结构不矛盾 |

此外查了旧模块名 lendingMount 的残留：额度拆借交付的三份文档与 `docs/architecture.md` 原来都指向它，头里已改指新文件 `quotaLendingMount.ts`；排除冻结证据后 `git grep lendingMount` 为空（S7 的 N2 已关）。

**C-63**（带数字的统计，B 对 B 的侧栏、H 对 H 的侧栏）：

| 位置 | 基线写的 | 基线侧栏的实际数 | 头写的 | 头侧栏的实际数 |
|---|---|---|---|---|
| `docs/design.md` 设置入口那句「rather than N rows」 | sixteen | 固定条目 18 个（基线本来就与自身侧栏对不上，不算头引入） | twenty | 固定条目 20 个，同口径 |
| `docs/design.md` 手机底部标签栏那句「N groups of M destinations」 | 「four groups of seventeen destinations」 | 5 组（文中把插件组另算，所以写四组）、18 个固定条目，17 对不上（基线本来就旧） | 「six groups of twenty destinations」 | 6 组（含插件组）、20 个，同口径 |
| 设置面板样式文件头部的注释 | sixteen entries | 18 个 | 「twenty entries … (nineteen in demonstration mode)」 | 20 个；演示模式因关掉操练场少 1 个，注释已写明 |
| `docs/cpamc-parity.md` 的页面清单 | 「totalling 15 live routed pages」，清单里没有新页 | 与清单不符 | 清单改写成含模型组、仓库自有密钥页、额度拆借等的全表，不再写总数 | 无残留旧数字 |

此外用 `four groups`、`4 groups`、`四个分组` 等写法在 docs 与根目录 md 里搜（排除交付文档），0 处。凡写明侧栏分组数或条目总数的统计，头里都与新侧栏同口径，没有残留旧数字。C-63 通过。

**C-64**：PR 头相对基线新增与改动的 14 份 Markdown，逐份运行 `node scripts/check-docs.mjs --file`，每份都是 `missing-path=0 retired-reference=0 line-reference=0`；全量检查头 9 条缺失路径、22 条行号引用，基线同样 9 与 22，逐行相同，新增 0。本分支额外带进来的三份文件另跑：S7 审查报告与本报告 0/0/0；S6 审查报告在逐份模式下报 1 条缺失路径（它引用的旧模块文件，S4 重命名之后不存在了），那是冻结的审查证据，全量检查按冻结证据处理不报，不算头引入，记在第 5 节。

### 4.4 AC-10：新页与拆页前逐项一致（F1～F12）

对照方法：同一份数据，两端各做同一组操作，每一步之后用同一种方式读两端，逐项比：面板内全部可见文字、控件、弹窗、提示，以及发出的读请求与写请求（方法、路径、查询、体字段）；并排看图。B 是基线的「密钥管理页下半块的面板」，H 是新的 `/repo-keys` 页。三层：**桩层**（请求拦截造出同一份状态化数据集，两端读同一份桩，覆盖空用量、泄露、审计推送两态、加载中、读取失败、列表为空、手机宽度这些真实环境造不出的态）；**真实层**（第二对环境 B2、H2，数据目录从同一份复制，同一组操作真发，再读管理接口里的状态与审计页）；**演示模式**（F12 只能在这里测）。面板区域内两端逐项相同，没有出现需要裁决的差异；面板以外的差异（页标题、面包屑、侧栏高亮、密钥管理页的页底指路、审批链接页的返回入口）由 C-24、C-28、C-12、C-32 核对，都能在 PRD 的变化清单里找到出处。

| 功能 | 层 | 用例 | 结果 |
|---|---|---|---|
| F1 面板头的说明句 | 桩层 | C-15 | 通过：中英文两端同一句 |
| F2 搜索与状态筛选 | 桩层 | C-15 | 通过：同样的关键词与状态得到同样的行 |
| F3 代 agent 提交申请 | 桩层、真实层 | C-19、C-23 | 通过：表单字段、提示、结果、提交后的待审批块两端相同；真实层请求体逐字段相同 |
| F4 待审批：审阅、批准、驳回、指纹不一致提示、重新启用申请 | 桩层、真实层 | C-17、C-23 | 通过：审阅详情与「上次指纹」提示相同；驳回要原因（空则按钮禁用），批准没有确认弹窗，两端相同（用例措辞与现状的出入见 9.2） |
| F5 待交付与代取密文 | 桩层、真实层 | C-18、C-23 | 通过：取密文的弹窗、关闭后的列表与发出的请求两端相同 |
| F6 密钥列表与四个操作 | 桩层、真实层 | C-16、C-23 | 通过：停用、申请重新启用、查看明文、详情四个操作与状态标签相同；泄露类的重新启用入口两端都是禁用态 |
| F7 密钥详情 | 桩层 | C-16 | 通过：近 7 天与 30 天统计窗、按模型分项相同 |
| F8 停用、申请重新启用、查看明文 | 桩层、真实层 | C-20、C-23 | 通过：停用的类别与原因、申请重新启用的提示、查看明文的弹窗两端相同；读数文件里只记「看起来是不是密钥」的布尔值，不存明文 |
| F9 一次性审批链接 | 桩层、真实层 | C-17、C-23（链接页与有效期见 C-32、C-33） | 通过：格式与有效期不变，批准与驳回的行为不变；页尾返回入口改指新页（PRD 变化清单里的一项） |
| F10 审计平台推送 | 只能桩层 | C-21 | 通过：启用与未启用两态的文案与数字相同；真实环境没有审计平台，缺口已在 9.1 写明 |
| F11 加载中、读取失败、列表为空、手机宽度 | 桩层 | C-22 | 通过：三种态与 390 宽下没有横向溢出，两端一致 |
| F12 演示模式只读 | 只能演示模式 | C-47 | 通过：每个写操作都被拒，状态码与错误码（403，`demo_operation_refused`）与基线相同，界面提示相同 |

真实层的 80 个检查点里，除可见内容、请求与弹窗外还核对了管理接口里的最终状态（状态、交付标记、停用类别、计数）与审计页上 `repo_key.*` 事件，两端相同。唯一的差异在审计页：B 的密钥管理页载入时会读网关密钥列表，审计里多一条「查看客户端密钥明文」事件，H 的新页不读网关密钥所以没有这条，这与 AC-24 一致（见 9.2 第 3 条）。

### 4.5 AC-24：没有新增的请求与写操作（C-49、C-50）

**C-49**：同一个浏览器上下文里分别打开 B 的密钥管理页（带面板）、H 的新页、H 的密钥管理页，记录从导航开始到页面稳定的全部数据请求，扣掉壳层共有的请求集合（S）后按「族」比较：

| 页面 | 仓库密钥族 | 网关密钥族 | 其他 |
|---|---|---|---|
| B 的密钥管理页（带面板） | 4 条：列表、待审批队列、待交付队列、审计推送状态 | 3 条：密钥列表（含明文开关）、按密钥的用量、配置 | 无 |
| H 的新页 | 与 B 逐条相同的 4 条（方法、路径、查询、条数） | 无 | 无 |
| H 的密钥管理页 | 只剩列表 1 条（给列表里所属仓库的小标用） | 同 B 的 3 条 | 无 |

结论：新页读的正是旧面板读的四样，没有多出一条；H 的密钥管理页比 B 少读三条（待审批、待交付、审计推送），没有任何一条是 B 与 S 里都没有的新请求；所有请求的主机只有应用自己，没有外部主机；三个页面都没有写请求。

**C-50**：逐页只打开页面，H 共 23 个页面、B 共 22 个，逐页比较非 GET 请求集合：H 与 B 逐页逐条相同，B 没有而 H 有的写操作为 0。唯一出现的非 GET 是操练场页的 `PUT /v1/preferences/playground_session`，B 上同样发出（PlaygroundPage 自己持久化会话偏好，本需求没有动 Playground 的任何文件），记为「既有，非新增」。侧栏相关操作的写请求：切换语言、收起与展开侧栏都不发写请求；设置面板的显隐开关只写 `PUT /v1/preferences/omc_nav_hidden`，B 与 H 的形状相同；面板各写操作（批准、驳回、取密文、停用、申请重新启用、查看明文、代提交、生成链接）两端的方法、路径与体字段逐条相同（并排读数在 C-17～C-20、C-23）。AC-24 满足。

## 5. 问题清单

### 5.1 范围内要修

**无。** 严重、一般、轻微三档都是 0 条：64 条用例没有一条失败，没有找不到 PRD 出处的差异，没有新增的请求或写操作，没有被放宽或删掉的断言。

### 5.2 范围外观察

下面 8 项都不是本需求引入的，也不要求本需求修；记下来供 PM 决定要不要另开事项。

| # | 观察 | 证据 | 影响与建议 |
|---|---|---|---|
| 1 | 基线上打开「密钥管理」页会读网关密钥列表（带明文开关），审计里记一条「查看客户端密钥明文」事件；H 的密钥管理页发同样的读取（C-49），新页 `/repo-keys` 不读网关密钥所以没有这一条 | C-23 的审计对照 | 既有行为。打开页面就记一次「查看明文」，审计里的这类事件会偏多；可另议是否改成点开才记 |
| 2 | 操练场页载入后发 `PUT /v1/preferences/playground_session`（PlaygroundPage 自己持久化会话偏好） | C-50 | 既有，B 同样，本需求没动 Playground 的文件；PM 已定口径，不记缺陷 |
| 3 | Windows 上 `pnpm test:fast --base <基线>` 的纯逻辑一路报「The command line is too long.」，脚本把整份改动文件清单放在命令行里 | 门禁日志 | 本机用不了该入口，需改跑 `pnpm test:logic`（同一组测试，90 个套件全过）；建议脚本分批或走文件传参 |
| 4 | 构建出的 Windows 二进制不设 `TZ` 起不来（`invalid_timezone`），基线同样 | 3.2 | `verify:browser` 与自托管环境脚本要先设 `TZ`；建议起不来时的报错里提示设法，或在 Windows 上有回退 |
| 5 | `check-docs.mjs --file` 对已冻结的审查证据（S6 的 `review-s3-r1.md`）报 1 条缺失路径：它引用的旧模块文件在 S4 重命名后不存在 | 4.3 的 C-64 小节 | 全量检查把交付证据按冻结处理不报，所以不影响门禁；逐份模式下会冒出来，知道即可 |
| 6 | `experience.py` 起来后给的停止提示写的是 `tools/experience.py`，真实路径是 `E:/works/Homan-context/_meta/scripts/loop/experience.py` | 工具输出 | 照提示敲会找不到文件；建议改提示 |
| 7 | antd 的两个汉字的按钮会被自动插入空格（「批 准」「驳 回」「详 情」），按钮文字匹配要容忍 | 脚本里的 `spaced` 小工具 | 两端都是，不影响功能；后续写脚本时留意 |
| 8 | 用例文字与现状有 9 处出入（批准没有确认弹窗、`docs\guides` 目录不存在、面包屑分隔符是「/」等） | 9.2 | 不改用例，已按现状判定；建议下一版用例按现状修订 |

## 6. AC 覆盖结论

AC-1～AC-29：除 PRD 标为不适用的 AC-26 外，28 条都有用例、用例全部通过，结论都是已实现；每条 AC 在 cases.md 里都含 P0 用例，这些 P0 也都通过。

| AC | 内容 | 用例 | 结论 |
| --- | --- | --- | --- |
| AC-1 | 分组与顺序 | C-01、C-55 | 已实现（用例全部通过） |
| AC-2 | 四种语言的分组名 | C-02、C-03 | 已实现（用例全部通过） |
| AC-3 | 标题样式 | C-04 | 已实现（用例全部通过） |
| AC-4 | 成员与顺序 | C-01、C-05、C-55 | 已实现（用例全部通过） |
| AC-5 | 条目名称与点击 | C-03、C-06、C-07 | 已实现（用例全部通过） |
| AC-6 | 图标可区分 | C-08 | 已实现（用例全部通过） |
| AC-7 | 模型组离开运行 | C-01、C-09 | 已实现（用例全部通过） |
| AC-8 | 密钥管理页只剩网关密钥 | C-10、C-11 | 已实现（用例全部通过） |
| AC-9 | 页底提示 | C-12、C-13、C-14 | 已实现（用例全部通过） |
| AC-10 | 逐项一致 | C-15～C-23、C-25、C-47 | 已实现（用例全部通过，F1～F12 对照见 4.4） |
| AC-11 | 标题与地址 | C-24 | 已实现（用例全部通过） |
| AC-12 | 现有地址不变 | C-26、C-27 | 已实现（用例全部通过） |
| AC-13 | 高亮与面包屑 | C-28、C-29、C-30、C-31 | 已实现（用例全部通过） |
| AC-14 | 审批链接页的返回入口 | C-32、C-33 | 已实现（用例全部通过） |
| AC-15 | 已存设置继续生效 | C-34 | 已实现（用例全部通过） |
| AC-16 | 新页面默认可见 | C-35 | 已实现（用例全部通过） |
| AC-17 | 锁定项仍锁定 | C-36 | 已实现（用例全部通过） |
| AC-18 | 整组隐藏不显示 | C-37、C-38 | 已实现（用例全部通过） |
| AC-19 | 设置面板按新分组列 | C-39、C-40 | 已实现（用例全部通过） |
| AC-20 | 收起栏 | C-41 | 已实现（用例全部通过） |
| AC-21 | 手机抽屉 | C-42 | 已实现（用例全部通过） |
| AC-22 | 加载占位 | C-43、C-44 | 已实现（用例全部通过） |
| AC-23 | 演示模式 | C-45、C-46、C-47、C-48 | 已实现（用例全部通过） |
| AC-24 | 没有新增请求与写操作 | C-25、C-49、C-50 | 已实现（用例全部通过，对照见 4.5） |
| AC-25 | 其余功能不变 | C-11、C-51、C-52、C-53、C-54、C-56、C-60、C-61 | 已实现（用例全部通过） |
| AC-26 | 不适用（PRD 已标，编号保留） | — | 不适用 |
| AC-27 | 额度拆借挂入 | C-57、C-58、C-59、C-60、C-61 | 已实现（用例全部通过） |
| AC-28 | 文档同步 | C-62、C-63 | 已实现（用例全部通过，读数见 4.3） |
| AC-29 | 文档检查 | C-64 | 已实现（用例全部通过） |

只能人工判断的部分，本轮的做法：AC-6 与 AC-20 的「图标一眼能分辨」，用渲染出的 SVG 签名两两比对，再看收起栏与展开栏的截图：20 个固定条目两两不同；看图时最相近的一对是仪表盘与模型组（都是方格类，一个是一大三小的看板，一个是四个等大方格），两者在基线里就各自是这个图标，相隔 11 个条目，图形不同，满足「两两不同」；S4 的 N1 修好的单元测试我另做了突变核对，见第 8 节；AC-3 的「没有额外装饰」，读样式取值并与相邻分组逐项比，再看图；AC-10 里差异的裁决，见 4.4；AC-25 的并排看图与断言清单，见 4.2 与 C-53（5 个页面内容区的像素差为 0）；AC-28 里文档句子是否写对，见 4.3 逐处读过。

## 7. 环境性红项与基线复现

下面头上红的每一项，都在基线 `11-24-t9-dev-base` 以同条件复跑过；基线没红而头红的才算缺陷，本轮没有出现。表中头上没有红、只是值得对账的几行（`check:ui` 的跨 revision 抖动、`request-list-interactions`、基线的 `verify:browser` 红）单独标明了。

| 项 | 头 | 基线 | 归因 |
|---|---|---|---|
| `node scripts/check-docs.mjs` | 9 条缺失路径、22 条行号引用，全在 2026-11-12 三凭证额度需求的交付文档里 | 逐行相同的 9 与 22 | 基线既有 |
| `pnpm test:self` | 505 个用例 495 过、10 败 | 504 个用例 494 过、10 败，失败名单逐条相同（头多出的 1 个用例是 S3 新增的规划器自测，通过） | 基线既有、环境性：本机无 go、无 zip、Windows 符号链接权限，另有 fixed-waits 棘轮 |
| fixed-waits 棘轮 | 5 个旧环境脚本共 8 处固定等待（环境脚本两个、`plugin-quota`、`oauthManagement` 探针、`quota-auto-refresh-round`），允许 0 | 同样 5 个文件 8 处 | 基线既有；本需求新增的 `capabilities-nav/` 下文件是 0 处 |
| `pnpm verify:toolchain` | 退出码 0，只告警：Node 24.16.0 对仓库钉的 22.23.2 | — | 环境性，非严格模式只告警 |
| `pnpm test:fast --base 11-24-t9-dev-base` 的纯逻辑一路 | Windows 报 `The command line is too long.`（脚本把整份改动文件清单放在命令行里） | — | 环境性；同一组测试单跑 `pnpm test:logic` 90 个套件全过 |
| `pnpm demo:generate --check` | 未跑（需要 go；已知 Windows 上必红 12 个叶子路径，`time.Local` 不认 `TZ`） | — | 环境性。替代证据：`pnpm check:demo`（演示数据新鲜度与覆盖）通过，155 条响应覆盖 20 条控制台路由；容器里 `internal/demo` 的 Go 测试通过 |
| `scripts/native-release.test.mjs`（tar、符号链接） | 在 `test:self` 的 10 个失败里 | 同 | 环境性 |
| `insights` 手机视口场景（跨 revision 的 flake） | `check:ui --all` 81 个场景全过，没有出现 | — | 本轮未复现 |
| Go 门禁 | 本机无 go；容器 golang:1.27.1 里 `go vet ./...`、`TZ=UTC go test ./...` 全过（42 个包 ok） | — | Go 在本需求里零改动 |
| `pnpm verify:browser` 不设 `TZ` | 二进制起不来，`invalid_timezone`，退出码 1 | 同样 | 环境性：Windows 上 Go 认不出本机时区名，cases.md 的 C-51 本来就写了要设 `TZ`；设 `TZ=Asia/Shanghai` 后头 381 项全过，见 3.2 |
| `pnpm verify:browser` 基线的 1 个红 | 头上没有 | 「密钥管理页只有一个容器」`cards=2 nested=0` | 基线既有：仓库自有密钥面板在同一页里是第 2 张卡，面板搬走后转绿，见 3.2 |
| `request-list-interactions` 场景（cases.md 提到的曾经的基线红） | `check:ui --all` 81 个场景全过，本轮没有出现 | 本轮没有在基线单独复跑 | 本轮未复现 |

基线复现证据放在 `net/s8/` 下：`gate-base-test-self.log` 对 `gate-head-test-self.log`，`c64.txt` 里头与基线的全量文档检查，`bundle-base.log` 对 `bundle-head.log`。

## 8. 引用的审查结论

- **S6（`review-s3-r1.md`，审查 S3）**：结论「要修 3 条，无严重项」。R1 页头刷新会连不活跃缓存一起重读；R2 审批链接页返回入口没有自动化断言；R3 一处过时注释。这三条 S4 都在 PR 头上修了，S7 做过突变核对。本轮的独立核对：①R1：C-25 在真实 SPA 上先敲搜索词、开详情切统计窗再关掉，点页头刷新，网络里只读当前显示的四条，没有再读搜索过的旧词与关掉的详情；②R2：C-32 在中英文各点一次审批链接页尾的入口，落在 `/repo-keys` 并高亮「仓库自有密钥」，基线同一入口回到密钥管理页；③S6 的 F1～F12 逐项一致：本轮用桩层加真实层各做了一遍并排对照（C-15～C-23），结论相同，并补上了 S6 因本机没有 go 而没跑的构建产物层。S6 的 O1（只有 S3 时新页在外壳里按「未登记路由」兜底）已由 S4 的菜单条目兑现：C-24、C-28 的高亮、面包屑、标签页标题都对。
- **S7（`review-s4-r1.md`，审查 S4）**：结论「要修 3 条，无严重项」：N1 图标测试比别名而不是图形；N2 额度拆借交付文档里 lendingMount 旧名还剩 4 行；N3 一处旧条数注释。S4 在 `5d3e5b4e` 修完，本轮重测：C-08 的界面层签名比对（20 个固定条目渲染出的 SVG 两两不同，收起栏与展开栏同一条目签名相同）照旧通过；N1 的新单元测试我自己做了突变，把仓库自有密钥的图标换成与「日志」同一图形的孪生别名，测试现在会红（`/repo-keys draws the same graphic as /logs`），不改时 18 个用例全过；N2 在排除冻结证据后 `git grep lendingMount` 为空；N3 注释已写成 twenty；`check-docs` 全量仍是基线的 9 与 22。S7 报告 4.5 与 X2 提到的「S4 头 `check:ui --all` 81 个场景全过」，我独立复跑得到同一结论：81 个场景全过，569.6 秒（`6c83fbfc` 源码上跑，之后的 4 个提交不涉及产品逻辑）。S7 的 X1～X5 我没有异议；其中 X2 说手机扫描没登记 `/model-groups` 与 `/quota-lending`，这两项在手机抽屉里的点击与落页本轮在 C-42 里验过。

## 9. 缺口与用例本身的问题

### 9.1 缺口（如实写，不当成通过）

- 浏览器只测了 Edge（Chromium 内核）一款；手机宽度是 Edge 的触摸加 390×844 模拟，不是真机；没有 Firefox、Safari。
- 真实环境造不出非零用量与审计平台推送（没有经这些密钥的真实请求、没有审计平台），这两项只在桩层对照（C-16、C-21），与用例的约定一致。
- `pnpm demo:generate --check` 没跑：它需要 go，且已知 Windows 上必红；用 `pnpm check:demo` 与容器里的 `internal/demo` 测试替代，见第 7 节。
- 基线上没有跑 Go 门禁：本需求 Go 零改动，头上的 `go vet`、`go test` 已全过。
- Orca 内置浏览器只覆盖主要 P0 用户路径的真点与截图，其余用例走 Edge 脚本，原因见 2.3。
- 本机文件中 Git 把 `.cmd` 与 `.ps1` 在检出时转成 CRLF 的告警是 Git 的换行设置，与本需求无关。

### 9.2 用例本身的问题（只记，不改用例）

1. **C-17 的批准步骤**：用例写「先不填原因确认一次，再填原因 nav-check 确认」，实际面板里批准没有确认弹窗和原因输入，点「批准」直接发 `POST …/approve`，体里是空 `reason`；只有驳回要原因（原因为空时确认按钮禁用）。B 与 H 逐项相同，按现状对照。
2. **C-62 命令 2**：用例命令里列了 `docs\guides\*.md`，这个目录在基线与头里都不存在，`Select-String` 报路径错误并整条中止，表面上「没有命中」。去掉该路径后重跑：基线命中 `docs/agent-capabilities.md` 里把面板写在密钥管理页上的那句，头没有命中。
3. **C-23 第 4 步的审计事件**：B 的「密钥管理」页载入时会读网关密钥列表，审计里多一条「查看客户端密钥明文」，H 的 `/repo-keys` 页不读网关密钥，没有这一条。这与 AC-24（新页只读面板自己的数据）一致，但用例写「事件的数量与种类相同」不够准，按面板各操作产生的 `repo_key.*` 事件对照。
4. **C-19 的「提交后」提示**：提示在仓库栏停止输入约 400 毫秒后才请求仓库现状，不是每敲一个字符出现，脚本按「等到那次读取返回」读。
5. **C-20 的校验**：用例写「不选类别或不填原因提交，读校验」，实际是原因为空时确认按钮禁用，没有校验提示文案，两端相同。
6. **C-31、C-28 的面包屑分隔符**：用例与 PRD 用「›」，界面里分隔符是「/」（DOM 里是两节文字加分隔符），按两节文字判定。
7. **C-41 的悬停名称**：收起栏的条目名称只靠原生 `title`，没有自绘的悬停提示，基线同样；按「名称在 title、等于展开栏名称」判定。
8. **C-55 的键盘**：侧栏是一个整体控件，方向键在条目间移动，不是每个条目一个 Tab 停靠点，按方向键加回车判定。
9. **C-48 的接口路径**：用例写 `POST …/api-keys`、`DELETE …/api-keys/<序号>`，实际用的是 `/api/v1/management/api-keys` 与 `/api/v1/management/api-keys/0`，两端都被拒（403，`demo_operation_refused`）。

### 9.3 PM 中途授权的两行改动与窗口核对

测试中 PM 发现浩文桌面上弹出标题为 omc-nav.exe 的终端窗口：detached 的 node 没有控制台，它再起控制台程序时 Windows 会为后者新开控制台窗口。授权只改两处：`scripts/acceptance/lending-experience-env.mjs` 里起服务的 `spawn(BINARY, [], {…})` 与 `scripts/acceptance/experience-env.mjs` 里同类的 `spawn(executable, [], {…})`，各在 `cwd: root,` 后加一行 `windowsHide: true,`，单独一个提交（`3a1ad144`）。验证：用 `env.mjs` 的方式（detached 的 node 父进程）起一个环境，起之前与起之后各列一次可见窗口的进程，两份名单逐行相同（可见窗口只有原来就在的那一个终端窗口，没有新增 WindowsTerminal、conhost、OpenConsole 窗口，没有标题含 omc 的新窗口）；演示模式的二进制、`experience.py start` 起的两个体验环境也各核对一次，结果相同。列表在 `net/s8/windows-before-patch.txt`、`windows-after-patch.txt`、`windows-after-patch-demo.txt`、`windows-after-patch-experience.txt`。此后新起的环境（包括基线）一律用打了补丁的脚本；已经弹出的窗口没有被手动关过，最后随服务一起退出消失。

## 10. 体验环境

**保留**，供浩文点着看，到 2026-11-25 00:00 由 `experience.py` 的看门狗自动关闭。两个环境都只监听 127.0.0.1，数据全是假的（仓库名 `example/...`，假 CPA 与演示数据），没有真实账号。造的数据：仓库自有密钥 5 把（生效 2、停用 3：轮换、滥用、泄露各 1），待审批 2（其中 1 条是重新启用），待交付 1；带页面的假插件已停用，所以侧栏恰好是 20 个固定条目。

体验卡（原文）：

```
体验链接：http://127.0.0.1:18491/omc/
  登录时填管理密钥：omc-e2e-management-key（测试夹具里的常量，不是真实密钥）
演示模式链接：http://127.0.0.1:18489/dashboard（不用登录，写操作会被拒）

体验要点（点着看，约 3 分钟）：
 ① 左侧菜单「观测」和「插件」之间多了「能力」三项：模型组、仓库自有密钥、额度拆借。
    点「仓库自有密钥」进新页，看到待审批 2、待交付 1、密钥 5 和审计推送，顶部路径是「能力 / 仓库自有密钥」。
 ② 点「密钥管理」：只剩网关客户端密钥列表，页底多一行指路「前往仓库自有密钥」，点过去落在新页。
 ③ 点「额度拆借」：菜单高亮和顶部路径落在「能力」下。
    再点左上角收起成窄栏，再把浏览器缩到手机宽度（390 左右）点开导航，各看一眼。

用例：64 条，通过 64、失败 0、阻塞 0、未测 0；P0 40 条全部通过。
报告：E:/works/Newoahil__oh-my-cpa/11-24-t9-s8-claude/docs/delivery/2026-11-23-omc-capabilities-nav/acceptance/report-r1.md
关闭：python E:/works/Homan-context/_meta/scripts/loop/experience.py stop t9-capabilities-nav
      python E:/works/Homan-context/_meta/scripts/loop/experience.py stop t9-capabilities-nav-demo
```

**怎么重启**（用的是本工作区 tmp 目录里按合并后源码构建的二进制与数据；清理本工作区之前先关体验环境，否则环境会失效）：

```
python E:/works/Homan-context/_meta/scripts/loop/experience.py start t9-capabilities-nav --cwd E:/works/Newoahil__oh-my-cpa/11-24-t9-s8-claude --cmd E:\works\Newoahil__oh-my-cpa\11-24-t9-s8-claude\scripts\acceptance\capabilities-nav\experience-env.cmd --url http://127.0.0.1:18491/omc/ --timeout 150
python E:/works/Homan-context/_meta/scripts/loop/experience.py start t9-capabilities-nav-demo --cwd E:/works/Newoahil__oh-my-cpa/11-24-t9-s8-claude --cmd E:\works\Newoahil__oh-my-cpa\11-24-t9-s8-claude\scripts\acceptance\capabilities-nav\experience-demo.cmd --url http://127.0.0.1:18489/dashboard --timeout 150
```

重启后数据目录里的造数还在，不用重新造；想回到初始数据，先关环境再删 tmp 下的 experience-data 目录，重启后运行 experience-seed.mjs。

**窗口核对**：体验环境经 `experience.py` 起来之前与之后各列一次有可见窗口的进程，两份名单逐行相同，没有新增终端窗口，没有标题含 omc 的窗口（最后一次重启前后 `WindowsTerminal` 都是 0 个、`OpenConsole` 数量不变；`conhost` 进程数随起进程略增，它们不带窗口）；原因与两行改动见 9.3。

**Orca 内置浏览器**：体验环境起来之后，用 Orca 内置浏览器打开体验链接与演示链接，登录、点了「仓库自有密钥」「密钥管理」「前往仓库自有密钥」「额度拆借」「模型组」、收起栏、深浅色，都能打开、页面内容与上面的要点相符，截图在 shots 下的 `experience.png`、`experience-repo-keys.png`、`experience-demo.png` 和各 `*-orca-*` 文件；手机宽度那一步 Orca 的设备模拟没有生效，由 Edge 脚本测过（C-42），不影响浩文在自己的浏览器里缩窗口看。

## 11. 证据索引

截图与日志都不入库（PM 的约定），只在这台机器上；入库的只有验收脚本与本报告。下面的位置都相对于 `E:/works/Newoahil__oh-my-cpa/11-24-t9-s8-claude/docs/delivery/2026-11-23-omc-capabilities-nav/acceptance/`。

| 内容 | 位置 | 说明 |
|---|---|---|
| 截图（两百多张） | shots 目录 | 命名：`C-<用例号>-<B 或 H>-<说明>-<语言或宽度>.png`，B 是基线，H 是被测头；`C-…-orca-…` 是 Orca 内置浏览器真点时截的；`experience.png`、`experience-repo-keys.png`、`experience-demo.png` 是体验环境的首页、新页与演示模式 |
| 每个检查点的读数 | net/s8 下的 `results-<脚本>.json` | 每条用例一项：结果、检查点数、失败项、备注、证据文件；`details` 里是每个检查点的名称与读到的值（已打码） |
| 并排对照读数 | net/s8 下的 `parity-*.json` | B 与 H 每一步的可见文字、控件、弹窗、读请求与写请求，C-15～C-23、C-47 的对照依据 |
| 请求对照 | net/s8 下的 `c49-requests.json` 等 | C-49 三个页面的数据请求清单 |
| 脚本的运行日志 | net/s8 下的 `run-*.log`、`rerun-*.log`、`run-all-summary.txt` | 每个脚本的控制台输出（已打码）；最终一轮与重跑的日志分开存 |
| 门禁日志 | net/s8 下的 `gate-*.log`、`bundle-*.log`、`c61.log` | 头与基线各一份；`gate-final-verify-browser.log` 是设了 `TZ` 的 `verify:browser`，`-no-tz` 的是没设时的失败 |
| 文档类读数 | net/s8 下的 `c62-*.txt`、`c64-*.txt` | C-62 的搜索输出（头与基线）、C-64 的逐份与全量检查 |
| 二进制校验 | net/s8 下的 `dist-hashes-*.txt` | 头与基线二进制的 SHA-256 与构建输入 |
| 窗口核对 | net/s8 下的 `windows-*.txt` | 起环境前后的可见窗口名单，对应 9.3 与第 10 节 |
| Orca 会话记录 | net/s8 下的 `orca-session.log` | Orca 内置浏览器每一步命令的记录 |
| 密钥扫描 | net/s8 下的 `secret-scan-*.log` | 被跟踪的树在干净副本里的扫描输出，对应 3.1 |
| 总表 | net/s8 下的 `report-case-table.md`、`report-ac-table.md` | 本报告 4.1 与第 6 节的原始表 |
| 验收脚本 | 仓库的 `scripts/acceptance/capabilities-nav/` | `run-all.ps1` 一条命令重跑整套；环境变量 `T9_ONLY=C-xx,C-yy` 只重跑指定用例并把结果合并回该脚本的完整结果文件 |

**两条测试路径**：能自动化的全部走 Edge 无头加 playwright-core 的脚本（并排对照要请求拦截造数据、挂起读取拉长占位、固定时钟、390 宽手机、抓每个请求，Orca 内置浏览器没有等价能力）；主要 P0 用户路径另用 Orca 内置浏览器真点并截图（原因与范围见 2.3）。两条路径的结论一致，没有出现互相矛盾的读数。
