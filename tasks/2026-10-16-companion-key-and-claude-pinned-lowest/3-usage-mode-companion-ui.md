---
id: 3
slug: usage-mode-companion-ui
prd: docs/prds/2026-10-16-companion-key-and-claude-pinned-lowest.md
state: ready-for-agent
category: enhancement
blocked_by: [1, 2]
---

## What to build

端到端让运营者在浏览器里就能完成两件事：(i) 设置 / 切换 / 移除 companion 页的锁（通过 management key 调既有 setter 端点）；(ii) 在 Claude accounts 表的每一行切「始终最后」开关（通过 companion key 调任务 2 的两条端点）。本任务完成后，`/usage-mode.html` 顶部新增 LockState 三态卡、表新增「最低优先级」列、`localStorage` 拆成 `cpa-companion-key` 与 `cpa-management-key` 两把钥匙并存。

**两条不变性**：
- 页面是 `go:embed` 静态资产，**没有任何自动化测试**。这与既有 usage-mode.html / management.html 的惯例一致（参考 PRD 2026-08-18 / 2026-09-30）。
- `management.html` 不受影响：所有改动都在 `usage-mode.html` 一个文件内。

## Key interfaces

- LockState 三态机 —— 由页面 JS 在加载时通过探活新端点确定状态：

  | 状态 | 触发 | 渲染 |
  |---|---|---|
  | `unlocked-not-set` | cfg `companion-secret=""`（首次安装） | "未上锁"banner + 「设置锁」表单（新 + confirm + Save）。Save 用 management key POST `/v0/management/companion-secret` |
  | `locked-no-key` | cfg 有锁 + 本地无 companion key 或错 | "请输入 companion 密码"banner + companion key 输入框（提交后存 `cpa-companion-key` + 重新探活） |
  | `unlocked-with-key` | cfg 有锁 + 本地 companion key 正确 | "已解锁"banner + 「更换锁」表单（current optional + new + confirm）+ 「移除锁」按钮（current 必填） |

  状态转移后都要刷新页面（`location.reload()`），让中间件在下一个请求上立即生效。

- localStorage 双钥匙：
  - `cpa-companion-key` —— companion key（新增）。
  - `cpa-management-key` —— management key（既有 `cliproxy.mgmtKey` 改名或共存；二选一时优先保留既有 key 不改名以避免打破已登录运营者的浏览器缓存）。
  - 二者各自独立存，不互相覆盖。

- 「最低优先级」列渲染：
  - 仅 `entry.provider === "claude"`（或既有 `isClaude(entry)` 谓词）的行渲染 toggle。
  - 非 Claude 行不渲染该列（或渲染空 cell 但不渲染 toggle，避免视觉错位）。
  - 颜色用现有调色板的 `--ded`（橙），与现有 dedicated badge 同色调，加 `title="always picked last"` tooltip。
  - 初始化：调 `GET /v0/management/companion/claude-pin-states`（companion key） → 拿到 states 数组 → merge 进既有行数据（按 id 匹配）。无 companion key 时不调（页面在 `locked-no-key` 态本就没渲染表）。

- 三个新 API 调用 helper（与既有 `headers()` / `load()` 同款 fetch 封装）：

  | helper | endpoint | key |
  |---|---|---|
  | `setLock(form)` | `POST /v0/management/companion-secret` | management |
  | `fetchPinStates()` | `GET /v0/management/companion/claude-pin-states` | companion |
  | `setPinned(id, bool)` | `POST /v0/management/companion/claude-pin-states/:id` body `{pinned_to_lowest: bool}` | companion |

- 表的 merge 逻辑：现有表 row 已经是 `(auth_id, name, usage_mode, available_window, ...)` 形态；merge 进 `pinned_to_lowest` 后 toggle 初始化值与文案。

操作行为：

- toggle 切 true → POST → 200 → 切视觉 + 改本地 row 状态。
- POST 401（companion key 失效） → 弹回 `locked-no-key` 态（清掉 localStorage companion key，让运营者重输）。
- 其它 4xx/5xx → 在 `msg` 区显示后端 error 文案，不动 toggle 视觉态（保持上次成功态，避免乐观更新与服务器漂移）。

## Known data variants

companion-secret 输入形态（运营者手输）：

- 明文任意 unicode（与 secret-key 同款）
- 含特殊字符如 `+` `/` `=` 在某些 transport 里会 base64 转义，**不要**客户端做转义，让浏览器 fetch 直接 body JSON 即可
- 含首尾空白 → `trim` 后再发

claude_pin_to_lowest toggle 形态（服务端权威返回）：

- 既有 toggle UI 都用 `boolean`；服务端响应里 `pinned_to_lowest` 也是 JSON `true` / `false`。不要做字符串解析。

lock-form 验证：

- 首次路径：new + confirm 必须一致才发；不一致 → 前端阻止 + 红字提示，不调后端。
- 更换路径：current optional、new + confirm 一致才发。
- 移除路径：current 必填。

页面 key 状态变更的退化：

