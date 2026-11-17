---
type: acceptance-review
slug: 2026-11-17-omc-repo-key-client
status: draft
updated: 2026-11-17
branch: 11-17-t5-integration（9da1e954）
baseline: 2f7794e8（S1 方案）
summary: "S9 代码审查 r1：分 key 使用端五块代码。1 条 P0（集成分支编译失败，三处重复声明）、1 条 P1（key status 忽略 OMCPA_CPA_BASE_URL）、若干 P2；PM 八条全部一致，六条硬约束无泄露或越界"
---

# 分 key 使用端闭环 · 代码审查 r1（S10 / 切片 S9）

## 结论摘要

1. **P0 阻断**：被审分支 `11-17-t5-integration`（`9da1e954`）**当前无法编译**——`internal/keyclient` 有三个包级常量在并行切片里各声明了一次（`ENV_OPENCODE_CONFIG`、`OPENCODE_PROVIDER_ID`、`CODEX_PROVIDER_ID`）；合并后没有跑过构建门禁。只删掉三处重复声明（不改任何逻辑）后，`go vet`、`internal/keyclient` 与 `cmd/oh-my-cpa` 的全部单测、Windows 交叉编译都通过，说明这是唯一阻断项。
2. **P1 一条**：`key status` 解析网关地址时没有读 `OMCPA_CPA_BASE_URL`，环境变量已设置时仍显示 `gateway: not set` / `base_url_source: absent`，与方案 §2.2、§5.7 和运行手册的说法不一致。
3. **六条硬约束均未发现泄露或越界**：明文只出现在 `exec` 子进程环境与 `install` 显式写入的配置文件；`install` 没有被任何其他命令隐式调用；`purge` 只清 state 记过的槽位、指纹不匹配不动；三家落点与方案一致；`key gen` 复用不轮换、`home_insecure` 与文件名映射成立；PM 已定的八条逐条一致。
4. 其余为 P2 建议：`key status --help` 一句不实、运行手册对 `--base-url` 的记忆说法不准确、两处指纹实现重复且 `RepoState.value_fingerprint` 生产代码未读、`purge --runtime` 的收尾文案、空仓库 purge 会建空 home、`install_not_owned` 时中止整轮与方案步骤顺序不同、`..` 形式的 `--repo` 被当作本地身份接受（与用例 C-06 的预期不一致，但安全属性成立）。
5. 证据：Docker（`golang:1.27.1`、`--network none`、`omc-go-mod` + `omc-gocache`）里的构建、vet、单测、e2e、Windows 交叉编译，以及用临时信封跑通的 `gen → accept → status → exec → install → purge → exec 失败` 进程级链路；命令与输出摘在文末。

## 判定前提（重要）

- 被审对象：`11-17-t5-integration` = `9da1e954`，对比基线 `2f7794e8`；工作目录 `E:/works/Newoahil__oh-my-cpa/11-17-t5-s10-claude`（从集成分支切出）。
- 因为 P0-1，**直接对分支跑任何命令都会在编译阶段失败**。下文所有“实跑”结论都来自一个诊断副本：`git archive HEAD` 导出到仓库外的临时目录，**只删除三处重复声明**，其余字节未改；没有改工作区、没有改被审分支。凡是依赖运行结果的判定都以此为前提，P0-1 修好后应在原分支复跑一遍。
- 没跑过的项不写通过；真实 OMC `--remote`、真实 CPA 网关、真实 Claude/Codex/OpenCode 生效性、Windows ACL 运行时断言仍是缺口，见「没跑什么」。

## AC 逐条

