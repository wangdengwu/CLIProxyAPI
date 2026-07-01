# Claude 5h/7d 额度告警与阻断 (PRD)

## Problem
用户用 CLIProxyAPI 代理 Claude OAuth 订阅（Pro/Max）请求。订阅额度按 5 小时滚动窗口计量，接近上限时会被 Anthropic 拒绝，但代理当前对此无感知：既不告警，也不主动规避——只能等到真正 429 才被动切号，运营者也无法提前得知某个账号快满了。同时代理已把响应头 `Header.Clone()` 保留下来（含限流信息），却从未解析。

## Solution
在代理 Claude 请求的执行后路径解析响应头里的滚动窗口用量（5 小时为主，7 天若有则一并读取），按账号（auth）做两件事：

1. **告警**：5h 用量达到告警水位（默认 80%）时，向企业微信群机器人推送一条通知，附带该账号的 5h（及 7d，若有）剩余/上限/reset 信息。推送有去抖，不会在跨过水位后狂推。
2. **阻断**：5h 用量达到阻断水位（默认 85%）时，把该账号标记为临时不可用（直到窗口 reset），复用现有账号池选择逻辑自动切换到其他可用账号；只有当所有账号都被阻断时，才向客户端返回错误。

两个水位、webhook 地址、冷却时间均可配置。

## User Stories
1. As a 运营者, I want 某个 Claude 订阅账号 5h 用量到 80% 时收到企业微信通知, so that 我能提前知道额度快耗尽并做准备。
2. As a 运营者, I want 告警文案包含账号标识、5h 已用百分比、阈值、剩余/上限、窗口 reset 时间和模型, so that 我不用登录就能判断严重程度。
3. As a 运营者, I want 若响应头带 7 天窗口信息，告警里也一并展示 7d 的剩余/上限/reset, so that 我能同时掌握长周期额度。
4. As a 运营者, I want 同一账号在同一个 5h 窗口内告警只推一次（分档：到告警水位一次、被拒/打满一次）, so that 我不会被同一件事反复轰炸。
5. As a 运营者, I want 窗口 reset 后该账号重新武装告警, so that 下一个窗口再次接近上限时我仍会被通知。
6. As a 运营者, I want 一个正交的硬冷却（默认 5 分钟）兜底同一账号的推送间隔, so that 即使逻辑异常也不会短时间狂推。
7. As a API 使用方, I want 当某账号 5h 达到 85% 时代理自动切到其他账号, so that 我的请求尽量不因单账号接近上限而失败。
8. As a API 使用方, I want 只有所有账号都被阻断时才收到错误, so that 阻断在有余量时对我透明。
9. As a 运营者, I want 被阻断的账号在其 5h 窗口 reset 时间到达后自动恢复可用, so that 无需人工干预。
10. As a 运营者, I want 通过配置开关整体启停该功能, so that 不需要时零副作用。
11. As a 运营者, I want 告警水位、阻断水位、webhook 地址、冷却时间都可配置且有合理默认值, so that 我能按自己的风险偏好调参。
12. As a API 使用方, I want webhook 推送失败不影响我的正常请求, so that 告警通道故障不会拖垮代理主链路。

## Implementation decisions

### 组件
- **限流头解析器**（新增，放在 executor helps 层，与现有 usage 解析同层）：输入响应头，输出结构化的滚动窗口状态——每个窗口含 `limit / remaining / reset`（以及派生的已用比例）。至少解析 5h 窗口；7d 窗口若存在则一并返回，缺失则为空。解析器对缺失/畸形头必须健壮（返回"无数据"，绝不 panic、绝不误判为 0%）。
  - **开放项（实现第一步必须先做）**：`anthropic-ratelimit-unified-*` 系列头（含 5h、7d）的确切字段名未在文档中确认。**无需加临时日志**——代理已内建响应头记录链路：Claude executor 每条响应都调 `helps.RecordAPIResponseMetadata(...Header.Clone())`，当 `request-log: true` 时 `writeHeaders` 会把全部响应头（`anthropic-ratelimit-*` 不在脱敏名单，原样保留）写入 `logs/` 下的请求日志。第一步：开 `request-log`、跑一条真实 OAuth 订阅请求、从日志读出字段名与格式，据此定解析器契约，收尾把开关恢复原值。
