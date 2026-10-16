---
id: 1
slug: companion-lock-and-setter
prd: docs/prds/2026-10-16-companion-key-and-claude-pinned-lowest.md
state: ready-for-agent
category: enhancement
blocked_by: []
---

## What to build

端到端打通"companion 页 + 后续 companion 端点有一个独立于 management key 的门"。本任务完成后，运营者从空 `companion-secret` 起步：可以首次 POST 设密（不需 `current_secret`），写入 `config.yaml` 并触发热重载；下一次 GET `/usage-mode.html` 立即被中间件拦截；可以携带正确 bearer / `X-Companion-Key` 解锁；可以携带旧 secret 覆盖；可以用旧 secret 清除回到首装状态。中间件同时支持明文与已 hash 化的存储值。

**两条不变性**：
- 默认空 `companion-secret` 时页面 **不** 拦截，与既有 `disable-control-panel` 默认行为不同（那条是默认关、显式开）。companion 页是默认开、显式锁。启动期要打 warn 日志把"未上锁"这件事说出来，让运营者有迹可循。
- 现存的 `/v0/management/*` 平面与 `management.html` 完全不动 —— 这是后续任务 2 / 3 的复用前提。

## Key interfaces

- `config.RemoteManagement.CompanionSecret` —— 与既有 `SecretKey` 同形态（裸 string，`LoadConfig` 沿用既有 `hashSecret` 路径：非空且不像 bcrypt 就 hash 后写盘）。
- `config.example.yaml` —— 在 `remote-management` 节下补 `companion-secret` 注释行，与 `secret-key` 注释同款写法。
- `CompanionAuthMiddleware` —— 新增函数，行为契约：
  - 空 `companion-secret` → 直接 `c.Next()`，不写库。
  - 非空 → 接受 `Authorization: Bearer <secret>` 或 `X-Companion-Key: <secret>` 任一。校验方式：若存储值是 bcrypt（`looksLikeBcrypt` 已存在），用 `bcrypt.CompareHashAndPassword`；否则 `subtle.ConstantTimeCompare([]byte(provided), []byte(stored))`。
  - 失败：401 + `WWW-Authenticate: Basic realm="Companion panel"`，让浏览器弹原生框。
- `POST /v0/management/companion-secret` —— 新增端点，**走 management middleware（不要 CompanionAuthMiddleware）**，因为首次设密时 companion 钥匙不存在。请求体：

  ```json
  { "current_secret": "...", "new_secret": "..." }
  ```

  行为表：

  | 当前 companion-secret | current_secret | 结果 |
  |---|---|---|
  | 空 | 任意 | set 新值（首次路径） |
  | 已设 | 正确 | set 或 clear（`new_secret=""` 表示清除） |
  | 已设 | 缺失 / 错 | 400 |

  写入路径：复用 `SaveConfigPreserveCommentsUpdateNestedScalar` 写 `remote-management.companion-secret`（与 `secret-key` 旋一致），**写完要触发配置 watcher 的热重载**，让同一进程内的下一次请求立刻看到新状态。
- 启动期 warn 日志：若 `cfg.RemoteManagement.CompanionSecret == ""`，启动日志打一条：「companion page is currently unlocked (no companion-secret configured). Set one via the page or `remote-management.companion-secret` to lock it.」
- 路由挂载：把 `CompanionAuthMiddleware` 应用到 `GET /usage-mode.html` 上（任务 2 的两条端点也共用这一中间件，但任务 2 自己挂）。

## Known data variants

`companion-secret` 的字段值是字符串，需扛住既有 `secret-key` 已能扛的形态：明文、bcrypt(`$2a$…` / `$2b$…`)、含特殊字符（`/` `+` `=`）、含 unicode、`TrimSpace` 后散落在文件中的空白。

请求体里的 `current_secret` 必须是**明文**，不能用 bcrypt（运营者交互面是输一把明文密码）。服务器拿这个明文与存储值（可能是 bcrypt 也可能是明文）按上面 `CompanionAuthMiddleware` 同一套比对逻辑校验。