| AC | 状态 | 证据与符号 | 缺口 / 备注 |
|---|---|---|---|
| AC-1 七步闭环可执行、五条子命令在 | 做到（修 P0-1 后实跑） | `cmd/oh-my-cpa/key.go` 的 `runKey`/`keyCommands` 注册六个子命令（含方案新增的 `accept`）；`key --help` 列出 `accept/exec/gen/install/purge/status`；未知子命令退出 2；`gen → accept → status → exec → install → purge → 再 exec` 全链路在临时信封上跑通，purge 后 `exec` 退出 4 且为 `key_not_found` | 真实 MCP `repo_key_apply`/审批/`repo_key_ciphertext` 不在本轮（S9 全链路 e2e 仍在做）；本轮只证明命令面与本地闭环 |
| AC-2 `key gen` | 做到（修 P0-1 后实跑） | `internal/keyclient/keystore.go` 的 `Generate`/`WritePrivateKey`/`fileStem`/`fileSlug`；实跑 stdout 只有一行 `x25519:<base64>`，私钥 0600，重复 gen 公钥不变；`https://GitHub.com/...` 与 `git@github.com:...` 归一到同一把 key；保留名 `CON` → `_CON-…`；空 `--repo` 退出 2 | Windows 的 0600 由 DACL 等价承担，运行时断言按 PM 第 7 条记为缺口；`--repo '../../../etc/passwd'` 被当作本地身份 `passwd` 接受（见 P2-7，与用例 C-06 预期不一致，但没有任何 `$OMC_HOME` 之外的写入） |
| AC-3 `key status` | 部分做到 | `internal/keyclient/status.go` 的 `BuildReport`/`WriteReport`/`WriteReportJSON`、`remote.go` 的 `RefreshRemote`；实跑可见私钥、信封、application、`approval_status`、`status_source`；`--remote` 有 mock 单测（成功/部分失败/缺配置/不重复响应体） | **P1-1：不读 `OMCPA_CPA_BASE_URL`**；`--remote` 未对真实 OMC 跑过（S9/S11）；`--help` 关于“唯一会写 state 的选项”不实（P2-1） |
| AC-4 `key exec` | 做到（修 P0-1 后实跑） | `internal/keyclient/exec.go` 的 `RunChild`、`internal/keyclient/runtime.go` 的 `Inject`；单测用 helper 子进程断言 Claude/Codex/OpenCode 三种注入、argv 无明文、退出码透传、失败不启动子进程；实跑用真实 repokey 信封让 claude 子进程看到匹配的 `ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_API_KEY` 已移除，stderr 无明文，home 在 exec 前后无任何变化；purge 后同一 exec 退出 4 | 真实三家运行时是否按注入生效是人工探针（方案 §11），本轮未跑；运行手册说带 `--base-url` 会写入 config.json，对 exec 不成立（exec 按 AC-4 不落盘，见 P2-2） |
| AC-5 `key install` | 做到（修 P0-1 后实跑） | `internal/keyclient/install.go` 的 `Install`/`warnPlaintext`/`backupConfig`/`verifyRuntimeEdit`；实跑 Claude 保留 `theme`、`env.OTHER`，写前备份 0600，明文只落在目标配置；OpenCode 写 v1 单数 `provider`、其他 provider 保留、**无 `models`**；Codex 只写受管块和一行 `model_provider`，purge 恢复 `other`；外来槽位无 `--replace` 退出 5，有则成功 | 只有 `--config` 临时文件证据，未动真实 `~/.claude`/`~/.codex`；真实运行时读取这些配置未验（方案 §11） |
| AC-6 `key purge` | 做到（核心），P2 若干 | `internal/keyclient/purge.go` 的 `Purge`/`purgeRuntime`/`purgeBackups`；实跑全清、`--runtime` 只清一个、`--dry-run` 零写入（文件 mtime/内容前后一致）、幂等、指纹不匹配拒绝、备份只删含本 key 的（单测）；只有 state 记过的运行时才会被清理，不会去猜默认配置文件 | `--runtime` 收尾文案仍说“local material cleared”而私钥还在（P2-4）；空仓库 purge 会建空 home（P2-5）；`install_not_owned` 会中止整轮而不是按方案 §2.8 的顺序继续（P2-6） |
| AC-7 端到端可复现 | 部分做到 | `internal/keyclient/e2e` 夹具 + `go test -tags e2e` 实跑通过（5.4s）：真实 OMC 二进制 + fake CPA + capability apply/审批/ciphertext + 用解出的 key 打网关 + 失败路径；README 明说 CLI 部分是标记过的替身 | 七步里的 `key gen/accept/exec/purge` 仍由夹具替身承担，CLI 全链路是 S9（执行中）；真实 CPA 一次成功请求仍为人工验收缺口 |
| AC-8 单测：status/purge/0600 | 做到（Unix） | `go test ./internal/keyclient ./cmd/oh-my-cpa` 全绿（85.4s + 50.0s）；`home_unix_test.go` 断言 0700/0600、含 umask 000 的创建后收紧；`accept_test.go` 断言信封 0600；`status_test.go`/`purge_test.go` 覆盖本地视图与全清/单运行时/dry-run/指纹 | Windows ACL 运行时断言按 PM 第 7 条记为缺口；`GOOS=windows go build` 通过（修 P0-1 后）；`home_windows.go` 的 `verifyOwnerOnly` 未断言“当前用户有 full control”（方案 §8.6 如此要求），见 P2-8 |
| AC-9 文档 | 部分做到 | `docs/repo-key-client.md` 七步、三家表、禁止事项（不进日志/对话/git/不提交配置）、退出码与错误码齐全；`docs/agent-capabilities.md`、`architecture.md`、`operations.md`、`install.md`、`install-for-agents.md`、`testing.md` 均按 AGENTS.md §2 同步；`node scripts/check-docs.mjs` 无新增红项（只剩既有 T18 交付文档的 9 missing-path + 25 line-reference） | 两处 `TODO(实现确认)` 未闭环：运行手册的 Claude OAuth 优先级、`docs/testing.md` 的测试名回填（S9b 范围）；`architecture.md` 新增行缺反引号（P2-10）；运行手册对 `--base-url` 的表述不准确（P2-2） |

