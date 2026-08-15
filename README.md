# Astra Harness

Uncertainty-Driven Software Engineering Runtime (v0.1).

Astra 不是又一个 “Chat + Edit + Run” 的 Coding Agent 外壳。它把 **Knowledge State、Claim、Evidence、Unknown、Goal、Action、Execution** 作为一等公民，运行闭环是：

> Understand → Build Knowledge State → Identify Unknowns → Estimate Importance → Choose Best Action → Execute → Collect Evidence → Update Knowledge State → Verify Goal → Repeat

Agent 是可替换、可销毁的计算资源；持久化的系统智能存在于 State Core 与 Evidence System。

## 功能

- **TUI**：对标 Claude Code / Codex CLI / OpenCode 的交互体验
  - 流式 Markdown 渲染、工具调用卡片、权限弹层、Agent 提问弹层
  - 斜杠命令补全与模糊匹配：`/status /claims /unknowns /evidence /goal /model /verify /plan /diff /sessions` 等
  - 状态栏实时显示 Provider/Model、模式、分支、Claim/Unknown/Evidence 计数、Token 用量
  - 键盘：`enter` 发送，`alt+enter` 换行，`ctrl+c` 停止/退出，`ctrl+u/d` 滚动，`x` 展开工具输出
- **State Core**：`.astra/` 下的 Event Sourcing（`events.jsonl`）+ 物化状态（`state.json`），可回放、可恢复
- **Knowledge Engine**：文件/符号/测试索引（Go、Rust、Python、TS/JS、Java、Kotlin、C/C++、C#、PHP、Ruby），ripgrep 检索 + 符号匹配排序
- **Uncertainty Engine**：`priority = impact × uncertainty × dependency_weight ÷ resolution_cost`
- **Decision Engine**：`utility = goal_progress × goal_weight + info_gain × uncertainty_weight − cost × cost_weight − risk × risk_weight`，并给出 Next Best Action
- **Agent Runtime**：OpenAI 兼容协议（OpenAI / DeepSeek / Qwen / OpenRouter / Ollama / 本地模型）+ Anthropic Messages API，统一流式接口与工具调用
- **Permission 模型**：READ / WRITE / EXECUTE / NETWORK / CREDENTIAL / DEPLOY / DELETE；`ask | allow | deny` 三档 + 会话级 always 决策 + Plan 模式
- **Verification**：自动识别 `go test`、`cargo test`、`npm test`、`pytest`、`gradle test`、`make test` 与对应 build 命令，测试/构建结果自动沉淀为 Evidence 与 Claim
- **State Compiler**：把 Goal、Verified Claims、Top Unknowns、Recent Actions、Next Best Action 编译为决策上下文，而不是堆聊天历史

## 快速开始

```bash
# 本地构建（CI 也会在 GitHub Actions 上远程编译）
go build -o astra ./cmd/astra

# 或直接使用 CI 产物 / release binaries
./astra              # 进入 TUI
./astra init         # 初始化 .astra 状态与索引
./astra "实现用户登录的 JWT 刷新"   # 带初始任务进入 TUI
./astra run "重构数据库层" --yes    # 无头运行，自动批准权限
./astra status       # 查看编译后的知识状态
./astra unknowns     # 查看按优先级排序的 Unknowns
./astra verify       # 跑测试/构建并记录证据
```

## 配置

首次启动会在项目 `.astra/config.json` 写入默认配置。也可以在 `~/.config/astra/config.json` 放全局配置，项目配置覆盖全局。

```json
{
  "default_provider": "anthropic",
  "default_model": "claude-sonnet-4-20250514",
  "permission_mode": "ask",
  "max_iterations": 20,
  "auto_verify": true,
  "providers": [
    {
      "id": "openai",
      "type": "openai-compatible",
      "name": "OpenAI",
      "base_url": "https://api.openai.com/v1",
      "api_key_env": "OPENAI_API_KEY",
      "models": ["gpt-4o", "gpt-4o-mini"]
    },
    {
      "id": "anthropic",
      "type": "anthropic",
      "api_key_env": "ANTHROPIC_AUTH_TOKEN",
      "models": ["claude-sonnet-4-20250514"]
    }
  ]
}
```

环境变量：`ASTRA_PROVIDER`、`ASTRA_MODEL`、`ASTRA_PERMISSION_MODE`，以及各 Provider 的 API Key 环境变量（`OPENAI_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`DEEPSEEK_API_KEY`、`DASHSCOPE_API_KEY`、`OPENROUTER_API_KEY` 等）。

## 状态模型

| 实体 | 说明 |
| --- | --- |
| Goal | 目标、优先级、Acceptance Criteria、进度 |
| Claim | subject/predicate/object，状态机 UNKNOWN → HYPOTHESIS → SUPPORTED/CONTRADICTED → VERIFIED → STALE |
| Evidence | SOURCE_CODE / TEST_RESULT / BUILD_RESULT / RUNTIME_RESULT / …，绑定 CodeState |
| Unknown | impact / confidence / resolution_cost / dependency_weight / priority |
| Action | SEARCH / READ / EDIT / RUN_TEST / RUN_BUILD / …，记录 cost / risk / expected_info_gain / utility |
| Event | 追加式事件日志，驱动 State Reducer |

所有重要结论默认不能直接成为 VERIFIED，必须由 Evidence 支撑；代码变化后旧 Evidence 的有效性需重新评估。

## 目录结构

```text
cmd/astra          CLI 入口
internal/core      State Core、Event Sourcing、State Compiler、Decision/Uncertainty
internal/knowledge 索引、符号提取、检索、Git
internal/llm       Provider 抽象、OpenAI 兼容流式、Anthropic 流式
internal/engine    Agent Runtime、工具、权限、验证、主循环
internal/tui       Bubble Tea TUI（聊天、斜杠命令、弹层、权限/提问）
```

## 路线图（对应设计文档 Phase）

Phase 0–8 已具备基础实现（Protocol/Event Model、Repository Index、Knowledge State、Claim+Evidence、Unknown/Risk、Basic Decision、Agent Runtime、Execution/Verification）。后续：Parallel Experiments、Information Gain、Adaptive Model Routing、Long-running Autonomous Tasks、PostgreSQL/pgvector。

## 远程构建

所有编译与测试由 [GitHub Actions](.github/workflows/ci.yml) 在远程完成：push 到 `main` 即触发 `gofmt` 检查、`go vet`、`go build`、`go test`；打 `v*` tag 自动产出 Linux/macOS/Windows 二进制。
