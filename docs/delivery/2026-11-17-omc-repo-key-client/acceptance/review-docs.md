# 验收审查：仓库 key 使用端运行手册与实现

- 日期：2026-11-17
- 基线：`11-17-t5-integration` / `9da1e954`
- 审查对象：`docs/repo-key-client.md` 与 `key` 子命令、`internal/keyclient` 的实现
- 对照基准：`docs/delivery/2026-11-17-omc-repo-key-client/impl-plan.md` §3、§5、§6、§9、§12；需求 AC-9
- 结论：**P0 1 条、P1 3 条、P2 7 条**。其中 P0 是基线构建失败，必须在任何合并或验收之前修复；三条 S5 TODO 均已给出结论；AC-9 的内容充分性通过；文档门禁只剩既有基线红项。

## 0. 方法与可复现性

本机没有 Go，构建与测试全部用 Docker 配方（`golang:1.27.1`、`--network none`、卷 `omc-go-mod` + `omc-gocache`）。所有会写盘的命令都使用临时 `HOME` / `OMC_HOME`，没有读取真实的 `~/.omc` 或 `~/.claude`，也没有使用真实密钥；测试用明文 key 是合成值，只存在于仓库外的临时目录。

**基线本身不可构建。** 直接对 `9da1e954` 跑 `go build ./cmd/oh-my-cpa` 得到：

```text
# github.com/oh-my-cpa/oh-my-cpa/internal/keyclient
internal/keyclient/runtime.go:33:2: ENV_OPENCODE_CONFIG redeclared in this block
	internal/keyclient/install.go:33:2: other declaration of ENV_OPENCODE_CONFIG
internal/keyclient/runtime.go:44:2: OPENCODE_PROVIDER_ID redeclared in this block
	internal/keyclient/install.go:26:2: other declaration of OPENCODE_PROVIDER_ID
internal/keyclient/tomlblock.go:18:2: CODEX_PROVIDER_ID redeclared in this block
	internal/keyclient/runtime.go:47:2: other declaration of CODEX_PROVIDER_ID
```

为了继续核运行手册，我把工作树导出到仓库外的临时副本，**只删除这三处重复常量声明**（两边的值与注释语义完全相同），构建了 Linux 与 Windows 二进制。下文所有"实际输出"都来自这份副本；它不是基线可构建的证据，而是绕过 R-01 后对行为的下限验证。删除重复声明后，同一容器配方的 `go test -count=1 ./internal/keyclient ./cmd/oh-my-cpa` 通过（102.064s / 64.572s），说明 R-01 是构建阻塞，不是更深的行为失败。

命令输出、构建日志和临时配置都在仓库外的 `E:/works/.tmp-t5s11-review/evidence`；本报告不进 git 的部分只有这些日志。

## 1. 编号结论

### R-01 基线不可构建

- **文档位置**：不适用（合并后构建门禁）
- **问题**：`9da1e954` 的 `internal/keyclient` 有三组重复常量声明，`key` 的六条命令无法从该提交构建；运行手册无法在真实基线上执行，任何"已跑通"结论都只能来自打过补丁的副本。
- **对照的实现与证据**：上面的 `go build` 输出。重复来自 `runtime.go`（S3 的实现）与 `install.go` / `tomlblock.go`（S8 的实现）：`ENV_OPENCODE_CONFIG`、`OPENCODE_PROVIDER_ID`、`CODEX_PROVIDER_ID` 各声明两次，值都是 `"OPENCODE_CONFIG"` 与 `"oh-my-cpa"`。合并 `034659cf` 把两条线合到一起后没有跑构建门禁。删除这三处重复声明后，`go build` 与 `go test ./internal/keyclient ./cmd/oh-my-cpa` 均通过。
- **建议怎么改**：保留一处声明（任意一边，值相同），删除另外一处；把 `go build ./cmd/oh-my-cpa` 加进合并后的最小门禁，避免再次出现"合并完成但二进制不存在"。修复前不要让这份基线的验收结论进入合并。
- **严重程度**：**P0**

### R-02 `key status` 不读 `OMCPA_CPA_BASE_URL`，`base_url_source` 的 `env` / `flag` 不可达

