---
id: 1
slug: preserve-helper-and-file-store
prd: docs/prds/2026-08-17-preserve-auth-metadata-on-rebind.md
state: ready-for-agent
category: bug
blocked_by: []
---

## What to build

凭证保存存在两条互斥分支。**元数据分支**（待保存的 auth 携带从文件加载来的完整元数据映射、不携带 token storage 对象）由 token 刷新、管理界面字段修改、启停操作走，写入全量映射，任何键都不丢。**storage 分支**（待保存的 auth 携带一个新构造的 token storage 对象）只有登录与凭证导入走，落盘是截断重写，只写 storage 结构体字段加上注入给它的元数据 —— 运营者手设的键因此被抹掉。

本切片建立保留机制，并在**文件后端**上把它端到端打通、用测试证明：走 storage 分支保存时，运营者手设的顶层键被带过去，而新换来的凭证正常落地。

完成后可独立演示：临时目录里一个含 `claude_usage_mode: "dedicated"` 的旧 auth 文件，经过一次携带新 token 的保存后，文件里的 access token 是新的、dedicated 标记还在。

核心不变式（**由构造保证，不靠约定**）：优先级为 `旧文件保留键 < 新载荷字段 < 调用方元数据`。既有的合并工具语义是「元数据覆盖 source 结构体字段」，若把旧元数据整个带回就会让旧 token 盖掉新 token —— 重绑表面成功、实际装回死凭证。规避方式是**保留集按定义排除新载荷的每一个键**，冲突键根本进不了保留集。实现时不得破坏这个性质。

## Key interfaces

- `misc.ApplyPreservedMetadata(path string, storage any, recordMeta map[string]any)` — **新增**，落在 `internal/misc`（与 `MergeMetadata` 同包；该包已 import `encoding/json` 与 logrus，无新依赖；`sdk/` 已 import `internal/`，同 module 合法）。无返回值。行为：
  1. 算新载荷 `fresh = MergeMetadata(storage, recordMeta)`，即落盘最终会写的键集合；
  2. 读 `path` 旧 JSON，`preserved = 旧顶层键 − fresh 的键 − {disabled}`；
  3. 把 `recordMeta ∪ preserved` 经 storage 的 `SetMetadata` 注入（**类型断言在 helper 内部做**，调用方不声明接口）；
  4. 文件不存在 / 空 / JSON 解析失败 / storage 不实现 `SetMetadata` → **静默 no-op**（坏 JSON 记 debug 日志）。任何情形都不得让保存或登录失败，不得 panic。
- `misc.MergeMetadata(source any, metadata map[string]any) (map[string]any, error)` — **既有，复用**。注意其语义是 metadata 覆盖 source 的字段，这正是上文那个陷阱的来源。
- storage 侧 `SetMetadata(map[string]any)` — **既有**。claude / gemini / codex / kimi 四个 token storage 都实现（各自持一个 `Metadata map[string]any` 且 `json:"-"`，由自身落盘动作 flatten）。vertex 与 empty storage 需确认，未实现即按 no-op 处理。
- `FileTokenStore.Save` 的 `case auth.Storage != nil` 分支（`sdk/auth/filestore.go`）— **当前**：函数体内声明一个局部 `metadataSetter` 接口，断言后注入 `auth.Metadata`；**期望**：改为在落盘动作之前调用 helper，局部接口声明被 helper 吸收后删除。对已有行为等价（`recordMeta` 仍被注入）并额外获得保留能力。

**差集只在顶层键上做，不实现深度合并。** 每个 provider 的新载荷都完整定义了自己的顶层形状，嵌套对象整体归新载荷所有（见下节 gemini）。

## Known data variants

取自 2026-08-17 lab 运行实例上的真实 auth 文件（仅采键名，未取值）。

