# Project knowledge

## Language

**Claude-usage-mode**:
Claude 账号 auth 文件的顶层键，标记该账号参不参与主动限流拦截。取值小写字符串：`shared`（默认/未设置即此）、`dedicated`、`exclusive`（`dedicated` 的别名，读时归一化为 dedicated）。

`shared` 账号受动态阈值拦截以保留余量；`dedicated` 不参与、一直接流量。丢失即退回 `shared`，独占账号会被误拦并推企微告警。

现在可由运营者通过管理接口设置（不再只能手工改文件）：`PATCH /v0/management/auth-files/fields` 的白名单已含 `claude_usage_mode`（写 dedicated 时双写 Metadata+Attributes、shared 时双删，空值即删同 priority/note），`ListAuthFiles` 也吐出当前值；内嵌伴随页 `/usage-mode.html` 提供 shared/dedicated 开关 UI。翻成 `dedicated` 时会顺带清除该账号可能挂着的内存限流拦截块（`ClearRatelimitBlock`，`applyRatelimitBlock` 的逆），使其立即恢复接流量，而非等 5h 窗口自然重置。

**Operator-set-keys**:
auth 文件里由运营者（而非 OAuth 流程）设定的顶层键，表达运营意图而非身份凭证：`claude_usage_mode`、`priority`、`note`、`headers`、`prefix`、`proxy_url`、`excluded_models`。

与凭证类键（`access_token` / `refresh_token` / `expired` / `last_refresh` / `id_token`）和身份类键（`email` / `type` / `account_id` / `project_id`）相对。凭证类由每次登录重新产生，运营者键必须跨登录存活。

取值形态不做规范化，读写都按原样透传：`priority` 可能是 JSON number 也可能是 string（合成器两种都接），`headers` 是嵌套对象，`excluded_models` 是数组。

**Available-window**:
auth 文件的顶层运营者键 `available_window`，声明该账号愿意参与调度的时段，格式 `"HH:MM-HH:MM"`。缺失或空串表示全天可用（绝大多数账号如此）。

语义：**左闭右开**（`18:00-09:00` 在 18:00 整开门、09:00 整关门）；`start > end` 表示跨午夜；`24:00` 表示当日终点；`start == end` 语义歧义，判为非法。锚定时区由全局配置 `auth-availability.timezone` 决定（默认 `Asia/Shanghai`，刻意不回落系统本地时区 —— 容器里通常是 UTC，会把 18:00 静默偏移八小时）。`auth-availability.enabled` 是 kill switch，关掉则所有窗口失效、全部账号回到全天可用。

**provider 无关、usage-mode 无关**：任何账号配了窗口就生效，不检查它是不是 Claude、是不是 shared。窗口外该账号对调度器完全不可见（硬门禁），但仍正常刷新 token。属 Operator-set-keys 家族，因此跨重新登录自动保留。运营者在 `/usage-mode.html` 编辑，该页同时显示服务端算好的 open/closed 徽章与下次开门时刻。