## 问题清单（按严重程度）

### P0 阻断

#### P0-1 集成分支无法编译：三个包级常量重复声明

- **文件与符号**：`internal/keyclient/runtime.go` 的常量块（`ENV_OPENCODE_CONFIG`、`OPENCODE_PROVIDER_ID`、`CODEX_PROVIDER_ID`）；`internal/keyclient/install.go` 的常量块（`ENV_OPENCODE_CONFIG`、`OPENCODE_PROVIDER_ID`）；`internal/keyclient/tomlblock.go` 的常量块（`CODEX_PROVIDER_ID`）。
- **问题**：同一 package 内同名常量各声明两次，`go build` 直接报 `redeclared in this block`。
- **为什么是问题**：方案 §7.2 让各切片只新增自己的文件、靠 `init()` 注册；但三个切片各自把共享常量写进了自己的文件，合并时没有任何构建门禁，结果整条集成分支不可用。这不是风格问题：AC-1～AC-8 在该提交上一条都跑不起来，S11/S12 也会被卡住。
- **怎么复现**：

```bash
docker run --rm --network none \
  -v "<worktree>:/src:ro" -v omc-go-mod:/go/pkg/mod -v omc-gocache:/root/.cache/go-build \
  -w /src -e GOPROXY=off -e GOFLAGS=-mod=readonly golang:1.27.1 \
  go build ./internal/keyclient ./cmd/oh-my-cpa
```

输出为三条 `redeclared in this block`（按符号归纳，不引行号）：

```text
ENV_OPENCODE_CONFIG   redeclared   runtime.go 与 install.go 各一份
OPENCODE_PROVIDER_ID  redeclared   runtime.go 与 install.go 各一份
CODEX_PROVIDER_ID     redeclared   tomlblock.go 与 runtime.go 各一份
```

- **建议怎么改**：保留一份共享常量（建议放 `internal/keyclient/types.go` 或 runtime.go 的常量块），`install.go`/`tomlblock.go` 引用它；同时把 `OPENCODE_NPM` 与 `OPENCODE_PROVIDER_PACKAGE`、`ValueFingerprint` 与 `KeyValueFingerprint` 一并收敛（见 P2-3）。合并每个切片后至少跑一次 `go build ./internal/keyclient ./cmd/oh-my-cpa`（或让 CI/`verify` 覆盖），避免同类冲突再次漏到集成分支。
- **验证**：只删除上述三处重复声明后，`go vet` 退出 0，`go test -count=1 ./internal/keyclient ./cmd/oh-my-cpa` 两包全绿，`GOOS=windows go build` 退出 0，`go test -tags e2e ./internal/keyclient/e2e` 退出 0。

