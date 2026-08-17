---
id: 2
slug: wire-remaining-stores
prd: docs/prds/2026-08-17-preserve-auth-metadata-on-rebind.md
state: ready-for-agent
category: bug
blocked_by: [1]
---

## What to build

把切片 1 建立的保留机制接入其余三个后端存储，使保留行为**与后端无关**（PRD User Story 11）。

四个后端各自实现了近似拷贝的保存逻辑，其中只有文件后端在 storage 分支上做了元数据注入，另外三份漂移缺失 —— **调用方即使把元数据准备正确，这三个后端也会丢弃它**。这正是本次 bug 的实际成因：生产与 lab 部署运行的是 Postgres 后端，所以在这一刀落地前，切片 1 的修复在真实环境里不产生任何效果。

**这是让实际部署真正被修好的一刀。** 它单列而非并入切片 1，是为了让这三处改动被当作正式改动 review，而不是被当成附带修改带过。

## Key interfaces

- `PostgresStore.Save` / `ObjectTokenStore.Save` / `GitTokenStore.Save` 的 `case auth.Storage != nil` 分支（依次在 `internal/store/postgresstore.go`、`internal/store/objectstore.go`、`internal/store/gitstore.go`）— **当前**：直接调用 storage 的落盘动作，无任何元数据注入；**期望**：在落盘动作之前调用 `misc.ApplyPreservedMetadata(path, auth.Storage, auth.Metadata)`，与文件后端**逐字相同**（同函数、同参数、同顺序、同位置）。
- `misc.ApplyPreservedMetadata` — 切片 1 交付，**契约不改**。本切片只做接线，不动 helper 语义。
- 三个后端各自 storage 分支之后的**后置步骤不动**：Postgres 需 upsert 数据库记录、Git 需提交推送、对象存储需上传。这些差异正是不合并四份 switch 的原因。

## Acceptance criteria

- [ ] 三处的 helper 调用与文件后端逐字相同（同参数、同顺序、位于落盘动作之前）
- [ ] 三处分支**除新增这一行外无其他改动** —— diff 里每个后端只应出现一行新增（必要的 import 除外）
- [ ] 三个后端各自 storage 分支之后的后置步骤（PG upsert / Git 提交推送 / 对象存储上传）行为不变
- [ ] `go build ./...` 通过
- [ ] `go vet ./internal/store/...` 干净
- [ ] 全量 `go test ./...` 无回归（`internal/store/gitstore_test.go` 现有测试须仍通过）

## 验证方式（读之前先看这条）

本切片**没有测试兜底**，这是 PRD 里已评估并接受的 accepted risk：Postgres、对象存储、Git 三个后端的保存逻辑当前本就没有单元测试（Git 侧现有测试只覆盖仓库准备逻辑；Postgres 与对象存储无测试文件且需要外部依赖）。为一行接线搭建三套新测试基建的成本高于收益，而共享逻辑本身已在切片 1 被完整覆盖，可能漂移的表面仅剩「是否记得调用」这一行。

因此**验证依赖 review 而非测试**：review 时必须逐个 diff 这三个分支，确认调用存在、参数正确、位置在落盘动作之前。不要因为测试全绿就认为这一刀被验证过 —— 测试全绿在这三处是空信号。

若希望补上真实覆盖，`internal/store/gitstore_test.go` 已有临时仓库基建，给 `GitTokenStore.Save` 补测试是可行的 —— 但那是一个独立决定，不在本切片范围内。

## Out of scope

- 不新增 Postgres / 对象存储 / Git 的测试（accepted risk，理由见上）
- 不收敛四份重复的 `Save` switch
- 不改登录与导入的十个调用点
- 不改 helper 契约（切片 1 交付，本切片只接线）
- 不改三个后端的后置步骤
- 不加配置开关
