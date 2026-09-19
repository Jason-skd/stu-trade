# StuTrade 工程边界

本文只保存 StuTrade 特有的架构、协议、权威来源、验证命令和交付边界，不规定代码风格。

## 验证入口

- 完整本地验证使用 `STUTRADE_TEST_DATABASE_URL='postgres://...' make pr-check`，其中数据库必须是专用测试数据库。
- PostgreSQL 不可用时，运行 `make check workflow-check`，并明确报告未执行数据库集成测试。
- 修改 `migrations/`、`queries/` 或 `web/openapi/stu-trade.yaml` 后，运行 `make generate` 并检查 `internal/db` 和 `internal/httpapi/contract.gen.go` 的派生 diff。
- 修改前端 OpenAPI 契约或生成类型后，在 `web/` 运行 `bun run check`。

## 文档所有权

- 根 README 负责当前项目目的、架构、仓库结构、运行命令、跨仓库引用和文档索引。
- `docs/project-description.md` 负责当前产品范围、用户可见交易语义、部署拓扑和跨组件边界。
- 各仓库的 `docs/implementation_plan.md` 只负责未完成目标和实施阶段，不证明当前行为。
- `docs/ci-cd.md` 负责 CI、制品、发布、部署和回滚流程。

## 权威来源

- 宿主领域实现和行为测试负责 StuTrade 业务行为与不变量。
- engine 仓库的导出 Go 代码和行为测试负责通用撮合模型、入口、逻辑持久化协议、恢复规则、错误和状态机行为。
- web 仓库的 `openapi/stu-trade.yaml` 负责 `/api/v1` 路径、传输 DTO、状态码和错误结构。
- `migrations/` 负责 PostgreSQL schema、约束和迁移顺序。
- `queries/` 负责 sqlc 查询文本。
- `internal/db` 中的 sqlc 代码是派生产物。
- `internal/httpapi/contract.gen.go` 是 OpenAPI schema 的派生产物。
- web 实现和行为测试负责页面、交互、前端路由、可访问性和前端发布行为。
- `go.mod`、engine gitlink、web gitlink 和 `web/bun.lock` 记录实际消费的依赖版本。

## 组件边界

- 本仓库负责业务宿主、HTTP 入口、业务特有数据、PostgreSQL 适配和交付装配。
- engine 负责通用挂单、撮合状态机和逻辑存取协议，不直接连接数据库。
- web 负责 HTTP 结构契约、页面、交互和独立静态产物。
- `internal/app` 负责进程配置、依赖装配和生命周期。
- `internal/httpapi` 负责传输语义和运行探针。
- `internal/db` 隔离 sqlc 细节，并实现 engine 协议或宿主自有持久化。
- engine 或 web 的变更先在所有者仓库完成；合并后，本仓库再更新对应 gitlink、派生产物和集成验证。

## HTTP 契约

- 业务 API 使用 `/api/v1`。
- `/health` 和 `/ready` 是无版本运行探针。
- web 的 OpenAPI schema 是调用方可见 HTTP 结构的权威来源；宿主负责审核产品语义、安全性、幂等性和可实现性。

## 测试边界

- PostgreSQL 约束、事务、锁、查询和迁移测试使用专用真实 PostgreSQL，并运行正式 migrations。
- Pull request 和 `main` CI 对 Go 测试使用 race detector 和明确 timeout。
- engine 和 web 按各自仓库固定 revision 的验证入口执行检查。

## 交付边界

- 父仓库记录 engine 和 web 的精确 commit；子仓库合并后，只有更新并验证对应 gitlink 才进入产品集成。
- GitHub Actions 验证 pull request 和每个 `main` revision。
- Main Gate 从同一个父仓库 commit 构建后端和 web 镜像，并在 `deployment-manifest` 中记录不可变 digest。
- Production tag 只提升和部署 Main Gate 已认证的 digest，不重新构建镜像。
- `develop`、`native` 和 `production` 是三个独立装配 profile。