### P1 要修

#### P1-1 `key status` 忽略 `OMCPA_CPA_BASE_URL`

- **文件与符号**：`cmd/oh-my-cpa/keystatus.go` 的 `runKeyStatus` 调用 `keyclient.ResolveBaseURL("", "", config)`。
- **问题**：第二个参数（环境值）恒为空，`key status` 只从 `config.json` 取地址。环境变量已设置、`config.json` 未写时，报告显示 `gateway: not set` 和 `base_url_source: absent`；而 `key exec`/`key install` 会用环境变量正常工作。
- **为什么是问题**：方案 §2.2 规定解析顺序 `--base-url > OMCPA_CPA_BASE_URL > config.json > base_url_required`，§5.7 规定 `base_url_source` 取 `flag | env | config | absent`；运行手册也说 status 显示“the remembered gateway address (and which of the three sources supplied it)”。当前实现会让使用者误以为网关没配置。
- **怎么复现**（诊断副本、修 P0-1 后）：

```bash
OMC_HOME=/tmp/omchome OMCPA_CPA_BASE_URL=http://127.0.0.1:8317 /tmp/omc key status | head -3
# gateway: not set; pass --base-url or set OMCPA_CPA_BASE_URL
OMC_HOME=/tmp/omchome OMCPA_CPA_BASE_URL=http://127.0.0.1:8317 /tmp/omc key status --json | head -4
# "base_url": "", "base_url_source": "absent"
```

- **建议怎么改**：与 `resolveInstallBaseURL` 一样传入 `os.Getenv(keyclient.ENV_CPA_BASE_URL)`；补一条命令层测试：环境变量已设、`config.json` 不存在时 `base_url_source == "env"`，人类输出显示该地址。
- **严重程度**：P1（功能与文档不一致，容易被当成网关未配置；不阻断执行）。

### P2 建议

#### P2-1 `key status --help` 说“只有 `--remote` 会写 state”，不实

- **文件与符号**：`cmd/oh-my-cpa/keystatus.go` 的 `KEY_STATUS_USAGE`。
- **问题**：`--application-id` 会调用 `keyclient.AssociateApplication` 写 `state.json`；实跑对比 state 文件确认会变。帮助文本却说 `--remote` 是“唯一会写 state 的选项”。
- **建议**：改成“`--remote` 是唯一的网络路径；`--application-id` 也会更新本地 state”，或与方案同步措辞。`--dry-run` 类语义不受影响，但状态写入应当说清。
- **严重程度**：P2。

#### P2-2 运行手册说“带 `--base-url` 的命令会记住地址”，对 `key exec` 不成立

- **文件与符号**：`docs/repo-key-client.md` “The gateway address” 一节；`cmd/oh-my-cpa/keyexec.go` 的 `runKeyExec`。
- **问题**：手册说“A successful command given `--base-url` remembers the value in `config.json`”，但 `exec` 按 AC-4 不落盘，不会写 config；只有 `gen`/`install` 会写。方案 §2.2 的同一句话也有歧义。
- **建议**：手册改为“`key gen` 和 `key install` 带 `--base-url` 成功后会记住；`key exec` 不会写盘”；或统一由方案澄清。实现按 AC-4 不写是正确方向，不建议改代码。
- **严重程度**：P2（文档准确性）。

#### P2-3 重复实现与未用字段

- **文件与符号**：
  - `internal/keyclient/accept.go` 的 `ValueFingerprint` 与 `internal/keyclient/install.go` 的 `KeyValueFingerprint` 函数体完全相同，各被不同调用点使用；
  - `internal/keyclient/types.go` 的 `RepoState.ValueFingerprint` 只在 `Accept` 里写入，生产代码没有读取（purge 用的是 `RuntimeSlot.ValueFingerprint`），方案 §5.1 的 state 示例也没有仓库级该字段；
  - `internal/keyclient/install.go` 的 `OPENCODE_NPM` 与 `internal/keyclient/runtime.go` 的 `OPENCODE_PROVIDER_PACKAGE` 值相同；
  - `internal/keyclient/errors.go` 的 `CODE_INSECURE_BASE_URL` 只出现在退出码表里，从未被构造（不安全地址只打警告）。
