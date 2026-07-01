---
id: 2
slug: parse-and-config
prd: docs/prds/2026-07-01-claude-ratelimit-alert-block.md
state: done
category: enhancement
blocked_by: [1]
---

## What to build
限流功能的公共基座：一个把 Claude 响应头解析成结构化滚动窗口状态的解析器，一个新的配置节，以及把两者接进 conductor 执行后路径、按开关打日志的落地。做完后能跑一条真实请求，在日志里看到该账号解析出的 5h（及 7d，若有）的 utilization/status/reset。

> ⚠️ **契约已被任务 1 实测校正（见 `1-confirm-header-fields.md` 的 CONFIRMED CONTRACT）**：Anthropic **不返回 limit/remaining**，而是每个窗口给 `Utilization`(已用比例小数，可 >1.0) + `Status`(`allowed`/`allowed_warning`/`rejected`) + `Reset`(**Unix epoch 秒**) + `Surpassed-Threshold`。解析器据此定契约，不要再找 limit/remaining。

行为：
- 解析器输入响应头，输出结构化窗口状态。至少解析 5h 窗口；7d 窗口若头里存在则一并解析，缺失则该窗口为空/不存在。对头缺失、字段畸形、reset 无法解析等情况必须健壮——返回"无该窗口数据"，绝不 panic、绝不把缺失误判成 0%。**已用比例直接取 `Anthropic-Ratelimit-Unified-{5h,7d}-Utilization`（不再由 remaining/limit 派生）**；无该头则视为无比例。`Reset` 解析 Unix 秒为 `time.Time`。
- 配置新增一整节（键名与项目 yaml 风格一致，建议 `claude-ratelimit-alert`），字段：`enabled`(默认 true)、`webhook-url`(默认 "")、`alert-threshold`(默认 0.80)、`block-threshold`(默认 0.85)、`cooldown`(默认 5m)。字段有合理默认值；`enabled=false` 或 `webhook-url` 空时，后续告警/阻断整体不生效（本任务只需读到配置并在解析/日志处尊重 enabled 开关）。
- 接入 conductor：在收到 executor 返回的 `Response`/`StreamResult`（两者都暴露 `Headers`）、且选中的 auth 仍在作用域的执行后路径，调用解析器，并在功能开启时打一条结构化日志（账号、5h/7d 各窗口的 utilization/status/reset）。executor 保持无状态。
- **注意**：统一限流头只在**上游 Anthropic 响应**出现；给客户端的下游响应只有 CORS/Content-Type，勿在下游侧解析。

## Key interfaces
- 新解析器（放在 executor helps 层，与现有 usage 解析同层）：`Parse(header http.Header) <RatelimitState>`，`RatelimitState` 含可选的 `FiveHour` 与 `SevenDay` 两个窗口，每个窗口有 `UsedRatio float64`（取自 `-Utilization`）、`Status string`（`allowed`/`allowed_warning`/`rejected`）、`Reset time.Time`（Unix 秒解析）。字段名以任务 1 的 CONFIRMED CONTRACT 为准（`Anthropic-Ratelimit-Unified-{5h,7d}-{Utilization,Status,Reset}`，另可选 `-Surpassed-Threshold` 与 `Retry-After`）。
- `config.Config`：新增 `ClaudeRatelimitAlert`（或同义）结构体字段，含上述 5 项，带 yaml/json tag 与默认值填充（参考现有 `QuotaExceeded QuotaExceeded` 的挂法）。
- conductor 执行后路径：现有拿到 `streamResult.Headers` / 非流式 `Response.Headers` 且持有 `auth` 的位置，新增一次解析+日志调用；不改变现有返回值与控制流。

## Known data variants
- 见任务 1 CONFIRMED CONTRACT。实测：reset 为 **Unix epoch 秒**（如 `1782884400`）；`Utilization` 为小数且**可 >1.0**（实测 1.09）；`Status` 为 `allowed`/`allowed_warning`/`rejected`。
- 无 unified 头（如 API key 请求或非订阅）→ 返回空 `RatelimitState`，调用方据此跳过。
- 仅 5h、无 7d 是正常情况，须支持。

