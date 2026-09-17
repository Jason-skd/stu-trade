# StuTrade

StuTrade 是面向校园交易场景的撮合平台。主仓库承载业务宿主和项目文档，通用撮合引擎与 React + TypeScript 前端分别通过 Git submodule 接入。

平台首先交付二手书交易 MVP，验证三条核心路径：系统自动撮合、卖方主动接单和买方主动购买。后续校园二手物品、跑腿和租赁场景复用同一个撮合引擎，各自在宿主中保留领域字段与成交规则。

## 架构边界

- **业务宿主**负责用户入口、领域数据、领域规则、撮合编排、交易确认和履约流程。
- **撮合引擎**负责需求、供给、候选匹配和撮合请求等通用能力，不依赖 Gin、PostgreSQL 或具体领域。
- **前端**与宿主在源码和开发流程上分离；发布时构建为静态资源并嵌入宿主程序。
- **PostgreSQL**是独立运行的数据服务，不打包进宿主程序。

开发态保持前后端分离，交付态由同一个进程提供页面和 API：

```text
web --bun build--> dist --go:embed--> stu-trade executable
                                         ├── Gin: Web UI + /api/v1
                                         ├── Engine: Go package
                                         └── pgx: PostgreSQL
```

## 技术选型

| 范围     | 选型                      | 用途                                 |
| -------- | ------------------------- | ------------------------------------ |
| 宿主语言 | Go                        | 业务实现、服务装配和可执行程序交付   |
| HTTP     | Gin                       | API、健康检查和前端静态资源服务      |
| 数据访问 | pgx/v5 + sqlc             | PostgreSQL 连接池与类型安全查询      |
| 数据库   | PostgreSQL                | 业务数据、约束和事务                 |
| 数据迁移 | goose                     | 管理并执行版本化 SQL migration       |
| 前端     | React + TypeScript + Vite | 浏览器用户界面与静态资源构建         |
| 本地编排 | Docker Compose            | 一键启动隔离的开发环境               |

sqlc 生成代码属于数据库适配层，不直接作为 HTTP DTO 或业务实体使用。迁移文件是数据库 schema 的唯一事实来源；生成文件不手工修改。

## 部署配置

项目将运行环境定义为三个部署配置。部署配置负责选择依赖和运行参数，不改变业务规则，也不等同于 Gin 的 `debug`、`release` 和 `test` 模式。

| 配置         | 中文名称             | 运行方式                      | 前端                         | PostgreSQL                 | Migration          |
| ------------ | -------------------- | ----------------------------- | ---------------------------- | -------------------------- | ------------------ |
| `develop`    | 本地开发模式         | `docker compose` 一键启动     | Vite 开发服务器，支持 HMR    | Compose 容器               | 启动时自动执行     |
| `native`     | 本机运行模式（本机版） | 用户运行唯一的 Windows exe    | 静态资源嵌入 exe，由 Gin 提供 | 用户电脑预装的 PostgreSQL  | exe 启动时自动执行 |
| `production` | 托管生产模式         | CI/CD 部署到项目服务器        | 随服务发布，通过域名访问     | 服务器或托管 PostgreSQL    | CI/CD 发布阶段执行 |

计划使用同一个命令入口承载各配置：

```text
stu-trade.exe                         # Windows 交付物，默认使用 native
stu-trade serve --profile=develop
stu-trade serve --profile=native
stu-trade serve --profile=production
stu-trade migrate up
stu-trade version
```

上述命令属于 MVP 目标接口，当前尚未实现。

### 本机运行模式

本机版不是 Win32 原生界面。exe 在 `127.0.0.1` 启动 HTTP 服务，提供嵌入的 Web UI 和 API，并在服务就绪后打开默认浏览器。Bun、Node.js 和 Docker 都只是构建或开发依赖，不是本机版的运行依赖。

