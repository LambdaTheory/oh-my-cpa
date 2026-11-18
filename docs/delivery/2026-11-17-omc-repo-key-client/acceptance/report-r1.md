---
type: acceptance-report
slug: 2026-11-17-omc-repo-key-client
status: draft
updated: 2026-11-17
branch: 11-17-t5-integration
commit: 6db0300b996006572cae458b7b430980ddfd35e7
cases: docs/delivery/2026-11-17-omc-repo-key-client/ 下的 cases.md（在 Newoahil/11-17-t5-s2-claude 的 92f6d075，未合入集成分支）
summary: "S14 集成验收 r1：44 条用例 38 通过 / 5 部分 / 1 不可测；要修 2 条（R-05 文档 P2、--json 未知 flag 边界 P3）；build/vet/两包单测/e2e 全绿"
---

# 集成验收报告：分 key 使用端闭环（T5-S14）

## 0. 结论摘要

1. 在集成分支 `11-17-t5-integration`（`6db0300b`）上独立复跑了门禁：`go build ./...`、`go vet ./...`、`go test -count=1 ./internal/keyclient ./cmd/oh-my-cpa` 全绿；`pnpm test:key-e2e` 的两个 e2e 全过（CLI 七步全链路 115 项断言、无一项失败）。
2. 按 `cases.md` 44 条用例执行：**38 条通过，5 条部分做到，1 条不可测**。5 条部分做到的原因均不是功能缺陷（方案未要求的断言、或已声明的平台/真实环境缺口），不要求本次返工。
3. **范围内要修 2 条**：① `docs/repo-key-client.md` 仍称非 loopback `http://` 会带 `insecure_base_url` 警告，实际 `gen` 不警告、`exec`/`install` 只打自然语言警告且该 code 从未构造（P2，R-05 未闭环）；② 未知 flag 加 `--json` 时 stderr 先输出一行 flag 文本再输出 JSON，与运行手册「one JSON object / one line」不符（P3，R-04 边界）。
4. 明文边界、三家注入、`0600`、`--dry-run` 零写入、purge 后 `key_not_found` 均有实跑证据；Windows ACL、真实 Claude Code OAuth 优先级、真实 Codex/OpenCode 接受度、真实 CPA 网关、真实 OMC `--remote` 仍是缺口，未写成通过。
5. **`cases.md` 不在集成分支上**（只在 `Newoahil/11-17-t5-s2-claude` 的 `92f6d075`）；本轮按该提交执行，建议 PM 合并 S2 分支或在 S12 补上，否则交付物缺执行清单。

## 1. 被测对象与基准

| 项 | 值 |
|---|---|
| 被测分支 / 提交 | `11-17-t5-integration` / `6db0300b996006572cae458b7b430980ddfd35e7` |
| 验收工作区 | `E:/works/Newoahil__oh-my-cpa/11-17-t5-s14-claude`（从集成分支切出，未改产品代码） |
| 验收用例 | `docs/delivery/2026-11-17-omc-repo-key-client/` 下的 cases.md，44 条，AC-1～AC-9（取自 `92f6d075`；集成分支上没有该文件） |
| 方案 | `docs/delivery/2026-11-17-omc-repo-key-client/impl-plan.md`（含 §12 PM 已定八条） |
| 前两轮审查 | `acceptance/review-r1.md`（代码，P0×1/P1×1/P2×10）、`acceptance/review-docs.md`（文档，P0×1/P1×3/P2×7） |
| 门禁基线 | PM 2026-11-17 亲手跑过 build/vet/两包单测；本报告是独立复跑，不引用其结论 |
| 环境 | Windows 11 + Docker `golang:1.27.1`；源码只读挂载，`--network none`；临时 `HOME`/`OMC_HOME` 在容器 `/tmp`；带 `ASSISTANT_NO_SEND=1` |
| 未触碰 | 真实 `~/.omc`、`~/.claude`、`~/.codex`、`~/.config/opencode`；真实 CPA、真实账号、生产、Dokploy |

## 2. 复跑的门禁（命令 → 结果）

全部在 `golang:1.27.1`、`--network none`、`GOPROXY=off`、`GOFLAGS=-mod=readonly`、`TZ=UTC`、`ASSISTANT_NO_SEND=1`、源码 `:ro` 下执行；Windows 上用 PowerShell 调 docker（Git Bash 会把 `-w /src` 改写成 Windows 路径）。

