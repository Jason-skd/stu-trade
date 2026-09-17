# StuTrade

StuTrade 是面向校园交易场景的撮合平台。本仓库的 `docs/` 是整个项目产品范围、架构和跨仓库契约的唯一文档真相源；本仓库的可执行代码只承载后端业务宿主、HTTP API 和运行装配。通用撮合引擎与 React + TypeScript 前端分别维护独立仓库，并通过 Git submodule 记录协作版本。

平台首先交付二手书交易 MVP，验证三条核心路径：系统自动撮合、卖方主动接单和买方主动购买。后续校园二手物品、跑腿和租赁场景复用同一个撮合引擎，各自在宿主中保留领域字段与成交规则。

## 架构边界

- **业务宿主**负责用户入口、领域数据、领域规则、撮合编排、交易确认和履约流程。
- **撮合引擎**负责需求、供给、候选匹配和撮合请求等通用能力，不依赖 Gin、PostgreSQL 或具体领域。
- **前端**独立构建和部署，只通过后端 API 协作；后端不构建、嵌入或提供前端资源。
- **PostgreSQL**是独立运行的数据服务，不打包进宿主程序。

主仓库文档描述整个项目如何发展；各子仓库只维护所属代码、局部开发入口和由代码生成的契约产物。跨仓库设计变更必须先更新主仓库文档，再在对应实现仓库落地，不能让三个 README 演化成互相竞争的产品规范。

前后端在开发和交付时都保持独立：

```text
web client ──HTTP──> Gin: /api/v1 ──> business host ──> engine
                                              │
                                              └──> pgx: PostgreSQL
```

## 技术选型

| 范围     | 选型                      | 用途                                 |
| -------- | ------------------------- | ------------------------------------ |
| 宿主语言 | Go                        | 业务实现、服务装配和可执行程序交付   |
| HTTP     | Gin                       | API 与健康检查                       |
| 数据访问 | pgx/v5 + sqlc             | PostgreSQL 连接池与类型安全查询      |
| 数据库   | PostgreSQL                | 业务数据、约束和事务                 |
| 数据迁移 | goose                     | 管理并执行版本化 SQL migration       |
| 前端     | React + TypeScript + Vite | 由 `web` 子仓库独立实现和交付         |
| 本地编排 | Docker Compose            | 一键启动隔离的开发环境               |

sqlc 生成代码属于数据库适配层，不直接作为 HTTP DTO 或业务实体使用。迁移文件是数据库 schema 的唯一事实来源；生成文件不手工修改。

## 部署配置

项目将运行环境定义为三个部署配置。部署配置负责选择依赖和运行参数，不改变业务规则，也不等同于 Gin 的 `debug`、`release` 和 `test` 模式。

| 配置         | 中文名称               | 运行方式                   | PostgreSQL                | Migration          |
| ------------ | ---------------------- | -------------------------- | ------------------------- | ------------------ |
| `develop`    | 本地开发模式           | `docker compose` 一键启动  | Compose 容器              | 启动时自动执行     |
| `native`     | 本机运行模式（本机版） | 用户运行唯一的 Windows exe | 用户电脑预装的 PostgreSQL | exe 启动时自动执行 |
| `production` | 托管生产模式           | CI/CD 部署到项目服务器     | 服务器或托管 PostgreSQL   | CI/CD 发布阶段执行 |

同一个命令入口承载各配置：

```text
stu-trade.exe                         # Windows 交付物，默认使用 native
stu-trade serve --profile=develop
stu-trade serve --profile=native
stu-trade serve --profile=production
stu-trade migrate up
stu-trade version
```

`serve` 是默认命令；`migrate up` 供 production 发布阶段独立执行。

### 本机运行模式

本机版 exe 在 `127.0.0.1` 启动后端 API。它不包含界面，也不要求 Bun、Node.js、Docker 或 C 运行库。

老师的电脑需要提前安装、启动并按课程约定配置 PostgreSQL。直接运行 exe 时，宿主使用内置的本机默认连接参数自动连接，不要求用户输入命令行参数；连接成功后自动建表或升级 schema。宿主不负责安装 PostgreSQL，也不在缺少管理员凭据时创建数据库用户或数据库。开发和本机环境允许通过 `STUTRADE_DATABASE_URL` 覆盖约定默认值；生产连接信息只能由 CI/CD Secret 或服务器环境注入。

### 托管生产模式

CI 构建 Go 服务，CD 在发布阶段执行 migration，再部署服务。服务监听内部端口，由反向代理或平台入口负责域名、TLS 和公网流量。

