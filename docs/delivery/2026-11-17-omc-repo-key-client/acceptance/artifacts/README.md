# 验收证据目录（T5-S14）

本目录是 `docs/delivery/2026-11-17-omc-repo-key-client/acceptance/report-r1.md` 的证据。
所有测试都在 `golang:1.27.1` 容器里跑，源码挂载只读，`--network none`，临时 `HOME` / `OMC_HOME` 在 `/tmp`；
没有连真实 CPA、真实账号或生产环境。

## 文件说明

| 文件 | 内容 |
|---|---|
| `gate-build.log` / `gate-vet.log` / `gate-unit.log` | 集成分支 `6db0300b` 上的 `go build ./...`、`go vet ./...`、两包单测 |
| `gate-unit-targeted.log` | `-v` 的单测子集（status / purge / 权限 / install / exec / accept），126 个用例全 PASS |
| `gate-e2e.log` | `pnpm test:key-e2e`（`TestKeyCLIFullLoop` + `TestRepoKeyDeliveryLoop`） |
| `e2e-evidence/` | e2e 自己写的证据：`key-cli-loop.json`（115 项断言）、`key-loop.json`、`omc.log` |
| `e2e-evidence/review-reuse.json` | 审查专用复用路径测试（C-38）的证据 |
| `case-logs/case-*.log` | 进程级用例的逐条断言记录 |
| `case-logs/configs/` | install/purge 前后的三家配置快照；明文已换成 `sk-cpa-SYNTHETIC-REDACTED` |
| `case-logs/status-json/` | `key status --json` 三态输出 |
| `case-logs/json-errors/` | 六个子命令 `--json` 失败信封 |
| `case-logs/help/` | 六条子命令的 `--help` 原文（C-44 对照用） |
| `case-0*.sh` | 实际跑的脚本（review-only，不进产品） |

## 首轮失败与更正

进程级脚本的第一轮里有几处是**测试脚本自身**的问题，后来用更正后的脚本重跑并保留了两轮日志：

| 首轮 | 失败原因 | 更正 |
|---|---|---|
| `case-01-surface-gen.log` | C-08 以 nobody 在 700 父目录下跑，测到的是父目录 | `case-01b-c08.log`（父目录 755，重跑全 PASS） |
| `case-02-status-exec.log` | C-21 换钥匙场景没带 `--base-url`；C-22 探针把 argv 拼成空格；C-15 目录名含 "corrupt" 误命中 | `case-02b-followup.log`、`case-02c-corrupt.log` |
| `case-03-install-purge.log` | 多个场景共用一个 key home，后一次 install 改了 state 槽位；pretty JSON 用了单行精确匹配 | `case-03c-install-purge-fixed.log`（每场景独立 home） |
| `case-07-reverify.log` | `--json` pretty 输出用无空格匹配；换钥匙的 exec 缺信封 | `case-07b-reverify.log` |

`case-02c`、`case-03c`、`case-07b` 里剩下的 FAIL 是**对用例预期的观察断言**（状态页不标记坏文件、junk TOML 被接受、`insecure_base_url` 文案不存在），结论写在报告正文。

## 大文件

`oh-my-cpa`（约 35 MB）、`keytest-helper`、`keytest-probe` 是可重建的评审二进制，已在 `.gitignore`
里排除，不提交。重建方式见报告「复跑方法」。
