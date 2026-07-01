---
id: 1
slug: confirm-header-fields
prd: docs/prds/2026-07-01-claude-ratelimit-alert-block.md
state: done
category: enhancement
blocked_by: []
---

## What to build
一个探针（spike）：确认 Anthropic 对 Claude OAuth 订阅请求返回的统一限流响应头里，5 小时与 7 天滚动窗口的**确切字段名**与**取值格式**，为后续解析器定契约。

**关键发现（已核实代码）**：代理已内建完整的响应头记录链路，**无需新增任何临时日志**。Claude executor 在每条响应上调用 `helps.RecordAPIResponseMetadata(ctx, cfg, status, httpResp.Header.Clone())`，当配置 `request-log: true` 时，`writeHeaders` 会把**全部**响应头排序后逐条写进该请求的日志文件；脱敏只作用于 `authorization/api-key/token/secret`，`anthropic-ratelimit-*` 头原样保留。日志落盘在 `logs/` 目录（`ResolveLogDirectory`），文件名形如 `v1-messages-<时间戳>-<id>.log`。

做法：
1. 在配置中设 `request-log: true`（其余保持不变）。
2. 用一个真实的 OAuth 订阅账号跑一条真实 Claude 请求。
3. 打开 `logs/` 下对应的请求日志文件，找到 `Headers:` 段，记录 `anthropic-ratelimit-unified-*` 系列（以及任何看起来是限流/窗口的头）的：字段名、值的格式（是绝对数、剩余数、百分比？reset 是 Unix 秒还是 RFC3339？）、5h 与 7d 各自对应哪些字段、以及哪些字段在何种情况下缺失。
4. 产出一份简短的字段契约（写入 PRD 的"开放项"处或本任务文件末尾），供任务 2 直接引用。
5. 收尾：把 `request-log` 恢复原值即可，**没有临时代码需要移除**。

## Key interfaces
- 产出物：一份字段映射契约，形如 `5h: {limit=<header>, remaining=<header>, reset=<header, 格式>}`、`7d: {...或标注不返回}`，供任务 2 的解析器直接引用。

## Known data variants
- 需实地确认（这正是本任务目的）：字段名可能是 `anthropic-ratelimit-unified-5h-*`、`anthropic-ratelimit-unified-*` 带 window 标识、或其它形态。
- 5h 一定存在；7d 可能存在也可能不返回——记录实测结论。
- reset 时间格式（Unix epoch 秒 vs RFC3339 字符串）必须实测确认，解析器据此定。
- 非订阅（API key）请求可能只返回 `anthropic-ratelimit-requests-*`/`-tokens-*` 而无 unified 头——记录以便解析器对"无数据"健壮。

## Acceptance criteria
- [x] 配置 `request-log: true`，用真实 OAuth 订阅账号跑通一条 Claude 请求，并在 `logs/` 下的请求日志文件 `Headers:` 段捕获完整响应头
- [x] 记录 5h 窗口字段名与格式（见下方 CONFIRMED CONTRACT）
- [x] 记录 7d 窗口是否返回及其字段名与格式（返回，见下方）
- [x] 字段契约写入可被任务 2 引用的位置（见下方）
- [x] 收尾：无临时代码需移除。按运营决定 `request-log` **保持开启**，并加 `logs-max-total-size-mb: 2048`（2GB 上限自动清理），两项均已持久化到 Postgres（跨重启存活）。生产已生效于 pod `cliproxyapi-5fdc6fbfb9-xrnpp`。

## CONFIRMED CONTRACT（2026-07-01 实测生产流量，OAuth 订阅账号）
> ⚠️ **重要：与 PRD/任务2 原假设的 `limit / remaining / reset` 模型不符。** Anthropic 统一限流头**不给 limit/remaining**，而是直接给 **utilization（已用比例）+ status（枚举）+ reset（Unix 秒）+ surpassed-threshold**。任务 2 的解析器契约需据此改写：`UsedRatio` 直接取 `Utilization`（不再由 remaining/limit 派生），另增 `Status`/`Reset`。

字段名（HTTP 头，大小写不敏感）：

**5h 窗口**
- `Anthropic-Ratelimit-Unified-5h-Utilization` — 已用比例，小数；**可 >1.0**（实测见 0.98 / 0.99 / 1.09）
- `Anthropic-Ratelimit-Unified-5h-Status` — 枚举：`allowed` / `allowed_warning` / `rejected`
- `Anthropic-Ratelimit-Unified-5h-Reset` — **Unix epoch 秒**（实测 `1782884400`）
- `Anthropic-Ratelimit-Unified-5h-Surpassed-Threshold` — 已越过的阈值档，小数（实测 `0.9` / `1.0`）

**7d 窗口**（确实返回）
- `Anthropic-Ratelimit-Unified-7d-Utilization` — 小数（实测 0.12 / 0.14）
- `Anthropic-Ratelimit-Unified-7d-Status` — `allowed` 等
- `Anthropic-Ratelimit-Unified-7d-Reset` — Unix epoch 秒（实测 `1783429200`）

**统一/代表窗口 + 附属**
- `Anthropic-Ratelimit-Unified-Utilization` / `-Status` / `-Reset` — 代表窗口的汇总值
- `Anthropic-Ratelimit-Unified-Representative-Claim` — 哪个窗口是代表，如 `five_hour`
- `Anthropic-Ratelimit-Unified-Fallback` (`available`) / `-Fallback-Percentage` (`0.5`)
- `Anthropic-Ratelimit-Unified-Overage-Status` (`rejected`) / `-Overage-Disabled-Reason` (`group_zero_credit_limit`)
- `Retry-After` — **秒**（实测 ~8970–9200，约 2.5h）；限流被拒时返回，可直接作阻断恢复时长的替代来源

**关键观察**
- 这些统一头出现在**上游 Anthropic 响应**（成功 200 及真实 429 `rate_limit_error` 均可能带）；**给客户端的下游响应只含 CORS/Content-Type**，不要在下游响应处解析。
- 真实 429 body：`{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit. Please try again later."}}` —— 正是 5h/7d 订阅额度耗尽场景。
- 采集时全部账号都处于限流（`5h-Status: rejected`, utilization ≥1.0），故 `allowed_warning`/`rejected` 两档都实测到了。

## Out of scope
- 不写解析器（任务 2）。
- 不改任何运行时行为，也不新增任何临时日志代码（复用现有 request-log）。