### 本地开发模式

Docker Compose 只负责启动 PostgreSQL 和后端宿主。前端仓库独立运行自己的开发服务器，并将 API 请求代理到后端；后端 Compose 不连接生产服务或生产数据库。

## MVP 范围

第一版围绕二手书交易实现：

1. 买方发布包含 ISBN 和最高价格的需求。
2. 卖方发布包含 ISBN、成色和售价的供给。
3. 当 ISBN 一致且卖方价格不高于买方报价时，引擎可以提出撮合请求。
4. 买卖双方确认撮合请求后建立交易。
5. 卖方可以直接接受需求，买方也可以直接购买供给。
6. 成交后双方可以查看并推进最小履约状态。

MVP 不包含真实支付、聊天、物流、竞价和自由文本智能匹配。ISBN 一致、不能与自己交易、挂单仍然有效等规则由宿主提供；撮合引擎通过这些能力编排通用挂单和撮合请求状态机。

## 宿主与撮合引擎

`engine` 已提供核心模型与首个发布挂单用例。后续业务 API 继续从三条交易路径的真实调用方提取最小协议，并由 engine 与宿主各自的行为测试共同固定。

| 责任       | 撮合引擎                                               | 业务宿主                                        |
| ---------- | ------------------------------------------------------ | ----------------------------------------------- |
| 通用模型   | Listing、MatchRequest 及其状态转换                     | 用户、二手书资源、订单和履约                    |
| 通用规则   | 价格兼容、候选优先级、成交价、确认与撤回               | ISBN 一致、禁止自买、资源有效和访问权限         |
| 数据持久化 | 声明所需 Protocol 并编排原子用例，不感知具体数据库     | PostgreSQL schema、查询、锁和 Protocol 适配     |
| 传输与运行 | 不感知 HTTP、Gin、日志或部署配置                        | API、错误翻译、日志和进程生命周期               |

engine 保持为同步、确定性的 Go 库。它通过自己消费的细粒度 Protocol 获取候选、执行宿主规则并开启原子工作单元，在工作单元内推进 Listing 和 MatchRequest、调用宿主订单创建能力。它不读取环境变量、不创建数据库连接、不启动后台协程，也不认识 PostgreSQL 类型。这样 engine 可以用纯单元测试覆盖通用状态机，宿主则以真实数据库适配 Protocol，并对 schema、锁和最终事务语义负责。

依赖方向固定如下：

```text
api ──> user / listing / trade ──> engine
                                      │ consumes
                                      v
                               engine Protocol
                                  ^        ^
                                  │        │
                         matching rules    db unit-of-work

app assembles every concrete dependency
```

engine 声明完成当前用例所需的最小 Protocol，宿主的 `matching` 与 `db` 适配器分别提供领域规则、候选查询、工作单元、存储和订单创建能力。适配器把宿主实体转换为 engine 的公开类型，并把 engine 的稳定领域错误转换为宿主语义。双方都不共享 Gin DTO、sqlc 行类型或数据库错误。

### 跨仓库契约

主仓库 `docs/` 定义产品范围、成交语义、部署方式和 HTTP 公共边界；engine 仓库实现通用撮合 API 与状态机；web 仓库实现页面、交互和前端依赖。发生冲突时，以主仓库当前规划为产品基线，再同步修订子仓库，而不是在三处长期保留不同语义。实现后，Go 类型与测试是 engine 运行协议的权威实现，生成的 OpenAPI 是前后端 HTTP 结构契约的权威产物，但二者都不能另行定义产品语义。

每类契约只有一个责任方：

| 契约 | 唯一责任方与权威来源 | 其他仓库的责任 |
| ---- | -------------------- | -------------- |
| 产品范围、用例语义、跨组件边界与部署拓扑 | 主仓库 `docs/` | engine 与 web 不另行扩展或改写产品语义 |
| engine 导出的 Go Protocol、模型、错误与状态机 | engine 的导出代码和行为测试 | 宿主按实际版本实现适配器；主仓库文档只描述能力边界，不复制签名 |
| HTTP 路径、DTO、状态码与错误结构 | 宿主接受并与运行时路由、DTO 同源的 OpenAPI | web 只从该 schema 生成传输类型，MSW 不另建契约 |
| PostgreSQL schema、约束、锁与事务语义 | 主仓库 migrations、queries 与真实数据库集成测试 | engine 不感知 PostgreSQL；web 不推断持久化结构 |
| 页面、交互、前端路由与前端发布 | web 实现和行为测试 | 主仓库只规定必须支持的用户流程和 HTTP 公共边界 |