- **为什么是问题**：AGENTS.md §3 明确鼓励删除冗余实现；两份指纹一旦漂移，purge 的所有权判断和 accept 的记录会不一致。
- **建议**：保留一个指纹函数（如 `ValueFingerprint`）和一个 npm 常量；`RepoState.ValueFingerprint` 要么删掉，要么让 purge 在没有 slot 指纹时真正用它；`CODE_INSECURE_BASE_URL` 若只是文档占位，删掉或注明。
- **严重程度**：P2。

#### P2-4 `key purge --runtime` 仍打印“local material cleared”

- **文件与符号**：`cmd/oh-my-cpa/keypurge.go` 的 `runKeyPurge` 成功分支。
- **问题**：只清一个运行时时私钥和信封按设计保留，但 stdout 仍输出“local material cleared; the key in CPA is still valid…”，与同一轮 stderr 的 `kept: state record … (a --runtime run keeps the key and envelope)` 矛盾。实跑确认 key 仍在。
- **建议**：`--runtime` 用单独文案，例如“removed <runtime>'s installed value; the private key and envelope are still on this machine”，全清才用“local material cleared”。
- **严重程度**：P2。

#### P2-5 空仓库的 real purge 会创建空 home，且提示“private key is already gone”

- **文件与符号**：`internal/keyclient/purge.go` 的 `Purge`（非 dry-run 调 `OpenStoreForUpdate` → `Home.Ensure`）与 `purgeKeyMaterial`/`purgeBackups`。
- **问题**：对一个从未有 key 的 home 跑 `key purge --repo <repo>`，命令会创建 `$OMC_HOME/{keys,envelopes,backups}`（0700），并打印“the private key is already gone, so a backup can only be recognised…”，而私钥其实从未存在。`--dry-run` 不创建任何东西（已验证）。
- **建议**：purge 先 stat 一次 home 再决定是否 `Ensure`；把“已删除”和“从未存在”分开措辞（`BackupScanUnavailable` 只在确实删过 key 时置位）。
- **严重程度**：P2（副作用小，文案会误导）。

#### P2-6 `install_not_owned` 时整轮 purge 中止，与方案 §2.8 的步骤顺序不同

- **文件与符号**：`internal/keyclient/purge.go` 的 `purgeRuntime` 返回 `CODE_NOT_OWNED`，`Purge` 直接返回。
- **问题**：方案 §2.8/§6.4 的顺序是“对每个运行时比对；不匹配报 `install_not_owned` 并保留 → 再删私钥/信封 → 最后按 pending 决定 state 记录”。当前实现一旦遇到不匹配就中止，私钥、信封、state 都不动，也不会走到 `purge_pending`。
- **建议**：这是一个安全取向的选择（先不动任何东西，让使用者手工处理），本身可接受；请与方案二选一并写进运行手册：要么按方案继续清能清的、最后报 `purge_pending`，要么明确“任何未所有权项都阻止整轮清理”。
- **严重程度**：P2（行为差异，不是泄露或误删）。

#### P2-7 `..`/绝对路径形式的 `--repo` 被接受为本地身份（与用例 C-06 预期不一致）