## Acceptance criteria
- [x] 解析器对"完整 5h+7d 头"返回正确的 utilization/status/reset（reset 由 Unix 秒正确解析为时间）— `TestParseClaudeRatelimit_Full5hAnd7d`
- [x] 解析器对"仅 5h、无 7d"返回 5h 有值、7d 为空 — `TestParseClaudeRatelimit_FiveHourOnly`
- [x] 解析器对"头全缺失/字段畸形/reset 不可解析"返回空窗口且不 panic — `NoHeaders`/`NilHeader`/`MalformedUtilizationYieldsNilWindow`/`UnparseableResetKeepsWindowZeroTime`
- [x] 配置新节可从 yaml 加载，缺省时应用默认值（0.80 / 0.85 / 5m / enabled=true / webhook 空）— `internal/config/claude_ratelimit_alert_test.go`（默认/覆盖/部分覆盖三例）
- [x] 执行后路径在功能开启时输出含 5h/7d 结构化状态的日志；关闭时不输出、无额外开销 — 见下方"集成点"；`e.cfg.ClaudeRatelimitAlert.Enabled` 关闭时 `ParseClaudeRatelimit`/`LogClaudeRatelimitState` 均不调用
- [x] 现有请求转发行为与返回值不变（纯新增旁路）— 仅在 `RecordAPIResponseMetadata` 之后新增 2 行只读解析+日志；控制流/返回值未改；Claude executor 全量测试通过

## 实现说明（本任务落地）

### 集成点偏差（已与运营确认）
任务原文要求"接入 conductor"，但 `helps` 包已 import `sdk/cliproxy/auth`（conductor 所在包），若 conductor 反向 import `helps` 会构成**循环 import**（Go 禁止）。为同时满足"解析器放在 helps 层"（Key interfaces 硬约束）与无环，改为在 **Claude executor 现有 `helps.RecordAPIResponseMetadata` 调用点**接入（`claude_executor.go` 的 `Execute` 与 `ExecuteStream` 两处，均在上游 Anthropic 响应处、`auth`/`e.cfg`/上游响应头都在作用域）。executor 仅新增只读解析+日志、保持无状态。`CountTokens`（第三处调用点）**故意不接**——它是 /count_tokens 端点、非受限的消息请求，接入只会增加日志噪声。

### 遗留给任务 3/4 的字段（本任务范围外，覆盖对抗评审 C-3/C-4/I-1/I-5 提出）
以下头在 CONFIRMED CONTRACT 中存在，但**不在本任务 Key interfaces 定义的 `RatelimitState`（每窗口仅 UsedRatio/Status/Reset）范围内**，任务原文亦将 `Surpassed-Threshold`/`Retry-After` 标注为"可选"。这些头仍原样留在响应上，任务 3/4 需要时扩展 `ClaudeRatelimitState` 即可，无数据丢失：
- **任务 4（阻断恢复）需要**：`Retry-After`（秒，限流被拒时的权威恢复时长）；`Anthropic-Ratelimit-Unified-Overage-Status`（`rejected` 表示信用额度层面的封锁，与利用率限流是不同成因）。
- **任务 3（告警分级）可能需要**：`Anthropic-Ratelimit-Unified-{5h,7d}-Surpassed-Threshold`（已越阈档 0.9/1.0）。
- **代表窗口汇总**：`Anthropic-Ratelimit-Unified-{Utilization,Status,Reset}` 与 `-Representative-Claim`（Anthropic 自己指认的绑定窗口）当前未解析；任务 3/4 若不想自行取 5h/7d 较差者，可加字段读取。

## Out of scope
- 不做告警推送（任务 3）与阻断（任务 4）——本任务只解析+配置+日志。
- 不做去抖状态。