- **claude 顶层键** → `access_token` `claude_usage_mode` `email` `expired` `id_token` `last_refresh` `refresh_token` `type`。`id_token` 实测为**空字符串但键存在** —— 空值键同样算"新载荷已定义"，绝不能被旧值补回。
- **gemini 顶层键** → `token`（**嵌套对象**）`project_id` `email` `auto`(bool) `checked`(bool) `type`。`client_id` / `client_secret` / `expiry` / `scopes`(数组) / `token_uri` / `universe_domain` / `expires_in` / `token_type` 全在嵌套的 `token` 里，**不是顶层** → 顶层 `token` 在 fresh 里即整体归新载荷，无需也不该做嵌套合并。
- **kimi 顶层键** → `type` `access_token` `refresh_token` `token_type` `scope` `timestamp` `expired`，`device_id` 仅在设备登录时存在。
- **运营者键的实际形态**：`claude_usage_mode` 是小写字符串（`shared` / `dedicated`，另 `exclusive` 是 dedicated 的别名）；`priority` **可能是 JSON number 也可能是 string**（文件合成器两种都接）；`note` 是 string；`headers` 是嵌套对象；`excluded_models` 是数组。保留逻辑必须**原值透传，不做类型规范化**。
- `disabled` 键在多数实测文件里**完全不存在**，存在时为 bool。两种情形都要处理。
- 同目录下混放多个 provider 的文件，helper 只读被传入的那一个路径，不扫目录。
- 实测同目录也存在 `claude_usage_mode` 为 `shared` 的文件与完全 unset 的文件 —— unset 即该键不存在，不是空字符串。

## Acceptance criteria

- [ ] 新建 `internal/misc` 的表驱动单测（该包目前无测试文件），风格照 `sdk/auth/filestore_test.go` 的 `TestExtractAccessToken` 与 `internal/runtime/executor/helps/ratelimit_*_test.go`：表驱动 + `t.Parallel()` + 子测试逐例命名
- [ ] 单测覆盖 7 例：①旧文件不存在 → no-op，注入等于 `recordMeta` 原样 ②旧文件含 `claude_usage_mode`+`priority`+`note` → 三者都带回 ③**旧文件的 `access_token`/`refresh_token`/`expired`/`last_refresh` 与新值不同 → 一个都不带回** ④旧文件 `disabled:true` → 不带回 ⑤坏 JSON / 空文件 → no-op 不 panic 不报错 ⑥`recordMeta` 与旧文件同键冲突 → `recordMeta` 赢 ⑦storage 不实现 `SetMetadata` → no-op 不 panic
- [ ] 用例 ③ 独立成例并可单独运行 —— 这是凭证回滚回归护栏，对应 PRD User Story 7，不得与其他用例合并
- [ ] 单测用假 storage（实现落盘动作与 `SetMetadata`，只记录被注入了什么）断言注入结果，不触碰真实文件写出
- [ ] 扩 `sdk/auth/filestore_test.go` 加落盘接线测试：临时目录放含 `claude_usage_mode:"dedicated"` 的旧文件，用真实 `ClaudeTokenStorage`（携带新 token）走完整 `FileTokenStore.Save`，断言落盘 JSON 中 access token 为新值且 `claude_usage_mode` 仍为 `dedicated`
- [ ] `priority` 为 number 与为 string 两种形态都能原样带回（不被规范化成另一种）
- [ ] 旧文件不存在时保存成功，落盘内容与改动前一致（首次绑定零副作用，对应 User Story 9）
- [ ] 旧文件为坏 JSON 或空文件时保存仍成功（对应 User Story 10）
- [ ] 旧文件含 `disabled:true` 时，重绑后落盘**不含** `disabled` 键
- [ ] gemini 形状的旧文件（顶层含嵌套 `token`）走一次保存后，嵌套 `token` 整体为新值，未发生嵌套合并
- [ ] `go test ./internal/misc/... ./sdk/auth/...` 通过
- [ ] 全量 `go test ./...` 无回归（尤其 `sdk/auth`、`internal/store`）

## Out of scope

- **Postgres / 对象存储 / Git 三个后端的接入** —— 切片 2
- 不改登录与导入的十个调用点（它们传递的元数据本来正确）
- 不收敛四份重复的 `Save` switch（错误前缀与后置步骤各异，是独立重构）
- 不加配置开关（bug 修复恒定生效）
- 不改 kimi 的时间戳文件名策略
- 不给 `claude_usage_mode` 补管理接口支持
- 不追认历史已丢失的字段
