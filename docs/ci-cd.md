# CI/CD 分层与发布流程

StuTrade 的自动化分为 PR Gate、Main Gate 和 Production Deploy 三个递进阶段。阶段按责任划分，不把同一套检查机械地复制到每个 Git 事件中。后续增加步骤时，应先判断它证明的是“允许合并”“main 可部署”还是“把已认证版本投入生产”，再放入相应阶段。

## 三个阶段的定性

### PR Gate：规范与治理闸门

PR Gate 回答“这组变更是否符合仓库契约并允许合并”。它在 `pull_request` 创建或更新时运行 `make pr-check`，覆盖：

- submodule revision 与父仓库记录一致；
- 生成代码没有漂移，Go 格式和静态分析通过；
- 宿主、engine 和 web 的单元及行为测试通过；
- web 的 OpenAPI、lint、测试和生产构建通过；
- 真实 PostgreSQL migration 与适配集成测试通过。
- GitHub Actions workflow 通过锁定版本的 actionlint 检查。

新提交会取消同一 PR 尚未完成的旧运行。PR 的 head 或 GitHub 临时 merge commit 通常不是最终 main commit，因此这一阶段不发布生产镜像，也不作为生产部署凭证。

### Main Gate：可部署闸门

Main Gate 回答“main 当前这个精确 commit 是否可部署”。每次 push 到 `main` 都运行，包括 PR merge commit 和允许直接提交到 main 的维护提交，例如 submodule revision 更新。

Main Gate 使用父仓库 commit SHA 构建并推送两个 ARM64 镜像：

```text
ghcr.io/jason-skd/stu-trade-backend:<commit-sha>
ghcr.io/jason-skd/stu-trade-web:<commit-sha>
```

镜像构建使用独立的 GitHub Actions BuildKit layer cache；cache 加速相同依赖和未变化层，但不是发布凭证。构建完成后，`make release-smoke` 使用镜像 digest 启动临时 PostgreSQL、执行 production migration、启动 backend/web，并检查 `/ready` 和首页。烟测针对将来真正部署的 ARM64 镜像，而不是另行编译的替代品。

成功后工作流上传 `deployment-manifest`，记录：

```text
SOURCE_SHA=<main commit>
BACKEND_IMAGE=<repository>@sha256:<digest>
WEB_IMAGE=<repository>@sha256:<digest>
```

这份 manifest 是该 main commit 的可部署凭证，保留 90 天。GHCR 中按 SHA 保存的镜像及其 digest 是不可变发布物；普通 Actions cache 不能替代它们。

### Production Deploy：真实生产发布

Production Deploy 回答“是否把已经通过 Main Gate 的版本投入生产”。只有符合 `vX.Y.Z` 或 `vX.Y.Z-prerelease` 的 tag 会触发。

tag 工作流不会重新运行 PR 检查、构建镜像或执行 Main Gate 烟测。它只会：

1. 验证 tag 格式，并确认 tag commit 位于 `main` 历史中；
2. 等待同一 commit SHA 的 Main Gate 成功，最长 20 分钟；
3. 下载并校验该 Main Gate 的 `deployment-manifest`；
4. 让 GHCR 的版本 tag 指向 manifest 中已经烟测的相同 digest；
5. 在 production Runner 上运行 `make deploy-production`；
6. 备份 PostgreSQL、执行向前 migration、启动认证镜像并检查本机和公网端点。

因此 Git tag、main commit、部署凭证和生产镜像之间存在精确映射：

```text
v1.2.3 -> main commit SHA -> deployment-manifest -> backend/web image digests
```

如果相应 Main Gate 失败、没有运行或部署凭证已过期，tag 不得绕过闸门重新构建并部署。

## 触发与 Make 入口

| GitHub 事件 | 阶段 | Make 入口 | 不属于该阶段的工作 |
| --- | --- | --- | --- |
| `pull_request` | PR Gate | `make pr-check` | 发布镜像、生产烟测、部署 |
| push `main` | Main Gate | `make release-smoke` | 重跑 PR 规范闸门、真实部署 |
| push `v*` tag | Production Deploy | `make deploy-production` | 重建镜像、重跑 PR/Main 检查 |
| 手动触发 Native Delivery | 独立课程交付 | `make native` | main 可部署认证、production 部署 |

Main Gate 的镜像构建与 digest 记录由 GitHub Actions 的 Buildx actions 完成，因为它们负责 GHCR 登录、BuildKit cache 和跨平台构建；Make 的 `release-smoke` 负责验证生成的精确镜像。Production Deploy 的版本 tag promotion 由 Buildx 完成，Make 的 `deploy-production` 负责服务器生命周期。

## Make 入口的实际命令

`make pr-check` 依赖 `make check` 和 `make test-integration`：

