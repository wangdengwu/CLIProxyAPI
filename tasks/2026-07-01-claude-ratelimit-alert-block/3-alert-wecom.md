---
id: 3
slug: alert-wecom
prd: docs/prds/2026-07-01-claude-ratelimit-alert-block.md
state: done
category: enhancement
blocked_by: [2]
---

## What to build
当某账号 5h 用量达到告警水位（默认 80%）时，向企业微信群机器人推送一条带去抖的通知，文案包含 5h（及 7d，若有）的账号/已用%/阈值/剩余/上限/reset/模型。

行为：
- **去抖告警器**：按 `auth_id` 维护状态 `{窗口key=reset时间戳, 已告警档位集合, 上次发送时间}`。给定某账号最新 5h 状态 + 配置（alert_threshold / cooldown）：
  - 分档 `[alert 水位, 被拒/打满]`，同一窗口内每档只推一次；
  - reset 时间戳变化即视为新窗口，清空已告警档位（重新武装）；
  - 正交硬冷却（默认 5m）：同一账号两次推送最小间隔，作为异常兜底；
  - 状态为进程内内存态，不持久化。
- **企业微信推送**：`POST {webhook_url}`，body `{msgtype:"markdown", markdown:{content:...}}`，content 含账号、5h 已用%、阈值、剩余/上限、reset 时间、模型；若解析出 7d 窗口，附带展示 7d 的剩余/上限/reset。content 控制在 4096 字节内。发送**异步**、失败仅记日志、绝不向主请求路径冒泡错误。
- 接入任务 2 的执行后解析点：解析出 5h 状态后喂给告警器；`enabled=false` 或 `webhook_url` 空时不发送。

## Key interfaces
- 去抖告警器：`ShouldAlert(authID string, state <RatelimitState>, cfg) (level, bool)` 或等价——纯判定、可单测，不自带 IO。
- 推送器：`Send(ctx, webhookURL, payload)`；payload 构造函数 `BuildMarkdown(authID, model, state) <WeComMessage>`，序列化为企业微信 markdown JSON。
- 消费任务 2 的 `RatelimitState`（含 5h 必有、7d 可选）与 config 的 `alert-threshold`/`cooldown`/`webhook-url`/`enabled`。

## Known data variants
- 7d 窗口可能缺失 → 文案只含 5h，不得因缺 7d 报错或留空占位。
- reset 时间格式见任务 1；文案里以人类可读时间展示。

## Acceptance criteria
- [x] 5h 首次跨过 alert 水位 → 推送恰好一次 — `TestShouldAlert_FirstCrossFires` + `TestMaybeAlert_EnabledCrossingDispatchesOnce`
- [x] 同一窗口内后续请求仍 ≥ 水位 → 不再推送 — `TestShouldAlert_SameWindowNoRepeat`
- [x] reset 时间戳变化（新窗口）后再次 ≥ 水位 → 重新推送 — `TestShouldAlert_NewWindowRearms`
- [x] "被拒/打满"档与 alert 档在同一窗口内各自独立推一次 — `TestShouldAlert_RejectedAndAlertTiersEachFireOnce`
- [x] 硬冷却在异常高频调用下限制同一账号推送间隔 — `TestShouldAlert_HardCooldownSuppressesWithinInterval`
- [x] payload 为合法企业微信 markdown JSON，含约定字段，长度 ≤4096 字节 — `TestBuildClaudeRatelimitMarkdown_Full` + `_ClampsTo4096`
- [x] 有 7d 时文案含 7d 段；无 7d 时文案正常且不含 7d 段 — `_Full`（有 7d）+ `_FiveHourOnly`（无 7d）
- [x] webhook 发送失败不影响请求转发（错误被吞并记日志）— 异步 goroutine + recover + 只记日志，永不回传主路径；`TestSendWeCom_Non2xxReturnsError` 证明错误被返回给吞错的 goroutine
- [x] `enabled=false` 或 `webhook_url` 空时不发送 — `TestMaybeAlert_DisabledDoesNotDispatch` + `_EmptyWebhookDoesNotDispatch`

## 实现说明
- **契约修正（延续任务 1/2）**：任务原文文案要求含"剩余/上限"，但 Anthropic 统一限流头**不返回 limit/remaining**（见任务 1 CONFIRMED CONTRACT）。故文案改以**已用%（utilization）+ status + reset** 表达用量,不臆造 remaining/limit。
- **落点**：`internal/runtime/executor/helps/ratelimit_alert.go`（`ShouldAlert` 去抖 + `BuildClaudeRatelimitMarkdown` + `SendWeCom`）+ `ratelimit_alert_wire.go`（`MaybeAlertClaudeRatelimit` 集成胶水,持进程级默认 alerter、解析 cooldown、异步 fire-and-forget)。接入 Claude executor `Execute`/`ExecuteStream` 的任务 2 解析点(与 `LogClaudeRatelimitState` 同处;`CountTokens` 不接)。
- **去抖粒度**：`ShouldAlert` 注入 `now`(纯判定、无 IO、无内部 `time.Now()`),按 `auth_id` 维护 `{windowKey=reset.Unix(), 已告警档位集合, lastSent}`;新窗口重置档位、每档每窗一次、正交硬冷却(默认 5m,首次豁免)。`-race` 通过。
- **发送**：异步 goroutine + `recover`,用**独立 background context**(不随请求 ctx 取消),失败仅记日志。

## Out of scope
- 不做阻断/切号（任务 4）。
- 不做企业微信以外的告警通道。
- 不持久化去抖状态。