- **文档位置**：§2 Step 1 — see what is already here（`--json` 字段说明）
- **问题**：文档说 status 显示"the remembered gateway address (and which of the three sources supplied it)"，并把 `base_url_source` 定义为 `flag` / `env` / `config` / `absent`。实际 status 只把 `config.json` 传进 `ResolveBaseURL`，既不读 `OMCPA_CPA_BASE_URL`，也没有 `--base-url` 参数，所以 `flag` 与 `env` 永远不会出现。
- **对照的实现与证据**：`cmd/oh-my-cpa/keystatus.go` 的 `runKeyStatus` 调用 `ResolveBaseURL("", "", config)`。在一个只有私钥、没有 `config.json` 的临时 home 上设置 `OMCPA_CPA_BASE_URL=http://127.0.0.1:9999` 后运行 `key status`，实际输出：

  ```text
  gateway: not set; pass --base-url or set OMCPA_CPA_BASE_URL
  ```

  `key status --json` 同时给出 `"base_url_source": "absent"`。同一台机器上 `key exec` 会使用这个环境变量，所以 status 的"没有网关"与 exec 的实际行为矛盾。方案的 `status --json` 契约（§5.7）把 `env` / `flag` 列为可能的 source。
- **建议怎么改**：在 `runKeyStatus` 里把 `os.Getenv(keyclient.ENV_CPA_BASE_URL)` 传给 `ResolveBaseURL`（flag 仍为空）；如果 PM 决定 status 只看持久化配置，则把运行手册和 §5.7 收窄为 `config` / `absent`，并说明环境变量只影响 `gen` / `exec` / `install`。
- **严重程度**：**P1**

### R-03 `key exec --base-url` 不会把地址记进 `config.json`

- **文档位置**：§1 The gateway address（顶层 `key --help` 的环境变量说明重复了同一句话）
- **问题**：文档写"A successful command given `--base-url` remembers the value in `config.json`"。`gen` 与 `install` 会写，`exec` 不会；方案 §5.2 也写明 `gen` / `exec` / `install` 带 `--base-url` 成功时写。
- **对照的实现与证据**：在一个没有 `config.json` 的临时 home 上运行 `key exec --runtime claude --repo org/repo --home <tmp> --base-url http://127.0.0.1:8317 -- cmd /c "exit 0"`，退出 0，随后 `config.json exists after exec --base-url: False`。`WriteConfig` 的调用点只有 `keystore.go` 的 `Generate` 和 `cmd/oh-my-cpa/keyinstall.go` 的 install 成功路径；`exec.go` 的 `RunChild` 只解析不持久化。
- **建议怎么改**：在 `exec` 成功启动子进程前或子进程退出后，按 gen / install 的方式在持锁状态下写入 flag 值；或者把文档和 help 改成"`key gen` 与 `key install` 会记住，`key exec` 只对本次运行生效"。
- **严重程度**：**P1**

### R-04 失败时 `--json` 没有 `code` 字段，错误仍是 stderr 文本

- **文档位置**：§5 Failures and what they mean — 错误码段开头
- **问题**：文档写"Error codes are stable and are written at the front of the message, and as `code` in `--json` output"。实际 `--json` 只影响成功输出；失败时 stdout 为空，stderr 是 `code: message` 文本，没有 JSON 对象。方案 §5.6 同样要求 `--json` 时把 code 放进 JSON。
- **对照的实现与证据**：`key status --remote --json` 在缺少 `OMCPA_SERVER_URL` / `OMCPA_CPA_MANAGEMENT_KEY` 时，stdout 为空，stderr 为：

  ```text
  remote_not_configured: --remote needs OMCPA_SERVER_URL and OMCPA_CPA_MANAGEMENT_KEY; nothing was changed
  ```

  `runKey` 打印错误时不看命令是否收到 `--json`；`statusJSON` / `acceptJSON` 都是成功结构，没有错误信封。
- **建议怎么改**：实现统一的 JSON 错误输出（至少 `{"code": "...", "message": "..."}`，写到 stderr 或 stdout 需先定契约），或删除文档中的 `--json` 错误码承诺，明确"失败只有 stderr 文本和退出码"。
- **严重程度**：**P1**

