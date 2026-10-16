---
id: 2
slug: claude-pinned-lowest-backend
prd: docs/prds/2026-10-16-companion-key-and-claude-pinned-lowest.md
state: ready-for-agent
category: enhancement
blocked_by: [1]
---

## What to build

端到端打通"运营者从 companion 端点给指定 Claude 账号打上「始终最后」标记；scheduler 立即把它送进最低桶；外部 management.html 既看不到也写不了"。本任务完成后，运营者 curl `POST /v0/management/companion/claude-pin-states/<id>` body `{pinned_to_lowest: true}`，下次 pick 调度器就把该账号排到 `math.MinInt` 那一桶的最末端（不论它自身 priority 是几）。多账号钉死时同桶轮询。同时，既有的 `GET /v0/management/auth-files` 不返回这个字段、`PATCH /v0/management/auth-files/fields` 不接受（沿用既有"未知字段 400"行为）。

**两条不变性**：
- 与现有 `claude_usage_mode`（Operator-set-keys 家族的一员）同形：**双写**到 `Metadata`（持久化源）+ `Attributes`（运行时镜像，selector 读这层）；空值/缺省/置 `false` 走"两边都删"，与既有 `priority`/`note` 同一惯例。
- **仅对 `type == "claude"` 生效**：其它 provider 写该字段被 400 拒绝；其它 provider 的 selector 读到该字段忽略。pin 钉死在语义上是 Claude-specific 的，不应污染其它 provider 的 priority 排序。

## Key interfaces

- 字段 `claude_pin_to_lowest` (bool) —— Operator-set-keys 家族新成员。落位与既有 `claude_usage_mode` 同：
  - 写入路径（PATCH 端点字段写）双写；`put` 旧值 → `false`/空 → 双删；与既有 `priority`/`note` 一致。
  - **必须**自动跟 `metadata_preserve` 走（既有 `claude_usage_mode` / `priority` / `note` 同款），跨 OAuth 登录不被覆盖。
- `authPriority(auth)` 扩展 —— 当前签名是 `(*Auth) int`。期望行为：
  - 若 `isPinnedLowest(auth)` 为真 → 直接返回 `math.MinInt`，**不论** `auth.Attributes["priority"]` 是几。
  - 否则按既有逻辑：`Attributes["priority"]` 字符串 trim → `Atoi` → 整数；解析失败回落 0。
- `isPinnedLowest(auth)` 助手 —— 类型 `(*Auth) bool`。契约：
  - `auth.Attributes["type"]` 不等于 `"claude"` → 永远 `false`（即便 Metadata 里有该键）。
  - 否则读 `Attributes["claude_pin_to_lowest"]` 优先；缺失回落 `Metadata["claude_pin_to_lowest"]`。规范化接受 `true` / `"true"` / `"1"` / `1`（与既有 bool 字段惯例一致）；其它都视为 `false`。
- 新增 companion 端点（**共用** Task 1 的 `CompanionAuthMiddleware`）：
  - `GET /v0/management/companion/claude-pin-states` —— 响应：

    ```json
    { "states": [ { "auth_id": "claude-abc", "pinned_to_lowest": true }, ... ] }
    ```

    仅列 `type == "claude"` 账号；`auth_id` 沿用现有 `ListAuthFiles` 中的 id 字段。
  - `POST /v0/management/companion/claude-pin-states/:id` body `{ "pinned_to_lowest": bool }`。行为：

    | 账号 type | body | 结果 |
    |---|---|---|
    | claude | true | 双写 + 触发 `ClearRatelimitBlock`（与 `claude_usage_mode → dedicated` 同一调用） |
    | claude | false | 双删（与 priority/note 空值即删同形） |
    | 非 claude | 任意 | 400 |
    | 不存在 | 任意 | 404 |

    写完后立即生效：复用 `claude_usage_mode` 那条 atomic 持久化 + 触发 manager 重 upsert 调度的链（与既有 `auth_files_patch_fields.go` 的 `claude_usage_mode` 块同源）。
- 既有端点的"隐藏"行为（**只过滤不破坏既有路径**）：
  - `GET /v0/management/auth-files` 的字段拼装（既有 `auth_files.go` 在 LIST 路径上组装 `entry` map）：**不要**把 `claude_pin_to_lowest` 加进 `entry`。其它字段（`priority`、`note`、`claude_usage_mode`、`available_window` 等）行为不变。
  - `PATCH /v0/management/auth-files/fields`：**不要**给该 PATCH 的请求结构体加 `RequestClaudeMapping` 字段。现有未知字段 400 的拦截器（既有）会自然挡住。
  - 这两个改动都是"按名单减一项"，不动签名、不动中间件、不动响应其它字段。

## Known data variants

`claude_pin_to_lowest` 是 bool，需扛住既有 bool 字段已能扛的形态：

- JSON `true` / `false`
- JSON `1` / `0`（既有 bool 字段已能接受）
- 字符串 `"true"` / `"false"` / `"1"` / `"0"`（与既有 bool 字段惯例一致）
- 大小写混用 `"True"` / `"TRUE"`（trim 后大小写归一）
- 缺省 / `null` / 空串 → 视为 `false` → 双删
- 仅在非 claude 账号上写（gemini/codex/kimi/antigravity 等）→ 400

账号 id 形态：现有 `ListAuthFiles` 的 id 字段是文件级唯一标识（auth.json 的 `ID()` 或文件名）。companion 端点的 `:id` 必须与该 id 形态完全一致，否则 POST 会一律 404，运营者找不到路径。

