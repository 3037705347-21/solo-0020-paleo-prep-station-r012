# 古生物修复制备台

古生物修复制备台是一个使用 Go 编写的后端基线，用于记录化石标本、审批修复方案、回填制备进度，并在全部工序完成后办理研究借阅放行。

该服务只使用进程内仓储，便于在本机重复启动和检查。它不依赖数据库、远端接口或其他在线资源。

## 目录

- `cmd/paleo-prep-station`：HTTP 服务入口和监听参数。
- `cmd/workflowcheck`：通过真实 HTTP 处理器执行四条业务链路的本地验证命令。
- `internal/httpapi`：路由、请求解码、状态码映射和 JSON 输出。
- `internal/station`：跨领域对象的编排、事务边界和业务流程。
- `internal/domain`：标本、方案、进度和放行记录的状态与不变量。
- `internal/repository`：互斥保护的内存仓储和原子提交。
- `internal/contracts`：稳定错误模型和公共请求结构。

## 构建与运行

```bash
go build ./...
go run ./cmd/paleo-prep-station --addr 127.0.0.1:18133
```

服务启动后，`GET /healthz` 返回就绪状态。

## 主要接口

- `GET /specimens` 与 `POST /specimens`
- `GET /specimens/{id}`
- `POST /specimens/{id}/plans`
- `GET /plans/{id}`
- `POST /plans/{id}/approval`
- `POST /plans/{id}/progress`
- `POST /specimens/{id}/release`
- `POST /specimens/{id}/release/revocation`
- `GET /specimens/{id}/releases`

## 本地验证

每条业务链路都可以独立执行：

```bash
go run ./cmd/workflowcheck --workflow register-specimen
go run ./cmd/workflowcheck --workflow approve-prep-plan
go run ./cmd/workflowcheck --workflow log-preparation
go run ./cmd/workflowcheck --workflow release-for-study
go run ./cmd/workflowcheck --workflow revoke-release
```

这些检查会启动临时 HTTP 处理器并调用真实公共路由。

## 撤销研究放行

当接收方取消借阅、运输损坏或研究计划撤销时，标本管理员可以对最新的有效放行提交撤销，请求体包含 `release_id`（可省略，默认撤销最新一条）、`reason` 和 `revoked_by`：

```bash
curl -X POST 127.0.0.1:18133/specimens/<id>/release/revocation \
  -H 'Content-Type: application/json' \
  -d '{"release_id":"release-xxxx","reason":"接收方取消借阅","revoked_by":"keeper-zhou"}'
```

撤销成功后标本回到 `stabilized`，不再可借阅；原放行记录保留全部原字段并新增 `status=revoked`、撤销原因、操作人和时间。`GET /specimens/{id}/releases` 可读取完整历史。撤销后必须重新建立并完成一轮修复方案，才能按原有条件重新放行。重复撤销、未知标本或放行、非最新放行以及存在未结束方案时返回 `409`/`404` 且不改变实体状态。

## 环境变量

项目不需要环境变量，也没有外部服务依赖。

## 测试边界

本基线按要求暂不生成单元测试、测试夹具或端到端测试文件。后续任务阶段会补充红绿验证测试，届时再替换或扩展当前的生产级链路检查。