### R-05 `insecure_base_url` 警告与实现不一致

- **文档位置**：§1 The gateway address
- **问题**：文档写非 loopback 的 `http://` 会带一个 `insecure_base_url` 警告出现在 stderr。实际二进制从不输出这个字符串：`exec` 与 `install` 打的是固定英文 `warning: ... is plain http ...`，`gen` 对同样的地址完全不警告。`CODE_INSECURE_BASE_URL` 在 `internal/keyclient/errors.go` 中定义，但没有任何调用点。
- **对照的实现与证据**：`key gen --home <tmp> --repo Org/Repo --base-url http://10.9.9.9:8317` 的 stderr 只有：

  ```text
  generated a key for Org/Repo (org/repo) at keys/org%2Frepo-57b681c5.key
  saved the gateway address in ...\config.json
  ```

  `exec` / `install` 在同一地址上只打上面提到的 `warning:` 文本，没有 code。`insecure_base_url` 在 `errors.go` 里映射到退出码 0，但从未被构造。
- **建议怎么改**：把三条命令的明文 http 提示统一到一个带稳定 code 的 helper（`gen` 是否提示由是否持久化地址决定），或把文档改成"`exec` / `install` 会在非 loopback http 时打警告；`gen` 只保存地址、不传输 key，因此不警告"。
- **严重程度**：**P2**

### R-06 `key accept` 的实际 stdout / `--json` 与文档模板不一致

- **文档位置**：§2 Step 5 — take the sealed key
- **问题**：文档给出"Expected stdout, one line"为 `accepted <repo-display>: key <key-mask>`；实际输出在末尾多出 `at <envelope-file>`，并且在 display 与 normalized 不同时打印 `Org/Repo (org/repo)`。重复接受时动词变成 `replaced the envelope for`。文档还说 `--json` 是"a machine-readable line"，实际是缩进多行的 JSON。
- **对照的实现与证据**：实际 stdout：

  ```text
  accepted Org/Repo (org/repo): key sk-cpa-te***tail at envelopes/org%2Frepo-57b681c5.json
  ```

  `--json` 输出是 `MarshalIndent` 的 12 行对象。命令行测试 `TestKeyAcceptStoresTheEnvelopeAndPrintsOneLine` 只断言"一行"，没有钉住具体前缀/后缀，所以文档与实际输出没有在测试里对齐。
- **建议怎么改**：把文档模板改成实际格式并补上首次/替换两种动词；或者简化输出为文档承诺的 `accepted <repo-display>: key <key-mask>`（会丢掉信封路径，信息更少）。`--json` 的描述改成"pretty-printed JSON"，不要写"line"。
- **严重程度**：**P2**

### R-07 `key install` 的备份说明少了一个条件

- **文档位置**：§2 Step 7 — make it persist, or clean up
- **问题**：文档写"It copies the target file to `$OMC_HOME/backups/` first"。这只在目标文件**已经存在**时成立；目标文件不存在时没有备份，也没有 `previous file backed up to` 输出。`key install --help` 自己写的是 "first when it exists"，两者不一致。
- **对照的实现与证据**：首次对不存在的临时 `settings.json` 运行 `key install --runtime claude`，stdout 只有 `installed ...` 和 `remove it with ...`，没有备份行；再次对已存在文件运行（带 `--replace`）才出现 `previous file backed up to ...`。`Install` 中 `existed` 为 false 时 `backupConfig` 返回空路径。
- **建议怎么改**：文档补上"when the target file exists"；如果希望总是有备份，先创建一个空文件再走备份流程，但那是行为变更。
- **严重程度**：**P2**

### R-08 换仓安装需要 `--replace`，文档句子读起来像自动替换

- **文档位置**：§2 Step 7 — one slot per runtime
- **问题**：文档写"One slot per runtime per machine: installing another repository replaces the slot and the previous values survive only in the backup."实际在没有 `--replace` 时会以 `config_owned_by_another_repo` 退出 5，必须加 `--replace` 或先 purge 另一个仓库。
- **对照的实现与证据**：同一 home 中已安装 `org/repo` 的 Codex 配置，再对 `org/other` 安装且不加 `--replace`：

  ```text
  config_owned_by_another_repo: ...\codex-config.toml already holds a key for org/repo; install org/other with --replace, or run `oh-my-cpa key purge --repo org/repo` first
  ```

  退出码 5。加上 `--replace` 后成功，旧值进入备份。
