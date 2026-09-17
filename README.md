# StuTrade

StuTrade 的业务后端与项目组合仓库。业务后端使用 Go；通用撮合引擎和 React + TypeScript 前端通过 Git submodule 接入。

## 仓库结构

```text
stu-trade/         业务后端与项目文档
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

## 验证

```bash
go test ./...
(cd engine && go test ./...)
(cd web && npm install && npm run lint && npm run build)
```
