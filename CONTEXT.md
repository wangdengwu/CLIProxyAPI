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

