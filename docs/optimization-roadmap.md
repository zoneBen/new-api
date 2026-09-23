# new-api 优化方向分析

> 本文基于对仓库当前代码（main 分支）的静态审计与可复现的命令验证，给出按优先级排序的优化方向。
> 审计范围：Go 主模块、`relaykit/`、`web/` 前端、`.github/workflows/`。
> 生成日期：2026-09-22。

## 0. 阅读说明

### 证据等级

文中每条结论都带证据。为便于判断可信度，标记如下：

| 标记 | 含义 |
| --- | --- |
| ✅ 复核 | 本次分析中直接读取源码 / 执行命令确认 |
| 🔍 审计 | 由分维度审计得出，已核对引用位置但未逐条复现 |
| ⚠️ 待确认 | 推断性结论，落地前需先复现（已集中在附录 B） |

### 修复进度标记

| 标记 | 含义 |
| --- | --- |
| 🛠️ 已修复 | 已在本分支实现、验证并提交（附 commit 短哈希） |
| 🚧 部分修复 | 只落地了其中一部分，剩余项在该节内单独列出 |
| ❌ 未修复 | 已评估但本分支刻意不改，理由写在该节内 |
| （无标记） | 尚未处理 |

修复记录见 [§1.1 修复进度](#11-修复进度)。标记只描述"本分支做了什么"，不改变原始问题的严重程度评估。

### 基线验证（本次实际执行的命令与结果）

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| 主模块构建 | `go build ./...` | ✅ 通过；冷构建约 8 min（含依赖下载），热构建约 25 s |
| 静态检查 | `go vet ./...` | ✅ 通过，约 5 s |
| `relaykit` 独立性 | `cd relaykit && GOWORK=off go build ./...` | ✅ 通过，符合 AGENTS.md 对独立模块的要求 |
| 后端测试 | `go test ./model/... ./service/... ./middleware/...` | ✅ 全部通过，约 30 s |
| 竞态检测 | `go test -race ./common/... ./setting/... ./service/authz/... ./model/...` | ✅ 通过（注意：现有用例未覆盖下文 §4 的配置读写路径） |
| 全量测试 | `GOWORK=off go test ./...` | ✅ 通过，退出码 0；约 4 min（与 `-race` 并发时约 13 min） |
| 语句覆盖率 | `go test -cover ./...` | ⚠️ 根模块 **38.5%**（**按包归属口径**，跨包调用不计入；`common` 19.9%、`oauth` 1.3%、`model` 32.7%、`relay` 32.2%）。勿直接当作盲区结论 —— 详见 P2-9 |
| 前端验证 | `bun run typecheck` / `bun run test` | ❌ 无法执行：环境未安装 `bun`，`web/node_modules` 缺失 |

> 前端无法本地验证本身就是一个工程问题：AGENTS.md 要求每次改动 TS/TSX 后必须 typecheck 与 lint，但仓库未提供可离线运行的兜底方式，AI 与外部贡献者容易在没有验证的情况下提交前端改动。

### 项目规模

| 模块 | Go 文件 | 行数 |
| --- | --- | --- |
| `relay/` | 244 | 51,763 |
| `controller/` | 127 | 48,233 |
| `relaykit/`（独立模块） | 130 | 35,194 |
| `model/` | 111 | 29,166 |
| `service/` | 112 | 26,307 |
| `pkg/` | 44 | 14,099 |
| `middleware/` | 38 | 8,694 |
| `common/` | 65 | 7,655 |
| `setting/` | 66 | 6,525 |
| `router/` | 18 | 2,980 |

其他基线数据：

- 路由共 **332** 条，其中 `router/api-router.go` 单文件 **266** 条 ✅
- 提交总数 6,446；近 6 个月 986 次 ✅
- 近 1000 次提交构成：`fix` 429、`feat` 265、`refactor` 44、`perf` 33、`chore` 63、`docs` 13、`test` 6 ✅ — 修复类提交是特性类提交的 1.6 倍，说明回归压力明显
- 改动最频繁的文件：`relay/common/relay_info.go`（79 次）、`router/api-router.go`（77 次）✅
- 测试文件分布：`controller` 49/127、`relay` 54/244、`model` 39/111、`service` 24/112、`middleware` 11/38、`setting` 17/66、`relaykit` 34/130；前端 164 个测试文件 ✅
- 测试规模：主模块 245 个、`relaykit` 34 个 Go 测试文件，测试代码 **83,460 行**；共 83 个包中 **40 个没有任何测试文件** ✅
- `TODO/FIXME/HACK/XXX` 共 113 处，其中 102 处在 `relay/channel/` ✅

---

## 1. 优先级总览

| # | 方向 | 等级 | 主要影响 | 预估成本 | 证据 |
| --- | --- | --- | --- | --- | --- |
| P0-1 | `/api/setup` 未授权可创建 root 账号 | 🔴 严重 | 全站接管 | 小 | ✅ 复核 |
| P0-2 | MJ 图片代理路由漏挂鉴权 | 🔴 严重 | 越权读取他人产物 | 小 | ✅ 复核 |
| P0-3 | 验证码/登录缺乏账户级限制，且客户端 IP 可伪造 | 🔴 严重 | 账号接管 | 中 | ✅ 复核 |
| P0-4 | 明文 API Key 可凭会话直接读取（无二次验证） | 🟠 高 | 额度盗用 | 小 | ✅ 复核 |
| P0-5 | 上游请求丢失客户端 context | 🟠 高 | 计费泄漏 + 资源泄漏 | 小 | ✅ 复核 |
| P0-6 | `recover` 吞掉 panic 导致错误被当成成功 | 🟠 高 | 数据/状态不一致 | 小 | ✅ 复核 |
| P0-7 | 计费字段在两条请求路径上取值不一致 | 🟠 高 | 计费错误 | 小 | ✅ 复核 |
| P0-8 | 渠道 API Key 被完整明文写入日志与 stdout | 🟠 高 | 凭据泄露 | 小 | ✅ 复核 |
| P1-1 | 默认部署实际无缓存，每请求 8–14 次同步查库 | 🟠 高 | 吞吐与延迟 | 中大 | 🔍 审计 |
| P1-2 | 渠道缓存刷新无失败保护，可造成全局路由中断 | 🟠 高 | 可用性 | 中 | ✅ 复核 |
| P1-3 | 流式路径内存放大（128 MB 缓冲、重复解码、全量累积） | 🟠 高 | 内存与 CPU | 中 | 🔍 审计 |
| P1-4 | 数据库连接池默认值导致连接抖动；缺索引与 N+1 | 🟠 高 | 数据库压力 | 中 | ✅ 复核 |
| P1-5 | 启动成本随数据量增长（迁移 + 逐用户回填） | 🟡 中 | 启动时长 | 中 | 🔍 审计 |
| P1-6 | 前端首屏强制加载 3.5 MB 语言包 + 未拆分的 Markdown/图标依赖 | 🟠 高 | 首屏体积与加载 | 中 | 🔍 审计 |
| P1-7 | 前端表格 memo 失效、Context 未记忆化、全局 `staleTime` 仅 10 s | 🟡 中 | 交互流畅度与请求量 | 中 | 🔍 审计 |
| P2-1 | 35 个渠道适配器缺少公共基类（约 1,500 行重复） | 🟡 中 | 改动成本 | 中大 | ✅ 复核 |
| P2-2 | 配置读写存在系统性竞态，配置来源有 4 套机制 | 🟠 高 | 正确性 + 维护 | 大 | ✅ 复核 |
| P2-3 | 分层被穿透：控制器含业务逻辑与直接 DB 访问 | 🟡 中 | 维护性 + 测试 | 大 | 🔍 审计 |
| P2-4 | 响应封套与状态码不统一（约 600 处手写） | 🟡 中 | 契约一致性 | 中大 | ✅ 复核 |
| P2-5 | CI 缺少 lint / 竞态 / 三库矩阵 / 覆盖率 / 依赖扫描 | 🟠 高 | 回归成本 | 中 | ✅ 复核 |
| P2-6 | 可观测性缺失：无指标端点、无健康检查、日志无级别与轮转 | 🟠 高 | 排障能力 | 中 | ✅ 复核 |
| P2-7 | 其他正确性与运维项（上传/下载限流未挂载、goroutine 泄漏、关闭丢计数、CORS、安全响应头等） | 🟡 中 | 正确性 + 运维 | 小–中 | ✅ 复核 |
| P2-8 | 一致性卫生：死代码、`encoding/json` 绕过包装、日志约定、分页分叉 | 🟢 低 | 卫生 | 小–中 | ✅ 复核 |
| P2-9 | 测试盲区：`ValidateUserToken`/`PostConsumeQuota`/`EnableChannel` 等 14 个核心函数全仓库零测试引用；28/40 适配器零测试 | 🟠 高 | 计费与鉴权回归 | 中大 | ✅ 复核 |
| P2-10 | 迁移无版本记录、失败降级为告警；启动与配置校验薄弱 | 🟠 高 | 升级事故 | 中 | ✅ 复核 |
| P2-11 | 构建与发布：版本注入失效、工具链漂移、产物不可比 | 🟡 中 | 可追溯性 | 小–中 | ✅ 复核 |
| P2-12 | 运维文档缺口：无故障排查 / 备份恢复 / 升级回滚 / 监控告警说明 | 🟡 中 | 运维可用性 | 小 | ✅ 复核 |

**建议的推进顺序：** P0 全部（安全 + 正确性，应尽快单独发版；P0-8 是一行修复）→ P2-5 补 CI 闸门（后续改动才可被验证，其中三库 CI job 性价比最高）→ P1-1/P1-2（服务端可用性收益最大）→ P1-6（前端首屏，单项收益最大且改动集中）→ P2-9/P2-10（覆盖与升级安全）→ 其余。

### 1.1 修复进度

分支 `dev/optimization-p0`，每项修复独立提交并推送，验证命令与结果见各节内的 🛠️ 块。

| 项 | 状态 | 提交 | 验证摘要 |
| --- | --- | --- | --- |
| P0-1 `/api/setup` 未授权创建 root | 🛠️ 已修复 | `91f20bae3` | `go build ./...`、`go vet`、`go test ./middleware/ ./common/ ./router/` 通过；17 个子用例覆盖令牌/回环/内网/公网/伪造 `X-Forwarded-For`/反向代理 |
| P0-2 MJ 图片代理越权读取 | 🛠️ 已修复 | `38ddfde30` | `go build ./...`、`go vet`、`go test ./relay/ ./service/ ./controller/` 通过；路由级用例覆盖无签名/篡改/他人任务签名被拒且不触达上游，正确签名返回 200 + `image/png` |
| P0-3 验证码与登录缺乏账户级限制 | 🚧 部分修复 | `927ab209e` 验证码<br>`6ec801848` 登录计时<br>`d3c953c7a` 代理告警<br>`826ee1357` 账户锁定 | `go build ./...`、`go vet ./...`、`go test ./controller/ ./service/ ./common/` 通过；`go test -race` 覆盖新增用例。剩余：`TRUSTED_PROXIES` 失败关闭（默认值未改，代价已写入告警与 `.env.example`）、验证码发送侧按邮箱限额、重置密码回显（需前后端联动） |
| P0-4 明文 API Key 可凭会话直接读取 | 🛠️ 已修复 | `c62f10985` 后端<br>`5447458ce` 前端 | `go test ./controller/ -count=1` 通过（243s）、`npx vitest run` 165 文件 / 2094 用例通过。两个接口均接入 `token.key.read` step-up，证明绑定 id 集合（排序去重、上限 100），限流改按用户。行为变更：**PAT 读 key 变 403**、聊天页与仪表盘新增验证弹窗 |
| P0-5 上游请求丢失客户端 context | 🛠️ 已修复 | `426a65d0a` | `go build ./...`、`go vet ./relay/...`、`go test ./relay/... -count=1`（18 包 ok）、`go test ./service/ -count=1` 通过；新用例 `-race` 通过且已验证"未绑定即失败"。两个入口 + 5 个自建请求的渠道站点 + MJ 提交改为绑定客户端 context；残余：ollama/baidu/jsplugin helper 与部分非中继调用方（见该节） |

---

## 2. P0 — 安全与正确性（建议优先修复）

### P0-1 `/api/setup` 可被匿名调用创建 root 账号 ✅

**问题.** 全新实例启动时 `constant.Setup == false`，此时 `POST /api/setup` 无需任何凭据即可创建 `Role: common.RoleRootUser` 账号。

**证据.**
- `router/api-router.go:25`：`apiRouter.POST("/setup", anonymousRequestBodyLimit, controller.PostSetup)`，仅带 `GlobalAPIRateLimit`（`:19`）
- `controller/setup.go:46-167`：仅以 `constant.Setup`（`:48`）与 `model.RootUserExists()`（`:57`）作为条件；随后用**请求体**中的用户名/密码创建 root（`:105-121`）
- `model/main.go:95-116`：空库启动时会主动置 `constant.Setup = false`

**影响.** 暴露在公网的实例在管理员完成初始化前，会被自动化扫描器抢先注册 root，从而拿到全部渠道密钥、用户与额度。

**建议.**
1. 初始化需要带外下发的**一次性 setup token**（环境变量或启动日志输出），请求必须携带；
2. 或在完成初始化前，仅允许回环地址访问该端点；
3. `GET /api/setup`（`router/api-router.go:24`）同样应受限，它当前是发现该状态的信号。

**验收.** 在新实例上，无 token 的 `POST /api/setup` 返回 403；携带正确 token 才能创建 root。

**🛠️ 已修复（`91f20bae3`）.**
- 新增 `middleware/setup_guard.go`，挂在 `router/api-router.go:25` 的 `POST /api/setup` 之前，两级判定：
  - 设置了 `SETUP_TOKEN` 时，必须通过 `X-Setup-Token` 或 `Authorization: Bearer` 提供，用 `subtle.ConstantTimeCompare` 比较；
  - 未设置时，仅接受**直连**的回环/RFC 1918/RFC 4193 对端且不带任何代理头（`X-Forwarded-For`、`X-Real-Ip`、`Forwarded`、`X-Forwarded-Proto`、`X-Forwarded-Host`），因此"反代在私网"的部署不会被 `X-Forwarded-For` 绕过。
  - 选择"默认可用而非默认拒绝"是为了不破坏本地/LAN/单机容器的既有初始化流程；公网部署由 `common/init.go:69-72` 的启动告警与 `.env.example` 提示引导设置 `SETUP_TOKEN`。
- `GET /api/setup` 保持开放（按上文建议 3 本可一并限制）：前端需用它判断是否显示初始化页，且它只返回"是否已初始化"。**残余风险**：该端点会向未认证的调用者暴露实例尚未初始化的状态；如需消除，应改为前端在初始化流程中另行提示。
- **残余缺口**：初始化页面还没有令牌输入框，经反向代理部署的实例目前只能用直接的 API 调用携带 `SETUP_TOKEN` 完成初始化。
- 验证：`go build ./...`、`go vet ./middleware/ ./common/ ./router/`（干净）；`go test ./middleware/ ./common/ ./router/`（通过）；`middleware/setup_guard_test.go` 17 个子用例，覆盖正确/错误/缺失令牌、Bearer 形式、令牌优先于代理头、IPv4/IPv6 回环、容器网关、LAN、IPv6 ULA、公网对端、公网伪造 `X-Forwarded-For`、私网对端经反代、仅 `X-Real-Ip` 的情形。

### P0-2 Midjourney 图片代理路由漏挂鉴权 ✅

**问题.** 路由注册顺序导致中间件未生效。

**证据.** `router/relay-router.go:209-211`：

```go
func registerMjRouterGroup(relayMjRouter *gin.RouterGroup) {
	relayMjRouter.GET("/image/:id", relay.RelayMidjourneyImage)   // ← 先注册
	relayMjRouter.Use(middleware.TokenAuth(), middleware.Distribute())  // ← Use 在其后
```

Gin 的 `Use` 只作用于其后注册的路由，因此 `/mj/image/:id` 与 `/:mode/mj/image/:id` 均为匿名可访问。`relay/mjproxy_handler.go:28-33` 仅以 MJ 任务 ID 查询（`model.GetByOnlyMJId`），没有任何用户归属校验。

**影响.** 枚举任务 ID 即可读取他人生成的图片产物。

**建议（已按下文 🛠️ 块修正）.**
1. ~~将 `Use(...)` 移到第一条路由注册之前~~ —— **此处原建议是错的**，实测会破坏功能：前端用普通 `<img src>` 渲染该 URL（`web/src/features/usage-logs/components/dialogs/image-dialog.tsx:84`，数据源 `drawing-logs-columns.tsx:195-222`），而 `<img>` 不会携带面板的内存 Bearer token；刷新 Cookie 又是 HttpOnly 且作用域为 `/api/user/auth`。给该路由加 `TokenAuth` 会让图片全部 401。
2. 正解是**签名能力 URL**：沿用仓库既有的 `service/task_artifact_access.go` 约定（用途域分离、`hmac.Equal` 恒定时间比较、`base64.RawURLEncoding`），把授权绑到 URL 上，`<img>` 无需任何头即可通过；handler 内的归属校验由"签名绑定任务 ID"承担。
3. 第一条路由仍应保留在 `Use(...)` 之前，但必须补一行注释说明这是有意为之，避免后人"顺手修正"回去。

**验收.** 无 `access` 参数 / 签名被篡改 / 签名属于其他任务 ID 时均返回 403，且不查询数据库、不触达上游；携带与请求任务 ID 匹配的签名时返回 200 与正确 `Content-Type`。

**🛠️ 已修复（`38ddfde30`）.**
- 新增 `service/midjourney_image_access.go`：`IssueMidjourneyImageAccess` 用 `common.CryptoSecret` 对 `mj-image-v1\x00<mjID>` 做 HMAC-SHA256（base64url），`VerifyMidjourneyImageAccess` 先校验长度与严格 base64、再 `hmac.Equal`；`BuildMidjourneyImageURL` 负责签发并拼出绝对 URL（`url.PathEscape` 转义 ID，容忍 `ServerAddress` 结尾斜杠）。
- `relay/mjproxy_handler.go:28-45`：签名校验置于 `model.GetByOnlyMJId` **之前**，失败直接 403 `midjourney_image_access_denied`，因此匿名请求不会产生任何 DB 读取或上游请求。
- 三处 URL 构造点改用 `service.BuildMidjourneyImageURL`：`relay/mjproxy_handler.go` 的 `coverMidjourneyTaskDto`、`controller/midjourney.go` 的 `GetAllMidjourney` 与 `GetUserMidjourney`（后两者抽成 `forwardMidjourneyImageURLs`）。原 `?rand=` 缓存击穿参数改为第二个参数用 `&` 追加。签名无法签发时回退为上游原始 URL，而不是发布一个代理自身会拒绝的 URL。
- 能力 URL 是**确定性的、无过期时间**的：同一任务的列表响应每次返回同一个 URL，浏览器 `<img>` 缓存与刷新行为不变；因此签名轮换（切换 `CRYPTO_SECRET`）会立即使旧 URL 失效，属预期。
- **残余风险**：`access` 查询参数只在 `/v1/tasks/...`、`/v1/videos/...` 路径被 `middleware/task_artifact_access.go` 的脱敏逻辑摘除，`/mj/image/:id` 不在此列。若部署侧记录了完整请求行，签名会进入访问日志；由于签名只能访问对应那一张图片，影响有限，但如需彻底消除应把 `/mj/image/` 也纳入脱敏路径。
- 验证：`go build ./...`、`go vet ./relay/ ./controller/ ./service/ ./router/`（干净）；`go test ./relay/ ./service/ ./controller/`（通过）。`relay/relay_task_test.go` 新增路由级用例（签名为他人任务 / 被篡改 / 缺失时 403，且断言上游命中次数为 0；正确签名返回 200、`image/png` 与图片字节），`service/midjourney_image_access_test.go` 覆盖篡改、密钥轮换、长度/编码异常、空 ID、`PathEscape` 往返。
- OWASP ASVS 5.0 V8（Authorization）：对象级授权在每个请求上强制执行，不再依赖 ID 不可枚举。

### P0-3 验证码与登录缺乏账户级限制，且限流可被绕过 ✅

**问题.** 三个缺陷叠加，使密码重置流程可被爆破：

1. **验证码可枚举且失败不计数.** `common/verification.go:50-58` 的 `VerifyCodeWithKey` 仅做 `return code == value.code`，无尝试次数上限、失败不作废、非恒定时间比较。`GenerateVerificationCode(6)` 只有 6 位数字（约 24 bit）。
2. **客户端 IP 默认可伪造.** `common/trusted_proxies.go:11-18` 在 `TRUSTED_PROXIES` 为空时默认信任 `127.0.0.0/8`、`10.0.0.0/8`、`172.16.0.0/12`、`192.168.0.0/16`、`fc00::/7`；`middleware/trusted_proxies.go:16-18` 仅打印警告。限流键取自 `c.ClientIP()`（`middleware/rate-limit.go:112`），因此在常见的"反向代理部署在私网"场景下，请求方只要附带 `X-Forwarded-For` 就能为每个请求拿到新的限流桶。同一问题也影响 `middleware/auth.go:424-438` 的令牌 IP 白名单。
3. **登录无账户级锁定，且存在计时差异.** `model/user.go:1058-1083` 在账号不存在或密码为空时**先于 KDF 计算**返回 `ErrInvalidCredentials`，argon2id 只在真实账号上运行；`controller/user.go:53-100` 的响应文案统一，但耗时差异可测。代码库中登录路径没有任何失败计数器或锁定逻辑。

**影响.** 结合 `POST /api/user/reset`（`router/api-router.go:41`，仅 `CriticalRateLimit`）可对已知邮箱爆破 6 位验证码；也可先做账号枚举再爆破口令。`controller/misc.go:280-313` 在重置成功时还会把新密码回显在响应中，进一步放大后果。

**建议.**
1. 验证码校验：`subtle.ConstantTimeCompare` + 失败即作废 + 每 key 少量尝试上限 + 按邮箱与按用户的独立限额；
2. `TrustedProxies` 默认置空并要求显式配置，无法归属到可信跳点时**失败关闭**（忽略该头部）；
3. 登录：miss 路径也执行一次固定参数的 argon2id 校验以抹平计时；引入按账户的指数退避锁定与全局（不分 IP）登录计数；
4. 密码重置响应中不再回显新密码。

**验收.** 针对单一邮箱连续错误验证码会触发锁定；伪造 `X-Forwarded-For` 不再改变限流桶；不存在账号与存在账号的登录响应耗时无稳定差异。

**🛠️ 部分修复（`927ab209e`、`6ec801848`、`d3c953c7a`、`826ee1357`）.**
- **① 验证码校验 —— 🛠️ 已修复（`927ab209e`）.** `VerifyCodeWithKey` 改用 `subtle.ConstantTimeCompare`；`verificationValue` 增加 `attempts`，上限 `common.VerificationMaxAttempts = 5`，失败计数达到上限即删除该 key（验证码作废，需重新申请）。成功时计数归零但**不**删除 key——删除仍由调用方在业务成功后显式执行，因此"注册因用户名被占用而失败"不会白白消耗验证码（`controller/user.go` 改为插入成功后才 `DeleteKey`）。过期与未知 key 行为不变。测试：`common/verification_test.go`（新文件，5 个用例）。
- **② 客户端 IP 可伪造 —— 🚧 部分修复（`d3c953c7a`）.** **默认值未改动**，理由与 P0-2 同属对本文档原建议的修正：把 `TRUSTED_PROXIES` 默认置空后，`ClientIP()` 取直连对端地址，容器 `-p` 发布端口场景下所有外部客户端都是同一个 Docker 网关地址、退化为共用同一限流桶；这是部署策略取舍而非纯收益，不应静默改变。已做的是把代价讲清楚：启动告警补充"私网来源可伪造 `X-Forwarded-For` 自选限流桶、冒充令牌 IP 白名单来源，容器发布端口与原样转发客户端头的反代都落入该情形"，`.env.example` 同时写明 `none` 的代价与"按账户维度限制兜底"的指向。**剩余**：在无法归属到可信跳点时让 `ClientIP()` 失败关闭（忽略该头）而非回退默认值。
- **③ 登录计时差异 + 账户级锁定 —— 🛠️ 已修复（`6ec801848` + `826ee1357`）.** 计时：`model/user.go` 两条 miss 早返回路径都调用 `common.EqualizePasswordVerificationCost`，用与配置算法一致的占位 hash 跑一次完整校验。锁定：新增 `service/login_attempt_limit.go`，按**提交的名字**（`ToLower`+`TrimSpace` 后 HMAC 哈希入 key，原始用户名不进入内存/Redis key）维护 20 次 / 15 分钟窗口；检查在凭据查询**之前**执行，故锁定不泄露账号是否存在、未知用户名同样消耗预算；DB 报错不计入预算，成功登录清零。计数在 Redis 配置时用 `SetNX`+`Incr`（单 TTL，窗口是计数而非重启），否则用进程内限流器，两条路径均**失败开放**。节点级计数跨账户失败达 100 次/窗口时**只告警不拦截**（全局拦截会让任何人停掉整个实例的登录）。`common/rate-limit.go` 为此新增 `Saturated` 与 `Reset`。测试：`service/login_attempt_limit_test.go`（新文件：内存与 miniredis 两条路径的耗尽/清零/TTL/失败开放、窗口内只告警一次）+ `controller/auth_flow_test.go` 的路由级用例（20 次错误口令后正确口令同样被拒、未知用户名同样被锁、其他名字不受影响）。
- **④ 密码重置不回显新密码 —— 未修复（需产品决策）.** `controller/misc.go:296-318` 仍把生成的 12 位新密码放在 `data` 返回；前端 `web/src/features/auth/reset-password-confirm/index.tsx:75-85` 依赖该字段展示并复制密码。因此"删除回显"不是单侧改动，应改为校验重置令牌后由用户自设新密码（前后端联动），不在本轮小步修复范围内。
- **其他残余**：验证码**发送**侧仍只有按 IP 的 2 次/30 秒限制（`middleware/email-verification-rate-limit.go`），分布式来源仍可对同一邮箱大量发送；按邮箱/按用户的发送限额未落地。登录计时的残留见 `6ec801848` 提交说明：滚动迁移算法期间占位 hash 跟随写入算法，旧格式账号仍可被区分。

### P0-4 明文 API Key 可凭会话直接读取，缺少二次验证 ✅

**问题.** `POST /token/:id/key` 与 `POST /token/batch/keys`（`router/api-router.go:281,286`）仅带 `CriticalRateLimit` + `DisableCache`，却返回 `token.GetFullKey()` 完整 `sk-` 密钥（`controller/token.go:188-206`、`:521-551`）。对照 `controller/access_token.go:24-49`：面板访问令牌的同类读取**正确要求**安全凭证（`RequireSecurityProof`）。

**影响.** 任何被盗会话（或泄露的 PAT——`UserAuth` 接受 PAT）都能一次性导出账户下全部中继密钥，且密钥不随密码修改而轮换，等于持久化的额度盗用。

**建议.** 两个路由接入 `RequireSecurityProof` / `SecureVerificationRequired`；限流改为按用户维度，而非按可伪造的 IP。

**🛠️ 已修复（`c62f10985` 后端、`5447458ce` 前端）.** 两个接口都接入了 step-up，按"两个接口都加"的选择执行：
- **作用域与绑定.** 新增 `VerificationScopeTokenKeyRead`（`token.key.read`），上下文为 `token_ids`。`normalizeTokenKeyReadIDs` 先排序去重，因此 `[A,B]` 与 `[B,A]` 等价、一份证明只覆盖它被签发的那个 id 集合；拒绝空集与非正数；上限 100，与批接口自身的 `MsgBatchTooMany` 对齐。
- **控制器.** `GetTokenKey` / `GetTokenKeysBatch` 在**参数校验之后**调用 `requireTokenKeyReadProof`，参数非法仍报 `MsgInvalidParams` 而不是"缺少证明"，避免把参数错误伪装成安全错误。
- **限流.** 两条路由由全局 `CriticalRateLimit` 改为 `UserCriticalRateLimit("token-key-read")`，不再与全站共享桶、也不再受可伪造 IP 影响。
- **前端.** 四处读取点都先弹二次验证再把证明放进 `X-Security-Proof`：`api-keys-provider` 的单条与批量、dashboard `RequestPreview` 的复制、聊天链接（`chat/$chatId`、`chat2link`、侧边栏外部客户端入口）。聊天链接的查询设 `retry: false`——一次弹窗是一个用户决定，不是重试循环；取消即中止且不会在用户背后重开弹窗，`isPending`/错误分支保持弹窗挂载。
- **测试.** 后端 `TestSecurityEnrollmentTokenKeyReadRequiresBoundProof`：该作用域只提供 password 方式、缺证明 403、授权读取、重放 `SECURITY_PROOF_CONSUMED`、用 A 的证明读 B 得 `SECURITY_PROOF_CONTEXT_MISMATCH`、单 id 证明打批接口被拒、`[A,B]` 与 `[B,A]` 等价、他人 token 可见但不返回 key；审计矩阵每个用例各签一份证明，保留原有的审计事件与"不泄露"断言。前端 `api-key-listing.test.tsx` 驱动弹窗并断言"验证前不发出任何 key 请求""取消后不发任何请求、不写剪贴板"。
- **验证.** `go test ./controller/ -count=1` 通过（243.162s）；`npx vitest run` 165 文件 / 2094 用例全部通过。**仅本地 SQLite 验证，未跑 MySQL/PostgreSQL**（本机无 DSN）；改动不含新查询形态，与方言无关。

**行为变更（需知晓）.** ① **PAT 不能再读 key.** step-up 证明是会话级的（`GetSessionAuthIdentity` 拒绝 PAT 认证），因此"用 PAT 读 key"从 `200` 变为 `403 SECURITY_PROOF_INVALID`。这是有意的，与 `channel.key.read` 的既有行为一致，但会打断用 PAT 导出密钥的脚本，这类自动化需改为会话登录。② **聊天页与仪表盘现在会弹二次验证**：进入 `/chat/$chatId`、`chat2link`、侧边栏外部客户端入口、dashboard 请求示例的复制按钮都会先要求验证；取消则不再生成链接、不写剪贴板。

**残余.** 未引入"同作用域短期免验证窗口"，因此同一会话连续查看多个 key 会多次弹窗（安全优先；若体验不可接受，可后续为同作用域加 N 分钟内的复用）。证明按 id 集合绑定，若将来新增批量导出类接口，需各自申请对应 id 集合的证明，不能复用单条读取的证明。

### P0-5 上游请求丢失客户端 context ✅

**问题.** 上游 HTTP 请求未绑定客户端请求上下文，客户端断开后不会被取消。

**证据.**
- `relay/channel/api_request.go:319`（`DoApiRequest`）与 `:349`（`DoFormRequest`）使用 `http.NewRequest`；`:562` 的 `relayClient.Do(req)` 未附加 `c.Request.Context()`
- 同一文件的**任务路径**写法正确：`newTaskAPIRequest`（`:623`）使用 `http.NewRequestWithContext(c.Request.Context(), ...)`
- 其他同类遗漏：`relay/channel/ollama/relay-ollama.go:338,390,431,505,538`、`coze/relay-coze.go:221,266`、`baidu/relay-baidu.go:219`、`dify/relay-dify.go:91`、`jimeng/adaptor.go:111`
- `relay/responses_websocket.go` 等路径的断开处理是正确的（`relay/helper/stream_scanner.go:194,304` 会 select 客户端 context），说明问题集中在 HTTP 直连路径

**影响.** 客户端放弃请求后，上游连接与 goroutine 继续占用，预扣额度不会回滚——即"为已放弃的请求付费"。

**建议.** 在 `doRequest` 内补 `req = req.WithContext(c.Request.Context())`，并逐个修正上列渠道站点；可加一条单测或 lint 规则防止回归。

**🛠️ 已修复（`426a65d0a`）.** 采用**在构造点绑定**而非在 `doRequest` 内统一改写：`doRequest` 同时被 `DoTaskApiRequest`（其 `newTaskAPIRequest` 已正确绑定）和外部 `DoRequest` 使用，在传输层静默替换调用方自设的 context 会让"调用方明确指定 context"这一语义失效；构造点绑定与仓库既有的正确写法一致。
- **入口.** `DoApiRequest` / `DoFormRequest`（几乎所有 HTTP 渠道适配器的必经之路）改用 `http.NewRequestWithContext(c.Request.Context(), ...)`。文档原建议中的 `:562 relayClient.Do(req)` **无需改动**——请求一旦携带客户端 context，`Do` 自然随客户端断开而取消。
- **自建请求的渠道站点.** `jimeng/adaptor.go`（经 `channel.DoRequest`）、`coze/relay-coze.go` 两处（chat retrieve 与 message list）、`dify/relay-dify.go`（文件上传）、`replicate/adaptor.go`（文件上传）。
- **额外发现并修复：`service/midjourney.go:341`.** MJ 提交此前用 `context.WithTimeout(context.Background(), timeout)`：超时本身有上界，但客户端断开后仍会跑满超时。改为 `context.WithTimeout(c.Request.Context(), timeout)`，超时与取消语义都保留。文档原证据列表未列出该处。
- **测试.** `relay/channel/api_request_test.go` 新增 `TestDoRequestEntryPointsInheritClientCancellation`：对 `DoApiRequest` 与 `DoFormRequest` 各起一个 httptest 上游，handler 先排空请求体（**net/http 只有在请求体被消费后才启动后台读**，否则服务端察觉不到客户端断开——这是本用例第一次写成时踩到的坑），再阻塞在 `r.Context().Done()` 上；只有真实取消才会回报，因此不会因为"服务端自己放弃"而误过。随后取消客户端 context，断言上游请求被取消且中继调用返回错误。**已确认未绑定版本会在 5 秒守卫处失败（红），绑定后通过（绿）。**
- **验证.** `go build ./...`、`go vet ./relay/...`、`go test ./relay/... -count=1`（18 个包全 ok）、`go test ./service/ -count=1`（ok）、新用例 `-race` 通过。

**残余（本轮未修）.** ① **ollama 的 5 处**（模型列表/拉取/流式拉取/删除/版本）、**baidu 的 access token 获取**、**task-plugin 的描述符请求**未绑定：前者的调用方既包含 `controller/channel.go` 的请求处理（有 `c`），也包含 `controller/channel_upstream_update.go` 的后台模型同步（无 `c`）；baidu 的 helper 藏在带缓存的共享函数后；jsplugin 的 `doFetchDescriptor` 由后台任务轮询调用。绑定它们需要逐个改签名并决定后台调用方用什么 context，属独立改动。② **非中继路径**的同类写法仍在：`controller/channel-billing.go:153,444`、`controller/channel_upstream_update.go:337`、`controller/wechat.go:31`、`controller/topup_creem.go:420`、`service/user_notify.go:161,254`、`service/webhook.go:95`（部分调用方本身没有客户端请求）。③ `RELAY_TIMEOUT` 默认为 0，出站客户端**没有超时**，因此上述任何丢失客户端 context 的路径只受 TCP keepalive 约束；为这些路径补超时预算是遗留项。

### P0-6 `recover` 把 panic 转成"成功"，计费写入被静默忽略 ✅

**问题 A：panic 被吞成 nil 返回值.** `model/ability.go:263-276`：

```go
func (channel *Channel) UpdateAbilities(tx *gorm.DB) error {   // 无具名返回值
	if tx == nil {
		tx = DB.Begin()
		...
		defer func() {
			if r := recover(); r != nil { tx.Rollback() }   // 无日志、无返回值
		}()
	}
```

panic 后函数返回零值 `nil`，调用方认为路由能力表已重建。同样形态见 `model/channel.go:461`、`model/user.go:424,463`、`model/topup.go:306,375,415`、`model/redemption.go:36,69`。

**问题 B：计费/额度写入未检查错误.** `model/usedata.go:120` 的 `quota_data` 插入未检查错误，紧接 `:123` 仍打印"保存数据看板数据成功"（同文件 `increaseQuotaData` `:128-140` 却检查了）。`model/user.go:732,789`、`model/checkin.go:118` 中 `_ = IncreaseUserQuota(...)` 后直接写成功日志。`relay/relay_task.go:580` 的 `_, _ = task.UpdateWithStatus(...)` 之后用内存对象构造响应（`:583-599`），客户端看到的状态可能从未落库。

**建议.** 具名返回值 + 记录 panic 并返回错误；计费与状态写入必须检查错误，失败时不得记录/返回成功。

### P0-7 计费取值在两条路径上不一致 ✅

**问题 A：任务计费的分组倍率疑似用错参数.** `service/task_billing.go:408`：

```go
groupRatio := ratio_setting.GetGroupRatio(group)
userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)   // 同一参数传两次
```

签名为 `GetGroupGroupRatio(userGroup, usingGroup string)`（`setting/ratio_setting/group_ratio.go:88`）。其他调用点均传两个不同值：`service/group.go:128`、`service/quota.go:117`、`controller/pricing.go:50`、`relay/helper/price.go:57`。

**问题 B：同一图片模型在两种传输路径下默认质量不同.** `relay/helper/valid_request.go:223-226`（multipart/form 路径）把 `gpt-image-1` 的空 `quality` 设为 `"standard"`；`:261-264`（JSON 路径）设为 `"auto"`。质量是计费乘数（AGENTS.md 明确列出），同一逻辑请求会因传输方式被不同计价。

**建议.** 由维护者确认 P0-7A 的正确第二参数后修正并补回归用例；P0-7B 抽取统一的 `gpt-image-1` 默认值（与 `dto.MaxImageN` 校验、`top_p` 夹取一样集中到一处）。

### P0-8 渠道 API Key 被完整写入日志 ✅

**问题.** `relay/channel/zhipu/relay-zhipu.go:42-44` 在密钥格式不符预期时把**完整的渠道密钥**拼进日志：

```go
split := strings.Split(apikey, ".")
if len(split) != 2 {
    common.SysLog("invalid zhipu key: " + apikey)   // ← apikey 明文落盘
```

触发条件是密钥**不含恰好一个 `.`**，即"格式不符"分支——这恰恰是运维最可能发生的场景：配置时粘错渠道的密钥、密钥含尾随空白或被截断、上游格式变更导致全部真实密钥不再匹配 `a.b`。`common.SysLog` 同时写 stdout 与日志文件，而本仓库的日志会**整文件轮转复制**（`logger/logger.go:55,115-122`），因此泄露的密钥会被复制进后续日志文件，而不只是留在原处。

同类形态：`controller/channel-billing.go:199` 构造 `...?api_key=%s`，密钥进入 URL 后会被 HTTP 日志与错误链路带上；§P2-7 记录的 `middleware/logger.go` 只对 OAuth 前缀脱敏，不能覆盖这类查询串。

**影响.** 任何能读日志的人（运维、日志聚合平台、CI 归档、`docker logs`）都能拿到该密钥。注意所记录的是**格式错误**的那个值，因此危害集中在"密钥格式从 `a.b` 变更"这一情形——那种情况下全部真实密钥都会落盘，且不会有人立刻发现。

**建议.** 立即改为只记录可识别信息（`len(apikey)`、末尾 4 位或哈希前缀），绝不记录原值；把 `middleware/logger.go:31-46` 的脱敏从"按路径前缀"改为"按参数名白名单"（`key`、`api_key`、`token`、`secret`、`access_token`），使 relay 与面板两侧同时受保护。

**验收.** 构造一个非法 zhipu key 触发该分支，日志与 stdout 中不出现该字符串；对带 `?api_key=` 的请求，访问日志中该参数为掩码。

---

---

## 3. P1 — 性能与伸缩

### P1-1 默认部署实际没有查询缓存 🔍

**问题.** 内存缓存默认关闭：`common/init.go:88` 为 `MemoryCacheEnabled = os.Getenv("MEMORY_CACHE_ENABLED") == "true"`，仅在设置了 `REDIS_CONN_STRING` 时被 `main.go:83-86` 强制打开。令牌、用户、渠道缓存都以此为开关（`model/token_cache.go:95`、`model/user_cache.go:118`、`model/channel_cache.go:220`）。

**影响.** 无 Redis 的部署（小型自建场景的常见形态）在每个中继请求前要付出约 8–14 次同步库往返，请求后约 5 次写。

**建议.** 让令牌/用户/渠道/价格走进程内有界 LRU，或复用仓库已有的 `pkg/cachex.HybridCache`（`pkg/cachex/hybrid_cache.go`）。同时把 `REDIS_POOL_SIZE` 默认值 10（`common/redis.go:39`）按并发上调。`model/token_cache.go`、`model/user_auth_cache.go` 的 Redis 失效设计（fence + tombstone）已经很完善，只需让无 Redis 路径也有缓存层，请先读注释再改。

### P1-2 渠道缓存刷新无失败保护，可造成全局中断 ✅

**问题.** `model/channel_cache.go:36` 与 `:46` 的全表查询**丢弃了错误**，随后 `:81-99` 无条件交换路由表：

```go
var channels []*Channel
DB.Find(&channels)          // 错误未检查
...
newChannelId2channel[...] = channel
```

`main.go:96-102` 的重试只覆盖 panic，不覆盖 DB 错误。刷新每 `SYNC_FREQUENCY`（默认 60 s，`main.go:106`）执行一次，且约 20 处管理端编辑会触发。

**影响.** 一次瞬时查询失败即发布空路由表，之后最长 60 秒内所有请求返回"无可用渠道"——DB 抖动被放大为全站中断。

**建议.** 检查错误与空结果，失败时保留旧快照；配合 `atomic.Pointer` 快照交换替代"加写锁重建 + 全量交换"，并把 `model/channel_cache.go:81-86` 中位于写锁内的 `GetKeys()` 循环移出锁（`RWMutex` 在有写者排队时会阻塞所有读者，等于停顿全站的渠道选择）。

### P1-3 流式路径的内存与 CPU 放大 🔍

按影响排序：

| # | 问题 | 证据 | 影响 |
| --- | --- | --- | --- |
| 1 | **SSE 行缓冲上限 128 MB，退化为只增不减** | `relay/helper/stream_scanner.go:27` `DefaultMaxScannerBufferSize = 128 << 20`（注释写 64MB，与值不符）；`common/init.go:182` `STREAM_SCANNER_MAX_BUFFER_MB=128`；`:51` 预分配 | 每并发流最多 128 MB；50 条即 6.4 GB。注意触发条件是**上游**行长度，非终端用户可控 |
| 2 | **非流式客户端经 Responses 路由时全量缓冲 SSE**，随后再复制两到三份 | `relay/channel/openai/chat_via_responses.go:70-120`（扫描全流）、`:141` `common.Marshal` | 峰值约为响应体 3 倍，**无字节上限**，按并发生效；缓冲是该 handler 的设计（名为 `Buffered`），应加上限 + 快速失败 |
| 3 | **磁盘缓存被自己的中间件抵消** | `middleware/distributor.go:255-259` `storage.Bytes()` 在 `application/json`（几乎全部对话请求）分支被调用；`common/body_storage.go:245-246` 整块读回堆内存；上限 128 MB（`common/init.go:184`）。同仓库 `common/gin.go:117-122` 已示范流式解码 | 开启 `DiskCacheEnabled` 时反而失去意义。`model` 与 `group` 都是顶层字段，读小前缀用 gjson 即可 |
| 4 | **每个 chunk 解码 2–3 次** | `relay/channel/openai/relay-openai.go:137-138` 两个消费者各自 `Unmarshal`（`:203` 与 `helper.go:144`），`ForceFormat`/`ThinkingToContent` 下第三次（`:31-34`） | 1 万 chunk 的回答 = 2–3 万次结构体解码，纯 CPU/GC 开销 |
| 5 | **每条流全量累积补全文本，且通常被丢弃** | `relay/channel/openai/relay-openai.go:116` 写入 `helper.go:125-126`，仅在 `!containStreamUsage` 时读取（`:184`） | 上游报 usage（常见）时纯属浪费 |
| 6 | 出站请求体在整个生成期被持有 | `relay/compatible_handler.go:143-150` 的 `defer closer.Close()` 实际在 `TextHelper` 返回时才执行 | 多一份完整请求体常驻 |
| 7 | 图片流固定持有最后一帧（多 MB base64） | `relay/channel/openai/relay_image.go:143-148`（`StringToByteSlice` 零拷贝） | 图片流量叠加 |

**建议.** 把 `STREAM_SCANNER_MAX_BUFFER_MB` 默认降到几 MB；给 `Buffered` 路径设字节上限并在超限时失败；`getModelFromJSONBody` 改为前缀读取；`observeStreamChoices` 与 `processTokenData` 共用一次解码结果。

### P1-4 数据库层：连接池、索引与 N+1 ✅/🔍

**已复核：**
- `model/main.go:211-213`：`SetMaxIdleConns(100)` 配 `SetMaxOpenConns(1000)`，且 `SetConnMaxLifetime(60s)` —— 超出 100 的空闲连接每次归还即被关闭，且池内连接每分钟全部重建。**SQLite 分支（`:182-185`）之后同样执行这段设置**，而 SQLite DSN 带 `_txlock=immediate`（`common/database.go:64`），写事务可达 30 s `busy_timeout`。
- `model/ability.go:162` 的 `DB.First(&channel, ...)` 与 `:185` 的 `Where("id IN ?", channelIds).Find(&channels)` 重复取同一行，后者还是 `SELECT *`（含全部渠道密钥）；`filterAbilitiesByConstraints`（`:169-185`）在 `filters` 为空时没有提前返回，直接触发上述冗余查询。

**审计得出（待复核）：** `model/channel.go:25,29`（`Type`、`Status`）与 `model/ability.go:22`（`Enabled`）等热过滤列无索引；`model/log.go:499` 管理端日志分页使用无上限 `COUNT(*)`；`model/model_meta.go:193` 以 `0, -1` 取消 LIMIT 后在 Go 侧分页。

**建议.** `MaxIdleConns` 与 `MaxOpenConns` 取齐、生命周期放宽到 30–60 min 或取消，并为 SQLite 单独设小连接数；补索引；删掉 `model/ability.go:162`；给管理端计数加上限。

> 按 AGENTS.md，上述任何 DB 行为改动都必须在真实 SQLite / MySQL / PostgreSQL 上验证（见 P2-5 的 CI 缺口）。

### P1-5 启动成本随数据量增长 🔍

- `model/main.go:380` → `model/external_identity_claim.go:121-135`：Telegram 身份回填是 O(用户数) × 每用户 3 条语句，无完成标记、无批处理，且在主节点每次启动时同步执行。1 万用户约 3 万条语句。
- `model/main.go:337-373,388,402`：36 张表的 `AutoMigrate` 每次启动无条件执行；另有两条 PG 迁移每次启动都会取 `ACCESS EXCLUSIVE` 锁（`model/token_migration.go:159`、`model/prefill_group_migration.go:132`）。
- `main.go:103` 与 `:112` 在启动时把渠道与能力表**完整读两遍**（`model/channel_cache.go:36,46` 与 `model/pricing.go:195,202,211`）；`model/channel_cache.go:61` 还在分组循环内调用 `channel.GetModels()`，使同一字符串被重复切分 G 次。
- `main.go:291-381` 各步骤严格串行，主库与日志库迁移是天然可并行的候选。

**建议.** 为回填加完成标记或改为单条 `INSERT ... SELECT`；提供 `AUTO_MIGRATE_ON_BOOT=false` 逃生开关；把 `GetModels()` 提到循环外；并行化独立的启动步骤。

### P1-6 前端首屏体积与加载 🔍

前端基线：1,360 个文件（880 `.tsx` + 465 `.ts`），源码约 45 万行，164 个测试文件，64 个路由文件，112 处 `useQuery`。以下体积数字由**源码统计**得出，未经真实构建产物核对（本地无 `bun`/`node_modules`，`web/dist` 只有 CI 占位文件）。

**1. 7 个语言包全部静态打进首屏 chunk：3.51 MB（minified）** 🔴 本项收益最大。

`src/i18n/config.ts:24-40` 把 `en, fr, ja, ru, vi, zh-TW, zh` 通过静态 `import` 放进 `resources`，`src/main.tsx:23` 无条件导入该模块；而配置为 `load: 'currentOnly'`，运行时只会用到其中一个。

| 语言 | raw (B) | minified (B) |
| --- | --- | --- |
| ru | 730,268 | 689,586 |
| ja | 605,231 | 564,549 |
| vi | 584,794 | 544,112 |
| fr | 567,176 | 526,494 |
| en | 502,496 | 461,814 |
| zh-TW | 486,686 | 446,004 |
| zh | 485,808 | 445,126 |
| 合计 | 3,962,459 | **3,677,685** |

**建议.** 在 `i18n.init` 前按当前语言（含 fallback）`import()` 对应语言包（`partialBundledLanguages` 或小型 backend），可去掉其中约 85%。

**2. 语言包内部 88% 的内容是重复的英文键名：约 1.4 MB.** 见 §5.4 —— 键名本身就是英文句子，每个语言文件都要把键复制一遍。

**3. 未登录落地页会同步拉入 Markdown + KaTeX 管线与整个品牌图标 barrel.** `src/routes/index.tsx:21` → `src/features/home/index.tsx:29`（静态 `Hero`）与 `:24`（静态 `RichContent`）→ `src/components/rich-content.tsx:20` → `src/components/ui/markdown.tsx:20,23`（`import * as katex from 'katex'` + KaTeX CSS + `marked`）；`src/features/home/components/sections/hero.tsx:19` 从 `@lobehub/icons` 的**聚合入口**静态导入。仓库已有正确写法：`src/lib/lobe-icon.tsx:165` 用 `lazy(() => import('@lobehub/icons/es/${key}/...'))` 配合 `webpackInclude` 过滤，页面内应照此改为 `lazy()`。

**4. 二级代码分割基本缺失.** 整个 `src/features/` 只有 **8** 处动态 `import()`，其中 5 处在同一个文件（`src/features/dashboard/index.tsx:80-110`）。路由直接导入整个 feature barrel：`src/routes/_authenticated/channels/index.tsx:24` → `@/features/channels` → `ChannelsDialogs`，从而静态拉入 `param-override-editor-dialog.tsx`（3,308 行）、`advanced-custom-editor-dialog.tsx`（1,849 行）、`channel-test-dialog.tsx`（1,389 行）、`codex-usage-dialog.tsx`（1,356 行）。各 feature 都有唯一的 `*-dialogs.tsx` 挂载点，按弹窗开启状态 `lazy()` 即可，每个弹窗一行改动。`rsbuild.config.ts:75` 当前只做路由粒度分割。

**5. `defaultPreload: 'intent'` 实际没有预加载任何数据.** `src/main.tsx:52-53` 设置了意图预加载与 `defaultPreloadStaleTime: 0`，但 64 个路由中只有 1 个 `loader`（`src/routes/_authenticated/chat/$chatId.tsx:34`），且它只校验参数；没有任何路由调用 `queryClient.ensureQueryData`。首屏数据仍在组件挂载后才开始请求。建议把高频首屏数据（用量日志、渠道、仪表盘）移入 `loader` + `ensureQueryData`（router context 已持有 `queryClient`，`src/main.tsx:43`），或移除该配置以免误导。

**6. 已安装但零引用的依赖与约 2,000 行死 UI 原语.** `knip.config.ts:5` 的 `ignore: ['src/components/ui/**']` 使死原语永远不会被报出。已确认 0 处导入：`chart.tsx`（389 行，引入 `recharts`）、`menubar.tsx`（303）、`context-menu.tsx`（295）、`navigation-menu.tsx`（192）、`pagination.tsx`（160）、`breadcrumb.tsx`（144）、`button-group.tsx`（105）、`input-otp.tsx`（103，引入 `input-otp`）、`resizable.tsx`（68，引入 `react-resizable-panels`）、`kbd.tsx`/`aspect-ratio.tsx`/`direction.tsx`。零引用生产依赖：`@tanstack/react-virtual`、`next-themes`，以及仅通过上述死文件可达的 `recharts`、`input-otp`、`react-resizable-panels`。另 `src/components/ui/dropdown-menu.test.tsx` 未放在 `__tests__/` 目录，违反 `web/AGENTS.md` §3.14。

**7. 依赖声明与依赖树问题.**
- `@xyflow/react` 被 5 个生产文件导入（`src/components/ai-elements/{node,panel,controls,edge,toolbar}.tsx`）却声明在 `devDependencies`（`package.json:104`），仅靠 hoisting 才能工作，裁剪安装即断。
- `@lobehub/icons` 的 `peerDependencies` 带 `@lobehub/ui`（`bun.lock:356`），Bun 自动安装 peer，于是 `bun.lock:358` 解析出完整的 `@lobehub/ui@5.22.3` 及 60+ 直接依赖（`mermaid`、`shiki`、`katex`、`marked`、`@mdx-js/mdx`、`@splinetool/runtime`、`antd-style`、`leva` 等），而 `src/` 对这些包零引用。锁文件共解析 **1,377 个包**，只为 2 个文件用到的品牌图标。代价是安装/CI 时间与攻击面（非运行时体积）。仓库已有 `src/assets/brand-icons/`，改用本地 SVG 或按需 vendor 即可。
- 5 套图标库并存：`lucide-react`（292 文件）、`@hugeicons/*`（34）、`react-icons`（15）、`@lobehub/icons`（2），另有 `src/components/react-icon-by-name.tsx:24+` 懒加载约 30 个 `react-icons/*` 包。锁文件中存在两个 `lucide-react` 主版本、两个 `katex`、两个 `marked`，以及重复的 `dayjs`/`es-toolkit`/`uuid`。建议统一到 `lucide-react`，并把 `react-icons` 收敛到唯一被静态使用的 `react-icons/si`。

> 说明：`lucide-react` 与 `@hugeicons/core-free-icons` 都是具名 barrel 导入，二者都声明支持 ESM tree-shaking，因此**很可能**已经摇树；此项需在真实构建产物上确认后再决定是否需要改动。

### P1-7 前端渲染与请求效率 🔍

| # | 问题 | 证据 |
| --- | --- | --- |
| 1 | 行级 memo 因 `columns` 数组身份不稳定而失效（`data-table-row.tsx:91-105` 按引用比较，行收到 `props.table.options.columns`，`data-table-view.tsx:325`）。最严重的是 `api-keys-columns.tsx:78` 的 deps 含 `now`，而它每 30 s 跳一次（`api-keys-table.tsx:221-228`）→ 全部密钥行每分钟重渲染 4 次。正确范例见 `common-logs-columns.tsx:895`、`channels-columns.tsx:631` | `redemptions-columns.tsx:38-40`、`deployments-columns.tsx:41-49`、`pricing-columns.tsx:43-48` 返回新数组字面量 |
| 2 | 11 个 Context Provider 中 10 个把内联对象字面量作为 `value`，消费者最多 12 个（`usage-logs-provider.tsx:68`）。唯一正确记忆化的是 `channels-provider.tsx:104-137`——可作为模板 | `subscriptions-provider.tsx:66`、`api-keys-provider.tsx:159`、`models-provider.tsx:99`、`search-provider.tsx:49`、`direction-provider.tsx:61`、`font-provider.tsx:66` |
| 3 | 密钥表格设了 `manualPagination` 但漏了 `manualFiltering`（`api-keys-table.tsx:319`，对照 `users-table.tsx:199`）。`api-keys-columns.tsx:131` 定义了客户端 `filterFn`，于是状态筛选只作用于当前页，而总数来自服务端 —— **用户可见的错误结果** | 一行修复 |
| 4 | 用量日志查询键同时包含 `searchParams` 与由 URL 同步而来的 `columnFilters`（`usage-logs-table.tsx:163-187`、`use-table-url-state.ts:153-156`），一次筛选产生两个键 → 一次多余请求；另 `t` 被放进 key 但函数会被 React Query 忽略，看似"切语言重查"实则不会 | 键改为只依赖 `buildApiParams`（`usage-logs/lib/utils.ts:250-256`） |
| 5 | 每次按键都发请求且未防抖：`create-deployment-drawer.tsx:207-244`、`:246-256`。仓库已有 `useDebounce`，`model-pricing-sheet.tsx:294` 用对了 | — |
| 6 | 同一端点两套 query key（`edit-tag-dialog.tsx:78` 的 `['all-models']` vs `channel-mutate-drawer.tsx:549` 的 `['channel_models']`）；`tag-batch-edit-dialog.tsx:104` 每次打开都发一次结果被丢弃的请求；`['perf-metrics-summary', 24]` 在 3 个文件里重复为字面量 | — |
| 7 | 价格目录一次性全量拉取（`use-pricing-data.ts:30`）后在前端分页（`pricing-table.tsx:63-75`）；`getVendors({ page_size: 1000 })` 出现在 4 处；移动端卡片列表既无虚拟滚动也无行 memo（`mobile-card-list.tsx:184-207`，`card-row-content.tsx` 未包 `memo`）。注意 `@tanstack/react-virtual` 已安装但**零引用** | `knip.config.ts` 未将其列入忽略 |
| 8 | `use-status.ts:26,36` 在**每次渲染**都执行 `readCachedStatus()`（localStorage 读取 + 整块 `JSON.parse`）并作为新的 `placeholderData` 传入，共 27 个调用点 | 量级未测，但属明确缺陷 |
| 9 | **全局 `staleTime: 10_000`**（`src/lib/query-client.ts:47`）导致重复请求：112 个 `useQuery` 中约 **68 个**未单独设置而继承该值，包含用量日志（`usage-logs-table.tsx:163`）、渠道（`channels-table.tsx:223`）、密钥（`api-keys-table.tsx:264`）、用户（`users-table.tsx:123`）等主列表；另 53 处显式值互不一致（0 / 10 s / 30 s / 60 s / 5 min / `Infinity`）。典型冲突：`['user-groups']` 在 `api-keys-columns.tsx:51-54` 用 `staleTime: 0`，在 `common-logs-filter-bar.tsx:134`、`api-keys-mutate-drawer.tsx:135` 却不设 → 这些视图每次挂载必然重取 | 建议全局默认提到 60–120 s，仅少数真正实时的面板单独下调 |
| 10 | `system-config-store` 把 `/api/status` 的投影持久化到 localStorage（`stores/system-config-store.ts:60-99`），而权威副本在 Query 缓存（`lib/status-query.ts:152-162`），于是同一份数据存在于**三处**，并需维护 `mapStatusDataToConfig`（`status-query.ts:63-104`）这第二套投影；同时 `useStatus()`（27 处）与 `useSystemConfig()`（9 处）两套钩子暴露同一载荷。`hooks/use-system-config.ts:64` 订阅**整个 store**（无选择器）且 `:104-109` 每次渲染返回新对象 → 任意字段变化都会重渲染全部消费者；`use-notifications.ts:100` 同形态 | 按 `web/AGENTS.md` §3.5，服务端状态不应镜像进客户端 store；建议只保留 Query 缓存 |
| 11 | 无任何乐观更新（`getQueryData` 出现 **0** 次，4 处 `onMutate` 只改本地组件状态），因此频道/密钥/状态开关每次都走 POST → invalidate → GET | 属体验优化而非缺陷；好在也**不存在回滚正确性问题**。高频开关可考虑快照 + `onMutate` 回滚 |

**建议.** 先修 #1/#3/#4（收益明确、改动小），再统一 Provider 记忆化（#2）与全局 `staleTime`（#9），最后处理请求层去重与防抖。修订前建议先用 React DevTools Profiler 与 Network 面板确认实际渲染与请求次数。

---

## 4. P2 — 架构与可维护性

### P2-1 35 个渠道适配器缺少公共基类 ✅

**问题.** 对 `relay/channel/*/adaptor.go`（36 个文件、8,561 行）做归一化去重后，发现 28 组完全一致的函数：

| 函数 | 完全相同的副本数 | 示例 |
| --- | --- | --- |
| `GetChannelName()` → `return ChannelName` | **34** ✅ | `relay/channel/ali/adaptor.go:202`、`aws/adaptor.go:190` |
| `GetModelList()` → `return ModelList` | 31 | `cloudflare/adaptor.go:129`、`moonshot/adaptor.go:129` |
| 空实现 `Init(info)` | 26 | `baidu_v2/adaptor.go:43` |
| `DoRequest` → `channel.DoApiRequest(...)` | 26 | `claude/adaptor.go:150`、`vertex/adaptor.go:327` |
| `ConvertGeminiRequest` 桩 | 24 | `ali/adaptor.go:49` |
| `ConvertAudioRequest` 桩 | 21 | `cohere/adaptor.go:31` |

另有 142 处 `"not implemented"` 桩，其中 **12 处是 `panic("implement me")`**（✅ 已复核：`relay/channel/{tencent,cohere,xunfei,dify,palm,baidu,cloudflare,zhipu,mokaai,mistral,jina,xai}/adaptor.go`），可从 `POST /v1/messages`（`relay/claude_handler.go:96`）与 `controller/channel-test.go:379` 触达，panic 值还会被 `main.go:189` 回显给客户端。

**影响.** 接口新增一个方法要改 35 个文件；误触桩代码会把内部 panic 文本泄露给调用方。

**建议.** 仓库已有现成范式可循：`relay/channel/task/taskcommon/helpers.go:85-96` 的 `BaseBilling`（对 `EstimateBilling` 等提供空实现）。照此在 `relay/channel/adapter.go` 增加 `channel.BaseAdaptor` 并嵌入全部 35 个适配器，可去掉约 1,500 行；同时把 12 处 panic 改为返回错误。

### P2-2 配置系统：4 套机制与系统性读写竞态 ✅

**问题 A（竞态）.** `model/option.go:342-349` 的 `updateOptionMap` 在 `common.OptionMapRWMutex.Lock()` 内执行包含 **97 处包级全局赋值**的 switch（例如 `:426-427` 写 `setting.MjNotifyEnabled`），而读者**不加锁**：`controller/misc.go:89`、`service/midjourney.go:315`（在每个 midjourney 请求内）。多字类型（切片/映射）的风险不只是竞态检测告警，而是真实的数据竞争：`setting/sensitive.go:18`、`setting/operation_setting/operation_setting.go:8`、`setting/chat.go:9`、`setting/rate_limit.go:25`。`main.go:115` 的 `go model.SyncOptions(...)` 会让该路径周期性重放，因此生产环境是持续存在的。

`service/`、`relay/`、`controller/` 中对 `setting.*` 的直接读取合计 **1,016 处** ✅。

**问题 B（无锁就地修改）.** `setting/config/config.go:165 updateConfigFromMap` 通过反射就地改写 25 个已注册结构体，无锁；`setting/model_setting/global.go:76 GetGlobalSettings()` 返回**可变指针** `var globalSettings`，约 12 处 relay 站点直接读取（`relay/image_handler.go:57`、`relay/common/relay_info.go:312,1104` 等），而写入来自管理端（`model/option.go:697`）。`setting/rate_limit.go:40-41` 更是在 **RLock** 下执行写操作。

**问题 C（4 套机制）.** 同一个配置可能来自：① `setting/*.go` 包级 `var`（由 `model/option.go:361-690` 的 144 分支 switch 写入）；② `config.GlobalConfig.Register` 注册结构体（25 个模块）；③ `common.OptionMap` 原始映射（18 处读取）；④ `common/constants.go` 的 88 个导出变量。`setting/operation_setting/payment_setting.go` 与 `payment_setting_old.go` 并存且被同一管理界面编辑；`controller/misc.go:44-120 GetStatus` 在一个响应里同时读取了 4 种机制。

**建议（分步）.**
1. 先给切片/映射类的全局量换成快照 + `atomic.Pointer` 读取（`pkg/jsplugin` 正是这么做的：`pkg/jsplugin/registry.go:232-238` 用 `sync.RWMutex` + `atomic.Bool`/`atomic.Pointer[RoutingGeneration]` 做代际切换，可作为模板）；
2. 把 `*_old.go` 变量迁入注册结构体后删除 `_old.go`，所有写入收敛到单一持锁访问器；
3. 修 `setting/rate_limit.go:40-41` 的锁类型。

**验收.** `go test -race` 覆盖配置热更新用例不再报错；`GetStatus` 只从一种机制读取。

### P2-3 分层穿透与超大函数 🔍

**A. 控制器里出现业务逻辑、事务与计费数学**

- `controller/subscription.go:207` `model.DB.Create(&req.Plan)`、`:282` `model.DB.Transaction(...)`、`:311` 事务内 `Updates`
- `controller/channel-test.go:539-559` 的 `settleTestQuota` 手写了一份生产计费公式（对照 `service/text_quota.go:230`），只把分层计费分支委托给了 `service.TryTieredSettle`——等于第二份金额计算
- 重试策略有两个实现：`controller/relay.go:792 decideTaskRetry` 与 `service/relay_error.go:21 DecideRelayRetry`

**B. `model/` 之外的直接数据库访问**：非测试代码约 31 处，分布在 13 个 controller 与 3 个 service 文件中；最重复的是 `service/codex_credential_refresh.go:94` 与 `controller/codex_usage.go:134` 写了同一条 `UPDATE channel SET key`。

**C. 超大函数**（已核对定义行到闭合括号）：

| 函数 | 位置 | 行数 |
| --- | --- | --- |
| `ManageMultiKeys` | `controller/channel.go:1710-2192` | **482** |
| `testChannel` | `controller/channel-test.go:72-524` | **452** |
| `FetchUpstreamRatios` | `controller/ratio_sync.go:211-633` | **422** |
| `PrepareTaskPluginRoute` | `middleware/task_plugin.go:50-317` | 268 |
| `normalizeV1Meta` | `pkg/jsplugin/registry.go:1183-1439` | 257 |
| `applyOperations` | `relay/common/override.go:844-1093` | 250 |
| `UpdateChannel` | `controller/channel.go:1118-1327` | 210 |

`ManageMultiKeys` 同时混合了请求绑定、鉴权、审计、每渠道锁、7 分支 switch、原地切片修改、6 次 `Update()`、6 次 `InitChannelCache()` 与 19 处 `c.JSON`。

**D. 两套参数覆盖引擎并存**：`relay/common/override.go` 同时维护 `applyOperationsLegacy`（`:799`，在 `:158`、`:169` 被调用）与 `applyOperations`（`:844`）。每新增一个覆盖能力都要改两处，否则旧渠道静默不一致。

**建议.** 按 §P2-1 的思路先把 `ManageMultiKeys` 拆为"纯函数服务层（返回新状态）+ 渲染层"，符合仓库的不可变原则；`testChannel`、`FetchUpstreamRatios` 都已有可提取的片段（`:701`、`:844`）。`applyOperations` 的 23 个 case 中有 14 个是完全相同的循环，可改为操作表驱动。最后确认 `applyOperationsLegacy` 的删除条件。

**E. 一个额外正确性风险：`controller/channel.go:110-135` 与 `model/channel.go:713-722` 各实现了一份渠道状态转换策略，且写入的状态值不同**（`ChannelStatusManuallyDisabled` vs `ChannelStatusAutoDisabled`），但 `status_reason`/`status_time` 相同。建议收敛到一处。

### P2-4 响应契约不统一 ✅

**问题.** 规范封套是 `{success, message, data}`（`common/ApiError*` 家族共 712 处调用），但同时存在：

1. **约 600 处手写 `gin.H{"success": ...}`**，以 `controller/channel.go` 最多（87–99 处）；
2. **第二种方言** `{message: "success"|"error", data}`，用字面量字符串表达成败：`controller/topup.go:252,274,349`、`controller/topup_waffo_pancake.go:28,33,53`；
3. **第三种（OpenAI 风格）** 在 `middleware/task_plugin.go:1402,1418,1423,1440` 手写 `gin.H{"error": ...}`，绕过了 `middleware/utils.go:14 abortWithOpenAiMessage`（33 处正确调用），从而丢失 request-id、错误码、插件路由与错误日志。

**问题（状态码）.** `common/gin.go:199-240` 的 `ApiError`/`ApiErrorMsg`/`ApiSuccess`/`ApiErrorI18n`/`ApiSuccessI18n` **全部硬编码 `http.StatusOK`**，包括真正的失败。而 `middleware/auth.go:57,61,220,232` 又按语义返回 401/403 —— 两套约定并存。

**建议.** 先把 `controller/channel.go` 的手写封套替换为 `common.ApiErrorMsg`，再统一方言 2/3；状态码改造需与前端联动（前端当前依赖 `success` 字段，因此可先只对 5xx/401/403 生效）。

### P2-5 CI 缺少关键闸门 ✅

**现有能力（`ci.yml` + `make test`）：** 后端 `go vet` + 双模块构建 + 测试；前端 `bun install --frozen-lockfile` + `typecheck` + `vitest`。这个底线是好的，但要看清它拦不住什么。

**已复核的缺口**（在 `.github/workflows/` 下 grep `TEST_MYSQL_DSN|TEST_POSTGRES_DSN|golangci|-race|coverprofile|govulncheck|bun run lint` **全部无命中** ✅）：

| 缺口 | 说明 |
| --- | --- |
| **无 lint** | `web/.oxlintrc.json`（6.9 KB）与 `bun run lint` 已配置、`README.md` 也要求贡献者执行，但**未接入 CI**；Go 侧无 `golangci-lint` 配置，`go vet` 覆盖不到未检查返回值、未关闭的响应体等（本报告多条发现正属此类） |
| **无竞态检测** | 任何 workflow 都没有 `-race`。网关大量使用共享状态（`model/channel_cache.go`、`common/rate-limit.go`、`model/user_cache.go`、`pkg/perf_metrics`）；本次我本地在 `common`/`setting`/`authz`/`model` 上跑通（✅ 通过），但 P2-2 的读写路径现有用例未覆盖，需专门用例 |
| **三库矩阵在 CI 中静默跳过** | **14 个测试文件中有 48 处 `t.Skip`**，全部由 `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN` 门控（`model/request_policy_test.go:24-42`、`model/token_migration_test.go:73,87`、`model/user_session_migration_test.go:146`、`controller/model_management_test.go:119,489,1082,1294,1514` 等）。**CI 中没有任何内容设置这两个变量**，因此 AGENTS.md 强制要求的三库真实验证与实际执行完全脱节 |
| **无覆盖率采集** | 无 coverprofile、无报告、无阈值（实测覆盖率见 P2-9） |
| **无依赖/供应链扫描** | 无 `govulncheck`、无 Dependabot、无 CodeQL、无镜像扫描 |
| **无迁移升级测试** | 未验证"上一版本数据库升级 + 幂等重跑" |
| **前端生产构建不在 CI 中** | `bun run build` 只在 `release.yml`/`docker-build.yml` 打标签时执行；Rsbuild 专属失败（路由树重生成、`//go:embed web/dist`）只会在发版时暴露 |

**建议.** 按性价比排序：① **增加一个带 `services: mysql:8.2 / postgres:15` 的矩阵 job 并导出两个 DSN** —— 这是全仓库性价比最高的一项改动：方言用例、迁移幂等用例**已经写好**，只差连接串，48 个跳过点可立即转为真实校验；② 接入 `bun run lint` + `format:check` + `bun run build`；③ `go test -race`（至少定时跑 `./common/... ./model/... ./service/... ./relay/...`）；④ `golangci-lint` 从少量高价值 linter 起步（`errcheck`、`bodyclose`、`rowserrcheck`、`gosec`）；⑤ `govulncheck` 与 Dependabot 定时任务；⑥ 覆盖率仅做趋势展示与 diff 门禁，不急于设全局阈值。

> 关于"用例质量"的更正：仓库确实按**行为**而非源文件名组织用例（`model/locking_test.go`、`service/quota_saturation_test.go`、`model/quota_reserve_test.go`、`model/usedata_flow_test.go`、`pkg/billingexpr/` 下 4 个文件等），因此按 `foo.go` → `foo_test.go` 推断覆盖会低估。但按**实测**覆盖率看，高风险路径的缺口是真实存在的，不能以"文件命名方式"解释掉 —— 详见 P2-9。

### P2-6 可观测性缺口 ✅

- **无 Prometheus 指标端点**：`github.com/prometheus/*` 只是间接依赖，唯一直接使用是 `controller/channel_inference.go:20` 引用 `expfmt` 去解析上游响应。仓库自有的是 `pkg/perf_metrics`（内存聚合后批量落库，设计正确），但仅面向面板展示，无法被外部监控抓取。缺的是运维最需要的几类：上游错误率与每渠道 QPS、延迟直方图、goroutine/连接数、预扣与结算差额计数。
- **无健康检查端点，且现有端点无法区分依赖状态**：`router/` 与 `main.go` 中没有 `/health`、`healthz`、`readyz`（✅ 已 grep 确认）。唯一的 `GET /api/status`（`router/api-router.go:26` → `controller/misc.go:44`）返回 UI/branding 配置，**不报告** DB 连通性、Redis 连通性或迁移状态——DB 断开时它仍可能返回 200，编排器无法区分"进程存活"与"可服务"。
- **pprof 开启后完全无认证且监听全网卡**：`main.go:41` 导入 `net/http/pprof`，`main.go:169` 在 `ENABLE_PPROF=true` 时 `http.ListenAndServe("0.0.0.0:8005", nil)` —— `nil` 即 `DefaultServeMux`，因此 `/debug/pprof/*` 对任意来源开放，**独立于可信代理配置**，且该端口未被 `EXPOSE` 或文档提及。Pyroscope 为可选（`common/pyro.go:11`）。
- **日志无级别、无结构化字段、无有界保留**：`logger/logger.go:20-25` 的 `INFO/WARN/ERR/DEBUG` 只是打印到 `gin.DefaultWriter` 的**字符串标签**，没有 `LOG_LEVEL` 环境变量（只有布尔 `common.DebugEnabled`），没有 JSON 编码，也没有 `channel_id`/`model`/`user_id` 等可过滤维度——统计每渠道错误只能抓原始文本。轮转阈值是 `logger/logger.go:27 maxLogCount = 1000000`（**按行数而非大小**，且 `:115` 的计数器不加锁），**旧文件永不删除**，`docker-compose.yml:27` 又挂载了 `./logs:/app/logs`，磁盘会无界增长；文件名仍是遗留的 `oneapi-%s.log`（`:55`）。
- **限流与缓存的关键指标不可见**：`common/rate-limit.go` 的命中/拒绝、`model/channel_cache.go` 的刷新结果、`model/quota_reserve.go` 的预留失败都没有计数出口。

**建议.** 增加 `/healthz`（进程存活）与 `/readyz`（DB `Ping` + 可选 Redis `Ping` + channel cache 是否已初始化）两个未认证轻量端点；把 pprof 改为绑定回环或挂在受认证的管理监听器上；引入 `LOG_LEVEL` + 基于大小的轮转与保留策略（`lumberjack`）；暴露 Prometheus `/metrics`，首批指标覆盖：请求量/延迟/错误码（按渠道与模型）、上游失败与重试、限流拒绝数、渠道缓存刷新成功与否、缓存命中率、预扣与结算差额。

### P2-7 其他正确性与运维项 ✅

| 项 | 证据 | 建议 |
| --- | --- | --- |
| 上传/下载限流已实现但**从未挂载** | `middleware/rate-limit.go:192,196` 的 `DownloadRateLimit`/`UploadRateLimit` 在 `router/` 中零引用；常量 `common/constants.go:225-229` 同样无引用 ✅ | 挂载或删除，二者择一 |
| 优雅关闭**丢失批更新缓冲区** | `main.go:164` 启用了 `model.InitBatchUpdater()`（`model/utils.go:35-42`），token/用户/请求计数在内存累积、按 `BATCH_UPDATE_INTERVAL` 落库；关闭路径（`main.go:229-245`）只做 `srv.Shutdown` 与配额数据导出，**从不调用 `batchUpdate`**（该函数未导出，main 也无法调用）。`BATCH_UPDATE_ENABLED=true` 时最后一次落库之后的计数在重启时静默丢失 ✅ | 导出并暴露 `FlushBatchUpdate()`，在 `CloseDB()` 之前调用；同时核对 `DataExportEnabled` 分支是否覆盖了同一批数据 |
| 后台循环无排空/取消 | `gopool.Go` 启动的 `model.SyncOptions`、`controller.SyncTaskPlugins`、`model.UpdateQuotaData`、`authz.StartPolicySync`、`SyncChannelCache`、`StartSystemTaskRunner` 均无 `WaitGroup` 或取消信号，可能被随后的 `CloseDB()` 在语句中途打断；`wsmanager.StartSubscriber(context.Background())`（`main.go:108`）用的上下文永不取消 | 统一下发可取消的根 context，关闭时按逆序排空 |
| Redis 预扣脚本无超时 | `model/quota_reserve.go:83,89,95,101` 全部使用 `context.Background()`；由 `service/funding_source.go:46` 在每请求预扣路径调用 ✅ | 传入带 deadline 的 context |
| 中继 HTTP 客户端默认无总超时 | `common/init.go:112` `RELAY_TIMEOUT` 默认 0；`service/http_client.go:129-130` 仅在非 0 时设置。响应头等待上限 1800 s（`:114`） | 给 body 阶段一个上限 |
| 无界中继协程池 | `common/gopool.go:14` `gopool.NewPool(..., math.MaxInt32, ...)` | 设为有限值 + 在途信号量，便于过载时快速失败 |
| 4 个遗留流式 handler 存在 goroutine 泄漏 | `relay/channel/palm/relay-palm.go:57-87`（无缓冲 chan，发送侧不 select 断开）、`cohere/relay-cohere.go:99-110`、`zhipu/relay-zhipu.go:163-186`、`xunfei/relay-xunfei.go:219-244`（其 `defer conn.Close()` 在阻塞发送之后，连接永不关闭） | 以 `relay/helper/stream_scanner.go:275-281` 为模板（缓冲 chan + select `ctx.Done()`） |
| 任务心跳协程可永久占锁 | `service/system_task.go:333-334` 的 `fn(ctx)` 无 `defer`，心跳循环 `:318-330` 只 select `done`/`ticker` 而不 select `ctx.Done()`；`gopool` 会吞掉 panic | `defer close(done)` 并增加 `ctx.Done()` 分支 |
| 设置项与其他布尔字段反复 `ALTER` | `model/subscription.go:160` 是仓库中唯一的 `gorm:"default:true"` 布尔标签，正是 AGENTS.md 警告的场景（调用方已在 `:600`、`:770` 用代码强制） | 去掉标签，改为构造/归一化时赋值 |
| CORS 允许全部来源且允许携带凭据 | `middleware/cors.go:10-11` `AllowAllOrigins = true` + `AllowCredentials = true` ✅ | 改为基于 `ServerAddress` 的显式白名单 |
| 安全响应头几乎缺失 | 仅在 `controller/task_plugin.go:337-338`、`controller/video_proxy.go:553-555` 出现；全仓库无 `X-Frame-Options`、HSTS、全局 CSP | 增加统一中间件（HSTS / `X-Frame-Options: DENY` / `nosniff` / `Referrer-Policy` / nonce CSP） |
| 会话 Cookie 的 `Secure` 默认关闭，配套来源校验同时失效 | `common/session_cookie.go:44-84`、`service/auth_session.go:316-325`、`middleware/auth_origin.go:18-35`（`SessionCookieOriginGuard` 在标志为 false 时直接放行） | 由 `ServerAddress` 的协议推导 `Secure`；来源校验无条件执行 |
| 日志可能写入密钥与可伪造内容 | `middleware/logger.go:31-46` 原样写入 `param.Path`（含查询串），仅对 `/api/oauth/`、`/oauth/` 前缀脱敏，因此 `?key=` 形式的密钥会被完整记录，且路径中的 CRLF 可伪造日志行（另见 P0-8） | 只记录路由模板，剥离查询串，过滤控制字符 |
| 令牌以明文存储 | `model/user.go:1228-1242` 按明文比较 `access_token`；`model/token.go:280-301` 同样 | 存哈希（附带短前缀用于展示），比较哈希 |

### P2-8 一致性卫生（低风险、可批量处理）✅

- **`encoding/json` 绕过包装层**：`common/json.go:14-19` 明确说明 `hostJSONCodec` 是"主机选择 JSON 引擎的唯一位置"，但业务代码中仍有 **130 处**直接调用 `json.Marshal/Unmarshal/NewDecoder/NewEncoder`（含测试），**133 个文件**导入 `encoding/json` ✅。主要分布：`oauth/*`（9）、`controller/*`（11）、`setting/*`（10）、`common/*`（9）、`pkg/ionet/*`（17）。修复后引擎替换才真正生效。
- **日志三种写法**：`common.SysLog`（268 处，无 context）、`logger.Log*`（约 678 处）、`fmt.Println`（15 处，其中 `middleware/model-rate-limit.go:91,114`、`relay/channel/aws/relay-aws.go:302,305` 在请求路径上）。另 `relay/channel/ollama/stream.go:232` 使用内建 `println`，始终写 stderr 且绕过日志级别。
- **分页实现分叉**：`common/page_info.go:41 GetPageQuery` 有 21 处使用；`controller/channel.go:452-453` 自行实现且默认值与回退行为不同。
- **现代 Go 写法残留**：`sort.Slice` 24 处 vs `slices.Sort*` 8 处；手写 min/max（`service/rankings.go:590`、`service/http_client.go:112`）；26 处 `strings.Index` + 切片可用 `strings.Cut`；`relaykit/relayconvert/internal/convdiag/collector.go:44` 使用了已废弃的 `reflect.Ptr`。
- **注释掉的代码块**：`relay/channel/volcengine/adaptor.go:122-215`（约 98 行，占该文件 403 行中的 106 行注释）、`service/error.go:36-60`、`model/main.go:219` 的空 `if` 体。
- **死代码**：`middleware/recover.go:12 RelayPanicRecover` 定义后从未注册 ✅；`controller/log.go:64,72` 两个已废弃接口仍挂在 `router/api-router.go:318,320` 上；`middleware/auth.go:251 WssAuth` 空实现；`controller/image.go` 整个文件为空操作；`model/channel.go:640-655` 的 `channelPollingLocks` 清理函数 `CleanupChannelPollingLocks` 从未被调用。
- **错误码被丢弃**：`service/error.go:62 ClaudeErrorWrapper(err, code, statusCode)` 接受 `code` 却从不使用。

### P2-9 测试覆盖的结构性盲区 ✅

**先读这一段，否则下面所有百分比都会被误读：**

`go test -cover ./...` 的覆盖率是**按包归属**的 —— 每个包只统计"该包自己的测试"执行了多少。跨包调用**不计入**被调用包的覆盖率。本次实测验证了这一点：

```bash
go test -cover ./middleware/                       # → 57.4%（middleware 自己的测试）
go test -coverpkg=.../middleware -cover -run 'TestResponsesWS|TestResponsesWebSocket' ./controller/
                                                   # → 13.5%（仅 controller 的 WS 测试就执行了 middleware 的 13.5%）
```

结论：**函数级 `0.0%` 不等于"从未被执行"**，只等于"该包自己的测试没直接跑到它"。因此下面的可行动信号是**"是否有任何测试引用过它"**（grep `*_test.go`），而不是 `go tool cover` 的百分比。本节的函数清单已逐条按 grep 复核。

**实测总体覆盖率：根模块 38.5%**（按包归属口径）。分档如下，仅供横向比较包与包之间的测试投入：

| 包 | 覆盖率 | 包 | 覆盖率 |
| --- | --- | --- | --- |
| `router` | 87.8% | `service` | 39.2% |
| `pkg/billingexpr` | 82.9% | `controller` | 38.0% |
| `pkg/jsplugin` | 82.5% | `model` | 32.7% |
| `pkg/wsmanager` | 76.4% | `relay` | 32.2% |
| `service/authz` | 73.9% | `common` | **19.9%** |
| `pkg/perf_metrics` | 72.3% | `oauth` | **1.3%** |
| `relay/helper` | 58.3% | `middleware` | 57.4% |

**83 个包中 40 个没有任何测试文件**（这一项与归属口径无关，是硬事实）。

#### 真实缺口：全部 `*_test.go` 中零引用的函数 ✅

以下函数在**整个仓库的任何测试文件里都没有出现过一次**，是确凿的覆盖盲区：

| 函数 | 属于 | 为什么重要 |
| --- | --- | --- |
| `model/token.go:220 ValidateUserToken` | 令牌认证 | **每个 API 请求的令牌校验热路径**，整条鉴权链的落点 |
| `service/quota.go:420 PostConsumeQuota` / `:87 PreWssConsumeQuota` | 结算 | 计费落库的最终写入，AGENTS.md 的计费门禁核心 |
| `relay/helper/price.go:216 ModelPriceHelperPerCall` / `:284 HasModelBillingConfig` | 取价 | 按次计费取价（与 P0-7 的质量乘数同属一类） |
| `model/channel_cache.go:219 CacheGetChannel` / `:233 CacheGetChannelInfo` / `:251 CacheUpdateChannelStatus` | 渠道缓存 | 缓存读写与状态变更，即 P1-2 所在处 |
| `model/ability.go:63 getPriority` / `:93 getChannelQuery` / `:357 FixAbility` | 渠道路由 | 重试与优先级路由的选路逻辑 |
| `service/channel.go:48 EnableChannel` / `:79 ShouldEnableChannel` | 可用性 | 渠道被自动禁用后的**恢复**路径 —— 恰恰在故障时需要正确 |
| `controller/relay.go:300 RelayMidjourney` / `:409 RelayTaskFetch` | 中继入口 | MJ 与任务中继入口（P0-2 所在处） |

#### 名义 `0.0%` 但实际有测试引用的函数（不要按"无覆盖"处理）

这些函数的 `0.0%` 是归属口径造成的假象，它们在别的包里有测试：

`middleware/auth.go` 的 `TokenAuth` / `AdminAuth` / `RootAuth` / `RequirePermission`（由 `controller/responses_websocket_test.go`、`controller/access_token_audit_test.go`、`controller/security_enrollment_test.go`、`controller/passkey_test.go` 等覆盖）；`model/user.go:1228 ValidateAccessToken`；`model/ability.go:263 UpdateAbilities` 与 `model/channel_cache.go:117 GetRandomSatisfiedChannel`（**注意**：这两处的引用位于 `controller/model_management_test.go` 的 DSN 门控块内，CI 中很可能被跳过）；`model/account_security.go:21 ChangeUserPassword`；`model/passkey.go` 的 `RegisterPasskeyForSession`；`oauth/*` 的 `ExchangeToken` / `GetUserInfo` / `IsUserIDTaken`。

**适配器层（与归属口径无关，是硬事实）**：40 个渠道适配器中 **28 个零测试文件**，合计约 **8,683 行**，含 `volcengine`（1,260 行）、`vertex`（727）、`replicate`（562）、`coze`（539）、`baidu`（517）。这些文件正是**各上游的 usage/token 提取点** —— 上游响应格式一变，计费会静默出错而不报错。即便有测试的适配器覆盖也偏薄（`claude` 14.4%、`moonshot` 13.6%、`ali` 15.2%、`tencent` 6.2%）。

**测试基础设施的现状（值得肯定）**：fixture 在 `TestMain` 中开内存 SQLite（`model/task_cas_test.go:19-60`、`service/task_billing_test.go:29-60`），并显式 `RedisEnabled = false`；416 处使用 `httptest.NewServer`，**测试中没有任何真实网络调用**；只有 4 处裸 `time.Sleep`（`relay/helper/stream_scanner_test.go:304,463`、`controller/task_generic_test.go:499,516`）；testify 使用 7,373 次 `require.*` vs 7,179 次 `assert.*`，符合 AGENTS.md。这套基础设施很快（全量约 4 min），因此补用例的**边际成本很低**。

**建议（按价值排序）.** ① `ValidateUserToken` + `PostConsumeQuota` —— 鉴权与结算的最终落点，两者回归都是全站性的，且目前**完全没有测试**；② `EnableChannel`/`ShouldEnableChannel` 的恢复路径（故障时才会走到，最缺回归网）；③ `CacheGetChannel*` + `getPriority`/`getChannelQuery`（同时为 P1-2 的失败保护提供验收标准）；④ **每个适配器一张 usage 提取表测试**（一个测试文件覆盖多个适配器，避免每渠道一个文件的碎片化），优先 `volcengine`/`vertex`/`replicate`/`coze`/`baidu`；⑤ 先补上 P2-5 的三库 CI job，让 `GetRandomSatisfiedChannel`/`UpdateAbilities` 这类已被引用但在 CI 中跳过的用例真正跑起来 —— 这比新增用例更快见效。注意 AGENTS.md 明确禁止为凑覆盖率添加 smoke/日志型用例，因此优先补**契约型**断言而非走通即可。

### P2-10 迁移与启动校验基础薄弱 ✅

**问题 A：没有 schema 版本记录，迁移只能向上、失败不阻断.**

- `model/main.go:320-393` 的 `migrateDB()` 是一串硬编码步骤（`migrateTokenKeyUniqueness`、`migratePrefillGroupUniqueness`、`migrateSubscriptionPlanPriceAmount`、`migrateTokenModelLimitsToText`、`migrateOptionPrimaryKey`），随后是对 33 个模型的 `DB.AutoMigrate(...)`，最后按方言分支。
- **不存在 `schema_migrations` / `schema_version` 表**（✅ 已 grep：唯一命中的 `SchemaVersion` 在 `service/system_instance.go:24`，是实例上报字段，与 DB schema 无关）。因此每次启动都以命令式方式重跑全部迁移，无法回答"这个库迁到哪一版了"，也没有 down 迁移或回滚流程。
- **`migrateOptionPrimaryKey` 失败只记日志继续**（`model/main.go:333-335`）：`common.SysError("failed to migrate options primary key: ...")`，进程会带着不一致的 schema 继续服务。
- CI 中没有"用上一版本生成的库跑当前版本迁移"的用例，AGENTS.md 要求的幂等性验证（至少跑两次）也无自动化。

**问题 B：启动时环境变量校验很薄弱.** 已存在的检查只有：`SESSION_SECRET` 不等于字面量 `"random_string"`（`common/init.go:51-56`，硬 `log.Fatal`）、Cookie 设置初始化（`:66`）、日志目录创建、MySQL 字符集/排序规则（`model/main.go:200`）、64 位配额 schema 检查。**未校验**：`PORT` 可解析性、`SQL_MAX_IDLE_CONNS`/`SQL_MAX_OPEN_CONNS`/`RELAY_*` 范围、`TRUSTED_PROXIES` 的非法 CIDR、`SESSION_COOKIE_SECURE=true` 但未设 `SESSION_COOKIE_TRUSTED_URL`（仅启动横幅提示，`common/sys_log.go:49-55`）、格式错误的 `REDIS_CONN_STRING`。结果是"先撞到哪个错就死在哪个"，而不是一次性列出全部配置问题。

另外 `setting/console_setting/validation.go`（310 行，含 `ValidateConsoleSettings` 与 `checkDangerousContent`）**零测试**，且只在 `controller/option.go:442-452` 被字符串调用，**从不在启动时对已持久化的 option 运行**。

**建议.** ① 引入 schema 版本表并以其为键使各迁移步骤幂等；把"无法迁移"的仅日志失败提升为启动失败（至少对 `options` 主键这类核心表）；② 增加"上一版本库 → 当前版本"的 CI 迁移用例（与 P2-5 的三库 job 合并做）；③ 增加一次汇总式的启动校验，一次性打印全部非法环境变量与 `SECRET_URL`/`TrustedURL` 类组合错误；④ 为 `validation.go` 补测试并让它在启动时对持久化 option 跑一遍。

### P2-11 构建与发布：版本注入与产物可追溯性 ✅

| 项 | 证据 | 建议 |
| --- | --- | --- |
| **`VERSION` 是 0 字节的已提交文件** | `wc -c VERSION` → **0**；`common/constants.go:14` 默认 `Version = "v0.0.0"`；`Dockerfile:28` 用 `-X ...=$(cat VERSION)` 注入。CI 在构建前写入标签（`docker-build.yml:58`），因此**本地 `docker build` 或 `makefile:18` 会产出空版本字符串** ✅ | 让 `VERSION` 由构建单一生成，或在 `main.go` 用 `debug.ReadBuildInfo()` 兜底；CI 中断言非空 |
| **两条版本注入路径不一致** | 镜像走 `-X ...=$(cat VERSION)`（`Dockerfile:28`），发布产物走 `-X ...=$VERSION`（`release.yml:51,56,105,156`） | 统一为一处 |
| **arm64 产物与镜像不是同一个构建** | `release.yml:56` 用 `CGO_ENABLED=1` + `CC=aarch64-linux-gnu-gcc` + `-extldflags '-static'` 交叉编译 arm64，而 amd64（`:51`）与镜像（`Dockerfile:11 CGO_ENABLED=0`）是纯 Go 构建；macOS 产物还漏了 `-s -w`（`:105` vs `:51`） | 明确二选一并写进文档，否则难以复现用户报错 |
| **Go 工具链三种解析** | `go.mod` 与 `relaykit/go.mod` 均为 `go 1.25.1`；镜像 `golang:1.26.1-alpine`（`Dockerfile:10`）；`release.yml`/`electron-build.yml` 用非固定的 `go-version: '>=1.25.1'` | 统一改为 `go-version-file: go.mod` 并把镜像固定到同一 minor |
| **实验性 GC 进入发布镜像** | `Dockerfile:16` `ENV GOEXPERIMENT=greenteagc`，未文档化，是 A/B 行为差异来源 | 明确保留或移除并记录 |
| **每个 tag push 直接发布，无测试闸门** | `release.yml:5` 的 `on:` 在任意 tag 触发，没有任何 `needs:` 或 `environment:` 审批（✅ 已确认无命中） | 增加 `needs: [test]` 或人工审批环境 |
| 镜像无 `HEALTHCHECK`，探针语义不清 | `Dockerfile:30-41` 最终阶段无指令；`docker-compose.yml:62-66` 的探针用 `wget .../api/status \| grep '"success": true'`，只有 DB **与** option map 都健康才通过，因此启动期表现为未就绪，且无法区分"DB 宕机"与"正在初始化" | 与 P2-6 的 `/healthz`、`/readyz` 一起解决 |

**供应链卫生是这块的亮点，应保留**：两个 Docker 阶段均按 digest 固定、`cosign sign`、`sbom: true`、`provenance: mode=max`、发布产物带校验和、所有第三方 Action 按完整 SHA 固定并带版本注释。

### P2-12 运维文档缺口 ✅

`docs/` 下有 7 个真正的指南（`authentication.md`、`plugin-api/v1.md` + schema、`openapi/*.json`、`channel/other_setting.md`、`installation/BT.md`、翻译词汇表），质量都不错，但**没有一个是运维向的**。缺失：

- **故障排查**：渠道自动禁用（`service/channel.go:29-46`）、渠道亲和缓存行为（`service/channel_affinity.go`）、系统任务 DB 租约抢占、ClickHouse 不可达时的降级、`FixAbility` 语义，全都只在代码里。
- **部署拓扑与扩展**：多节点、共享 Redis 的必要性、反代下的流式/WebSocket 注意事项、水平扩展边界。`docs/authentication.md` 已经把这部分写得很清楚，但它只覆盖登录会话，不含 relay 面。
- **升级 / 回滚 runbook**：`README.md:235` 只写了"升级前先备份"，而 P2-10 说明实际上没有回滚路径。
- **备份与恢复**：无脚本、无 `pg_dump`/`mysqldump` 流程、无恢复演练，也没有磁盘空间预算（`./logs` 无界增长见 P2-6、`./data`、上传目录、`body_storage` 临时文件）。
- **监控与告警**：无告警项说明（在 P2-6 落地前也确实无可抓取的指标）。
- **没有 `CHANGELOG`**：`README.md:235` 让用户"查看发布说明"，但仓库内没有变更日志文件。
- **域名不一致**：`README.md:231,281,283,345` 用 `docs.newapi.ai`，`docs/installation/BT.md:5,139,140,141` 用 `docs.newapi.pro`，其中至少一个已失效。
- 环境变量没有完整参考：`.env.example`（140 行）只覆盖部分变量，其余依赖外部站点。

**建议.** 先补"备份与恢复"+"升级与回滚"两篇（体量最小、事故时最刚需），再补故障排查与监控告警（可与 P2-6 的端点一起交付）；顺手统一文档域名并加一个 `CHANGELOG` 或从 release 自动生成。

---

## 5. 前端（结构、复用、i18n、可访问性、测试）

体积与渲染性能问题见 P1-6 / P1-7，本节是其补充。

### 5.1 代码质量与结构 🔍

- **超大单组件是真实问题，但需要区分对待.** 30 个文件超过 800 行、17 个超过 1,000 行，1,181 个文件中 352 个（30%）超过 `web/AGENTS.md` §3.3 的"超过约 200 行考虑拆分"建议。按**单组件跨度**看：
  - `src/features/channels/components/drawers/channel-mutate-drawer.tsx` 共 5,064 行，其中单个组件 **4,662 行**（声明于 `:402`，一个函数内 67 个 hook），而同目录 `sections/` 合计仅 326 行；最大缩进 36 空格（约 9 层 JSX），1,444 行缩进 ≥20 —— 明显超出"嵌套不超过 4 层"。**这是最需要拆的一个文件**，且拆分目标目录已存在。
  - `src/features/system-settings/integrations/payment-settings-section.tsx` 1,636 行，单组件 1,419 行。
  - `src/features/pricing/components/model-details.tsx`（1,702 行，最大组件仅 104 行）与 `param-override-editor-dialog.tsx`（3,308 行，最大组件 149 行）虽然文件大，但内部已按 30–60 个小组件分解，**不是**同类问题，不应仅因行数而重构。
- **缺失的筛选语义导致错误结果.** `src/features/keys/components/api-keys-table.tsx:319` 设了 `manualPagination: true` 但漏了 `manualFiltering`（对照 `users-table.tsx:199`、`channels-table.tsx:342`、`models-table.tsx:208`、`redemptions-table.tsx:150`、`usage-logs-table.tsx:223`）；`api-keys-columns.tsx:131` 定义了客户端 `filterFn`，于是状态筛选只作用于当前页，而总数与分页来自服务端。**一行修复，属用户可见的正确性问题。**
- **`Intl` 剩余遗漏**（`project/intl-locale` 规则已覆盖大部分场景）：`components/data-table/core/pagination.tsx:73,115` 的 `toLocaleString()` 未传 locale，会跟随浏览器而非界面语言；`components/ui/calendar.tsx:64,237` 传的是 react-day-picker 的 `locale?.code` 而非 `toIntlLocale` 结果；`components/ai-elements/context.tsx` 在 11 处硬编码 `'en-US'`（`:130,176,180,183,236,284,325,366,407,433` 等），导致 token/费用读数永不本地化。
- **类型严格度可再提一档.** `tsconfig.app.json` 已开启 `strict`、`noUnusedLocals`、`noUnusedParameters`、`noFallthroughCasesInSwitch`，但未开 `noUncheckedIndexedAccess` 与 `exactOptionalPropertyTypes`。好消息：1,345 个源文件中只有 6 个使用 `any`。`oxlint` 中 `typescript/no-explicit-any` 与 `react/rules-of-hooks` 仅为 `warn`，而 `react/exhaustive-deps` 是 `error` —— 现有的约 10 处 `exhaustive-deps` 豁免集中在 `data-table/layout/mobile-card-list.tsx:136`、`card-row-content.tsx:72,149`、`card-grid.tsx:146`、`toolbar/toolbar.tsx:278`、`checkin-calendar-card.tsx:82`。建议把前两条提升为 `error`，并逐步开启 `noUncheckedIndexedAccess`。
- **小瑕疵**：`src/components/confirm-dialog.tsx` 中 `className={cn(className && className)}` 是空操作，应为 `cn(className)`。

### 5.2 组件复用：整体良好，两处集中重复 🔍

**做得好的部分（不要动）**：基础原语层面复用是真正落实的 —— `@/components/dialog` 有 **83** 处导入，直接使用 `components/ui/dialog` 的只有 3 处；`ConfirmDialog` 46 处；`EmptyState`/`LoadingState`/`ErrorState` 各 34 处；**75** 个文件使用 `@/components/data-table`。

**重复集中在这两处：**

1. **两个 93 行的删除弹窗是近似克隆，且绕过了仓库自己的 `ConfirmDialog`.** `src/features/keys/components/api-keys-delete-dialog.tsx` 与 `src/features/redemption-codes/components/redemptions-delete-dialog.tsx` 都恰好 93 行，diff 后约 20 行不同（导入、实体名、成功文案），其余 `AlertDialog` 骨架、`handleServerError`、mutation 与 toast 逻辑逐字相同。而同类的 4 个删除弹窗已正确使用共享封装：`users-delete-dialog.tsx:2`、`model-delete-dialog.tsx:3`、`api-keys-multi-delete-dialog.tsx:2`、`delete-account-dialog.tsx:3`。直接违反 `web/AGENTS.md` §3.3。改用 `<ConfirmDialog destructive …/>`（`components/confirm-dialog.tsx:48-88` 已具备 `destructive`/`isLoading`/i18n 取消确认能力）可删掉约 150 行。
2. **6 个 `*-mutate-drawer` 重复约 600 行的表单生命周期.** `channel-mutate-drawer.tsx`(5,064)、`subscriptions-mutate-drawer.tsx`(841)、`api-keys-mutate-drawer.tsx`(776)、`users-mutate-drawer.tsx`(593)、`redemptions-mutate-drawer.tsx`(463)、`model-mutate-drawer.tsx`。每个都重复：`useForm({resolver: zodResolver(schema), defaultValues})` → 监听 `open` 的 load-vs-reset `useEffect` → 关闭时重置的 `useEffect` → 含 create/update 分支与 `toast.success` 的 `onSubmit` → 同时挂到 `<form onSubmit>` 与页脚按钮的 `form.handleSubmit(onSubmit, onInvalid)`。证据行：`api-keys-mutate-drawer.tsx:201,202,226,236,284,393`；`redemptions-mutate-drawer.tsx:109,110,122,130,169,292`；`users-mutate-drawer.tsx:135,136,146,154,167,247`；`subscriptions-mutate-drawer.tsx:112,113,119,121,162,288`。布局层已由 `components/drawer-layout.ts` 抽象，剩下的重复正是**表单生命周期**，可抽取 `useEntityFormDrawer`（加载/重置/增改分支/字段错误映射），预计节省 400–600 行。

### 5.3 可访问性 🔍

- **没有自动化校验.** `.oxlintrc.json` 加载了 `typescript, unicorn, oxc, react, import, promise`，但**没有 `jsx-a11y` 插件与相关规则**，而 `web/AGENTS.md` §3.12 要求键盘可操作、ARIA 与 WCAG 2.1 AA 对比度。手工属性使用是合格的：`aria-label` 448 处、`aria-invalid` 118、`role=` 146、`aria-describedby` 31、`aria-expanded` 34、`aria-labelledby` 25；但 `aria-selected` 仅 **3** 处，对 75 个表格消费者而言偏低，建议抽查行选择语义。**建议在 oxlint 中启用 `jsx-a11y` 推荐规则，并把 `axe`/`vitest-axe` 接入现有 vitest 流程。**
- **弹窗原语可靠，封装有缺口.** `components/ui/dialog.tsx:19` 基于 `@base-ui/react/dialog`，焦点陷阱/恢复与 `aria-modal` 由库提供，`DialogTitle`/`DialogDescription` 也已接线。缺口是：`components/ui/dialog.tsx:91` 硬编码 `<span className='sr-only'>Close</span>`，**永不翻译**，所有语言的读屏用户都会听到英文；另有 16 个文件直接使用 `ui/alert-dialog` 而未走 `ConfirmDialog`。`autoFocus` 17 处、手动 `.focus()` 33 处，叠加在已自带焦点管理的库之上，建议复查是否在争夺初始焦点。
- **缺少行内错误态.** 集中式错误处理很强（见 §6），但 109 个使用 `useQuery` 的文件中只有 40 个引用 `isError`，因此列表请求失败时只弹 toast 并显示空表，而不是行内 `ErrorState` + 重试。页面级封装提供了重试（`settings-page.tsx:146`、`system-instances-panel.tsx:686`、`system-task-history.tsx:204`），问题是有些 feature 未使用这些封装。

### 5.4 i18n 健康度 🔍

- **键名设计违反项目自身约定，并使载荷翻倍.** `src/i18n/locales/en.json` 有 6,778 个键，其中 **5,951 个（88%）是英文句子本身**（例如 `"Please wait before editing to avoid overwriting saved values."`），只有 **25 个（0.4%）** 使用 `web/AGENTS.md` §3.1 要求的 `dashboard.overview.title` 式层级键。129 个键超过 120 字符，3 个键是纯数字（`"360"`、`"1000"`、`"10000"`），平均键长 30.9 字符。**键文本本身每个语言占 209,474 字节，7 个语言共约 1.4 MB —— 即 P1-6 总计 3.51 MB 中的约 40% 是同一批键名被存了 7 遍。** 源码侧同样失衡：4,255 处长度 ≥12 的 `t('…')` 字面量，而点号式 `t('a.b.c')` 只有 20 处。建议新代码使用短层级键，并对高频 cluster 做一次 codemod —— 单语言体积可降 30–40%，改写文案也不必再动 7 个文件。
- **键完整性是好的（0 缺失 / 0 多余）.** 已用程序核对：6 个非英文语言相对 `en` 均无缺失键、无多余键；`scripts/sync-i18n.mjs` 与 822 行的 `static-keys.ts` 注册表对动态键的处理有效。
- **未翻译残留很少.** 与英文逐字节相同的值：fr 248（3.7%）、vi 168（2.5%）、ru 147（2.2%）、ja 138（2.0%）、zh-TW 138（2.0%）、zh 137（2.0%）。
- **源码中的 i18n 纪律很强**：`toast.error/success('字面量')` **0 处**，`toast.*(t('…'))` 194 处；全仓库仅 5 处疑似硬编码 JSX 文本，其中 4 处位于未被使用的 `components/ui/carousel.tsx`。

### 5.5 测试与构建校验 🔍

- **测试基础设施是真实存在的**（与常见误解相反）：vitest 4 + jsdom + Testing Library，`src/test-setup.ts` 含手写 Storage polyfill，164 个测试文件（其中 150 个正确位于 `__tests__/`），并有带实测依据的非默认 20 s 超时说明。
- **但没有覆盖率门槛.** `vitest.config.ts` 无 `coverage` 配置块、无 `thresholds`，`package.json` 也没有 `coverage` 脚本 —— 与 `web/AGENTS.md` §3.14 的 80% 目标无对应机制。
- **CI 只跑了 typecheck 与 test.** `ci.yml:60-88` 的 frontend job 只有 `bun install --frozen-lockfile`、`bun run typecheck`、`bun run test`：**`bun run lint`、`bun run format:check`、生产构建都不在 CI 中**（backend job 还会用 `ci.yml:38-42` 的空 `web/dist/index.html` 顶替前端，前端只在 `Dockerfile` 发布时才真正构建）。后果是：本报告中的全部前端发现对 CI 不可见，lint error 可以直接合入。
- **建议**：把 `bun run lint`、`bun run format:check`、`bun run build` 与产物体积预算加入 frontend job；为 `vitest.config.ts` 增加 `coverage.thresholds`；把 `knip.config.ts:5` 的 `ignore: ['src/components/ui/**']` 收窄为显式白名单，使死原语可被检出。

---

## 6. 已做得好的部分（不要动）

这些是本仓库明显优于同类项目的部分，重构时应作为范式而非重构对象：

1. **模块边界干净**：`relay/` 不 import `controller/`（0 处）；`relaykit/` 不依赖主模块（0 处，且 `GOWORK=off go build ./...` 通过 ✅）；`pkg/jsplugin/` 仅在 `registry.go:25` 接触宿主。
2. **行锁完全符合约定**：GORM v1 的 `gorm:query_option` 在仓库中只出现在文档注释里，`clause.Locking` 只出现在 `model/locking.go:24 lockForUpdate` 内部（20+ 个模型文件复用），AGENTS.md 的警告已被彻底落实。
3. **原始 SQL 都有方言分支与回退**：`model/db_time.go:10-17`、`model/token_migration.go:133`、`model/user.go:399,404`、`model/ability.go:366`、`model/audit_log.go:247`。
4. **`relay/helper/stream_scanner.go` 是流式处理的正确实现**：缓冲 `stopChan`（关闭而非发送）、对 `ctx.Done()`/`c.Request.Context().Done()` 的 select、`cleanupOnce`/`stopOnce`、`ExtendWriteDeadline` 保护无条件 `wg.Wait()`。其它流式路径应以它为准。
5. **认证与会话设计扎实**：argon2id（m=19456, t=2）+ 恒定时间比较（`common/account_password.go:85`）；每次登录生成新 SID（`service/auth_session.go:111`，无会话固定）；refresh 轮换 + HMAC 摘要 + 30 秒重放窗口 + 复用检测；按用途派生 JWT 密钥（`service/auth_token.go`）；单次、1 分钟、绑定上下文与范围的 step-up 凭证（`middleware/secure_verification.go`）。
6. **授权**：Casbin 解析器 deny-beats-allow + 已知权限白名单 + root 短路（`service/authz/resolver.go:10-35`）；`ManageUser`（`controller/user.go:1053-1182`）实现了角色层级、禁止删除/降级 root、降级时撤销会话并审计。
7. **注入与 SSRF 防护到位**：未发现字符串拼接的原始 SQL（排序列白名单、参数化查询）；SSRF 默认开启，含拨号时与每跳重定向复验（`service/http_client.go`、`service/protected_fetch_client.go`、`common/url_validator.go`）。
8. **`pkg/jsplugin` 的并发模型**：`sync.RWMutex` + `atomic.Bool`/`atomic.Pointer[RoutingGeneration]` 的代际切换（`registry.go:232-238`），是 P2-2 应复制的模板。
9. **请求体磁盘分级缓存**（`common/body_storage.go`）：内存/磁盘阈值、`NewReader()` 零拷贝重放、`Close` 即删、启动清理、请求结束释放（`middleware/body_cleanup.go`）。设计正确，只有 `middleware/distributor.go:255-259` 一处抵消了它。
10. **已有可复用的有界组件**：`pkg/billingexpr/compile.go:122-123` 表达式程序按 SHA-256 缓存且有界；`pkg/perf_metrics/flush.go` 批量落库；审计响应体上限 64 KB（`middleware/audit.go:116,244,286`）；`StreamStatus.Errors` 上限 20；WebSocket 设置 `SetReadLimit`。
11. **HTTP 客户端按 `(proxy, policy)` 缓存复用**（`service/http_client.go:153,209-259`），带 `ForceAttemptHTTP2` 与有界响应头超时，无逐请求构造。
12. **`pkg/cachex`** 已提供 Redis + 有界 LRU 的混合缓存，可作为 P1-1 / P1-2 / 定价缓存的统一落点。

前端同样有多处值得保留的设计：

13. **集中式错误处理**：`createAppQueryClient` 用 `QueryCache`/`MutationCache` 的 `onError` 统一提示，支持 `meta.errorToast: false` 关闭，500 跳 `/500`（`web/src/lib/query-client.ts:50-63`）；`handleServerError`/`createServerError`/`requireServerSuccess` 分别在 114/112/58 个文件中使用；`toast.error('字面量')` 为 **0** 处。
14. **精确的缓存失效**：101 处 `invalidateQueries` 中**没有一处省略 key**；最宽的前缀失效都是有意的（`channels-provider.tsx:98`、`common-logs-filter-bar.tsx:225`）；`routes/__root.tsx:63-71` 只在会话 ID 变化时清空整个缓存，语义正确。
15. **表格工程化**：`data-table/core/data-table-row.tsx:91` 使用带自定义比较器的 `React.memo`，关键列定义已 `useMemo`（`channels-columns.tsx:631`、`common-logs-columns.tsx:895`、`users-columns.tsx:301`），配套 README 与 75 个使用方。
16. **基础组件复用被真正执行**（83 处 `@/components/dialog` vs 3 处直接 `ui/dialog`、46 处 `ConfirmDialog`），以及**自定义 `Intl` locale lint 规则**（`project/intl-locale`，`error` 级别，且带自己的测试）—— 这种投入在同类项目中少见。
17. **图标按需加载的正确示范**：`web/src/lib/lobe-icon.tsx:165` 用 `lazy()` + `webpackInclude` 过滤动态导入单个图标变体并提供兜底。
18. **日期栈单一**：仅 Day.js、仅一个插件（`web/src/lib/dayjs.ts:20`），无 `moment`，无重复日期库。
19. **路由搜索参数用 Zod 校验**（`routes/_authenticated/channels/index.tsx:29-38`，带 `.catch()` 兜底）；4 个 Zustand store 都使用 `persist` + `partialize`，不会误持久化整份状态。
20. **i18n 键完整性 0 缺失 / 0 多余**，由 `sync-i18n.mjs` 与 822 行的 `static-keys.ts` 注册表支撑。
21. **无硬编码密钥、生产路径无 `console.log`、AGPL 头由脚本强制**（`web/scripts/add-copyright.mjs`）。

运维与工程契约方面同样有值得保留的部分：

22. **供应链卫生到位**：两个 Docker 阶段均按 digest 固定、`cosign sign`、`sbom: true`、`provenance: mode=max`、发布产物附校验和，且所有第三方 GitHub Action 都按完整 SHA 固定并带版本注释。
23. **请求关联链路完整**：`middleware/request-id.go` 同时写入 `c.Set`、上下文值与 `X-Oneapi-Request-Id` 响应头，并被每请求 GIN 日志与 `logger.logHelper` 采用；错误响应也带同一 ID。日志脱敏也有正确示范：`middleware/logger.go:34-36` 会剥离 OAuth 回调的查询串，`common/str.go:20-25 LocalLogPreview` 会截断错误详情，`middleware/audit.go` 的审计事件有意排除凭据（符合 AGENTS.md）。
24. **多实例能力真实存在**：系统任务使用 DB 租约去重并保留运行历史、Casbin 策略只有一个同步循环、每进程心跳（`service.StartSystemInstanceReporter` / `model.SystemInstance`）。
25. **ClickHouse 日志库支持 TTL 保留**（`model/main.go:405-434`，`LOG_SQL_CLICKHOUSE_TTL_DAYS`），以及**可选的 Pyroscope 连续性能分析**（`common/pyro.go:11-39`）—— 后者在同类项目中很少见，应作为 P1-1/P1-3 性能复核的直接工具。
26. **`AGENTS.md` 本身是一流的工程契约**：强制三库验证、计费读取门禁、OWASP ASVS 引用，并明确禁止为覆盖率填测试。它的**要求远超实际执行力度** —— 本报告多数缺口正是"规范已写、闸门未接"，因此补齐 CI（P2-5）的收益比新增规范更大。

---

## 7. 建议路线图

### 阶段一：安全与正确性（建议尽快单独发版）

1. ~~P0-1 `/api/setup` 加一次性 setup token 或限制回环；顺带限制 `GET /api/setup`~~ 🛠️ 已修复（`91f20bae3`；`GET /api/setup` 有意保留开放，见该节残余风险）
2. ~~P0-2 修正 MJ 路由中间件顺序 + 补归属校验~~ 🛠️ 已修复（`38ddfde30`；改为签名能力 URL，**不是**移动中间件顺序，原因见该节建议 1）
3. P0-3 ~~验证码恒定时间比较 + 失败作废 + 账户级限额~~ 🛠️ 已修复（`927ab209e`、`826ee1357`）；~~登录 miss 路径跑一次 dummy argon2id~~ 🛠️ 已修复（`6ec801848`）；~~`TrustedProxies` 默认置空~~ 🚧 部分修复（`d3c953c7a`，**未改默认值**，改为把代价写进告警；残留见该节）；~~重置密码不回显~~ ❌ 未修复（需前后端联动，见该节 ④）
4. ~~P0-4 两个 token key 接口接入 step-up 验证~~ 🛠️ 已修复（`c62f10985`、`5447458ce`；按"两个接口都加"执行，证明绑定 id 集合、限流改按用户；**PAT 读 key 变为 403**、聊天页与仪表盘新增验证弹窗，见该节）
5. ~~P0-5 补齐 `c.Request.Context()`（含各渠道站点）~~ 🛠️ 已修复（`426a65d0a`；在构造点绑定而非改写 `doRequest`，额外发现并修复 MJ 提交的 `context.Background()` 超时；ollama/baidu/jsplugin helper 与部分非中继调用方见该节残余）
6. P0-6 修正 `recover` 返回值与计费写入错误检查
7. P0-7 与维护者确认任务分组倍率参数；统一 `gpt-image-1` 默认质量
8. P0-8 移除 zhipu 密钥明文日志；日志脱敏改为按参数名白名单（`key`/`api_key`/`token`/`secret`）
9. 顺手：挂载或删除上传/下载限流；Redis 预扣脚本加 deadline；CORS 与安全响应头；关闭时刷新批更新缓冲区

### 阶段二：可用性与性能

10. P2-5 先补 CI 闸门，**其中三库矩阵 job 优先** —— 48 个已写好的方言用例只差连接串即可转为真实校验
11. P1-6 前端首屏：语言包按需 `import()`（可减掉约 3 MB）、`lazy()` 落地页的 Markdown/KaTeX/图标、按需拆分弹窗
12. P1-2 渠道缓存失败保护 + 快照交换（服务端收益最高、风险中）
13. P1-1 无 Redis 路径的进程内缓存；DB 连接池参数修正
14. P1-3 流式路径：降低 scanner 上限、`Buffered` 加上限、前缀读 body、单次解码
15. P1-7 前端渲染与请求：表格 `columns` 记忆化、Context value 记忆化、全局 `staleTime`（先修 `manualFiltering` 一行问题）
16. P2-6 健康检查 + Prometheus 指标 + pprof 收口 + 日志级别与有界轮转
17. P1-5 启动路径：回填加标记、`AUTO_MIGRATE_ON_BOOT` 开关、去重读取
18. P2-9 补鉴权/渠道路由/结算三条主干的用例（先 `TokenAuth`+`ValidateUserToken`，再 `GetRandomSatisfiedChannel`）
19. P2-10 schema 版本表 + 启动校验汇总；P2-12 先写"备份恢复"与"升级回滚"两篇

### 阶段三：可维护性债

20. P2-2 配置系统收敛（先多字类型 → 再删 `_old.go`）
21. P2-1 `channel.BaseAdaptor`（约 1,500 行）+ relay handler 尾部抽取（覆盖 11 个文件的 36 处 `statusCodeMappingStr` 样板）
22. P2-3 拆分 `ManageMultiKeys` / `testChannel` / `FetchUpstreamRatios`；下线 `applyOperationsLegacy`
23. P2-4 响应封套统一（先 `controller/channel.go`）
24. 前端结构治理：拆分 `channel-mutate-drawer.tsx`、两个删除弹窗改用 `ConfirmDialog`、抽取 `useEntityFormDrawer`、启用 `jsx-a11y` 与覆盖率门槛、i18n 键名 codemod
25. P2-7/P2-8 一致性卫生批量处理（死代码、`encoding/json` 包装、日志统一）
26. P2-11 版本注入统一为单一来源；P2-9 适配器 usage 提取表测试（每文件覆盖多个适配器）

---

## 附录 A：复现命令

```bash
# 构建与静态检查
go build ./... && go vet ./...
cd relaykit && GOWORK=off go build ./... && GOWORK=off go test ./...

# 测试与竞态
make test
go test -race ./common/... ./setting/... ./service/authz/... ./model/...

# 覆盖率（P2-5 / P2-9 的数字来源）
go test -cover ./...                                   # 总体约 38.5%（按包归属口径）

# 覆盖率是按包归属的，跨包调用不计入被调用包 —— 下面两条命令可验证这一点：
go test -cover ./middleware/                                                     # → 57.4%
go test -coverpkg=github.com/QuantumNous/new-api/middleware -cover \
  -run 'TestResponsesWS|TestResponsesWebSocket' ./controller/                    # → middleware 13.5%

# 因此判断真实盲区应看"是否有任何测试引用"，而不是 cover 百分比：
for fn in ValidateUserToken PostConsumeQuota ModelPriceHelperPerCall CacheGetChannel \
          getPriority getChannelQuery FixAbility EnableChannel RelayMidjourney; do
  grep -rq "\b$fn\b" --include='*_test.go' . || echo "零测试引用: $fn"; done

for p in $(go list ./... | sed 's|github.com/QuantumNous/new-api/||'); do \
  go test -cover ./$p 2>/dev/null | grep -q 'no test files' && echo "NO TESTS: $p"; done   # 40 个包无测试

# CI 闸门缺失的核实
grep -rn 'TEST_MYSQL_DSN\|TEST_POSTGRES_DSN\|golangci\|-race\|coverprofile\|govulncheck\|bun run lint' .github/workflows/   # 无命中
grep -rln 'TEST_MYSQL_DSN\|TEST_POSTGRES_DSN' --include='*_test.go' . | wc -l                     # 14 个文件、48 处 t.Skip

# 迁移与发布（P2-10 / P2-11）
grep -rn 'schema_migrations\|schema_version' --include='*.go' .        # 无 DB schema 版本表
wc -c VERSION                                                          # 0（空文件）
grep -n 'GOEXPERIMENT\|CGO_ENABLED' Dockerfile                         # greenteagc / CGO_ENABLED=0

# 规模与热点
find . -path ./web -prune -o -name '*.go' -print0 | xargs -0 wc -l | sort -rn | head -30
git log --since="12 months ago" --name-only --pretty=format: | grep -E '\.go$' | sort | uniq -c | sort -rn | head -25

# 一致性
grep -rn 'json\.Marshal(\|json\.Unmarshal(' --include='*.go' . | grep -v _test.go | wc -l
grep -rn 'panic("implement me")' --include='*.go' .
grep -rn 'UploadRateLimit\|DownloadRateLimit' --include='*.go' .

# 前端（需先安装 bun）
cd web && bun install --frozen-lockfile && bun run typecheck && bun run lint && bun run test
```

## 附录 B：待确认项

以下结论在落地前必须先复现，不要直接据以改动：

1. **P0-7A 的语义**：`service/task_billing.go:408` 传两个相同参数，属笔误还是刻意？需维护者确认正确语义（对照 `relay/helper/price.go:42-69` 与 `service/quota.go:106-120` 两种写法，后者使用类型化常量 `constant.ContextKeyAutoGroup`，前者用字面量 `"auto_group"`）。
2. **P1-1/P1-2/P1-3 的性能量级**：全部为静态分析结论，未经 profiler 实测。建议先在生产可观测的实例上采一次 CPU/heap profile 与 DB 查询计数，再按实际占比排序。
3. **定价缓存的竞态**：`model/pricing.go:80,90` 在锁外读写 `pricingMap` / `lastGetPricingTime`（写方 `:97,327,429,447`）。本次 `-race` 未触发，因为现有用例未覆盖该路径；**需要专门的并发用例确认**后再修。
4. **渠道状态就地修改的竞态**：`model/channel_cache.go:258` 在写锁内改 `channel.Status`，读者 `service/channel_select.go:296`、`relay/responses_websocket.go:537`、`relay/mjproxy_handler.go:317` 无锁读取。同样需要专门用例确认。
5. **128 MB scanner 上限的实际可达性**：触发条件是上游单行长度，未验证真实渠道是否可能返回如此长的单行。
6. **第三方流程细节**：WeChat 登录是否已在下游强制 `code` 单次使用；`connect.linux.do` 是否校验 `redirect_uri`。均未验证。
7. **前端既有问题清单**（P1-6、P1-7 与第 5 节）来自静态审计，未在运行中的应用上验证渲染次数与请求次数；建议用 React DevTools Profiler 与 Network 面板确认后再优化。
8. **前端体积数字来自源码统计，不是构建产物.** 本地无 `bun` 与 `web/node_modules`，`web/dist` 只有 CI 占位文件，因此 3.51 MB 等数字是 minified 估算值而非实测 bundle。执行 `bun run build` 后用产物复核，再定优先级。
9. **`lucide-react` / `@hugeicons/core-free-icons` 的 barrel 摇树效果未验证**：两者都声明支持 ESM tree-shaking，需在真实构建上确认后再决定是否逐个具名导入。
10. **`@lobehub/ui` 及其 60+ 传递依赖是否进入产物未验证**：已确认 `src/` 对其零引用、且 `bun.lock` 完整解析了它，但它在构建产物中的实际占用未测量（预期仅影响安装与 CI 时间）。
11. **用量日志查询键中的 `t` 是否是死内容未验证**：`usage-logs-table.tsx:186` 把 `t` 放进 key，结论"React Query 会把函数哈希掉、因此切语言不会重查"依赖 v5 内部行为，无 `node_modules` 无法核实。
12. **`use-status.ts` 每次渲染 `readCachedStatus()` 的开销未测量**：日志与 status blob 的实际体积未知，需实测后再判断是否值得优先处理。

以下为第二轮（测试 / CI / 运维维度）新增的待确认项：

13. **批更新缓冲区丢失的实际影响面**：已确认关闭路径不调用 `batchUpdate`（`main.go:229-245` 与未导出的 `model/utils.go:65`），但**未确认** `DataExportEnabled` 分支（`main.go:243` 附近的配额数据导出）是否已覆盖同一批数据，也未确认 `BATCH_UPDATE_ENABLED` 的默认值。若默认关闭，则该项影响仅限显式开启的部署。落地前先确认这两点。
14. **P0-8 的真实触发频率**：日志分支只在密钥不含恰好一个 `.` 时命中。需要确认线上是否出现过该分支（grep 日志中的 `invalid zhipu key`），以判断它是"理论隐患"还是"已经发生过"。
15. **覆盖率数字有明确时点与口径**：38.5% 与分档表是 2026-09-22 在 main 特定提交上 `go test -cover ./...` 的结果，**且为按包归属口径**（跨包调用不计入被调用包，已实测验证）。P2-9 的函数级结论我已改用"是否有测试引用"重新复核；若要用覆盖率本身衡量，需改用 `-coverpkg=./...` 重跑，届时各包数字会显著变化，不要把两套口径混用。引用前请重跑附录 A 的命令。
16. **`GOEXPERIMENT=greenteagc` 的行为差异未实测**（`Dockerfile:16`）：发布镜像与本地 `go build` 因此可能使用不同 GC 行为，未做 A/B 对比。判断是否移除前应先确认该实验标志当前是否仍是默认值。
17. **`migrateOptionPrimaryKey` 在真实大库上的失败概率未验证**（`model/main.go:333-335`）：只确认了失败会降级为告警并继续启动，未验证什么样的表规模/方言组合会真正触发。