| 检查 | 命令 | 结果 | 证据 |
|---|---|---|---|
| 构建 | `go build ./...` | 退出 0 | `artifacts/gate-build.log` |
| vet | `go vet ./...` | 退出 0 | `artifacts/gate-vet.log` |
| 单测 | `go test -count=1 ./internal/keyclient ./cmd/oh-my-cpa` | `ok internal/keyclient 104.046s`、`ok cmd/oh-my-cpa 61.819s` | `artifacts/gate-unit.log` |
| 单测子集（-v） | `go test -count=1 -v -run 'Test.*(Status\|Purge\|Perm\|0600\|PrivateKey\|Generate\|Accept\|Exec\|Install)' …` | **126 个 `--- PASS`，0 FAIL** | `artifacts/gate-unit-targeted.log` |
| e2e | `pnpm test:key-e2e` | `PASS TestKeyCLIFullLoop (19.01s)`、`PASS TestRepoKeyDeliveryLoop (1.41s)` | `artifacts/gate-e2e.log`、`artifacts/e2e-evidence/` |
| Windows 交叉编译 | `GOOS=windows go build ./internal/keyclient ./cmd/oh-my-cpa` | 退出 0（不运行 ACL） | `artifacts/gate-windows-build.log` |
| 文档门禁 | `node scripts/check-docs.mjs` | 退出 1，**剩余红项全部在既有 T18 交付文档**（9 missing-path + 25 line-reference），本需求文件 0 红项 | 终端输出；与 review-docs §D 一致 |
| 文档门禁自测 | `node --test scripts/check-docs.test.mjs` | 13 pass / 0 fail | 终端输出 |

e2e 证据：`key-cli-loop.json` 14 个步骤、**115 个断言全部为 true**，步骤为 `gen → apply → approve → ciphertext → accept → exec-claude → exec-codex → exec-opencode → install → purge → exec-after-purge → no-plaintext`；`key-loop.json` 11 个步骤通过。复用路径另有审查专用 e2e（`review-reuse.json`，7 项断言全过），见 C-38。

## 3. 用例逐条结果（44 条）

判定：**通过 / 部分做到 / 不可测**。证据列用 `artifacts/` 下的相对路径；`case-logs/*.log` 里的 `ASSERT … PASS` 是实际断言。

### AC-1 七步闭环可执行、五条子命令都在

| 用例 | 结果 | 证据 |
|---|---|---|
| C-01 命令面齐全、帮助可读、未知子命令退出码 | 通过 | `case-logs/case-01-surface-gen.log`：六个 `--help` 退出 0；`key nope` 退出 2、stderr 有 `unknown command` 与用法、stdout 为空 |
| C-02 七步与五条命令的对应关系可走通 | 通过 | 同上：空 home `status` 退出 0 并指 `key gen`；伪造信封后 `exec` 退出 1、错误点名 envelope，无 panic |
| C-03 既有子命令不回归 | 通过 | `mcp -h` 用量在 stderr、stdout 为空；`updater -h` 退出 0；`repo_key_query` 仍注册（`internal/operations/repo_keys.go` 的 `registerRepoKeys`），`repo_key_apply/status/ciphertext` 在 e2e 实跑；两包单测全过 |

### AC-2 `key gen`

| 用例 | 结果 | 证据 |
|---|---|---|
| C-04 首次生成：私钥落盘、stdout 只有公钥 | 通过 | `case-01`：stdout 一行 `x25519:`、base64 解码 32 字节；私钥 32 字节；stderr 只有 `generated a key …` |
| C-05 私钥 0600（Unix 必验） | 通过（Unix）；Windows 为缺口 | `stat -c %a` = `600`；`su nobody … cat` 权限拒绝；源码 `internal/keyclient/home.go` 的 `FILE_MODE=0o600`、`internal/keyclient/home_unix.go` 的 `Chmod`。Windows ACL 运行时断言见 §6 |
| C-06 仓库标识归一化与文件名安全 | 通过 | `case-01`：三种写法复用同一把 key、同一个文件；`../../etc/passwd`、`a/../../b`、空串、TAB、300 字符均退出 2；`$OMC_HOME` 外无新文件（P2-7 已修） |
| C-07 同一仓库重复 `key gen` | 通过 | `case-01`：第二次退出 0、公钥与私钥哈希不变、stderr 说 `reused`（方案 §5.3 的复用语义） |
| C-08 目录不可写 / 只读文件 / `OMC_HOME` 是普通文件 | 通过 | `case-logs/case-01b-c08.log`：只读 keys 目录退出 1 且报 permission denied；home 是普通文件退出 1 且报 `not a directory`；可写 home 恢复生成；无 0 字节残留。只读私钥同仓 `gen` 按方案复用（退出 0）——用例原预期与方案 §5.3 冲突，按方案判 |
| C-09 只打印公钥：stdout / stderr / 落盘扫描 | 通过 | `case-01`：私钥 hex/base64 不在 gen 两路输出；除私钥文件自身外，工作目录内无任何文件含私钥 |
| C-10 单测：私钥创建 0600 断言 | 通过 | `gate-unit-targeted.log`：`TestHomePermissionsAreOwnerOnly`、`TestPrivateKeyKeepsItsModeUnderAPermissiveUmask`、`TestAcceptedEnvelopeFileIsOwnerOnlyAndAtomic` 全 PASS；源码 `internal/keyclient/keystore.go` 的 `WritePrivateKey` 创建后 `Chmod`，`internal/keyclient/home_unix_test.go` 的 `assertMode` 断言 `Perm()` |

