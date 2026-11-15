# AgentMesh

**AgentMesh 是一个 Local-first 的 AI Agent 组织与执行平台，使用 Go 构建。** 它整合模型接入、工具执行、持久化 Agent、Workflow、Coding Runtime，以及统一的执行生命周期管理。

- [English](README.md) | 简体中文
- 文档总索引：[docs/README.md](docs/README.md)
- P2 统一执行管理：[docs/p2-execution.md](docs/p2-execution.md)

## 项目能力

| 模块 | 能力 |
|---|---|
| Model Gateway | Provider / Model 注册、凭证管理、模型路由与统一调用 |
| Agent Runtime | 持久化任务、工具调用、能力权限、人工审批、上下文与记忆 |
| Workflow Runtime | 版本化 DAG、并行分支、Checkpoint、事件历史与恢复 |
| Coding Runtime | Git Worktree 隔离、Coding Agent 执行、沙箱测试、Patch 检查与修复 |
| Unified Execution（P2） | Agent、Coding、Workflow、CLI 共用 Execution / Attempt 生命周期，同时保留各自已有队列 |

项目采用 Go Modular Monolith + SQLite。P2 通过 Runtime Adapter 统一执行身份、状态、Attempt 和结果，不会用第二套通用 Job 队列替换现有 Runtime。

## 环境要求

- Go **1.27.3**，以仓库 `go.mod` 为准。
- SQLite 使用 `modernc.org/sqlite`，不需要 CGO。
- Coding Agent 的沙箱执行需要 Docker 和 Git。
- 根据使用的模型与功能，配置 Provider API Key 及相应 Coding Agent CLI。

## 快速开始

```bash
cp configs/config.example.yaml configs/config.yaml
export OPENAI_PRIMARY_API_KEY="your-api-key"
go run ./cmd/agentmesh --config configs/config.yaml
```

示例配置默认监听 `127.0.0.1:8080`，本地数据存放在 `./data`。请根据实际环境修改 `configs/config.yaml`。真实 API Key 通过环境变量提供，不要提交到 Git。

健康检查：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

除非已经明确配置并验证认证和网络访问边界，否则请保持本机 loopback 监听。不要将本地开发实例直接暴露到公网。

## 开发与验证

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

P2 统一执行层专项验证：

```bash
bash scripts/verify-p2.sh
```

该脚本会先格式化 Go 文件，再执行格式检查、构建、静态检查和 race tests。请使用仓库声明的 Go 版本。

## 文档

请从 [docs/README.md](docs/README.md) 开始查阅文档。各文档的用途、里程碑记录、P2 API 和验证入口均在索引中说明。历史里程碑文件保留当时的实现记录；其中旧的验证说明只代表文件编写时的环境，不代表当前分支 CI 状态。