- **文件与符号**：`internal/keyclient/home.go` 的 `DeriveRepoID`/`fileSlug`；`internal/keyclient/repo.go` 的 `ResolveRepo`/`InferRepo`。
- **问题**：实跑 `key gen --repo '../../../etc/passwd'` 退出 0，生成 `keys/passwd-<hash8>.key`；`--repo 'a/../../b'` 生成 `keys/a%2F..%2F..%2Fb-<hash8>.key`。所有新文件都落在 `$OMC_HOME/keys` 下（`ls` 已列），代码路径也全部由 `Home` 拼出，安全性成立；但用例 C-06 预期这类输入应“退出码非 0、可读错误”。
- **为什么是问题**：用例是基线之一，实现沿用了方案 §4.3“本地目录取目录名”的规则，两者没有对齐。安全属性（路径穿越、保留名、超长）本身没有被破坏。
- **建议**：在 S11 前把判定统一：要么对含 `..` 段或绝对路径的 `--repo` 明确拒绝（`repo_invalid`），要么在用例里把预期改成“归一化为本地目录名且不逃逸出 `$OMC_HOME`”。`a/../../b` 这类未清理的 `..` 段即使被百分号编码也建议归一化后再用。
- **严重程度**：P2（需对齐，不是安全漏洞）。

#### P2-8 Windows ACL 校验没有断言“当前用户有 full control”

- **文件与符号**：`internal/keyclient/home_windows.go` 的 `verifyOwnerOnly`；`internal/keyclient/home_windows_test.go` 的 `assertOwnerOnlyACL`。
- **问题**：两者都只检查“没有 Everyone/Users/Authenticated Users/Guests 的 allow ACE”和 DACL 已去继承；方案 §8.6 还要求断言当前用户 SID 有 full control。
- **为什么是问题**：PM 第 7 条已把 Windows ACL 运行时断言记为缺口，本轮最小证据是 `GOOS=windows` 编译 + 代码审查；但代码审查也应按方案 §8.6 的两条看，当前少一条。
- **建议**：在 `verifyOwnerOnly`/测试里补当前用户 SID 的 allow ACE 检查（代码已经授予 `GENERIC_ALL`，补断言即可）；运行时证据仍按缺口记录。
- **严重程度**：P2。

#### P2-9 `key status --remote` 的 agent/device 不按 `mcp` 的默认值补齐

- **文件与符号**：`internal/keyclient/remote.go` 的 `newBridgeRemote`；`cmd/oh-my-cpa/main.go` 的 `mcpAgentName`/`mcpDeviceName`。
- **问题**：`mcp` 在环境变量缺失时会用 `oh-my-cpa-mcp` 和主机名；`status --remote` 传空值，`Bridge.WithCaller` 于是不带 `X-Omc-Agent`/`X-Omc-Device`。方案 §2.4 说“和 mcp 一致”。
- **建议**：复用 `mcpAgentName`/`mcpDeviceName` 的默认值，或把默认逻辑下沉到 `mcpbridge`；这两项对只读能力影响很小，但属于方案一致性。
- **严重程度**：P2。

#### P2-10 风格与文档细节

- **布尔命名**：`Reused`、`Unchanged`、`DryRun`、`Insecure`、`BackupScanUnavailable` 等没有 AGENTS.md §6 要求的状态前缀（`is`/`has`/`can`/`should`）。如果项目对导出结果字段另有惯例，可忽略；否则建议统一。
- **文档格式**：`docs/architecture.md` 新增的 `internal/keyclient` 包表行没有像其他行一样加反引号。
- **未闭环的 TODO**：`docs/repo-key-client.md` 的 Claude OAuth 优先级、`docs/testing.md` 的测试名回填仍是 `TODO(实现确认)`；按计划由 S9b 处理，本轮只记录。
- **严重程度**：均为 P2，不阻断。

## 通过项：六条硬约束与 PM 八条

### 硬约束