### AC-3 `key status`

| 用例 | 结果 | 证据 |
|---|---|---|
| C-11 三种状态都能读懂 | 通过 | `case-02`：A 空 home 说没有 key 并指 `key gen`；B 有 key 无信封说 envelope 未取到并指 `accept`/ciphertext；C 显示 `approved`、mask、envelope 时间，无明文/私钥 |
| C-12 `status --json` 机器可读 | 通过 | `case-logs/status-json/status-{A,B,C,C2}.json`；本机用 Node `JSON.parse` 校验通过，顶层含 `home/base_url/base_url_source/orphan_files/repos/runtimes` |
| C-13 单测：状态判定 | 通过 | `TestKeyStatusListsTheThreeStates`、`TestKeyStatusJSONIsParseable`、`TestBuildReportTellsTheThreeStatesApart`、`TestBuildReportOnAMachineWithNoKeys`、`TestRefreshRemoteKeepsLocalStateWhenOneReadFails` 等 PASS |
| C-14 审批状态来源与离线可用性 | 通过（真实 OMC 为缺口） | `case-02`：离线 status 仍显示本地 `approved`；无 `OMCPA_SERVER_URL/MANAGEMENT_KEY` 时 `--remote` 退出 3 `remote_not_configured`；连 `127.0.0.1:9` 退出 1、错误可读、本地视图保留；真实 OMC `--remote` 见 §6 |
| C-15 坏文件（坏信封 / 0 字节私钥） | 部分做到 | `case-02c`：`status` 退出 0、无 panic，但只按“文件存在”报告，不标记损坏；`exec` 对坏信封报 `envelope_invalid`、0 字节私钥报 `key_unreadable`（可读、退出非 0）。方案 §2.4 未要求 status 校验文件内容，记范围外观察，见 §7 |

### AC-4 `key exec`

| 用例 | 结果 | 证据 |
|---|---|---|
| C-16 只给子进程注入变量 | 通过 | `case-02`：父 shell `env` 前后 diff 为空；子进程 `ANTHROPIC_BASE_URL` 等于配置值；token 的 SHA-256 与测试明文逐字节相同、长度一致；继承的 `ANTHROPIC_API_KEY` 被移除 |
| C-17 stdout/stderr/落盘/进程 argv 无明文 | 通过 | `exec` 两路输出无明文；工作目录内只有测试自己的 `secret.txt` 含明文；扫描 `/proc/*/cmdline` 无明文 |
| C-18 子进程退出码透传、自身失败可区分 | 通过 | 42→42、0→0、缺 key→4；SIGTERM 子进程→1（Go `ExitCode` 返回 -1，方案 §3.4 明确“不额外转换”，用例写 143 属预期偏差，见 §7） |
| C-19 三家运行时各自变量 | 通过 | claude：仅 `ANTHROPIC_BASE_URL/AUTH_TOKEN`，无 `OPENCODE_*`/`OMC_REPO_KEY`；codex：`OMC_REPO_KEY` 与五条 `-c` override 逐字匹配，argv 无明文；opencode：`OPENCODE_CONFIG_CONTENT` 合法 JSON、含 `provider.oh-my-cpa`、保留 `other`/theme、baseURL 带 `/v1`、apiKey 哈希等于明文；未知 runtime 退出 2 并列出三家 |
| C-20 `exec` 不落盘 | 通过 | `key exec` 前后 `$OMC_HOME` 与临时 HOME 的文件路径/大小/mtime 快照完全一致 |
| C-21 错误路径可读、可区分 | 通过 | `case-02` + `case-02b`：无信封 `envelope_missing`/4；未知仓库 `key_not_found`/4 并给 `key gen` 行；错钥匙信封 `decrypt_failed`/1（有 base URL 时）；非法 JSON/`alg`/`v` 均 `envelope_invalid`/1；31 字节私钥 `key_unreadable`/1；错误均无明文 |
| C-22 `--` 分隔符与参数透传 | 通过 | `case-02b`：`ARGV_JSON=["%s","a b","--flag"]`，空格参数保持一个参数；缺少 `--` 时退出 2、报 `unexpected argument` |
| C-23 信封 0600 与责任边界 | 部分做到 | `case-02`：私钥/信封/state/config 均 600；信封由 `key accept` 写（方案 §2.3）。把信封改成 644 后 `exec` 仍成功且不警告——方案未要求读取时校验权限，记范围外观察 |
| C-24 并发与重复执行 | 通过 | 5 个并发 `exec` 全部退出 0，无半读信封/panic |
| C-25 明文不外泄统一扫描 | 通过 | `case-02`：`$WORK` 内除测试 `secret.txt` 外零命中；e2e 的 `no-plaintext` 步骤同时扫描所有 CLI 流、HTTP body 与服务日志，115 项断言全过 |