- **建议怎么改**：把句子改成"installing another repository replaces the slot **with `--replace`** (or after purging the other repository)"，与错误码表里的 `config_owned_by_another_repo` 行保持一致。
- **严重程度**：**P2**

### R-09 `key status --json` 的字段清单不完整

- **文档位置**：§2 Step 1 — see what is already here
- **问题**：文档把 `--json` 描述为"the stable shape for a script"，只列了 `home`、`base_url`、`base_url_source`、`repos[]`、`runtimes{}`。实际顶层还有 `orphan_files`；每个 repo 还有 `installed`（map）、`decided_at`、`remote_error`；`runtimes{}` 的值含 `config_path` 与 `backup_path`。脚本作者按文档读不到这些稳定字段。
- **对照的实现与证据**：实际输出包含：

  ```json
  "orphan_files": {"keys": 0, "envelopes": 0}
  ```

  `internal/keyclient/status.go` 的 `statusJSON` / `repoStatusJSON` 还包含 `decided_at`、`remote_error`、`installed`；`runtimeStatusJSON` 包含 `config_path` 与 `backup_path`。
- **建议怎么改**：补齐顶层与 repo/runtime 字段表，或明确"这里只列核心字段，完整 schema 以 `statusJSON` 为准"，避免把稳定契约说成不完整清单。
- **严重程度**：**P2**

### R-10 `docs/testing.md` 的 TODO 未替换为真实包路径与用例名

- **文档位置**：`docs/testing.md` — Repository key client regressions 末尾的 `TODO(实现确认)`
- **问题**：TODO 仍在，理由是"S2a–S4b 在各自分支上，写出路径会让 check-docs 在文件还不存在时失败"。现在包和测试都已合入基线，这个理由不成立；README 要求的"the path should be backticked once it exists"也没有做。
- **对照的实现与证据**：`internal/keyclient` 及其测试、`internal/keyclient/e2e` 都在；`node scripts/check-docs.mjs --file docs/testing.md` 为 0 finding，加回反引号路径不会变红。真实测试文件与代表用例见 §2 的 TODO-2 结论。
- **建议怎么改**：删除 TODO，补一段按层归属的测试名映射（见 §2），并把 `internal/keyclient` 用反引号写出。
- **严重程度**：**P2**

### R-11 Windows 上已存在但仅继承 ACL 的 `--home` 目录被拒绝，文档理由不准确

- **文档位置**：§1 `OMC_HOME` 段落；§5 `home_insecure` 行
- **问题**：文档说目录"cannot be locked down"才报 `home_insecure`，错误表说"or it already lets group/other write"。实际 Windows 的 `verifyOwnerOnly` 还要求目录的 DACL 带 `SE_DACL_PROTECTED`；一个由用户在普通父目录下创建、只继承父目录 ACE（没有 Everyone/Users 等宽 ACE）的目录也会被拒绝，工具不会尝试收紧它。
- **对照的实现与证据**：预先用 PowerShell 创建临时目录后运行 `key gen --home <pre-created>`：

  ```text
  home_insecure: ... still inherits permissions from its parent; restrict it to this user
  ```

  删除目录让工具自己创建后，同一命令成功，`keys/`、`envelopes/`、`backups/` 与 `config.json` 都按预期生成。`home_windows.go` 的 `verifyOwnerOnly` 先检查 `SE_DACL_PROTECTED`，再检查宽 ACE；`Ensure` 对已存在目录只检查不改（S5 的既定取舍）。
- **建议怎么改**：二选一：(a) 让 `verifyOwnerOnly` 接受"继承 DACL 但不含 Everyone/Authenticated Users/Builtin Users/Guests 的 allow ACE"的目录（仍拒绝宽 ACE），或在 (b) 运行时手册和错误信息里明确 Windows 的 `--home` 必须是工具创建的目录或已移除继承的目录，并给出可操作的 `icacls` 指引。
- **严重程度**：**P2**

## 2. 三条 S5 TODO 的结论

