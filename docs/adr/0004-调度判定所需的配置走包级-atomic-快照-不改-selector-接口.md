# ADR 0004 — 调度判定所需的配置走包级 atomic 快照，不改 Selector 接口

**Status:** accepted

凭证选择的判定点（`isAuthBlockedForModel`）是包级函数，调用链 `Pick → getAvailableAuths → collectAvailableByPriority → isAuthBlockedForModel` 全程不带配置。而 `Pick` 是公开的 `Selector` 接口方法 —— 给它加一个 config 参数会破坏接口，波及所有自定义 selector 实现。

因此：凡是判定点需要的应用配置，在 `Manager.SetConfig` 时解析成一个**不可变快照**存进包级 `atomic.Value`，判定点直接读。`ApplyRatelimitBlock` 借 `activeRatelimitTarget` 找到当前 Manager 用的是同一个形状，本仓已接受这个模式。

三条配套约束：

- 快照必须有非 nil 初值（`init()` 里 Store 一次），否则配置加载前判定点读到 nil 会 panic。
- 一切昂贵解析在 `SetConfig` 里做完（如 `time.LoadLocation` 会读文件系统），判定点只做纯计算 —— 它在每个请求的热路径上。
- 快照是并发读 + 热加载写，必须有 race 用例覆盖。

代价：引入包级可变状态，测试需要保存/恢复快照且不能 `t.Parallel()`。接受，因为替代方案（改公开接口）代价更大。