- **去抖告警器**（新增）：按 `auth_id` 维护告警状态 `{窗口key=reset时间戳, 已告警档位集合, 上次发送时间}`。契约：给定某账号最新 5h 比例与配置阈值/冷却，决定是否发送告警；分档 `[alert 水位, 被拒/打满]` 各窗口各推一次；reset 时间戳变化即视为新窗口并重置已告警档位；正交硬冷却兜底。发送动作异步、失败吞掉（记日志），不阻塞调用方。
- **企业微信推送**（新增）：`POST {webhook_url}`，`msgtype: markdown`，content 为账号/5h 已用%/阈值/剩余/上限/reset/模型（+7d 若有），控制在 4096 字节内。
- **配置扩展**：在既有 config 结构下新增一节（键名 `claude_ratelimit_alert` 或与项目现有命名风格一致），字段：`enabled(bool, 默认 true)`、`webhook_url(string, 默认 "")`、`alert_threshold(float, 默认 0.80)`、`block_threshold(float, 默认 0.85)`、`cooldown(duration, 默认 5m)`。`webhook_url` 为空或 `enabled=false` 时功能整体关闭。
- **接入点（conductor 执行后路径）**：注意 `MarkResult` 的入参 `Result` **不携带响应头**。真正能同时拿到「选中的 auth」与「响应 Headers」的地方，是 conductor 收到 executor 返回的 `Response` / `StreamResult`（两者都暴露 `Headers`）、且 auth 仍在作用域的那段执行后路径。在此处：解析头 → 喂给去抖告警器（≥alert 触发推送）→ 若 5h ≥ block_threshold，将该 auth 标记为临时不可用并设恢复时间为 5h reset 时间。阻断的状态写入复用 `MarkResult` 同款的「注册表加锁改 auth」模式（对 `m.auths[authID]` 加锁后置 `Unavailable`/`NextRetryAfter`）。executor 保持无状态，不在其中做判定。

### 阻断复用现有机制（不造新轮子）
- 阻断 = 设置账号的既有"临时不可用 + 下次可重试时间"字段（`Unavailable` / `NextRetryAfter` 语义），恢复时间取 5h reset 时间。
- 现有选择器的"跳过被阻断账号"逻辑（`isAuthBlockedForModel` / `getAvailableAuths`）会自动切号；当可用集合为空时选择失败，客户端得到错误。窗口 reset 后 `NextRetryAfter` 过期，账号自动回到可用集合。无需新增 reset 清理逻辑。
- 阻断维度为**账号级**（订阅 5h 额度是账号维度、跨模型共享），非按模型。

### 固有限制（写入设计，不视为缺陷）
- 滚动窗口用量只能从**响应头**得到，即只有请求完成后才知晓。因此阻断基于"上次已知状态"，存在**一条请求的滞后**：跨过阈值的那条请求本身会放行，从下一条起才拦。这是响应头模型的固有限制，接受之。

## Testing decisions
测试尽量落在最高、最少的现有接缝上，验证外部行为而非实现细节。

1. **限流头解析（单元）**——接缝：executor helps 层，与 `internal/runtime/executor/helps/usage_helpers_test.go` 同风格（构造 header 直接断言解析结果）。用例：完整 5h+7d 头 → 正确的 limit/remaining/reset/比例；仅 5h、无 7d；头缺失/畸形 → 返回"无数据"不 panic。
2. **去抖告警器（单元）**——接缝：新组件自身的表驱动测试。用例：跨过 alert 水位推一次、同窗口再来不重推、reset 时间戳变化后重推、被拒/打满档独立推一次、硬冷却在异常高频调用下生效。
3. **阻断标记与切号（单元/集成）**——接缝：`sdk/cliproxy/auth/` 下，参考 `conductor_availability_test.go` 与 `selector_test.go`。用例：喂入 5h ≥ block_threshold 的解析结果 → 断言目标 auth 被标记为不可用且恢复时间等于 5h reset；选择器在有其他可用账号时切号成功；所有账号被阻断时选择返回空/错误；到达 reset 时间后账号恢复可用。
4. **webhook payload 序列化（单元）**——接缝：企业微信推送组件。用例：给定账号+5h(+7d)状态与阈值 → 断言 JSON 结构为 `{msgtype:"markdown", markdown:{content:...}}` 且 content 含预期字段、长度 ≤4096 字节；发送失败路径不向调用方冒泡错误。

## Out of scope
- **实时预判/前置拦截**：不在转发前预测本条请求是否会超限——只能基于上次响应状态，故不做。
- **滞回（hysteresis）重新武装水位**：YAGNI，窗口 key + 硬冷却已足够防狂推。
- **7d 窗口阻断**：7d 仅展示，不参与阻断判定。
- **其他 provider（非 Claude OAuth 订阅）**：本 PRD 只覆盖 Claude OAuth 订阅路径的统一限流头。
- **告警通道多样化**：本期只做企业微信群机器人一种出口；其他（邮件、Slack 等）不做。
- **持久化告警/阻断状态**：去抖状态与阻断标记为进程内内存态，不做跨重启持久化。