### AC-5 `key install`

| 用例 | 结果 | 证据 |
|---|---|---|
| C-26 首次 install：备份 + 警告 | 通过 | `case-03c`：claude 目标 600、备份与原文件逐字节相同且 600；stderr 有 plaintext/后果/purge 四条警告；`env` 只增两个键，`OTHER`/`permissions`/`model` 保留 |
| C-27 重复 install 幂等 | 通过 | `case-03c`：第二次退出 0、文件哈希不变、token 键只有 1 个；备份保留（方案：不自动限份） |
| C-28 三家只改自己的一项 | 通过 | `case-03c`：codex 受管块 + `model_provider`，保留 `[model_providers.other]`；opencode 写 `provider.oh-my-cpa`（v1 单数，无 `models`），保留 `provider.cpa`/theme/model；claude 保留其它键；项目级 `.claude/settings.json` 未被写 |
| C-29 目标文件缺失 / 损坏 / 目录不存在 | 部分做到 | `case-03c`/`case-03d`：父目录不存在时退出 1、`install_failed` 可读、原文件不变（用例允许“创建或明确报错”）；损坏 JSON 与未闭合表头 TOML 退出非 0、原文件逐字节不动；一行 junk（`this is [ not toml`）被当作普通顶层文本接受并追加受管块——方案的行式编辑器只承诺在“无法确定顶层边界”时拒绝，记范围外观察 |
| C-30 install 后的配置能被运行时读到（只读探测） | 部分做到 | 本机 OpenCode **1.19.5** `opencode debug config` 输出里出现 `provider.oh-my-cpa.options.baseURL`（用 install 产物做只读探测）；Codex 0.169.0 无只读配置转储（`debug models` 不含 provider），Claude 2.1.326 需真实运行才加载 `--settings`，见 §6 |

### AC-6 `key purge`

| 用例 | 结果 | 证据 |
|---|---|---|
| C-31 purge 清私钥、信封、状态记录 | 通过 | `case-03`：本仓库私钥/信封/state 记录消失，`status` 回到无 key；另一个仓库的 key 哈希不变；输出含“CPA 里的 key 仍有效” |
| C-32 purge 清 install 写过的配置项 | 通过 | `case-03c`：三家配置里都不再含明文；claude 保留 `OTHER`、codex 恢复 `model_provider = "other"` 并保留 `[model_providers.other]`、opencode 保留 `provider.cpa`；含本 key 的备份被删，其余保留 |
| C-33 purge 后 `exec` 失败、重复 purge 幂等 | 通过 | `case-03`：`exec` 退出 4、`key_not_found` 可读；重复 purge 退出 0、输出 `nothing to remove; the repository was already clean` |
| C-34 单测：purge 影响面 | 通过 | `TestPurgeRemovesEverythingOfOneRepository`、`TestPurgeRemovesOnlyBackupsThatHoldTheKey`、`TestPurgeRefusesABorrowedValue`、`TestPurgeDryRunChangesNothing`、`TestPurgeIsIdempotent`、`TestPurgeOnAMissingHomeCreatesNothing` 等 18 个 purge 用例 PASS |
| C-35 purge 路径安全与失败语义 | 通过 | `case-03`：`../../../etc/passwd` 退出 2 `repo_invalid`、空 repo 退出 2、保留的 key 未动；`case-03c`：keys 目录不可写时退出 1、`purge_pending` 可读、`$OMC_HOME` 外无删除；`case-09`：值被手改后 `install_not_owned`、值保留、私钥保留 |
| 附加：`--dry-run` 零写入 | 通过 | `case-logs/case-08-dryrun.log`：dry-run 前后 `$OMC_HOME` + 临时 HOME 的路径/大小/mtime 快照完全一致，并列出 `would remove` |

### AC-7 端到端

| 用例 | 结果 | 证据 |
|---|---|---|
| C-36 全链路 e2e | 通过（fake CPA） | `pnpm test:key-e2e`：`TestKeyCLIFullLoop` 19.01s、115 项断言全 true——gen 只打印公钥、apply→审批→ciphertext→accept、三家 exec 各自 200、install 备份+警告、purge 全清、purge 后 exec 退出 4 且子进程未启动、所有流无明文 |
| C-37 人工浏览器审批走查 | **不可测** | 本构建 `internal/web/dist` 只有 `.gitkeep`（前端未构建），审批页无法渲染；需要 `pnpm build` 后重建二进制并起浏览器。审批 HTTP 路由本身已由 e2e 用与页面相同的 `POST /omc/api/v1/management/repo-keys/applications/{id}/approve` 验证 |
| C-38 免审复用路径 | 通过 | 审查专用 e2e（临时副本，不进产品）：同一 caller/公钥第二次 apply 为 `approved`/`kind=reuse`、新 application id、网关 key 数量仍 1、第二份 ciphertext accept 后 exec 仍 200；证据 `e2e-evidence/review-reuse.json` |
| C-39 e2e 清场 | 通过 | 容器全部 `--rm`，无残留 `oh-my-cpa` 进程；e2e 的临时目录在 `t.TempDir()`；工作区 `git status` 只有本报告的 `acceptance/artifacts/`（以及 node_modules 被 gitignore） |