## Acceptance criteria

- [ ] `LoadConfig` 路径：空 `companion-secret` 写入 cfg 不报错（保持空）；非空且不像 bcrypt → 自动 hash 后写盘 + 写回 cfg；非空已是 bcrypt → 不动。
- [ ] `CompanionAuthMiddleware` 单元 / 集成测试覆盖：空时 200 passthrough；非空 + 错 bearer / 错 X-Companion-Key → 401 + `WWW-Authenticate` 头存在；非空 + 正确 → 200；非空 + bcrypt 存储值 + 明文调用方 → 200。
- [ ] `POST /v0/management/companion-secret` 测试覆盖：
  - 首次路径：cfg 无 companion-secret，body `{new_secret:"x"}` → 200，写盘后值是 bcrypt 形态。
  - 覆盖路径：cfg 已有，body `{current_secret:"old", new_secret:"new"}` → 200，旧明文仍可解，但新明文才是真值。
  - 错 current → 400，存储值不变（同时验证"未改盘"）。
  - 清除路径：body `{current_secret:"old", new_secret:""}` → 200，下一次请求中间件走 passthrough。
  - 必须 management key：拿 management key 调通；只拿 companion key（或无）→ 401。
  - 持久化落 `config.yaml` 的 `remote-management.companion-secret` 行。
  - **同进程热重载**：同一 `Handler` 实例内，先调 POST 设密，再发一个走中间件的请求，必须看到新状态生效（不是要等重启）。
- [ ] 启动 warn 日志：空 `companion-secret` 时日志里出现上面那条文案。
- [ ] 既有功能不被破坏：`go test ./...` 全绿（重点跑 `auth_files_*_test.go`、`auth_files_usage_mode_test.go`、`auth_files_window_status_test.go`、既有 management middleware 测试）。
- [ ] 变异检查（PRD 2026-09-30 沉淀的纪律）：
  - 删掉 `WWW-Authenticate` 头那一行 → 中间件测试至少一条红。
  - 删掉 `current_secret` 校验 → 覆盖路径测试红。
  - 强制走 bypass 路径（即便 secret 非空也 `c.Next()`） → passthrough 测试红。
  - 启动 warn 删掉 → 启动日志断言红。

## Verify

代码部分用 `go test ./...` 全绿验证。

人工部分（`ready-for-human`）：部署 `v2026.10.<n>` 到 lab，运营者浏览器往返：
- 升级后无 companion-secret 状态打开 `/usage-mode.html`，看到 banner「未上锁」+ 设置表单（这是 Task 3 的 UI 雏形，本任务只在 backend 落地，页面 banner 文案可以先用最简版占位）。
- 在表单提交一把明文 secret，POST 走通；reload 后无 companion key 看到 401；填正确 secret → 200。
- 在表单用 current_secret 覆盖一次、再清除一次，每次 reload 后中间件立即生效。
- 既有 `/v0/management/auth-files`、`/v0/management/auth-files/fields`（PATCH priority=0 等）继续以 management key 工作正常。

人工 verify 通过后写 `.req/journal.md` 一条 task-complete + ledger。**回滚纪律**：本任务的 commit 与任务 2 的 commit 之间不能交叉；如果决定回滚本任务，应同时回滚任务 2（任务 2 的新端点消费本任务的中间件；单独回滚会留下孤儿端点）。

## Out of scope

- 任务 2 的 `claude_pin_to_lowest` 端点、selector 改动、隐藏过滤。
- 任务 3 的 usage-mode.html 主体改造（仅在最小范围内占位一条 banner 文案，**不要**把 LockState 三态机、pin-lowest 列、双钥匙 localStorage 写进本任务）。
- `management.html`、其资产更新器、其对外 PRD（just the local companion page 与 backend 端点）。
- 任何对既有 `/v0/management/*` 路由的鉴权语义修改（这些继续只认 management key）。
- 任何 env 变量支持（仅 yaml 内 companion-secret；与既有 secret-key 一样靠 `LoadConfig` 自动 hash）。