### TODO-1 Claude Code 在 OAuth 登录态下 `ANTHROPIC_AUTH_TOKEN` 是否压过登录态

**结论：代码无法定论，仍需真实 Claude Code 验证。** `cmd/oh-my-cpa/keyexec.go` 只做参数解析并调用 `RunChild`；`internal/keyclient/runtime.go` 的 `injectClaude` 只删除继承的 `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_BASE_URL`，再设置自己的两个变量。整条链路不读取、不检测、不修改 Claude Code 的 OAuth 登录态；测试也没有覆盖 OAuth 场景。`key install` 写的是用户级 settings 的 `env`，与 `exec` 同属环境变量层，因此同一不确定性也适用于 install。`claude auth status` 子命令在本机存在，但按约束没有读取真实登录态。保留运行手册的 TODO 原文是诚实的；建议把句子说清"这条对 `exec` 和 `install` 都成立"，并在真实环境做一次 `claude auth status` / 退出登录对照实测后再删 TODO。

### TODO-2 `docs/testing.md` 的测试文件与用例名

**结论：TODO 应替换为下面的真实映射。** 全部路径已存在，且逐文件 check-docs 为 0 finding。

| 文档里的层 | 实际文件 | 代表用例 |
| --- | --- | --- |
| home 解析、布局、文件名、`home_insecure` | `internal/keyclient/home_test.go`、`home_unix_test.go`、`home_windows_test.go` | `TestResolveHomePrecedence`、`TestEnsureCreatesLayout`、`TestFileStemMapping`、`TestHomePermissionsAreOwnerOnly`、`TestHomeACLIsOwnerOnly` |
| `key gen` stdout 纯度、复用、修复 | `internal/keyclient/keystore_test.go`、`cmd/oh-my-cpa/key_test.go` | `TestGenerateCreatesThenReuses`、`TestGenerateRepairsStateFromAnExistingKey`、`TestKeyGenStdoutIsOnlyThePublicKey`、`TestKeyGenSecondRunReusesAndSaysSo` |
| `key accept` 解析阶梯、错 key 拒绝 | `internal/keyclient/accept_test.go`、`cmd/oh-my-cpa/keyaccept_test.go` | `TestParseCiphertextResultAcceptsBothShapes`、`TestAcceptRefusesAnEnvelopeForAnotherKey`、`TestAcceptReplacesThePreviousEnvelope`、`TestKeyAcceptStoresTheEnvelopeAndPrintsOneLine` |
| `key status` 本地报告、孤儿、`--remote` 部分失败 | `internal/keyclient/status_test.go`、`remote_test.go`、`cmd/oh-my-cpa/keystatus_test.go` | `TestBuildReportTellsTheThreeStatesApart`、`TestBuildReportOnAMachineWithNoKeys`、`TestRefreshRemoteKeepsLocalStateWhenOneReadFails`、`TestKeyStatusRemoteNeedsConfiguration` |
| `key exec` helper 子进程、明文边界、预检查 | `internal/keyclient/exec_test.go`、`runtime_test.go`、`cmd/oh-my-cpa/keyexec_test.go` | `TestExecClaudeChildSeesTheKeyAndNothingElse`、`TestExecCodexChildSeesTheKeyAndTheProviderFlags`、`TestExecOpenCodeChildSeesTheMergedConfiguration`、`TestExecWritesNothing`、`TestExecMissingKeyNeverStartsTheChild` |
| `key install` / `key purge` 临时配置、备份、回滚、指纹 | `internal/keyclient/install_test.go`、`jsondoc_test.go`、`tomlblock_test.go`、`purge_test.go`、`cmd/oh-my-cpa/keyinstall_test.go`、`keypurge_test.go` | `TestInstallClaudeRefusesAnExistingAPIKey`、`TestInstallBackupHoldsThePreviousFileExactly`、`TestInstallRequiresReplaceForAForeignSlot`、`TestPurgeDryRunChangesNothing`、`TestPurgeRefusesAValueItDoesNotOwn`、`TestKeyInstallConflictIsExitFive` |
| e2e 七步 | `internal/keyclient/e2e/e2e_test.go` + 同目录 fixture | `TestRepoKeyDeliveryLoop`；入口 `scripts/key-e2e.mjs` / `test:key-e2e` |

