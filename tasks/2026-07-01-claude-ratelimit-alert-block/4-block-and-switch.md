---
id: 4
slug: block-and-switch
prd: docs/prds/2026-07-01-claude-ratelimit-alert-block.md
state: ready-for-agent
category: enhancement
blocked_by: [2]
---

## What to build
当某账号 5h 用量达到阻断水位（默认 85%）时，把该账号标记为临时不可用直到其 5h 窗口 reset，复用现有账号池选择逻辑自动切换到其他可用账号；只有当所有账号都被阻断时，选择才失败并向客户端返回错误。窗口 reset 时间到达后，账号自动恢复可用。

行为：
- 在任务 2 的执行后解析点，若解析出的 5h 已用比例 ≥ `block_threshold`，对该 auth 在注册表中加锁写入不可用状态：置 `Unavailable=true` 且 `NextRetryAfter=<5h reset 时间>`（复用 `MarkResult` 同款的 `m.auths[authID]` 加锁改写模式；不要在无锁处直接改）。
- 阻断为**账号级**（5h 订阅额度跨模型共享），不是按模型。
- 不新增选择器逻辑：现有 `isAuthBlockedForModel` / `getAvailableAuths` 已会跳过 `Unavailable && NextRetryAfter > now` 的账号；可用集合为空时选择返回空/错误，客户端得到失败。`NextRetryAfter` 到期后账号自然回到可用集合，无需额外清理。
- 固有滞后：跨过阈值的那条请求本身放行，从下一条起才拦（响应头模型固有，不视为缺陷）。
- `enabled=false` 时不做阻断。

## Key interfaces
- Manager 新增/复用一个加锁写入方法，如 `applyRatelimitBlock(authID string, resetAt time.Time)`：锁 `m.mu`，取 `m.auths[authID]`，置 `Unavailable`/`NextRetryAfter`。语义与 `MarkResult` 里设置不可用的路径一致。
- 消费任务 2 的 `RatelimitState.FiveHour`（Reset 与 UsedRatio）与 config 的 `block-threshold`/`enabled`。
- 依赖既有 `Auth.Unavailable`/`Auth.NextRetryAfter` 与选择器的 `isAuthBlockedForModel`/`getAvailableAuths`（不改其签名，只依赖其现有跳过语义）。

## Known data variants
- reset 时间格式见任务 1；`NextRetryAfter` 取该 reset 时刻。
- 5h 无 limit/无法算比例时不阻断（无数据不误伤）。

## Acceptance criteria
- [ ] 喂入 5h ≥ block_threshold 的解析结果 → 目标 auth 被置 `Unavailable`，`NextRetryAfter` 等于该窗口 5h reset 时间
- [ ] 存在其他可用账号时，选择器跳过被阻断账号、切号成功
- [ ] 所有账号都被阻断时，选择返回空/错误（客户端得到失败）
- [ ] `NextRetryAfter`（reset）到期后，账号重新出现在可用集合中
- [ ] 阻断为账号级，不因某一模型而只阻断该模型
- [ ] 状态写入走注册表加锁路径，无数据竞争
- [ ] `enabled=false` 时不发生阻断
- [ ] 现有转发/选择行为在未触发阻断时保持不变

## Out of scope
- 不做告警/推送（任务 3）。
- 不做实时前置预判——只能基于上次响应状态。
- 7d 不参与阻断。
- 不持久化阻断状态（进程内内存态，随重启清空）。
