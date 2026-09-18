# StuTrade

StuTrade 是面向校园交易场景的撮合平台。当前版本以二手书交易为首个业务宿主，使用独立的 Go 撮合引擎、Go 后端宿主和 React 前端验证自动撮合、卖方接单与买方购买三条路径。

## 先看这里

- 本仓库负责项目文档、业务宿主、HTTP 入口、业务特有数据、PostgreSQL 适配和交付装配。
- [`engine/`](engine/) 是独立 Go module submodule，负责通用挂单与撮合状态机。
- [`web/`](web/) 是独立 React + TypeScript submodule，负责 OpenAPI 结构契约、页面和前端交付。
- 当前代码已经具备账户会话、买卖挂单宿主 API、PostgreSQL 持久化、部署配置、健康检查、引擎核心模型，以及前端认证、市集浏览和挂单详情；主动成交、自动撮合、履约及发布和本人挂单等 Web 交互尚未实现。生成的契约类型不代表对应业务能力已经可用。
- 当前产品定义见[项目说明](docs/project-description.md)，后续工作见[实施方案](docs/implementation_plan.md)，跨仓库责任见[仓库治理补充](docs/governance/repository.md)。

## 架构概览

```text
web client --HTTP--> Gin: /api/v1 --> business host --> engine
                                             |              |
                                             |              +--> engine Protocol --+
                                             +--> host repositories --------------+--> PostgreSQL
```

| 组件       | 当前责任                                                       |
| ---------- | -------------------------------------------------------------- |
| 业务宿主   | 用户、二手书与订单，业务规则、完整对象组装、HTTP 和运行装配    |
| 撮合引擎   | 通用 Listing、MatchRequest、状态机及逻辑存取入口，不直连数据库 |
| 前端       | 页面、交互、OpenAPI、生成的传输类型和独立静态产物              |
| PostgreSQL | engine 状态与宿主扩展的物理存储、约束、事务与并发正确性        |

核心技术栈为 Go 1.27、Gin、pgx/v5、sqlc、goose、PostgreSQL、React 19、TypeScript 6、Vite 8 和 Bun 1.4。

## 仓库结构

```text
stu-trade/
|-- cmd/stu-trade/       后端命令入口
|-- internal/            宿主装配、HTTP 与数据库适配
|-- migrations/          PostgreSQL migration
|-- queries/             sqlc 查询源
|-- engine/              通用撮合引擎 submodule
|-- web/                 React 前端 submodule
|-- docs/                项目、计划、治理与课程资料
|-- compose.yaml         本地开发编排
`-- Makefile             本地与 CI 的统一命令入口
```

## 开始开发

克隆仓库及固定版本的子仓库：

```bash
git clone --recurse-submodules git@github.com:Jason-skd/stu-trade.git
```

已有工作副本同步 submodule：

```bash
git submodule update --init --recursive
```

常用入口：

```bash
make run          # 启动后端，默认 native profile
make develop      # Compose 启动 PostgreSQL、后端与 web HMR
make check        # 校验宿主、engine、web 与生成契约
make native       # 构建 Windows 后端与独立 web 静态产物
make production   # 构建 Linux 后端、镜像与独立 web 静态产物
```

需要真实 PostgreSQL 的集成测试单独运行：

```bash
STUTRADE_TEST_DATABASE_URL='postgres://...' make test-integration
```

环境变量示例见 [`.env.example`](.env.example)。业务 HTTP 路径统一位于 `/api/v1`；`/health` 和 `/ready` 是宿主运维端点。

## 文档索引

项目文档：

- [项目说明](docs/project-description.md)
- [实施方案](docs/implementation_plan.md)

仓库治理：

- [工作流治理](docs/governance/workflow.md)
- [工程治理](docs/governance/engineering.md)
- [验证治理](docs/governance/verification.md)
- [仓库治理补充](docs/governance/repository.md)

课程要求译文：

- [小组项目说明与提交要求](docs/course-requirement-translation/01-JC2001小组项目说明与提交要求-2026-27.md)
- [项目提案模板](docs/course-requirement-translation/02-项目提案模板.md)
- [小组工作量档案](docs/course-requirement-translation/03-小组工作量档案.md)

课程原始文件：

- [Group workload profile](<docs/course-requirement/Group workload profile (Introduction to software engineering).docx>)
- [Group project description and requirements](docs/course-requirement/JC2001-GroupProjectDescription-and-Requirements-2026-27.pdf)
- [Proposal template](docs/course-requirement/Proposal_Template.pdf)