1. **明文边界**：逐路径读过代码并实跑扫描。`exec` 的明文只进 `Injection.Env`，错误只带 repo/显示名/路径/退出码；`accept` 只在内存里试解、state 只存 SHA-256 指纹；`install` 是唯一写明文的命令且先打四条警告；`purge` 只在内存里比对。实跑在 `$OMC_HOME`、`state.json`、`config.json` 和 stderr 中 `grep` 不到测试明文；`key exec` 前后 home 文件快照完全一致。Codex 的 token 只走 `OMC_REPO_KEY`，单测有“明文不在 argv”的断言。未发现漏点。
2. **`install` 必须显式**：只有 `cmd/oh-my-cpa/keyinstall.go` 调用 `keyclient.Install`；`gen` 只写 state/config，`accept` 只写信封/state，`exec`/`status`/`purge` 不写运行时配置。
3. **`purge` 范围**：只清 `state.runtimes` 记过的槽位（`purge.go` 的 `Purge` 循环）；指纹不匹配报 `install_not_owned` 且不动；备份只删内容含本仓库明文的（`purgeBackups`）；`--dry-run` 实跑前后文件 mtime/内容一致，且不会创建 home。
4. **三家落点**：Claude 只写用户级 `~/.claude/settings.json`（或 `CLAUDE_CONFIG_DIR`/`--config`）；Codex 只动受管块加一行 `model_provider`，purge 恢复原值；OpenCode 写 v1 单数 `provider`，不写 `models`。实跑文件内容与方案 §2.7 一致。
5. **`key gen` 重复行为**：复用、退出 0、公钥不变、不静默轮换（`keystore.go` 的 `Generate`）；`home_insecure` 在 Unix group/other 可写时拒绝（`home_unix.go`），Windows DACL 失败即拒绝（`home_windows.go`）；文件名 `slug-hash8` 对 `/`、保留名、超长、大小写不敏感都安全（`home.go` 的 `fileStem`/`fileSlug`）。
6. **PM 已定八条**：默认 `~/.omc` 不按 agent 推导（`ResolveHome`）；`accept` 只有 stdin、没有 `--remote`（`keyaccept.go`）；Codex exec 只接受 `codex`/`codex.exe`（`isCodexCommand`）；OpenCode 不写 `models`；备份不自动限份、purge 只删含本 key 的；`status --remote` 用 `OMCPA_CPA_MANAGEMENT_KEY`；Windows ACL 按缺口处理；`test:key-e2e` 只做本地入口（`package.json`，未进 `verify:full`/CI）。**未发现偏差。**

## 代码质量小结（B 路）

- **注释**：Go 代码注释全部为英文，且解释约束与取舍（例如 `exec` 为什么不装信号处理器、`install` 为什么拒绝符号链接、TOML 为什么不整份解析），没有“复述代码”的注释；中文只出现在测试字符串字面量里，不在注释里。
- **命名**：常量走 `SCREAMING_SNAKE_CASE`、函数是动宾结构、布尔局部变量多用 `has`/`is` 前缀（`hasSlot`、`ownsSlot`、`isCodexCommand`、`isRegularFile`）；少数导出结果字段没有状态前缀，列在 P2-10。
- **错误处理**：稳定的 snake_case 错误码集中在 `internal/keyclient/errors.go`，命令层统一 `keyclientMessage`/`ExitCode` 输出；`fmt.Errorf` 只用于包内部会被上层包装成 `config_conflict`/`install_failed` 的扫描/校验错误，没有把私钥、明文或信封正文拼进消息。
- **测试**：单测都在 `internal/keyclient`，命令面测试在 `cmd/oh-my-cpa`；进程级注入用 `TestHelperProcess` 子进程而不是只断言环境切片；没有固定 `time.Sleep`；`t.Skip` 只用于平台能力（umask/符号链接/git 缺失）或 helper 入口；没看到“为了过而放宽断言”的写法。关键负向断言都在：另一把 key 的信封拒收、argv 无明文、exec 前后 home 不变、dry-run 零写入、指纹不匹配不删。
- **文件归属**：S2b 的提交确实改了 S2a 的 `types.go` 与 `state_test.go`（新增 `decided_at`/`value_fingerprint` 字段），但提交说明写了理由，属于共享 state schema 的必要扩展；其余切片只改自己归属的文件，没有无理由的越界修改。
- **依赖**：`go.mod`/`go.sum` 未变，没有新增 Go 依赖。

## 范围外观察