### AC-8 单测

| 用例 | 结果 | 证据 |
|---|---|---|
| C-10 / C-13 / C-34 与 0600 断言 | 通过（Unix） | 见上；`gate-unit.log` 两包全绿、`gate-unit-targeted.log` 126 个 `--- PASS`。Windows ACL 运行时断言为缺口（§6） |

### AC-9 文档

| 用例 | 结果 | 证据 |
|---|---|---|
| C-40 明文不外泄逐条检查（日志/错误/verbose/shell history/git） | 通过 | `key` 无 `--verbose`（方案未提供）；错误全文见 C-21；`case-02` 扫描零命中；e2e 的 `no-plaintext` 扫描 CLI 流/HTTP body/服务日志；`git status`/`git log` 无夹具或证据产物；报告与脚本里没有把明文写进命令参数的示例 |
| C-41 文档从零照做能走通 | 通过 | `case-logs/case-05-doc-walk.log`：按 `docs/repo-key-client.md` 走 status→gen→关联 application→accept→exec（三家变量形状）→install→dry-run→purge→exec 失败，17 项断言全过；命令、`--json` 字段、accept 输出行与页面一致 |
| C-42 禁止事项齐备且具体 | 通过 | `docs/repo-key-client.md` §4 八行表格覆盖：不打印、不进对话、不进 git、不提交运行时配置、不把 `$OMC_HOME` 放仓库、不用 shell 重定向写信封、不转发密文、purge 不是吊销；§7 另写同用户 `/proc/<pid>/environ`、Windows 内存转储、install 落盘、备份可能含其他凭据等已知风险 |
| C-43 能力索引与架构/安装文档同步 | 通过 | `docs/agent-capabilities.md`「Repository-owned API keys」新增 `oh-my-cpa key` 六命令与指向 `docs/repo-key-client.md` 的指针；`docs/architecture.md`（模块表与运行时说明）、`docs/install.md`、`docs/install-for-agents.md`、`docs/operations.md`、`docs/testing.md` 已同步；`node --test scripts/check-docs.test.mjs` 13 pass；`check-docs` 只剩既有 T18 文档红项 |
| C-44 文档命令面与 `--help` 逐字一致 | 通过 | 文档出现的自有 flag（`--repo/--runtime/--remote/--json/--base-url/--replace/--config/--home/--dry-run/--application-id/--help`）在六份 `--help` 里全部存在；`--version` 属 opencode 自身、`--model` 文档明确写“不提供”；原文见 `case-logs/help/` |

## 4. AC-1～AC-9 汇总

| AC | 结论 | 说明 |
|---|---|---|
| AC-1 七步闭环可执行、五条子命令在 | 做到 | 六条命令（含方案新增 `accept`）；`key nope` 退出 2；七步全链路 e2e 通过 |
| AC-2 `key gen` | 做到（Unix） | 私钥 0600、stdout 只有 `x25519:<base64>`、归一化/穿越/超长安全、重复 gen 复用；Windows ACL 为缺口 |
| AC-3 `key status` | 做到 | 三态可读、`--json` 合法、离线可用、`--remote` 缺配置/不可达可读；坏文件只报存在性（方案未要求校验，观察） |
| AC-4 `key exec` | 做到 | 内存解密、三家注入、父环境不变、不落盘、argv/流无明文、退出码透传；信号退出码按方案不转换 |
| AC-5 `key install` | 做到 | 显式、写前备份+警告、三家精确编辑、幂等、损坏 JSON/TOML 不覆盖；junk 单行 TOML 记录为观察 |
| AC-6 `key purge` | 做到 | 全清/单运行时/dry-run 零写入/指纹所有权/备份清理/路径安全/幂等；CPA key 仍有效提示齐全 |
| AC-7 端到端可复现 | 做到（fake CPA） | `TestKeyCLIFullLoop` 115 项断言 + 复用路径 review-only e2e；真实 CPA 与浏览器审批为缺口 |
| AC-8 单测 | 做到（Unix） | 126 个 `-v` 用例全过；Windows ACL 运行时断言为缺口 |
| AC-9 文档 | **部分做到** | 手册可照做、禁止事项与风险齐全、索引同步、命令面一致；**R-05 的 `insecure_base_url` 表述不实**（见 §5） |

## 5. 范围内要修

### F1（P2，文档）R-05 未闭环：运行手册声称的 `insecure_base_url` 警告不存在