## Acceptance criteria

- [ ] `auth_priority_test.go` 覆盖下表：

  | attributes[claude_pin_to_lowest] | attributes[priority] | type | effective priority |
  |---|---|---|---|
  | 缺省 | 缺省 | claude | 0 |
  | 缺省 | 5 | claude | 5 |
  | true | 任意 | claude | math.MinInt |
  | true | 任意 | gemini | （忽略，按 base priority） |
  | true | 5 | claude | math.MinInt（pin 压过数字） |
  | false | 缺省 | claude | 0 |

- [ ] 变异检查：把 `math.MinInt` 退回 `base`（即把 pin 改成"加成偏移"或干脆删掉）→ 上表至少 3 行红。
- [ ] `scheduler_test.go` 新增：3 个 Claude 账号 A（无 pin）、B（pinned）、C（pinned）；反复 pick N 次，picked 序列必须是 `[A, A, ..., B, C, B, C, ...]`（A 优先用完才轮到 B/C，B/C 之间轮询）；A 不可用（block / disabled / window-closed）时只剩 B/C，仍轮询。
- [ ] `auth_files_pin_hidden_test.go`（新文件）：
  - GET `/v0/management/auth-files` 任意 entry 不含 `claude_pin_to_lowest` 键（即便该账号文件里写了 true）。
  - PATCH `/v0/management/auth-files/fields` body 含 `claude_pin_to_lowest` → 400，与既有未知字段行为一致。
- [ ] `companion_pin_states_test.go`（新文件）：
  - GET 不带 companion key + cfg `companion-secret=""` → 200，列表仅含 Claude 账号；非 Claude 账号不在 states 里。
  - GET 不带 companion key + cfg `companion-secret="x"` → 401。
  - GET 带正确 companion key → 200。
  - POST `/:id` body `true` + claude 账号 → Metadata+Attributes 双写；调用方下一行 `authPriority` 返回 `math.MinInt`；若该账号原本 `RatelimitBlockUntil` 非零，写完后被清（与 `claude_usage_mode → dedicated` 同链）。
  - POST `/:id` body `false` → Metadata+Attributes 双删；`authPriority` 回到 base priority；selector 行为还原。
  - POST `/:id` 非 claude 账号 → 400。
  - POST `/:id` 不存在 id → 404。
  - 当 `companion-secret` 已设，仅 management key（无 companion key）调两条端点 → 401。
  - 当 `companion-secret` 已设，正确 companion key + 同时带 management key → 200（key-management 与 api-key 互相独立，但两条端点不要求 management key，只看 companion key）。
- [ ] 既有功能不破坏：`go test ./...` 全绿（重点跑 `selector_test.go`、`scheduler_test.go`、`auth_files_*_test.go`、`auth_files_usage_mode_test.go`）。
- [ ] 变异检查：
  - 删掉 `isPinnedLowest` 里"非 claude 返回 false"那一行 → 非 claude 变红（至少一条）。
  - 把 pin 写入路径上少一边（仅写 Metadata 不写 Attributes，或反之反之）→ selector 测试红（因为 selector 读 Attributes）。
  - 把隐藏过滤删掉 → `auth_files_pin_hidden_test.go` 的 LIST 断言红。

## Verify

代码部分 `go test ./...` 全绿 + 变异检查通过。

人工部分（`ready-for-human`）：部署同 `v2026.10.<n>`（与任务 1 一起），运营者浏览器往返这一片功能（用 curl，因为本任务 UI 部分留到任务 3）：
- curl POST companion pin-states 把某个 Claude 账号标 true → 该账号下一次接口调用被排在其它账号之后（可用最近的代理日志或 selector 的 picked 序列观察）。
- curl GET `/v0/management/auth-files`（management key） → 看不到 `claude_pin_to_lowest` 字段。
- curl PATCH `/v0/management/auth-files/fields` body 含 `claude_pin_to_lowest` → 400。
- curl POST companion pin-states 把同一个账号标 false → 选号行为回到"无 pin"位置。

通过后写 `.req/journal.md` 一条 task-complete + ledger。

**回滚纪律**：本任务**必须**与任务 1 一起回滚（或任务 1 在前、本任务在后顺序回滚）。单独回滚本任务而保留任务 1 的 `CompanionAuthMiddleware` 会留下孤儿中间件；单独保留本任务而不用任务 1 的中间件则端点完全无鉴权，是安全事件。

## Out of scope

- 任务 3 的 usage-mode.html UI 改造（LockState 三态机、双钥匙 localStorage、「始终最后」列）。
- 任务 1 的 companion-secret 设定 / 中间件本身（仅复用其 `CompanionAuthMiddleware`）。
- `priority` 整数字段语义变化（pin 是叠加 / 正交的新机制，不替换 priority）。
- 非 Claude provider 的「最低优先级」标记（gemini / codex / kimi / antigravity 都不在本任务内；运营者明确说"claude 账号"）。
- 管理端的"按账号查 effective priority"端点（运营者只需要 toggle，不需要 debug 视图）。
- `claude_pin_to_lowest` 在 config diff 日志里的脱敏（既有 operator-set-keys 也都不脱敏；如后续真要脱敏属于另一个 PRD）。
- 把现有 `priority: -N` 的设置迁移到 `claude_pin_to_lowest: true`（两种机制语义不同，不自动迁移）。