```text
sqlc generate
go generate ./internal/httpapi
git diff --exit-code -- internal/db internal/httpapi/contract.gen.go
gofmt -l ...
go vet ./...
go test -race -timeout 10m ./...
cd engine && go vet ./... && go test -race -timeout 10m ./...
cd web && bun install --frozen-lockfile && bun run check
go test -tags=integration ... ./internal/auth ./internal/listing ./migrations
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
```

`make release-smoke` 要求 `BACKEND_IMAGE` 和 `WEB_IMAGE`，并运行 `deploy/smoke.sh`。脚本创建隔离的 Compose project，执行 migration，启动精确镜像，检查服务后删除测试容器和 volume。

生产 Compose 同时把 PostgreSQL 18.0 Alpine 固定到多架构 manifest digest。Main Gate 烟测和 Production Deploy 因而使用父仓库 revision 记录的同一数据库镜像；更新 PostgreSQL 必须通过新的父仓库提交和 Main Gate 认证，不能在发布阶段跟随可变 tag 漂移。

`make deploy-production` 同样要求两个 digest image reference，并运行 `deploy/deploy.sh`。脚本使用部署锁防止并发发布，拉取镜像、备份数据库、执行 migration、切换服务并检查本机入口。

## 新步骤应放在哪一层

使用以下判断顺序：

1. 失败是否表示变更不应合并，例如格式、生成漂移、协议、单元测试或治理问题？放入 PR Gate。
2. 失败是否表示 main 的产物无法在 production 拓扑启动或运行，例如跨平台镜像、migration、Compose wiring 或 readiness？放入 Main Gate。
3. 该步骤是否会改变生产外部状态，例如数据库备份、migration、版本 promotion、服务切换或公网验证？放入 Production Deploy。
4. 若步骤服务于独立交付物而不证明 production 可部署，例如 Windows native 课程包，使用独立的手动 workflow。

同一个行为原则上只在最早能够证明其责任的阶段执行。后续阶段消费前一阶段按 commit SHA 和 digest 记录的结果，而不是再次构建“理论上相同”的产物。

## Production Deploy 启用条件

合并工作流会立即启用 PR Gate 和 Main Gate，但满足以下外部条件前，不得创建 production tag：

- 树莓派已经注册 repository-scoped self-hosted Runner，并具有 `Linux`、`ARM64` 和 `stutrade-production` 标签；
- GitHub 已建立 `production` Environment，并将 `STUTRADE_PUBLIC_URL` variable 设为实际 HTTPS 入口；
- `/opt/stu-trade/.env` 已在服务器上以仅部署用户可读的权限保存 PostgreSQL 与 StuTrade 配置；
- Cloudflare Tunnel 已将生产 hostname 转发到树莓派的 `http://127.0.0.1:8081`；
- GHCR 中两个 package 允许本仓库的 `GITHUB_TOKEN` 写入，并允许 production job 读取。

这些是仓库之外的部署状态，不由 PR diff 证明。`v0.1.0` 的 Production Deploy
已经使用受保护的 `v*` tag、专用 ARM64 Runner 和 production Environment 完成
digest promotion、数据库备份与迁移、服务切换以及公网检查；后续发布仍须针对目标
revision 重新验证同一组外部条件，不能用此前成功的部署替代当前证据。

## 发布操作

先确认目标 main commit 的 Main Gate 成功，再创建并推送 tag：

```bash
git tag -a v1.2.3 -m "Release v1.2.3" <main-commit>
git push origin v1.2.3
```

即使 tag 在 Main Gate 尚未结束时推送，Production Deploy 也只会等待对应 SHA 的结果，不会抢先部署。

## 回滚与发布物保留

仓库维护者负责从已认证的 `deployment-manifest` 选择回滚版本并核对 source SHA 与两个镜像 digest；production Environment 的发布操作员负责批准和执行生产变更。两项责任可以由同一人承担，但回滚不得从可变 tag、Actions cache 或重新构建的镜像推断发布物。

回滚前先判断目标版本与当前数据库 schema 是否向后兼容。兼容时，使用该版本 manifest 记录的精确 backend/web digest 重新运行 `make deploy-production`，随后检查本机 `/ready`、首页以及配置的公网 `/ready`。不兼容时，不得直接启动旧应用；发布操作员应进入维护窗口，使用目标发布前的受控数据库备份恢复 schema 与数据，再部署对应 digest 并完成同样的本机和公网检查。失败的回滚保持维护状态，并保留恢复所用备份与日志供后续处置。

`deployment-manifest` 在 Actions 中保留 90 天。GHCR 不得删除仍被受支持版本、90 天内的 manifest 或生产回滚记录引用的 digest；只有仓库维护者确认相应版本不再受支持且没有保留中的回滚记录后，才能清理这些镜像。生产数据库备份的保存周期和异地副本由 production 发布操作员按服务器运维策略管理，仓库自动化不擅自删除备份。