- **文件**：`docs/repo-key-client.md` §1「The gateway address」（当前第 117-120 行）。
- **复现**：
  1. `oh-my-cpa key gen --repo org/other --home <tmp> --base-url http://10.9.9.9:8317`
     实际 stderr 只有 `generated a key …` / `saved the gateway address …`，**没有任何警告**。
  2. `oh-my-cpa key exec --runtime claude --repo org/repo --home <有信封的 home> --base-url http://10.9.9.9:8317 -- true`
     实际 stderr：`warning: http://10.9.9.9:8317 is plain http to a host that is not this machine; the key and the traffic are readable on the network`——可读，但**没有 `insecure_base_url` 这个 code**。
  3. `oh-my-cpa key install --runtime claude --repo org/repo --home <home> --base-url http://10.9.9.9:8317`
     实际 stderr 另有一条 `warning: … is plain http to a host other than this machine, so the key crosses the network unencrypted`，同样没有 code。
- **预期**：文档与实现一致。二选一：实现里构造并输出 `insecure_base_url`（并说明 gen 是否提示），或把文档改成「`exec`/`install` 对非 loopback `http://` 打一条普通警告；`gen` 只记录地址、不警告；警告不带稳定 code」。
- **实际**：`CODE_INSECURE_BASE_URL` 在 `internal/keyclient/errors.go` 定义后从未被构造；两处警告分别在 `internal/keyclient/exec.go`（exec 的 plain-http 警告）与 `cmd/oh-my-cpa/keyinstall.go`（install 的 plain-http 警告），都没有 code；`gen` 完全不警告。
- **为什么是范围内**：R-05 是 `review-docs.md` 的 P2 项，PM 在 S10 派发说明第 6 条明确要求「改实现让文案统一，或者改文档，选改动小的那个并在进度文件里说明理由」；S10b 的提交 `7dbef501` 没有处理它。
- **证据**：`artifacts/case-logs/case-07b-reverify.log`（三条命令的实际 stderr）。
- **严重程度**：P2（不阻断任何流程，但文档对稳定 code 的承诺不成立，会误导按 code 分支的脚本）。

### F2（P3，`--json` 边界）未知 flag + `--json` 时 stderr 有两行，不是一行 JSON

- **复现**：`oh-my-cpa key gen --json --bogus`
- **实际**：stderr 为两行：
  ```text
  flag provided but not defined: -bogus
  {"code":"usage","message":"flag provided but not defined: -bogus"}
  ```
- **预期**：运行手册第 513-521 行承诺「With `--json`, a failure writes one JSON object to stderr … the object is one line」。
- **原因**：`newKeyFlagSet` 把 flag 包的输出接到 stderr（`cmd/oh-my-cpa/key.go`），解析失败时 flag 先打印一行；随后 `writeKeyFailure`（同文件）再写 JSON。
- **建议**：请求了 `--json` 时把 `FlagSet` 的输出改为丢弃（或只进 JSON），由 `writeKeyFailure` 统一输出；若决定保留，则在手册注明「flag 解析错误会先有一行文本」。六个子命令的**运行期**失败已经严格一行 JSON（`case-04`），只有用法错误受影响。
- **证据**：`artifacts/case-logs/json-errors/usage.err`。
- **严重程度**：P3（只影响未知 flag 这类用法错误的机器解析）。

## 6. 不可测的缺口

| 缺口 | 为什么本机测不了 | 需要什么条件 |
|---|---|---|
| Windows ACL 运行时断言（PM 第 7 条） | 本机没有 Windows Go 工具链；Docker 里只能 Linux 断言，Windows 只能 `GOOS=windows go build` | 在 Windows 上用 Go 跑 `home_windows_test.go` 的 ACL 断言，或一台 Windows + Go 的 runner |
| 真实 Claude Code 在 OAuth 登录态下的优先级 | 需要真实登录态与真实请求，且会消耗真实账号额度；运行手册的 `TODO(实现确认)` 仍在 | 真实 Claude Code + OAuth 登录 + 一次可丢弃的请求；按手册建议先 `claude auth status` |
| 真实 Codex / OpenCode 对注入的接受度 | e2e 只证明环境形状与 fake 网关 200；真实运行时是否按注入发起请求需要真实上游。本机只做了 OpenCode 1.19.5 的只读配置探测（`opencode debug config` 能看到 `provider.oh-my-cpa`），Codex 0.169.0 无只读配置转储，Claude 2.1.326 需真实运行才加载 `--settings` | 三家 CLI + 可接受测试 key 的真实网关；只做只读探测不能算通过 |
| 真实 CPA 网关 | 全程只用 fake CPA（仓库自带夹具 + e2e 内嵌 fake），没有连真实 CLIProxyAPI | 一台可控的 CPA 实例 + 一把真实签发、可停用的测试 key |
| 真实 OMC 的 `key status --remote` | `--remote` 需要真实 console URL + 管理密钥；本轮只测了缺配置（退出 3）和连接拒绝（退出 1），成功路径只有 mock 单测 | 真实 OMC 实例、只读管理密钥、一个已登记的 application |
| 需要真实账号 / 生产环境的任何一项 | 按验收约束不碰 | — |
| C-37 浏览器人工审批走查 | 本构建 `internal/web/dist` 只有 `.gitkeep`，二进制不含可渲染的前端；审批 HTTP 路由已由 e2e 验证，页面属既有能力、本需求未改 | `pnpm build`（生成 SPA dist）→ 重建二进制 → 起 fake CPA/OMC → 浏览器打开 `approval_url` 并截图 |