在宿主尚未实现 HTTP API 期间，`web/openapi/stu-trade.yaml` 是用于并行开发的临时提案，不是长期的第二份契约。宿主接受或修订它时，必须在同一项跨仓库变更中确定宿主侧唯一 schema，切换 web 的生成输入、MSW 和漂移检查，并删除或停止维护临时副本。契约演进先修改责任方的权威来源，再更新消费者；消费者的 README、手写 DTO 或 mock 响应不能反向成为事实来源。

engine Protocol 遵循同一规则：每个 engine 用例只声明当前实际消费的小型能力接口。宿主拥有具体实现和事务保证，但不得在宿主中维护一套平行的 engine 接口；README 中的 `CandidateFinder`、`MatchPolicy`、`UnitOfWork` 和 `TradeCreator` 是能力边界名称，只有 engine 导出代码与测试落地后才构成具体运行契约。

## MVP 领域模型

MVP 只保留完成当前用例所需的实体：

| 实体         | 所有者 | 当前职责                                                                 |
| ------------ | ------ | ------------------------------------------------------------------------ |
| 用户与会话   | 宿主   | 提供发布者、购买者和确认者身份；支持注册、登录、退出和当前用户查询       |
| 二手书资源   | 宿主   | 记录 ISBN、书名、作者、成色和可选备注，作为 Listing 的不透明资源引用     |
| Listing      | engine | 记录所有者、资源引用、买卖方向、价格、状态和稳定排序所需时间信息         |
| MatchRequest | engine | 记录参与方、相关 Listing、发起方式、成交价、确认、状态和宿主订单引用    |
| 订单         | 宿主   | 固化买卖双方、成交来源、成交价格以及最小履约状态                         |

数据库列和 Go 字段在实现对应用例时再确定，避免仅为未来场景预留字段。当前必须成立的不变量是：

- MVP 固定使用人民币；金额以分为单位的正整数表示，不使用浮点数，也不为每条记录保存无变化的币种字段。
- ISBN 作为业务字符串规范化和校验，不转换为整数；MVP 接受有效 ISBN-10 或 ISBN-13。
- 用户不能与自己发布的需求或供给成交。
- 只有仍可交易的需求和供给才能参与撮合或直接成交。
- 同一条需求或供给不能因并发请求建立多个交易。
- 一个 Listing 同时最多参与一个 pending MatchRequest，进入 pending 后必须处于 reserved 状态。
- 自动撮合以卖方售价成交；卖方主动接单以买方报价成交；买方主动购买以卖方售价成交。
- 自动撮合产生的 pending MatchRequest 只有在双方均确认后才建立订单；重复确认必须幂等。
- 主动接单和主动购买也记录 MatchRequest，但在同一事务内直接完成，不进入待确认状态。
- 主动成交不要求发起方预先发布对向 Listing，也不创建对用户可见的虚假挂单；真实存在的发起方 Listing 可以复用。
- 交易、确认和挂单消耗必须在同一个数据库事务内完成。
- 时间在服务端生成并以 UTC 存储，对外使用带时区的 RFC 3339 表示。

## 核心流程

### 发布与自动撮合

1. API 完成身份识别与请求结构校验，宿主校验 ISBN、书名、金额和所有权。
2. 宿主原子保存二手书资源及对应 Listing，再显式触发一次同步自动撮合；engine 不运行后台轮询。
3. engine 通过 CandidateFinder 获取候选，按价格、创建时间和 ID 稳定排序，并通过宿主 MatchPolicy 检查 ISBN、自买限制和资源有效性。
4. 对通过的候选，engine 在 UnitOfWork 内按稳定顺序锁定双方 Listing、再次执行成交前检查、预留挂单并创建唯一的 pending MatchRequest。
5. pending MatchRequest 不创建宿主订单；并发或重复扫描命中同一组合时返回稳定的已有结果或冲突。

候选发现和最终写入分开处理。初次查询用于缩小候选范围，真正影响正确性的状态检查必须在写事务中再次执行，不能依赖先前读取的快照。

### 双方确认

1. 宿主鉴权保证当前用户只能操作自己作为买方或卖方参与的 MatchRequest。
2. engine 在 UnitOfWork 内锁定 MatchRequest 及关联 Listing，并幂等记录当前一方的确认。
3. 第一方确认后请求保持 pending；第二方确认时再次调用宿主规则。
4. 规则仍成立时，engine 在同一事务中完成 MatchRequest 和双方 Listing，并通过 TradeCreator 创建唯一宿主订单；任一 Listing 已被其他流程消耗时返回冲突。

