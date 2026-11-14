# AgentMesh P0 / P1 修复包

本包只覆盖 P0/P1 涉及的文件，不包含整个项目源码，也不会覆盖配置、路由静态资源等上一轮 hardening 文件。

## 应用

在 AgentMesh 仓库根目录解压本包，允许覆盖同名文件：

```bash
unzip -o agentmesh-p0-p1.zip -d /path/to/agentmesh
cd /path/to/agentmesh
chmod +x verify-p0-p1.sh
./verify-p0-p1.sh
```

如果使用 Finder 解压，请将包内 `internal/` 目录合并到项目根目录，并允许覆盖同名文件。

## P0：工具授权与人工审批

### 授权边界

- `ApprovedTools` 不再等价于人工审批。
- Agent Runtime 为每次工具调用附加可信的 Agent Invocation 上下文；Tool Engine 在服务端检查非 Read 工具是否在服务端加载的 `ApprovedTools` 能力名单中。
- `Engine.Invoke(..., approved bool)` 为兼容旧内部接口保留参数，但**忽略这个客户端可伪造的 bool**。
- 人工审批通过短时、一次性 token 表示。token 仅返回给调用者，数据库只保存 SHA-256 哈希。
- token 绑定工具名、规范化参数摘要、Workspace 路径和过期时间；消费通过 SQLite 条件 UPDATE 原子完成，不能重放。
- 审计事件区分 `agent_capability` 与 `human_approval`。

### HTTP 调用方式变更

旧的 `approved: true` 不再有效。对于策略要求人工审批的工具，先签发审批：

```http
POST /api/v1/tools/file.write/approvals
Authorization: Bearer <AGENTMESH_API_TOKEN>
Content-Type: application/json

{
  "workspace_id": "my-workspace",
  "arguments": {"path": "hello.txt", "content": "hello"}
}
```

响应包含 `approval_id` 和 `expires_at`。随后用同一工具、同一 Workspace、同一参数调用：

```http
POST /api/v1/tools/file.write/invoke
Authorization: Bearer <AGENTMESH_API_TOKEN>
Content-Type: application/json

{
  "workspace_id": "my-workspace",
  "arguments": {"path": "hello.txt", "content": "hello"},
  "approval_id": "<上一步返回的 approval_id>"
}
```

审批有效期默认 2 分钟，最多 5 分钟；一次消费后立即失效。参数、工具或 Workspace 任一不同都会拒绝。

本地 loopback 模式如果没有配置 API Token，可省略 Authorization；非 loopback 访问仍必须按项目既有配置启用强 API Token。

### 数据库

新增 migration：`000022_tool_approvals`。
- 新增 `tool_approvals` 表。
- `tool_execution_audit` 新增 `authorization` 字段。
- down migration 会重建旧审计表结构，恢复原列集合。

## P1：Coding 并发、事件一致性与关闭

- 用引用计数锁表替换永久增长的 `sync.Map`；同一 task 的 Run 使用引用计数锁；Diff / Patch / Revert / Cleanup 使用 TryLock，若任务正在执行或同一 task 已有操作占锁则立即返回冲突，不会排队等任务结束后再修改。不同 task 可并发。
- `Start` 在创建后台 goroutine 前预留容量为 2 的并发槽位；满载直接返回 `ErrConflict`，不创建等待中的无界 goroutine。
- `Run` 和 `Start` 的 WaitGroup 注册与 Close 状态检查共用同一把 mutex，避免 `Wait` 与 `Add` 竞态。
- `CloseContext` 取消运行任务并在调用方 deadline 内等待；旧 `Close()` 保留并使用 30 秒默认等待上限。
- Container 先停止 Worker，再等待 Coding / Workflow / Scheduler；如果超时，不会提前关闭仍可能被 Worker 使用的数据库连接。
- Coding task 创建、认领、关键状态更新与对应事件在 SQLite transaction 中提交。
- 事件、Step、状态持久化失败会被返回，不再静默忽略。
- Agent Definition Run 根据 context cancellation / deadline / 普通错误分别持久化为 canceled / timed_out / failed；完成记录写入使用独立 5 秒上下文，持久化错误会返回。

## 新增/更新测试

- Tool Engine：客户端 bool 不能越权、一次性审批不能重放、Agent ApprovedTools 在服务端强制执行。
- AgentCatalog：失败、取消、超时、完成记录持久化失败。
- Coding：锁表回收、并发槽位上限、关闭取消、运行中禁止 Patch / Revert / Cleanup、事件生命周期。

## 验证

`verify-p0-p1.sh` 执行：

```bash
gofmt -w <本补丁修改的 Go 文件>
go test -race ./...
go vet ./...
go build ./...
```

本次生成环境的 Go 是 1.23.2，而项目 `go.mod` 要求 Go 1.27.3；该环境无法联网下载对应 toolchain。因此没有声称全项目测试通过。已在隔离临时模块中使用 Go 1.23.2 对 `internal/tools/...`、`internal/agentcatalog`、`internal/coding` 锁表测试运行 `go test -race` 和 `go vet`，均通过；SQLite migration up/down 和一次性消费 SQL 也通过独立 SQLite 验证。最终仍以你本机完整项目验证结果为准。

## 应用注意

- 本包会覆盖 `internal/coding/service.go`、`internal/tools/tools.go` 等列出的同名文件；这些文件已包含本轮 P0/P1 的替代实现。
- 不要在本包之后再次运行上一轮 `agentmesh-hardening.zip` 中的旧 `apply_fixes.py`，它会尝试修改已替换的 Coding Service 结构。
- 本包不向 GitHub 写入任何内容，也不自动提交代码。