## 7. 范围外观察（只记录，不要求本次改）

1. **C-15：`key status` 不标记坏文件**。0 字节私钥、非法信封在状态页仍显示为“存在”，只有 `key exec` 会报 `key_unreadable`/`envelope_invalid`。方案 §2.4 只要求按文件名/存在性报告，AC-3 也未要求完整性校验；若希望状态页能标坏文件，应作为后续增强。
2. **C-18：信号终止的子进程退出码是 1，不是 143**。Go 的 `ExitCode()` 对信号返回 -1，`internal/keyclient/exec.go` 的退出码回退取 1；方案 §3.4 明确「被 Ctrl-C 中断时以控制台/进程组行为为准，工具不额外转换」，故按方案判过。
3. **C-23：读到 644 的信封不警告**。方案只保证写入 0600；读取时校验权限不在方案与 AC 内。
4. **C-29：一行 junk TOML 被接受**。`this is [ not toml` 不是表头，行式编辑器把它当普通顶层文本，仍追加了受管块；未闭合表头、损坏 JSON 都会被拒绝且原文件不变。方案的行式编辑器只承诺拒绝“无法确定顶层边界”的情况。
5. **`key status --json` 的字段省略**。`decided_at`、`repo_state`、`repo_state_updated_at`、`remote_error` 在从未取到值时按 `omitempty` 省略；方案 §5.7 的示例把它们画成常驻字段，脚本作者应按“未知时省略”处理。另外 `base_url_source` 的 `flag` 对 `status` 不可达（status 没有 `--base-url`），手册照方案列了四值。
6. **P2-3 / P2-8 未处理**：`ValueFingerprint` 与 `KeyValueFingerprint` 仍是两份相同实现（`internal/keyclient/accept.go` 与 `internal/keyclient/install.go`）；Windows ACL 测试只断言“没有宽 ACE”，未断言当前用户 SID 有 full control（`internal/keyclient/home_windows_test.go`）。两项都不在 PM 的 S10 修复选择里，且 P2-8 的运行时证据本来就是缺口。
7. **`check-docs` 的既有红项**：9 个 missing-path + 25 个 line-reference 全部在 `docs/delivery/2026-11-12-omc-three-credential-quota/impl-plan.md`（T18），本需求文件逐文件 0 红项；与 review-docs 的结论一致。
8. **`cases.md` 不在集成分支**：只在 `Newoahil/11-17-t5-s2-claude` 的 `92f6d075`。本报告按该提交执行；建议 PM 在 S12 一并合入或补文件，否则交付缺执行清单。

## 8. 前两轮审查项复验

| 项 | 结论 | 证据 |
|---|---|---|
| R-01 / P0 集成分支编译失败 | **已修** | `go build ./...` 退出 0；重复常量已归并 |
| R-02 `status` 不读 `OMCPA_CPA_BASE_URL` | **已修** | `case-07b`：人类输出显示 `(env)`，`--json` 为 `"base_url_source": "env"`；单测 `TestKeyStatusReadsTheGatewayFromTheEnvironment` |
| R-03 `exec --base-url` 不写 config | **已修（文档）** | 手册第 109-111 行改成“gen/install 会记住，exec 只对本次生效”；`case-02` C-20 证明 exec 零写入 |
| R-04 `--json` 失败无 code | **已修（六子命令）** | `case-04` 六条失败均一行 `{"code","message"}`、退出码不变、stdout 空；未知 flag 边界见 F2 |
| R-05 `insecure_base_url` 不一致 | **未修** | 见 F1 |
| R-06 accept 输出与文档不一致 | **已修** | 手册第 278-292 行与实际输出一致；C-41 实测 `accepted org/repo: key sk-cpa-01***cdef at envelopes/…` |
| R-07 install 备份说明缺条件 | **已修** | 手册第 344-346 行写明“目标存在时才有备份” |
| R-08 换仓需要 `--replace` | **已修** | 手册第 349-352 行写明“with `--replace`（或先 purge 另一个仓库）” |
| R-09 `status --json` 字段不完整 | **已修** | 手册补齐 `orphan_files`、`installed`、`decided_at`、`remote_error`、`config_path/backup_path`；实际 JSON 顶层与文档一致（省略规则见 §7.5） |
| R-10 `docs/testing.md` TODO | **已修** | 全仓已无该 TODO；`docs/testing.md` 的「Repository key client regressions」一节是真实文件与用例映射 |
| R-11 Windows `--home` 理由 | **已修（文档）** | 手册第 61-64 与错误码表写清“已有目录只检查不重新 ACL；继承父 ACL 会被拒绝 + icacls 指引” |
| P2-1 `status --help` 措辞 | **已修** | `KEY_STATUS_USAGE` 现称 `--remote` 与 `--application-id` 都会写 state |
| P2-2 手册 `--base-url` 记忆说法 | **已修** | 同 R-03 |
| P2-4 `purge --runtime` 文案 | **已修** | `case-07b`：`removed claude's installed value; the private key and envelope are still on this machine` |
| P2-5 空仓库 purge 建空 home | **已修** | `case-07`：never-used home 下 0 个条目，文案 `nothing to remove` |
| P2-6 `install_not_owned` 中止整轮 | **已记录为安全取向** | `case-09`：值保留、`install_not_owned` 可读、私钥保留；手册第 365-369 行已写明会保留并报告 |
| P2-7 `..`/绝对路径 `--repo` 被接受 | **已修** | `case-01` C-06、`case-03` C-35：均退出 2 `repo_invalid`，不写/不删 `$OMC_HOME` 外文件 |
| P2-9 `status --remote` 缺省 agent/device | **已修** | 单测 `TestNewRemoteClientReportsTheSameCallerAsMCP` PASS |
| P2-10 布尔命名 | **未修（PM 定为不改）** | 按 PM 2026-11-17 决策记录，纯风格不改 |

