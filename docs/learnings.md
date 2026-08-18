# Learnings

Curated, bounded conventions and gotchas that outlived a single requirement. An entry that graduates into a stable convention or decision moves into CONTEXT.md or docs/adr/ and is removed from here.

## auth-save-has-two-mutually-exclusive-branches

凭证保存有两条互斥分支，只有一条会丢键。改动持久化行为前先确认自己在哪条上：

- **元数据分支**（`auth.Metadata != nil`，无 `auth.Storage`）—— 待保存的 auth 携带从文件加载来的完整元数据映射。token 刷新、管理界面改字段、启停操作都走这条。写入是全量映射，任何键都不会丢。
- **storage 分支**（`auth.Storage != nil`）—— 待保存的 auth 携带一个新构造的 token storage 对象。**只有登录与凭证导入走这条**（管理界面与 CLI/SDK 两套入口，覆盖 Claude / Gemini / Codex / Kimi / Vertex）。落盘是截断重写，只写 storage 结构体的字段加上注入给它的元数据。

「日常改动不丢、一重新绑定就丢」这类症状，根因几乎一定是这个不对称。storage 分支上的保留由 `misc.ApplyPreservedMetadata` 负责。

## json-field-ownership-comes-from-the-schema

判断「某个 JSON 键归不归这个结构体所有」时，用**类型声明的 tag**，不要用 `json.Marshal` 的输出。带 `omitempty` 的字段在值为空时根本不出现在序列化结果里，但它仍然属于该 schema —— 按输出判断会把它误判成「无主键」，于是从别处（比如旧文件）把陈旧值补回来。

本仓踩过一次：`KimiTokenStorage` 的 `scope` / `device_id` / `expired` 都是 `omitempty`，一次 refresh 登录留空它们，差点让旧文件里的过期时间盖到全新 token 上。`misc.declaredJSONKeys` 就是为此存在的（反射走 json tag，含 `omitempty`，排除 `-`，递归无 tag 的匿名嵌入结构体）。

配套注意 `misc.MergeMetadata(source, metadata)` 的语义是 **metadata 覆盖 source 的结构体字段**，不是反过来。把一份旧元数据整个喂给它，就等于用旧值盖掉新值。

## auth-filename-derivation-decides-path-stability

任何「以 auth 文件路径为锚」的功能（保留、缓存、按文件比对），其生效范围由各 provider 的文件名派生策略决定，而不是由功能本身决定：

- **Claude / Gemini** —— 文件名由账号邮箱派生，重新绑定同一账号路径不变 → 生效
- **Codex** —— 文件名含订阅计划与账号 ID 摘要，计划变更会改变路径 → 仅计划不变时生效
- **Vertex 导入** —— 文件名由 service account 派生，重复导入同一个 → 生效
- **Kimi** —— 文件名基于登录时间戳，每次登录必然是新文件 → **永远 no-op**

写这类功能时把这张表当作它的实际覆盖面，别在文档里承诺「所有 provider 一致」。反过来也要留神：靠「路径正好不同」来避免某个 bug，是巧合不是设计。

## postgres-store-keeps-local-files-as-the-working-copy

Postgres 后端（生产与 lab 实际运行的那个）并不是「只写数据库」：auth 文件仍然真实地落在本地 authDir，数据库是它的镜像。`PostgresStore.Save` 先写本地文件，再 `upsertAuthRecord` 把内容同步进库。

反方向由 `Bootstrap → syncAuthFromDatabase` 完成：启动时清空并从库里重建整个本地 authDir。所以即使容器冷启动、本地磁盘为空，服务开始处理请求时旧 auth 文件也是在盘上的。

这一点是任何「读取旧 auth 文件」的功能能在生产生效的前提。对象存储与 Git 后端是同一形状。如果哪天本地 spool 策略变了，所有依赖这个前提的功能都要重新评估。

## claude-shared-ratelimit-defaults-live-in-two-sources

Claude「shared」限流策略的默认值有**两处代码来源**,改一处必须同步另一处,否则全新安装与从空/nil config 解析出的策略会不一致:

- `internal/config/config.go` 的 `SetDefaults` —— 配置层默认(config 层单测走这条)。
- `internal/runtime/executor/helps/ratelimit_policy.go` 的 `defaultClaudeSharedRatelimitPolicy` —— 策略层回退,policy 从空/nil config 解析时用它(block 路径单测 `sharedPolicyForMode("shared", nil)` 走这条)。

外加 `config.example.yaml` 的 `claude-ratelimit-alert.shared` 段是文档来源,也要对齐。生产 configmap 通常不写这些键(靠默认值),所以改代码默认即在生产生效。

配套陷阱:7 天 guard 的单测把 day base 硬编进了期望值公式(形如 `base * ((hardCap-used)/(hardCap-soft))`)。改 base 要手工重算该期望值,不能对字面量做全局替换 —— 公式里还有个数值相同的量是 7d used 输入,替换会算错。

## istio-manifest-drifts-from-live-cluster

`istio/` 是 gitignored 的本地基础设施文件(deployment/service/virtualservice 等),不在仓库里,且**会与线上集群漂移**:线上镜像常被带外升级(`kubectl set image` 之类),本地 `istio/deployment.yaml` 不会同步 —— 出现过文件停在旧 tag、集群实际跑更新 tag 的情况(线上 `last-applied-configuration` 注解还停在更早的 apply 版本即是证据)。

所以别对陈旧 manifest 直接 `kubectl apply -f`,它可能把文件里过时的字段回写、盖掉线上带外改动。先 `kubectl diff -f`(server 端 dry-run,只读)确认唯一实质差异就是镜像 tag;diff 若出现其它字段就停下核对,不要盲目 apply。lab 集群用 context `dengwu.wang-local-lab`、namespace `gemini`。

## management-api-call-resolves-by-auth-index

前端要按需拿某账号的上游数据（如 Claude `/api/oauth/usage` 的 5h/7d 额度）时，复用现成的 `POST /v0/management/api-call` 代理即可，后端零改动：请求体 `{auth_index, method, url, header}`，header 里的 `$TOKEN$` 由服务端替换成该账号 access token；响应 `{status_code, header, body}`，`body` 是上游响应体的**字符串**（要 `JSON.parse`，且先判 `status_code`；注意限流账号会返回 valid-JSON 的 `{"error":{"type":"rate_limit_error"}}`，常带 429）。

关键陷阱：api-call 用 `authByIndex(auth_index)` 按 `auth.Index`（`EnsureIndex` 派生的十六进制运行时 id，如 `c4e92118e023e341`）匹配凭证 —— **不是**文件名/id。auth-files 列表暴露三种不可互换标识：`id`/`FileName`（给 `PATCH .../auth-files/fields` 与 `GetByID`）、`auth_index`（给 api-call/`authByIndex`）、`name`（展示）。给 api-call 传文件名会查不到凭证、`$TOKEN$` 不被替换、请求以字面 `$TOKEN$` 发出（表现为上游 401/429）。用列表 entry 的 `auth_index` 字段。