- 探活 companion 端点返回 401 时，页面进入 `locked-no-key` 态。
- 探活返回 200 但响应体不是预期的 `{states: [...]}` → 把 states 当作空数组处理，列里所有 toggle 默认 false（运营者需要主动切）。
- 探活本身 5xx → 同样空数组处理 + 顶部 msg 区提示。

## Acceptance criteria

- [ ] 页面加载按上面三态机切换，互不漏态：
  - 无 companion-secret 状态打开 → banner 出现「未上锁」+ 「设置锁」表单。提交后页面 reload，立即进入 `locked-no-key`（因为服务端现在有锁但 localStorage 没有）。
  - 输正确 companion key → 页面进入 `unlocked-with-key`，表渲染，「更换锁」/「移除锁」可见。
  - 故意输错 companion key → 401，banner 仍 `locked-no-key`。
  - 「移除锁」要求 current secret；提交后页面 reload，进入 `unlocked-not-set`。
- [ ] 「最低优先级」toggle 渲染：
  - Claude 行有 toggle；非 Claude 行不渲染（不渲染占位 cell 或渲染空 cell，看你的视觉选择）。
  - toggle 颜色与 dedicated badge 同色，加 tooltip。
- [ ] toggle 交互：
  - 切 true → POST 200 → toggle 视觉切到 ON、该行 `msg` 区显示"已启用始终最后"。
  - 切 false → POST 200 → 视觉切 OFF。
  - POST 401（companion key 失效）→ toggle 视觉回滚上次成功态，进入 `locked-no-key` 提示。
  - 其它 4xx/5xx → 视觉回滚 + `msg` 区显示后端 error。
- [ ] 双钥匙共存：表单里两个输入框各自独立存各自的 localStorage key，互不覆盖；operator 重开页面两个输入框都自动填回。
- [ ] 既有 usage-mode toggle（`claude_usage_mode` 切 shared/dedicated）+ available_window 编辑继续以 management key 工作正常。
- [ ] `go test ./...` 全绿（任务 1 / 2 测试不回归）。
- [ ] 既有 `usage-mode.html` 测试文件（`usage_mode_panel_test.go`、`usage_mode_window_test.go`）继续过 —— 但这些是测试 HTML 是否被服务、是否引用正确端点字符串的，不是测试 JS 行为；本任务对端点字符串的引用要继续一致。
- [ ] 变异检查（手工 verify 范围，不写自动化）：
  - 把 `cpa-companion-key` 拼成 `cpa-management-key` → 浏览器往返会发现 companion 操作会盖掉 management key。
  - 把 toggle 渲染成无条件全部行都渲染 → 非 Claude 行也会出现 toggle，违反 acceptance。
  - 把 locked-no-key 状态下也渲染表 → 违反 acceptance（信息泄露）。

## Verify

代码部分 `go test ./...` 全绿。

人工部分（`ready-for-human`，不可自动化）：

部署同 `v2026.10.<n>`（任务 1 / 2 / 3 一起）。运营者浏览器往返：
- 升级后首开 `/usage-mode.html`，看到「未上锁」banner + 「设置锁」表单。设一把 secret；reload 后看到 companion key 输入框；填正确 → 解锁；填错 → 401 banner。
- 在表里选一个 Claude 账号，切「始终最后」toggle → 视觉切到 ON、`msg` 区显示成功。
- 在 management key 输入框登入，把同一个账号的 `claude_usage_mode` 切 `dedicated` → 既有功能不受影响。
- 用 management key 登录 `management.html`（外部面板）：列表里看不到 `claude_pin_to_lowest`；尝试在它能编辑字段的地方写 `claude_pin_to_lowest` 400。
- 「更换锁」、「移除锁」、「回滚到无锁」三态循环都验一遍；每次 reload 后中间件行为正确。
- 把 companion key 与 management key 在不同的浏览器 profile 里分别登录，验证双钥匙独立：companion key 能 toggle pin-lowest 但不能切 usage-mode；management key 能切 usage-mode 但不能 toggle pin-lowest（前提是 companion-secret 已设）。

通过后写 `.req/journal.md` 一条 task-complete + ledger，并跑 `req:learn` 把可复用的洞见沉淀到 `CONTEXT.md` / `docs/learnings.md`。

## Out of scope

- 任务 1 / 2 的任何后端改动（仅消费）。
- `management.html`、其资产更新器、其路由；外部面板与本任务的页面独立演化。
- 在 usage-mode.html 之外再加一个 companion 页面（如 `available-window.html`、`priority.html`）—— 现有 PRD 只涉及 usage-mode.html 一个，未来再开。
- 「锁定页 secret 提示词」、「解锁失败提示文案」的多语言 —— 现有页面是英文文案，本任务沿用。
- 给页面加 build pipeline / minify / JS 框架 —— 现有 inline `<script>` 直接覆盖。
- 把 `cliproxy.mgmtKey` 这个既有 key 改名（避免已登录运营者的浏览器缓存瞬间失效）。
- 自动把现有 `priority: -N` 的设置迁到 `claude_pin_to_lowest: true`（见任务 2 out-of-scope）。