## 9. 证据与复跑方法

### 9.1 证据位置

全部在 `docs/delivery/2026-11-17-omc-repo-key-client/acceptance/artifacts/`：

- 门禁：`gate-build.log`、`gate-vet.log`、`gate-unit.log`、`gate-unit-targeted.log`、`gate-e2e.log`、`gate-windows-build.log`。
- e2e：`e2e-evidence/key-cli-loop.json`（115 项断言）、`key-loop.json`、`review-reuse.json`、`omc.log`。
- 进程级：`case-logs/case-*.log`（首轮与更正轮都在，更正说明见 `artifacts/README.md`）、`status-json/`、`json-errors/`、`help/`、`configs/`（明文已脱敏为 `sk-cpa-SYNTHETIC-REDACTED`）。
- 脚本：`case-0*.sh`、`debug-dryrun.sh`（review-only）。

### 9.2 复跑命令（Windows 上用 PowerShell）

```powershell
$src='E:/works/Newoahil__oh-my-cpa/11-17-t5-s14-claude'
$art="$src/docs/delivery/2026-11-17-omc-repo-key-client/acceptance/artifacts"

docker run --rm --network none -v "${src}:/src:ro" -v "omc-go-mod:/go/pkg/mod" -v "omc-gocache:/root/.cache/go-build" -w /src -e GOPROXY=off -e GOFLAGS=-mod=readonly -e TZ=UTC -e ASSISTANT_NO_SEND=1 golang:1.27.1 go test -count=1 ./internal/keyclient ./cmd/oh-my-cpa

$env:OMC_E2E_EVIDENCE_DIR="$art/e2e-evidence"; pnpm test:key-e2e

# 进程级（先构建二进制到 artifacts；keytest-helper/probe 是评审专用工具，源码在 tmp/review，见 README）
docker run --rm --network none -v "${src}:/src:ro" -v "${art}:/evidence" -v "omc-go-mod:/go/pkg/mod" -v "omc-gocache:/root/.cache/go-build" -w /src -e GOPROXY=off -e GOFLAGS=-mod=readonly golang:1.27.1 sh -c 'CGO_ENABLED=0 go build -trimpath -o /evidence/oh-my-cpa ./cmd/oh-my-cpa'
docker run --rm --network none -v "${art}:/evidence" -w /evidence golang:1.27.1 bash /evidence/case-01-surface-gen.sh
```

评审专用的 `keytest-helper`（按 `x25519` 公钥封真实信封）与 `keytest-probe`（替身运行时，只打印变量名/长度/哈希）源码在验收工作区的 `tmp/review/src/cmd/`，不进入产品分支；它们复用 `internal/repokey`，不复制密码学实现。

### 9.3 未跑/未引用

- 没有引用 PM 的门禁结论作为证据；所有结果来自本报告的独立复跑。
- 没有连接真实 CPA、真实 OMC、真实账号或生产；所有 URL 都是 `127.0.0.1`。
- 没有修改产品代码、实现计划或验收用例；工作区只新增本报告与 `acceptance/artifacts/`。

---

**派发**：works 2026-11-17#T5-S14（父记录 `2026-11-17-2026-11-17-T5`）。
**被验收提交**：`6db0300b996006572cae458b7b430980ddfd35e7`（`11-17-t5-integration`）。