- `node scripts/check-docs.mjs` 仍红，但全部在既有 T18 交付文档 `docs/delivery/2026-11-12-omc-three-credential-quota/impl-plan.md`（9 missing-path + 25 line-reference），与本需求无关，不计入本次。
- `gofmt -l` 只报 `cmd/oh-my-cpa/main.go`，差异是 `MCP_USAGE` 常量后缺空行；已在基线 `2f7794e8` 上复现，不是本次引入。
- 真实 CPA 网关、真实 Claude/Codex/OpenCode 生效性、Windows ACL 运行时断言、真实 OMC `--remote` 属于方案 §11 已声明的缺口，本轮没有把它们写成“通过”。
- 集成分支的合并提交都没有跑构建门禁（否则 P0-1 不会进集成分支）；这是流程观察，供跟进 Agent 在后续合并时参考。

## 跑了什么（命令与输出摘要）

环境：Docker `golang:1.27.1`；`--network none`；卷 `omc-go-mod:/go/pkg/mod`、`omc-gocache:/root/.cache/go-build`；`GOPROXY=off`、`GOFLAGS=-mod=readonly`、`TZ=UTC`、`ASSISTANT_NO_SEND=1`。源码挂载只读；被审工作区未修改。

| 检查 | 命令 | 结果 |
|---|---|---|
| 构建（被审分支原样） | `go build ./internal/keyclient ./cmd/oh-my-cpa` | 失败：三处重复声明（P0-1） |
| 构建（诊断副本，仅删三处重复声明） | 同上 | 退出 0 |
| vet | `go vet ./internal/keyclient ./cmd/oh-my-cpa` | 退出 0 |
| 单测 | `go test -count=1 ./internal/keyclient ./cmd/oh-my-cpa` | `ok internal/keyclient 85.432s`；`ok cmd/oh-my-cpa 49.957s` |
| e2e 夹具 | `go test -tags e2e -count=1 ./internal/keyclient/e2e` | `ok … 5.420s` |
| Windows 交叉编译 | `GOOS=windows go build ./internal/keyclient ./cmd/oh-my-cpa` | 退出 0 |
| 文档门禁 | `node scripts/check-docs.mjs` | 只剩既有 T18 红项 |
| 文档门禁自测 | `node --test scripts/check-docs.test.mjs` | 13 pass / 0 fail |
| 进程级链路 | 临时 home + `repokey.Encrypt` 生成的真实信封（诊断副本里的 review-only helper）→ `gen/accept/status/exec/install/purge/exec` | 见下 |

进程级链路的关键输出：

```text
key --help: accept exec gen install purge status
gen stdout: x25519:4BJxLo59…            （仅一行）
keys/org%2Frepo-57b681c5.key            600
gen 第二次: same_public_key=yes
status: approval=approved (accept), application_id=app-1, envelope_present=true
exec claude: token_match=yes, api_key_present=no, exec 前后 home 快照一致
install claude: backup 0600、env.OTHER 保留、目标文件 600、state 无明文
install opencode: provider 单数、其他 provider 保留、MODELS_PRESENT=no
install codex: 受管块 + 一行 model_provider；purge 恢复 model_provider="other"
purge --dry-run: 文件前后一致、未创建 home
purge 后 exec: exit 4, key_not_found
full purge 后 grep 明文: no plaintext left in home or configs
```

## 没跑什么（缺口）

- 真实 OMC 的 `key status --remote`（需要真实 console/管理密钥；单测用 fake client 覆盖）。
- 真实 CPA 网关接受这把 key 的一次请求（AC-7 的人工验收项）。
- 真实 Claude Code / Codex / OpenCode 是否按注入生效（方案 §11 的人工探针）。
- Windows 上的 ACL 运行时断言（本机无 Go；按 PM 第 7 条记为缺口，只有 `GOOS=windows` 编译）。
- `pnpm verify` / `pnpm test:fast` 全量 Node 门禁（此 worktree 未安装 `node_modules`；只跑了不依赖安装的 `check-docs` 与自测）。

## 交付信息

- 被审提交：`9da1e954`（`11-17-t5-integration`）；基线：`2f7794e8`。
- 本报告：`docs/delivery/2026-11-17-omc-repo-key-client/acceptance/review-r1.md`。
- 派发：works 2026-11-17#T5-S10（父记录 `2026-11-17-2026-11-17-T5`）。