建议替换文本的要点：TODO 删除；`internal/keyclient` 加反引号；按上表列出文件与代表用例，不再写"S2a–S4b 在各自分支"的理由。

### TODO-3 `key status` 默认输出形状

**结论：整体形状与文档一致，但 gateway 的 source 描述要改（见 R-02）。** 在一个带 key、envelope、application 的临时 home 上实际运行 `key status`：

```text
home: E:\works\.tmp-t5s11-review\home-win
gateway: http://10.9.9.9:8317 (config)

Org/Repo (org/repo)
  private key: keys/org%2Frepo-57b681c5.key
  envelope:    envelopes/org%2Frepo-57b681c5.json, received 2026-11-17T12:13:31Z
  application: app-review-0001 (issue)
  approval:    approved (accept, 2026-11-17T12:13:31Z)
  key mask:    sk-cpa-te***tail
  installed:   no runtime holds this key
```

文档描述的 home、网关、每个 repository 的私钥/信封/application/approval/installed 都在；没有列宽依赖，适合人读。只有 `(config)` 这一项与"three sources"的表述不符：没有 `--base-url` 的 `flag` 来源，`env` 也不被读取。若 R-02 按代码修好，文档可原样保留；若按文档收窄，则把 "three sources" 改成 "config.json, or absent"。

## 3. 通过项

### A. 逐字一致

- **命令与参数**：6 条子命令（`status` / `gen` / `accept` / `exec` / `install` / `purge`）与运行手册一致；27 个参数/开关（含 `-- <command>`）全部能在 `--help` 和解析代码里找到，拼写一致。`key --help` 列出六个命令、环境变量与退出码；未知子命令、未知 flag、缺 `--runtime`、缺 `-- <command>` 都退出 2。证据：docker 构建的二进制（打过 R-01 补丁的副本）的 `key --help`、六个 `--help` 与错误路径实测。
- **错误码存在性**：错误码表里的 26 个 code 都在 `internal/keyclient/errors.go` 的 `CODE_*` 常量里；`insecure_base_url` 也存在但未使用（单列 R-05）。退出码映射与 `exitCodes` 表逐条一致：实测 `command_required` / `runtime_unknown` / `config_owned_by_another_repo` = 2/2/5，`remote_not_configured` = 3，`key_not_found` / `envelope_missing` / `install_not_installed` = 4，`install_not_owned` / `purge_pending` / `exec_failed` = 1。
- **路径与布局**：临时 home 实际生成 `keys/`、`envelopes/`、`backups/`、`state.json`、`config.json`、`.lock`，与 §1 的布局一致；私钥文件名由 repository 身份派生（示例 `keys/org%2Frepo-57b681c5.key`），运行手册没有写出错误的文件名规则。`state.json` / `config.json` 不含私钥、明文或信封正文。
- **三家运行时的变量与落点**：Claude Code 注入 `ANTHROPIC_BASE_URL=<root>` 与 `ANTHROPIC_AUTH_TOKEN=<plaintext>`，并删除 `ANTHROPIC_API_KEY` / 旧 `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_BASE_URL`；Codex 通过 `OMC_REPO_KEY` 与 `-c model_provider`、`.name`、`.base_url`、`.env_key`、`.wire_api="responses"`；OpenCode 通过 `OPENCODE_CONFIG_CONTENT` 写 `provider.oh-my-cpa.options.baseURL=<root>/v1` 与 `apiKey`。install 路径 `$CLAUDE_CONFIG_DIR/settings.json`、`$CODEX_HOME/config.toml`、`$OPENCODE_CONFIG` 与默认用户级路径都与 `RuntimeConfigPath` 一致；Codex 的受管块、`experimental_bearer_token`、`wire_api=responses`、OpenCode 的 `npm` / `name` / `options` 均实测出现。`key exec` 的 stdout/stderr 不含合成明文。

### B. TODO

见 §2：TODO-1 仍需真实 Claude Code 验证；TODO-2 已给出真实文件与用例映射；TODO-3 形状一致，gateway source 由 R-02 单列。

### C. 内容充分性（AC-9）

通过。运行手册：