老师的电脑需要提前安装、启动并按课程约定配置 PostgreSQL。直接运行 exe 时，宿主使用内置的本机默认连接参数自动连接，不要求用户输入命令行参数；连接成功后自动建表或升级 schema。宿主不负责安装 PostgreSQL，也不在缺少管理员凭据时创建数据库用户或数据库。开发和本机环境允许通过 `STUTRADE_DATABASE_URL` 覆盖约定默认值；生产连接信息只能由 CI/CD Secret 或服务器环境注入。

### 托管生产模式

CI 构建前端和 Go 服务，CD 在发布阶段执行 migration，再部署服务。服务监听内部端口，由反向代理或平台入口负责域名、TLS 和公网流量。用户通过网址使用系统，不需要安装客户端或 PostgreSQL。

### 本地开发模式

Docker Compose 负责启动 PostgreSQL、宿主和 Vite 开发服务器。Vite 将 `/api` 代理到宿主，前端保留热更新；所有依赖均在本地运行，不连接生产服务或生产数据库。

## MVP 范围

第一版围绕二手书交易实现：

1. 买方发布包含 ISBN 和最高价格的需求。
2. 卖方发布包含 ISBN、成色和售价的供给。
3. 当 ISBN 一致且卖方价格不高于买方报价时，引擎可以提出撮合请求。
4. 买卖双方确认撮合请求后建立交易。
5. 卖方可以直接接受需求，买方也可以直接购买供给。
6. 成交后双方可以查看并推进最小履约状态。

MVP 不包含真实支付、聊天、物流、竞价和自由文本智能匹配。ISBN 一致、不能与自己交易、挂单仍然有效等规则由宿主提供；撮合引擎只执行宿主授予的通用能力和规则回调。

## MVP 目录规划

```text
stu-trade/
├── cmd/
│   └── stu-trade/
│       └── main.go             # serve、migrate、version 命令入口
├── internal/
│   ├── app/                    # 配置、依赖装配和进程生命周期
│   ├── api/                    # Gin 路由、请求绑定、DTO 和响应
│   ├── user/                   # MVP 用户与所需能力接口
│   ├── listing/                # 买方需求、卖方供给和二手书字段
│   ├── trade/                  # 撮合请求、双方确认、成交与履约
│   ├── matching/               # 宿主规则及与 engine 的适配
│   ├── db/                     # sqlc 生成代码和领域存储适配器
│   └── webui/                  # 嵌入资源、静态服务和 SPA fallback
├── migrations/                # goose migration 与嵌入入口
├── queries/                   # 按领域组织的 sqlc 查询源
├── engine/                    # 通用撮合引擎 submodule
├── web/                       # React 前端 submodule
├── build/                     # 容器和本机版构建入口
├── compose.yaml               # develop 一键启动
├── sqlc.yaml
├── Makefile
└── go.mod
```

`internal/app` 是唯一装配全部依赖的位置。业务域只声明自己实际需要的能力；`internal/api` 不承载业务规则；`internal/db` 负责 sqlc 行类型与领域实体之间的转换以及 PostgreSQL 错误翻译；`internal/matching` 隔离宿主与撮合引擎的协议转换。

MVP 采用一个宿主进程，不把二手书、跑腿和租赁拆成微服务。后续场景优先作为宿主内的独立领域模块接入同一引擎，只有出现明确的独立部署需求时才重新评估进程边界。

## 当前仓库结构

```text
stu-trade/         业务宿主与项目文档
├── engine/        通用撮合引擎（submodule）
└── web/           React + TypeScript 前端（submodule）
```

## 克隆

```bash
git clone --recurse-submodules git@github.com:Jason-skd/stu-trade.git
```

已有工作副本可运行：

```bash
git submodule update --init --recursive
```

## 当前验证

```bash
go test ./...
(cd engine && go test ./...)
(cd web && bun install && bun run lint && bun run build)
```

更完整的业务范围和课程交付关系见 [`docs/implementation_plan.md`](docs/implementation_plan.md)。
