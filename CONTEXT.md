# Project knowledge

## Language

**Claude_usage_mode**:
Claude 账号的 auth 文件里的顶层键，标记该账号参不参与主动限流拦截。取值是小写字符串：`shared`（默认，未设置时即此）、`dedicated`、以及 `exclusive`（`dedicated` 的别名）。

`shared` 账号受动态阈值拦截以保留余量；`dedicated` 账号不参与，会一直接流量。标记丢失即退回 `shared`，独占账号会被误拦并推出企微告警。

目前只能手工改文件设置 —— 管理接口的字段修改只认 `prefix` / `proxy_url` / `headers` / `priority` / `note`，不认这个键。

**Operator-set-keys**:
auth 文件里由运营者（而非 OAuth 流程）设定的顶层键，表达运营意图而非身份凭证：`claude_usage_mode`、`priority`、`note`、`headers`、`prefix`、`proxy_url`、`excluded_models`。

与凭证类键（`access_token` / `refresh_token` / `expired` / `last_refresh` / `id_token`）和身份类键（`email` / `type` / `account_id` / `project_id`）相对。凭证类由每次登录重新产生，运营者键必须跨登录存活。

取值形态不做规范化，读写都按原样透传：`priority` 可能是 JSON number 也可能是 string（合成器两种都接），`headers` 是嵌套对象，`excluded_models` 是数组。