### 主动成交

- 卖方主动接受需求时，以需求中的买方报价成交。
- 买方主动购买供给时，以供给中的卖方售价成交。
- 两条路径复用相同的身份、有效性和自买检查，不创建 pending 请求。
- engine 在单个 UnitOfWork 中锁定目标 Listing，记录参与方和真实存在的相关 Listing，创建 completed MatchRequest，通过 TradeCreator 创建订单并完成目标挂单。
- 卖方接单时需要提交或选择实际交付的二手书资源；买方购买时只需要买方身份和目标供给。engine 的公开协议不得要求调用者制造没有产品含义的 Listing。

三条路径最终进入同一种交易记录和履约流程，来源字段只用于解释成交方式，不派生三套订单模型。

### 拒绝与撤回

- 任一参与方可以拒绝 pending MatchRequest；engine 将请求标记为 declined，并把仍有效的双方 Listing 从 reserved 恢复为 open。
- 发布者可以撤回 open Listing；撤回 reserved Listing 时，engine 同时取消关联的 pending MatchRequest，并释放仍有效的对手 Listing。
- completed MatchRequest 和已建立订单不通过撤回挂单回滚。MVP 不提供成交后的争议或退款状态机。
- 拒绝、重复拒绝、撤回和确认并发时都必须返回稳定结果，不能遗留没有有效请求占用的 reserved Listing。

## 数据与事务

`migrations/` 是 schema 的唯一事实来源，`queries/` 是查询源，sqlc 生成代码统一放在 `internal/db`。业务域与 engine 都不直接依赖 sqlc；数据库适配器负责实体映射、PostgreSQL 错误翻译和 engine Protocol 的事务实现。

首批 migration 只覆盖用户与会话、二手书资源、Listing、MatchRequest 和订单。结构性规则优先由 PostgreSQL 保证，包括外键、唯一约束、检查约束和必要的状态组合约束。宿主实现的 UnitOfWork 按 Listing ID 的稳定顺序加锁，宿主订单以 MatchRequest ID 建立唯一约束，防止重试或竞争重复创建订单。

需要重点验证的竞争场景包括：

- 两个买方同时购买同一供给，最多一方成功。
- 一个供给同时被自动撮合与主动购买，最多建立一笔交易。
- 双方同时提交最终确认，只建立一笔交易。
- 确认与拒绝或撤回并发时，只能有一个终态，并正确释放或完成相关 Listing。
- 重复提交确认或直接成交请求，不重复扣减状态或创建交易。

goose migration 嵌入发布二进制。`native` 启动时只执行向前 migration；`production` 在服务发布前独立执行 migration，应用实例只校验数据库版本和可达性。

## HTTP 边界

业务 API 统一放在 `/api/v1`；`/health` 只表示进程存活，`/ready` 检查 PostgreSQL 和 schema 是否可用。运行时 API 结构与 OpenAPI 文档必须来自同一份路由和 DTO 定义，不能由 README 手工维护另一份字段清单。

用户、会话、出售挂单、求购需求、撮合和订单资源均映射到该版本前缀。前端通过独立发布的客户端和生成契约访问这些 API，后端不提供 SPA fallback 或静态资源路径。

市集纵切包含 ISBN、书名、作者、成色、价格和可选备注，支持按 ISBN 或书名搜索，并按买卖方向与成色筛选。MVP 固定人民币，前端负责按分格式化显示，不向每条记录附加可变币种。书籍封面不属于必填数据，第一版不实现图片上传。

MVP 用户系统采用宿主自有的本地账户：密码只保存安全哈希，登录态使用 `HttpOnly`、`SameSite` Cookie，并由服务端验证。开发代理和生产入口由前端部署或反向代理配置，不进入后端业务层。

Gin 注册健康检查和 `/api/v1`。不存在的路径统一返回后端 404，不回退到前端页面。

## 配置、构建与发布

部署配置只在 `internal/app` 的装配边界生效。领域包和 engine 不读取 `STUTRADE_PROFILE`，也不根据 `develop`、`native` 或 `production` 分支业务行为。配置加载顺序为命令行显式参数、环境变量、对应 profile 默认值；生产环境缺少必需配置时直接拒绝启动。

发布构建按以下顺序进行：

1. 校验 sqlc 生成结果和 migration 同步状态。
2. 执行 Go 格式、静态检查和测试。
3. 生成 Linux 服务和 Windows 本机版二进制。
4. 构建 production 容器镜像。