---

## 实现修正与交付状态（as-built，2026-07-01）

> 本节记录**实际实现**相对上文原始需求的偏差。偏差主要源于任务 1 的实测发现与代码结构约束；上文保留原始需求脉络不改，以本节为准。

**交付状态：已完成并合并入 `main`。**

| 任务 | 交付 | 合并 |
|------|------|------|
| 1 确认响应头字段 | spike（无代码），产出 CONFIRMED CONTRACT | — |
| 2 解析器 + 配置 + 日志 | `internal/runtime/executor/helps/ratelimit_helpers.go`、config `claude-ratelimit-alert` 节 | PR #3 |
| 3 企业微信去抖告警 | `helps/ratelimit_alert.go` + `ratelimit_alert_wire.go` | PR #4 |
| 4 账号级阻断 + 切号 | `helps/ratelimit_block.go`、`sdk/cliproxy/auth/ratelimit_block.go`、`selector.go` 修正 | PR #6 |

### 关键偏差

1. **响应头字段：不是 `limit/remaining`，而是 `utilization/status/reset`。**
   任务 1 实测生产流量确认：Anthropic 统一限流头**不返回 limit/remaining**，而是每个窗口给
   `Anthropic-Ratelimit-Unified-{5h,7d}-Utilization`（已用比例，小数，**可 >1.0**）
   ＋ `-Status`（`allowed`/`allowed_warning`/`rejected`）＋ `-Reset`（**Unix epoch 秒**）。
   因此解析器契约改为 `RatelimitState{FiveHour, SevenDay}`，每窗口 `UsedRatio/Status/Reset`；
   已用比例**直接取 `-Utilization`**（不再由 remaining/limit 派生）。
   → 影响上文 L31、L34、L49 及 User Story 2/3 中"剩余/上限"的措辞——实际文案用"已用% + status + reset"表达，不含 remaining/limit。
   （原"开放项：字段名待确认"已由任务 1 关闭。）

2. **接入点：不在 conductor，而在 Claude executor 的解析点。**
   `internal/runtime/executor/helps` 已 import `sdk/cliproxy/auth`（conductor 所在包），若 conductor 反向 import helps 会构成**循环 import**（Go 禁止）。为同时满足"解析器放在 helps 层"与无环，
   解析 + 日志（任务 2）、去抖告警（任务 3）、阻断判定（任务 4）均接在 **Claude executor `Execute`/`ExecuteStream` 现有的 `helps.RecordAPIResponseMetadata` 调用点**（上游响应头、`auth`、`cfg` 都在作用域；executor 仅新增只读旁路，保持无状态）。`CountTokens` 不接。

3. **阻断写入：包级 `ApplyRatelimitBlock` 转发到 Manager 加锁写。**
   executor 不持有 Manager 引用，故在 `sdk/cliproxy/auth` 新增包级 `ApplyRatelimitBlock(authID, resetAt)` → 已注册的活动 Manager 的 `applyRatelimitBlock`（锁 `m.mu` 改写 `m.auths[authID]` 的 `Unavailable`/`NextRetryAfter`，再 `scheduler.upsertAuth`，不持久化）。语义与 `MarkResult` 一致。

4. **选择器需要一处修正才能真正做到"账号级阻断"。**
   上文 L40 假设"现有选择器已会跳过被阻断账号"。实测发现 `isAuthBlockedForModel` 对**带 model 的请求**（真实 Claude 请求都带 model）在无 per-model state 时会提前 return，**不看账号级 `Unavailable`**。故任务 4 在该函数 `Disabled` 检查后新增账号级前置检查：`Unavailable && NextRetryAfter.After(now)` → 对所有 model 阻断并按 reset 自动恢复（签名不变）。此改动影响面超出本特性（任何账号级 `Unavailable` 旧用法现在也对 model 请求生效），已跑全量 `sdk/cliproxy/auth` 测试确认无回归。

### 未变项（与原设计一致）
配置字段与默认值（`enabled`/`webhook-url`/`alert-threshold=0.80`/`block-threshold=0.85`/`cooldown=5m`）、去抖分档与硬冷却语义、账号级阻断 + 复用选择器切号 + reset 自动恢复、一条请求的固有滞后、异步发送失败不冒泡、以及全部 Out of scope 边界均按原 PRD 落地。
