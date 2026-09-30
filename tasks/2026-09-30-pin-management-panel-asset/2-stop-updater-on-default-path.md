---
id: 2
slug: stop-updater-on-default-path
prd: docs/prds/2026-09-30-pin-management-panel-asset.md
state: ready-for-agent
category: enhancement
blocked_by: [1]
---

## What to build

任务 1 之后，默认路径服务的是内置面板资产，磁盘上那份文件再也不会被读。但后台自动更新器仍在每约 3 小时把第三方仓库的 latest 下载下来写盘 —— 下载一个没人读的 2.7MB 文件，并继续在启动/同步日志里刷那条 GitHub API 403（IP 级限流）的 warn。

这一片让后台更新器**只在逃生舱被显式配置时才运行**。默认路径上它完全不动，网络流量归零，那条 403 噪音随之消失，日志里剩下的 warn 都是真问题。

**依赖方向是硬的**：必须在任务 1 之后。反过来先关更新器、资产又还没内置，新 pod 上既没有内置资产、也不会去下载，`/management.html` 直接 404 —— 比当前故障更严重。

顺带在示例配置里补上 `remote-management.panel-github-repository` 的说明 —— 该键目前在示例配置里根本没有条目，而它现在承担了新语义：**非空不只是换源，而是解钉**（回到跟随该仓库 latest 的老行为）。同处的 `disable-auto-update-panel` 同样缺条目，一并补上。

## Key interfaces

- **「后台面板更新器该不该跑」谓词** — 新抽的纯函数，入参是配置快照，返回布尔。下列任一成立即返回 false：
  - 配置快照尚不可用（nil）
  - `remote-management.disable-control-panel` 为真
  - `remote-management.disable-auto-update-panel` 为真
  - `remote-management.panel-github-repository` 为空 ← **本次新增的那一条**

  前三条是把更新器定时循环里现有的三处提前返回原样搬进来，行为不变；只有第四条是新语义。纯函数、无 IO、无网络。

- **更新器的定时循环** — 当前契约：每次 tick 时依次检查上述前三个条件再执行同步。期望契约：改为调用该谓词，为假则跳过本次 tick，其余逻辑一行不改。记 debug 日志说明跳过原因，与既有三条的处理保持一致。

- **按需兜底下载路径** — 不在本片范围内，任务 1 已经让它在默认路径上不可达。注意它历史上**绕过** `disable-auto-update-panel`（文件缺失就下载），所以只改定时循环从来就修不好这个问题 —— 这条约束由任务 1 承担，这里只是不要把它改回来。

## Acceptance criteria

- [ ] 谓词表测试：配置为 nil / 面板禁用 / 自动更新禁用 / 逃生舱为空 —— 四种情况均返回 false
- [ ] 谓词表测试：逃生舱非空且其余门禁均放行时返回 true
- [ ] 定时循环调用该谓词决定是否同步，跳过时不产生任何出站请求
- [ ] 示例配置中 `panel-github-repository` 有条目，并写明「非空 = 跟随该仓库 latest = 解除内置资产的钉死」
- [ ] 示例配置中 `disable-auto-update-panel` 有条目
- [ ] 任务 1 的面板服务行为不受影响（默认仍吐内置资产，逃生舱仍吐磁盘文件）

## Out of scope

- 不碰面板处理器的服务逻辑（任务 1 的事）
- 不碰更新器的下载、digest 校验、原子写盘、fallback 页降级逻辑 —— 逃生舱路径上这些必须原样可用
- 不新增配置键
- 不改 `disable-control-panel` / `disable-auto-update-panel` 的既有含义