- 禁止事项具体、带后果与替代做法：§4 的八行表格覆盖不进日志、不进对话/issue/PR、不进 git、不提交运行时配置、不把 `$OMC_HOME` 放进仓库、不用 shell redirect 写信封；每条都写了后果（scrollback、历史、clone、umask）和替代（`key exec`、`key accept`、`key install`）。
- `key purge` 明确"只清本机，CPA 里的 key 仍有效"：§Rules 5、Step 7 加粗段、§6 表格、错误码表都重复了这一点，实测输出也是 `local material cleared; the key in CPA is still valid ...`。
- 已知风险如实：§7 写了 Linux `/proc/<pid>/environ`、Windows 内存转储/调试器、`install` 留盘、备份可能含其他凭据、子进程/运行时可能自行记录 key；没有把 `exec` 说成绝对安全。
- 没有把功能说成默认开启：`exec` 是默认路径，`install` 明确"only when it must persist"、"never the default"。
- 反例与限制齐全：Codex 必须是 `codex` / `codex.exe` 本身，包装脚本/`npx` 被 `codex_command_required` 拒绝；OpenCode 不写 `models`、不提供 `--model`，并提醒 `opencode --version` 的 schema 风险。

### D. 文档门禁

- 全仓 `node scripts/check-docs.mjs`：`Context documents checked: 183`，剩余 `missing-path=9 retired-reference=0 line-reference=25 missing-document=0`，全部来自既有 `docs/delivery/2026-11-12-omc-three-credential-quota/impl-plan.md`（T18 的 9 + 25），本次只读审查没有新增。
- 逐文件 `--file`：`docs/repo-key-client.md`、`docs/agent-capabilities.md`、`docs/architecture.md`、`docs/operations.md`、`docs/install.md`、`docs/install-for-agents.md`、`docs/testing.md` 均为 `missing-path=0 retired-reference=0 line-reference=0 missing-document=0`。
- `node scripts/check-docs.test.mjs`：13 pass / 0 fail。
- `pnpm check-i18n`：退出 0，`Total defined keys in DICT: 4256`。本轮没有 UI 文案改动，这项只作为门禁记录。

## 4. 范围外观察（只记录，不要求本次修）

1. `key status --help` 说 `--remote` 是"the only one that writes state"，但 `--application-id` 也会调用 `AssociateApplication` 写 state；这句话在 help 里不准确，运行手册没有重复它。
2. `key install --help` 说"Only the user-level file of each runtime is written"，但同一 help 又提供 `--config <path>`，可以指向任意文件（运行手册明确警告不要指向项目级 settings）。两句话互相收紧，建议后续统一。
3. `remote_error:<code>` 的 `<code>` 实际上总是 `remote_unavailable`：`NewRemoteClient` 与 `bridgeRemote.invoke` 把所有远端失败都包成 `CODE_REMOTE_UNAVAILABLE`，方案 §5.6 里设想的 `remote_refused:<code>` 没有实现。运行手册的写法不算错，但读者会以为能拿到服务端的稳定 code。
4. `key exec` 的 Ctrl-C 行为（父进程不装 signal handler，让子进程自己收）没有运行时测试；代码路径与文档描述一致，但这是一条设计声明，不是实测结论。
5. `key accept` 还接受 `structuredContent` 和两层字符串包装，比运行手册写的"one level"宽；是超集，不影响文档现有说法。

## 5. 建议修复顺序

1. **R-01（P0）**：去重复常量，`go build ./cmd/oh-my-cpa` + `go test ./internal/keyclient ./cmd/oh-my-cpa` 过门禁后再允许合并。
2. **R-02 / R-03 / R-04（P1）**：按方案补 status 的 env 来源、exec 的 `--base-url` 持久化、`--json` 错误信封；如果 PM 决定不改代码，则同步收窄运行手册和方案契约。
3. **R-05–R-11（P2）**：运行手册措辞与字段表；`docs/testing.md` 的 TODO 替换；Windows `--home` 的说明或校验放宽。
4. 复审时只需重跑本报告 §3 里列出的命令与三个门禁；R-01 修复后应把真实基线的 `key --help` 和 `key status` 输出重新采集一次，替换本报告用的补丁副本证据。
