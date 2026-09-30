---
id: 1
slug: embed-pinned-panel-asset
prd: docs/prds/2026-09-30-pin-management-panel-asset.md
state: done
category: bug
blocked_by: []
---

## What to build

运营者打开 `/management.html` 被「当前后端可使用 v0 管理接口，但不支持 v8」拦下，面板整体不可用。原因是该资产每约 3 小时从第三方仓库的 latest release 自动覆盖，而该项目 v1.25.0（2026-09-29）把 API 基址从 `/v0/management` 换成了 `/v8/management`，我们这条 fork 只提供 v0。

这一片让面板资产**随我们的版本一起发布**：把已知可用的 v1.24.2 `management.html` vendor 进仓库、嵌进二进制，默认路径上服务面板**既不碰网络也不碰磁盘**。

行为：

- **默认（未配置面板仓库）**：直接返回内置字节。不查磁盘、不发请求、不写文件。今天那条「本地文件缺失就同步下载」的按需兜底在这条分支上不存在。
- **配置了面板仓库（逃生舱）**：完全保留今天的行为 —— 读磁盘，缺失则经资产更新器下载。一行不改。
- `disable-control-panel` 的门禁位置不变，仍在两个分支之前（面板关则 404，伴随页同步关闭，见 ADR 0003）。
- 启动或首次服务时在日志里标出当前钉住的面板版本，否则这个版本号在运行时完全不可见。

**自愈是免费的**：lab pod 上那份已落盘的 v1.25.0 不需要任何人工清理 —— 默认路径根本不再读磁盘，它自然失效。

### 为什么是内置而不是「钉死 URL 后照样下载」

先考虑过「钉死一个固定的 release 直链 + 内置 sha256 校验」并否决了：换钉子照样要改常量、发版，敏捷性一点没买到，却留着一个只在最需要的时刻才失效的运行时依赖（上游删旧 release、lab 出网被掐、pod 重建撞上下载失败 —— 任何一条都让面板从「旧了」变成「没了」）。不要把它改回下载。

## Key interfaces

- **面板处理器** — 当前契约：查静态目录里的资产文件，缺失则同步调用资产更新器下载，再 `c.File` 吐出去。期望契约：先按 `remote-management.panel-github-repository` 是否为空分叉；为空走内置资产，非空走上述既有逻辑。`disable-control-panel` 的 404 判定保持在最前。

- **内置资产** — 与已内置的伴随页（`usage-mode.html`）用同一套嵌入方式、放在同一处，保持一致。资产原样入库，**不做 gzip**：git 本就压缩 blob，存原始与存 gzip 体积相当，不值得引入解压逻辑。

- **资产来源与校验值** — `router-for-me/Cli-Proxy-API-Management-Center` 的 release `v1.24.2`，资产名 `management.html`：
  - 下载直链：`https://github.com/router-for-me/Cli-Proxy-API-Management-Center/releases/download/v1.24.2/management.html`
  - 大小：2,767,070 字节
  - sha256：`51b24db8170a5414875602c2f4e35017185b1c1dd7e9a6b1c0de69905a81e997`（下载实测与 release digest 双向一致）

  sha256 以常量形式与资产一同入库，一值两用：自检断言 + ETag 取值。

- **条件请求** — 内置字节直接返回会丢掉原先 `c.File` 白送的条件请求语义，变成每次打开全量传 2.7MB。用标准库的静态内容服务并预设强 ETag（带引号，取值即上述 sha256），让 `If-None-Match` 命中时返回 304 且无 body。Content-Type 为 `text/html; charset=utf-8`。

## Acceptance criteria

- [ ] 默认路径（未配置面板仓库）返回 200、Content-Type 为 HTML，body 即内置资产
- [ ] body 含 `/v0/management` 且**不含** `/v8/management` —— 将来误换成 v8 面板必须在 CI 就红
- [ ] 响应带强 ETag；带匹配的 `If-None-Match` 再请求返回 304 且 body 为空
- [ ] `disable-control-panel` 为真时返回 404
- [ ] 配置了面板仓库时改吐磁盘上的资产文件而非内置资产（测试用静态目录环境变量指向临时目录并预置可识别文件，文件存在故不触网）
- [ ] 内置字节的 sha256 等于随资产入库的常量
- [ ] 默认路径上无任何 HTTP 出站请求、无任何磁盘写入
- [ ] 日志中可看出当前钉住的面板版本
- [ ] **欠的人工验证**：部署到 lab 后由运营者做一次浏览器往返 —— 登录、账号列表、日志三项正常。自动化证明不了 v1.24.2 对着我们这条 v6.10.9 血统的 v0 API 是否功能完好；最强先验是它在 2026-09-22 至 09-29 期间就是 lab 上跑着并可用的那一版

## Out of scope

- 不碰任何 `/v0/management/*` 端点的契约
- 不碰 `/usage-mode.html` 伴随页的任何行为
- 不新增配置键 —— 逃生舱复用既有的 `remote-management.panel-github-repository`（非空 = 跟随该仓库 latest = 解钉）
- 不改后台自动更新器的运行条件（任务 2 的事）
- 不实现 `/v8/management` 门面，不迁移到上游 v8
- 不提供运行时切换面板版本的能力 —— 换版本 = 换文件 + 发版，这是刻意的
