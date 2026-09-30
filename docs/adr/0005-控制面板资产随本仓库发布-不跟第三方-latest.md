# ADR 0005 — 控制面板资产随本仓库发布，不跟第三方 latest

**Status:** accepted

`management.html` 原先从 `router-for-me/Cli-Proxy-API-Management-Center` 的 **latest release** 拉取并每约 3 小时自动覆盖。该项目 v1.25.0（2026-09-29）把 API 基址从 `/v0/management` 换成 `/v8/management` 并在登录处设了门禁，于是在我们没有任何发布动作的情况下，运营界面一夜变砖 —— 本仓 fork 自上游 v6.10.9，只提供 v0。

因此：默认路径把一个已知可用的面板构建 `go:embed` 进二进制，直接吐内置字节，**不读磁盘、不发请求**。面板版本成为构建期决定：不提交就改不了，且能挺过 GitHub 不可达、release 被删、pod 重建。

**被否决的方案：钉死一个固定 release 直链但照样下载。** 换钉子照样要改常量、发版，敏捷性一点没买到，却留着一个只在最需要时才失效的运行时依赖；而且它必须同时禁用 fallback 页（`cpamc.router-for.me` 永远是最新版，不堵就会从这里漏掉钉子），等于亲手拆掉最后一条退路。**为一个固定值保留网络获取，是纯亏损。**

**逃生舱**：`remote-management.panel-github-repository` 指向一个**不同于默认**的仓库时，回到「跟随该仓库 latest」的老行为（下载、digest 校验、fallback 全套），后台 updater 也只在此时运行。判据不能用「字段为空」——见 learnings `config-loader-backfills-defaults-so-emptiness-is-not-a-signal`。

**引入的新约束**：升级面板 = 换文件 + 发版。这对当下是好事（我们要的就是冻结），对将来是成本，且随下面这笔债结清而自动失效。

**债与触发条件**：本仓落后上游两个大版本。上游 v8 的管理面是重新分组的 façade（`/credentials`、`/observability/*`、`/routing/*`、`/plugins/*`），credentials/logs/usage 复用同一批 handler，但配置端点吃的是 v8 新 YAML 配置树（`NormalizeConfigLayout`）—— 这是大版本重构的核心，不可能便宜地在 v6 上补出来，所以**不实现 `/v8/management` 门面**。关键事实：**上游 v8.0.4 仍然同时提供 `/v0/management`**，v0 契约没死，钉死是安全的。真正该启动迁移的信号是「我们需要 v1.25+ 面板的新能力」或「上游开始移除 v0 路由」。

与 [ADR 0003](0003-运营界面缺口用自建内嵌伴随页补-不去改外部控制台.md) 的关系：0003 的决定（不 fork 外部面板、缺口用自建内嵌伴随页补）**仍然成立**；本 ADR 只推翻它所依据的前提之一 —— 外部资产不再无条件自动覆盖。我们依然一个字节都不改那个面板，只是把它随版本带走。