构建产物不依赖 Bun、Node.js、Docker 或 C 编译运行库。二进制包含 migration、版本号和构建提交信息，不包含前端资源。`native` 可以包含课程约定的本地 PostgreSQL 默认连接参数；它绝不复用生产凭据。`production` 只从部署环境读取密钥和数据库连接信息。

## 验证计划

| 层级                  | 重点                                                                  |
| --------------------- | --------------------------------------------------------------------- |
| engine 单元测试       | 价格边界、候选排序、状态转换、宿主规则拒绝、成交价和确定性结果        |
| 宿主领域测试          | 身份、所有权、状态转换、幂等确认和三条成交路径                        |
| PostgreSQL 集成测试   | migration、约束、sqlc 映射、事务、锁和并发竞争                       |
| HTTP 测试             | 路由、认证、绑定、权限、状态码和响应结构                              |
| 跨仓库端到端测试      | 由集成环境验证前端客户端与后端 API 的完整用户流程                     |
| 发布烟雾测试          | Compose 健康检查、Linux 服务启动和 Windows exe 连接预装 PostgreSQL    |

快速测试可以跳过外部 PostgreSQL；完整测试必须使用真实 PostgreSQL 和实际 migration，不能用 mock 证明约束、事务或锁行为。异步检查使用条件和明确超时，不使用固定休眠猜测完成时间。

## 交付阶段

阶段按可演示结果推进，不以创建空目录或补齐形式化 CRUD 作为完成标准：

1. **交付基线**：确定 engine 最小协议；建立配置、日志、migration、sqlc、健康检查、Compose 和发布构建；三个 profile 能连接各自数据库。
2. **账户与挂单纵切**：两个用户能够登录并发布、浏览和关闭自己的需求或供给；前端、API 和 PostgreSQL 全链路可运行。
3. **主动购买纵切**：买方能够直接购买供给，按卖方报价创建 completed MatchRequest 和唯一订单；先验证最小 engine Protocol、事务与订单协作。
4. **主动接单纵切**：卖方提交实际书籍接受需求，按买方报价成交，并复用主动购买的核心而非增加 mode 驱动的平行流程。
5. **自动撮合纵切**：发布相容挂单后产生 pending MatchRequest，双方确认后只建立一笔订单；覆盖候选排序、规则拒绝和并发确认。
6. **履约与交付**：双方查看订单并完成最小履约；验证 develop 一键启动、native 单 exe 和 production CI/CD 三条发布路径。

每一阶段在本仓库交付对应 migration、生成代码、API 和后端测试；前端在独立仓库按相同 HTTP 契约推进。MVP 完成标准是跨仓库集成环境能够演示三条核心路径，关键并发场景有自动化回归测试，且后端同一提交能够构建 Linux 服务与 Windows 本机版。

## MVP 目录规划

```text
stu-trade/
├── cmd/
│   └── stu-trade/
│       └── main.go             # serve、migrate、version 命令入口
├── internal/
│   ├── app/                    # 配置、依赖装配和进程生命周期
│   ├── httpapi/                # Gin 路由、请求绑定、DTO 和响应
│   ├── user/                   # MVP 用户与所需能力接口
│   ├── listing/                # 二手书资源、市集读模型和发布用例
│   ├── trade/                  # 宿主订单、TradeCreator 和履约
│   ├── matching/               # MatchPolicy 及与 engine 的类型适配
│   ├── db/                     # sqlc、engine Protocol 和领域存储适配
│   └── testutil/               # PostgreSQL 等测试资源辅助能力
├── migrations/                # goose migration 与嵌入入口
├── queries/                   # 按领域组织的 sqlc 查询源
├── engine/                    # 通用撮合引擎 submodule
├── web/                       # React 前端 submodule
├── build/                     # Windows 本机版打包与构建辅助脚本
├── compose.yaml               # develop 一键启动
├── sqlc.yaml
├── Makefile
├── Dockerfile                 # develop/production 多阶段镜像构建
└── go.mod
```

`internal/app` 是唯一装配全部依赖的位置。业务域和 engine 各自声明自己实际消费的最小能力；`internal/httpapi` 不承载业务规则；`internal/db` 负责 sqlc 行类型转换、PostgreSQL 错误翻译和 engine 工作单元；`internal/matching` 提供宿主规则并隔离双方类型。

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
make check
(cd engine && go test ./...)
(cd web && bun run check)
make build-linux
make build-windows
STUTRADE_TEST_DATABASE_URL='postgres://...' make test-integration
docker compose config --quiet
```

更完整的业务范围和课程交付关系见 [`docs/implementation_plan.md`](docs/implementation_plan.md)。
