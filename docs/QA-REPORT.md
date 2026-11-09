# s-library QA 报告（M1）

> 文档版本 v1.0 · 2026-11-09 · 角色：QA 测试工程师 · 计划见 docs/QA-PLAN.md
> 结论均基于真实命令输出或代码引用（文件:行号）；本次只报告不修复。

---

## 1. 总体结论：**有条件发布（可发布 M1，附 1 项发布条件）**

M1 后端（Go）与前端（React）实现质量良好：构建/静态检查/全部自动化测试通过；11 个契约端点字段级核对一致；迁移 47 在全新库成功应用；权限三档与 401/403 行为运行时验证正确；41 个专项单测覆盖加密、防御解析、failover、流水线状态机、去重与队列并发。

**发布条件（1 项，P1）**：修复 DEF-1——前端设置面板调用 `GET /api/zlibrary/settings`，后端未注册该路由（运行时确认 404）。修复前，设置面板的并发数/默认书库/权限档无法回显（永远显示默认值），且再次点击「保存」会用默认值**静默覆盖**管理员已保存的配置（含权限档——把已放宽的 `all` 档改回 `admin` 档）。
**限制说明**：真实 Z-Library 账号联调（US-M1-14/15/16 的端到端部分）本轮无法执行，用例清单见 §6——这是 M1 验收口径（"真实账号全流程"）的必要补充，应在发布前由用户完成冒烟。

---

## 2. 缺陷清单

| 编号 | 严重级 | 描述 | 位置 | 复现方式 | 修复建议 |
| --- | --- | --- | --- | --- | --- |
| DEF-1 | **P1 重要** | 前端调用 `GET /api/zlibrary/settings` 读取配置回显，后端只注册 `PUT`，运行时确认 404（`{"error":"endpoint not found"}`）。降级路径仅从 `/status` 恢复域名列表，`behavior`（concurrency/defaultLibraryId/allowScope）回退默认值 `{1, "", "admin"}`；管理员随后点「保存」将以默认值覆盖真实配置（`PUT /settings` 对 behavior 整体覆盖） | 前端 `frontend/src/api/zlibrary.ts:233-236`、`frontend/src/components/ZLibrarySettingsPanel.tsx:107-142`（catch 降级）、`223-226`（保存整体提交）；后端 `internal/handler/routes_zlibrary.go:45`（仅 PUT） | ① 以管理员打开 设置→Z-Library，DevTools Network 可见 `GET /api/zlibrary/settings` → 404；② 先 `PUT` 保存 `allowScope:"all"`，刷新面板，单选显示「仅管理员」，点保存后 `/status` 的 `allowScope` 变回 `admin` | 后端补 `GET /settings`（admin，返回 domains+behavior 快照）；或前端改为从 `/status` 携带 behavior 回显（status 已有 `allowScope`，可加 `concurrency/defaultLibraryId`） |
| DEF-2 | P2 一般 | 后端错误码全集 17 个（`internal/zlibrary/models.go:17-34`），前端映射表仅显式覆盖 12 个，`Z_LIB_NOT_FOUND`/`Z_RATE_LIMITED`/`Z_UPSTREAM`/`Z_FORBIDDEN`/`Z_INTERRUPTED` 落入 default 分支直接展示后端中文 `error` 消息（`format.ts:92-93`） | `frontend/src/components/zlibrary/format.ts:58-93`、`frontend/src/api/zlibrary.ts:137-152` | 对无权限用户触发 403（已验证返回 `{"code":"Z_FORBIDDEN","error":"无权使用 Z-Library 功能"}`）——en 界面将显示中文 | 前端补 5 条 i18n 文案（zh/en）；后端 message 已是人话故不致裸码，但 US-M1-15 要求 en 翻译 |
| DEF-3 | P2 一般 | 「清空历史」只清当前加载的 ≤50 条任务（`fetchTasks` 固定 `limit:50`），FR-5.4/US-M1-06 要求可清空最近 200 条历史；且无聚合端点，逐条 DELETE 在 200 条时需 4 轮 | `frontend/src/hooks/useZLibraryTasks.ts:202-213`（clearFinished 基于 state.tasks 过滤）、`:56`（limit:50） | 积累 >50 条终态任务后点「清空历史」，仅删除可见部分 | 后端补 `DELETE /downloads?scope=finished` 聚合端点；或前端循环按页清除 |
| DEF-4 | P2 一般 | `npm run build` 不含 dist 拷贝步骤，`web/dist` 初始只有 `.gitkeep`（实测为空）；embed 有空目录防御（降级 dev 模式，不崩溃但二进制不带前端）。仅 `npm run build:all`（package.json:9）或手动 `node scripts/copy-dist.mjs` 后产物才完整 | `frontend/package.json:8-9,19`、`frontend/scripts/copy-dist.mjs`、`web/embed.go:30-43` | 全新 clone 后按 `cd frontend && npm run build && cd .. && go build` 顺序出包 → 运行时所有页面 404（无前端） | QA 已手动 copy-dist 闭环验证（§3-A6）。建议团队约定发布必须用 `npm run build:all`，或在 Makefile/CI 强制 |
| DEF-5 | P3 建议 | 配额 `resetAt` 字段永不填充（`RefreshQuota`/`SetQuota` 均不写 ResetAt），前端「将于每日 {time} 重置」插值显示为空字符串 | `internal/zlibrary/service/quota.go:70-77`、`frontend/src/components/zlibrary/format.ts:36-44` | 登录后查看搜索页配额文案 | 适配层从 profile 解析重置时间；或文案去掉 {time} 段 |
| DEF-6 | P3 建议 | 重试非 failed 任务返回 HTTP 500 `Z_UPSTREAM`，架构契约 §4(9) 约定 completed → 409；前端 TaskCard 对 completed 不渲染重试按钮，用户不可达，纯文档级偏差 | `internal/handler/zlibrary_handler.go:657-660` vs `docs/ARCHITECTURE.md` §4(9) | `curl -X POST .../downloads/:id/retry`（completed 任务） | 改为 409 + `Z_DUP_*` 类码，或修订架构文档 |

未发现 P0 阻断缺陷。前端自报 6 处契约假设核实结果见 §4.3。

---

## 3. 测试执行记录（真实命令与结果）

### A. 构建与静态检查

| # | 命令 | 结果 |
| --- | --- | --- |
| A1 | `GOPROXY=https://goproxy.cn,direct go build ./...` | ✅ exit 0 |
| A2 | `GOPROXY=https://goproxy.cn,direct go vet ./...` | ✅ exit 0，无输出 |
| A3 | `GOPROXY=https://goproxy.cn,direct go test ./... -count=1` | ✅ exit 0；12 个包 ok（含 `internal/zlibrary` 1.23s、`internal/zlibrary/service` 1.94s、`internal/store` 5.09s、`internal/handler` 10.48s） |
| A4 | `cd frontend && npm run build` | ✅ exit 0（tsc -b 类型检查 + vite build 1.40s） |
| A5 | `cd frontend && npm run lint` | ✅ exit 0（eslint + 附带 test-tag-colors / test-tag-navigation 脚本通过） |
| A6 | 产物链：实测 `web/dist` 初始仅 `.gitkeep`；手动 `node frontend/scripts/copy-dist.mjs` → `web/dist` 填充（index.html/assets/icons…）→ 复跑 `go build ./...` ✅，embed 链路闭环；ZLibrarySettingsPanel 等 chunk 确认存在于 `frontend/dist/assets/` | ✅（触发方式：`npm run build:all` / `npm run copy-dist`，见缺陷 DEF-4） |

### B. 前后端契约逐字段核对（静态）

核对基准：`internal/handler/zlibrary_handler.go` 请求绑定结构体 ↔ `frontend/src/api/zlibrary.ts` 类型。

| # | 端点 | 请求字段核对 | 响应字段核对 | 结论 |
| --- | --- | --- | --- | --- |
| 1 | GET /status | — | `{connected,accountName,accountType?,quota{used,total,resetAt},currentDomain,personalDomain,domains[]{url,enabled,isDefault,healthy,lastCheckedAt,latencyMs},allowScope,canUse}` ↔ 前端 `ZLibraryStatus`（全可选字段）+ `canUse` 前端防御字段后端确实返回（handler:142） | ✅ 一致（运行时 T5/T9 复核） |
| 2a | PUT /settings | `domains[]{url,enabled,isDefault}`（settings.go:62-66 JSON tag）+ `behavior{concurrency,defaultLibraryId,allowScope}` ↔ 前端 `ZSettingsPayload` | `{"success":true}` | ✅ 一致（运行时 T6/T13 复核持久化） |
| 2b | **GET /settings** | 前端调用（zlibrary.ts:233） | 后端无路由（routes_zlibrary.go:45 仅 PUT；运行时 404） | ❌ **DEF-1** |
| 3 | POST /auth/test | `email/password` 或 `remixUserid/remixUserkey`（handler:217-221）↔ 前端 `creds`（zlibrary.ts:258）——remix 令牌字段名**逐字符一致** | `{connected,accountName,accountType,quota{used,total}}` ↔ `ZAuthTestResult` | ✅ 一致（前端假设 6 成立） |
| 4 | DELETE /auth | — | `{"success":true}` | ✅ 一致 |
| 5 | POST /search | `message/languages[]/extensions[]/yearFrom/yearTo/order/page/limit`（handler:316-324）↔ `ZSearchRequest`；order 枚举 bestmatch/newest/rating 两端一致 | `{total,page,limit,books[]}`，books 为 `ZBook`(zbookId/hash/title/subtitle/author/publisher/year/language/ext/fileSize/rating/coverUrl)+`dedupStatus`+`localBookId` ↔ `ZSearchResponse`/`ZBook`（模型 `internal/zlibrary/models.go:71-88` JSON tag 逐一比对，`year/rating` 后端指针→null 兼容） | ✅ 一致 |
| 6 | GET /book/:id/:hash | 路径参数 id/hash | `{"book":{...上述字段,description,formats[]{ext,fileSize},dlDomain}}` ↔ `ZBookDetail` | ✅ 一致 |
| 7 | POST /downloads | `zbookId/hash/ext?/libraryId?`（handler:473-478）↔ 前端 `createZDownload` req | 202 `{"task":{...}}` | ✅ 一致 |
| 8 | GET /downloads | query `status/page/limit`（handler:588-591）↔ `fetchZDownloads` | `{tasks[],total}`；task 19 字段 JSON tag（`internal/model/zlibrary.go:26-51`）↔ `ZTask`（zlibrary.ts:91-113）**逐一比对一致**，admin 视图 `Username json:"user,omitempty"` ↔ 前端 `user?` | ✅ 一致 |
| 9 | POST /downloads/:id/retry | — | 202 `{"task"}` | ✅ 一致（文档级偏差 DEF-6：非 failed → 500 而非 409） |
| 10 | DELETE /downloads/:id | — | `{"success":true}` | ✅ 一致 |
| 11 | POST /domains/health-check | — | `{results[]{url,healthy,latencyMs,error}}`（client.go:217-220）↔ `ZHealthCheckResult`（zlibrary.ts:329-334） | ✅ 一致 |

**错误码核对**：后端 17 码（models.go:17-34）vs 前端显式映射 12 码 → **5 码缺口记 DEF-2**；HTTP 状态映射（handler:22-46）与契约 §4 约定一致（Z_AUTH_FAILED→422、配额/重复→409、域名类→502 等）。

### C. 迁移与权限运行时验证

环境：`DATA_DIR=$(mktemp -d)`、`PORT=6799`、临时库全新迁移；验证后进程已停止、临时目录与项目内测试产生的 `data/`（DB 实际落盘于 cwd/data，见 `internal/config/config.go:573-579`）均已清理。

| # | 验证项 | 结果 |
| --- | --- | --- |
| C1 | 迁移：日志 `[Migrate] Applying migration 47: Add Z-Library download task... → Applied successfully`；`_migrations MAX(version)=47`；`ZlibraryDownloadTask/ZlibraryImportedBook/ZlibrarySetting` 三表创建 | ✅（另确认迁移注释所述 37~46 确实存在，47 编号正确无冲突） |
| C2 | 未认证 `GET /api/zlibrary/status` → `401 {"error":"Unauthorized"}` | ✅ |
| C3 | 未配置凭据时管理员 `GET /status` → 200：`{"connected":false,"accountName":"","currentDomain":"https://z-lib.sk","domains":[5 个预置域名],"personalDomain":"","quota":{"used":0,"total":0,"resetAt":""},"allowScope":"admin","canUse":true}` —— 与 `useZLibraryStatus`/`quotaViewOf`（total<=0 返回 null 不渲染）**解析兼容** | ✅ |
| C4 | 未认证 `GET /api/zlibrary/settings` → `404 {"error":"endpoint not found"}` | ❌ 实锤 DEF-1 |
| C5 | `POST /auth/test` 无凭据（管理员）路径存在；普通用户访问 → `403 {"error":"Forbidden"}`（配置端点恒 admin） | ✅ |
| C6 | 管理员 `PUT /settings`（concurrency=2 + allowScope=all + 1 域名）→ 200；DB 复核 `behavior/domains` 行持久化正确 | ✅ |
| C7 | 普通用户 `PUT /settings` → `403 {"error":"Forbidden"}` | ✅ |
| C8 | 权限三档运行时切换：`all` 档普通用户 `/status` 200（`canUse:true`）→ 管理员改回 `admin` 档 → 同用户 `/status` `403 {"code":"Z_FORBIDDEN","error":"无权使用 Z-Library 功能"}` | ✅ |
| C9 | 未连接凭据时创建任务 → `401 {"code":"Z_AUTH_REQUIRED","error":"Z-Library 未连接，请先在设置中登录"}`（与需求 §7 矩阵文案一致） | ✅ |
| C10 | `POST /search` 空关键词/非法 order → `400 {"code":"Z_UPSTREAM",...人话}` | ✅ |
| C11 | `DATA_DIR/zlib-secret.key` 自动生成（FR-1.3，权限 0600） | ✅ |
| C12 | 任务列表空态 `{"tasks":[],"total":0}` | ✅ |

### D. 需求覆盖走查（PM-PLAN 16 条故事）

| 故事 | 后端证据（文件:行） | 前端证据（文件:行） | 缺口 |
| --- | --- | --- | --- |
| M1-01 凭据管理 | routes_zlibrary.go:46-47；zlibrary_handler.go:215-287（三种凭据路径）；settings.go:305-329（仅存令牌，AES-GCM）；crypto_test.go×4 | ZLibrarySettingsPanel.tsx:88-97/257-281（双模式）、372-430（密码不回显+禁用占位）、722-762（解绑二次确认） | 真实登录/令牌失效标红 → 联调 |
| M1-02 域名与 failover | settings.go:220-258（顺序即优先级/去重/至少一启用）；client.go failover（search_test.go:355-426 网络错误/5xx/全不可达）；handler:724-732 | ZLibrarySettingsPanel.tsx:164-198（增删改/↑↓/默认）、200-217（健康检查）、591-600（当前/个人域名）、668-705（blackbox 指引） | 真实域名宕机切换 → 联调 |
| M1-03 搜索/详情 | handler:314-370（校验/默认值/limit≤100）、377-404（详情+formats+去重）；search_test.go×11 | page.tsx:98-133（搜索）、215（加载更多）；SearchFilterBar | 真实上游 → 联调 |
| M1-04 前置校验 | handler:489-544 六步顺序（登录态→配额→书库→canManage→扩展名→去重） | page.tsx:146-161（前端预检 connected/exhausted） | 配额语义 → 联调；C9 已验证登录态步 |
| M1-05 流水线 | pipeline.go 全链（fetch_info→downloading→validating→moving→scanning；checkDiskSpace :407-424；sanitizeFilename :471-497；SyncLibraryByID 退避 :317-348）；pipeline_test.go×8 + queue 测试×3（并发=1/取消） | TaskCard 进度/阶段渲染 | 真实下载 → 联调 |
| M1-06 任务 API | handler:582-618（WHERE 注入 :593、admin 填 user :601-616）、652-693（仅 failed 可重试 :657）、696-717；store_test.go 状态机/裁剪/中断 | useZLibraryTasks（retry/cancelOrRemove/clearFinished）、TaskCard（retryBlocked :53-55） | 200 条裁剪真实触发 → 联调 |
| M1-07 配额 | quota.go 全部；handler:103-107、279-285（test 后写缓存）；profile donor/free 映射单测 search_test.go:273 | QuotaBadge.tsx；format.ts:245-258 | 真实字段映射 → 联调 |
| M1-08 权限档 | routes_zlibrary.go:16-32（中间件）、44-48（配置端点恒 admin）；settings.go:395-424（三档判定） | main.tsx:79-97（路由守卫）、DesktopSidebar.tsx:41/66、DashboardTopBar.tsx:52 | ✅ 运行时全验证（C5-C8） |
| M1-09 设置页 | — | ZLibrarySettingsPanel 五分组齐全（连接/域名/行为/权限/兜底） | **DEF-1**（回显） |
| M1-10 搜索页 | — | page.tsx（历史 10 条 :78/90-96、双视图、配额条 :259）；format.ts:149-178（内置筛选，偏差已声明）；入口可见性 DesktopSidebar:41 | 真实搜索 → 联调 |
| M1-11 详情与一键入库 | handler:468-564（含 202） | ZBookDetailDrawer（FormatSelector/LibrarySelector/pickDefaultExt）；page.tsx:146-188 快速入库无打断弹窗 | 真实入库 → 联调 |
| M1-12 任务中心 UI | — | TaskCenterDrawer（2s 轮询 :34-41、完成 toast :43-53）；TaskCard（他人任务标识 :143-145、scanFailed 手动扫描 :182） | 轮询仅抽屉打开时运行（设计自洽，角标关闭时靠乐观更新+挂载拉取） |
| M1-13 配额展示 | QuotaPrecheck（quota.go:84-97） | QuotaBadge 搜索页 header + 设置页复用；配额 0 前端拦截 page.tsx:152-161 | 真实数值 → 联调 |
| M1-14 全链路 | 流水线代码齐备 | 全部页面/交互齐备 | ⛔ 整条需真实账号 |
| M1-15 错误文案 | 17 码结构化 + 人话 msg（如 Z_INTERRUPTED「进程重启导致下载中断，可重试」store:337-340） | 12 码 i18n 映射（zh/en 双语抽查一致）+ err.message 兜底 | **DEF-2**（5 码缺专属 en 文案）；真实触发 → 联调 |
| M1-16 去重与重试 | 唯一索引（migrate.go:548）；store:409 ClearZlibraryImportedBookLocal（删除后允许再入库）；pipeline_test.go:418 stale mapping；store_test.go:242 | dedupStatus 三态渲染 + imported 跳转（ZBookCard.tsx:35-39/124-126/179-181、TaskCard:205） | 端到端 → 联调 |

**结论：16 条故事全部有代码级覆盖证据，无缺失故事；3 条依赖真实账号联调收尾。**

### D2. `// s-library:` 标注抽查与改动范围

- 后端 3 处修改文件全部有标注：`internal/handler/router.go:59`、`cmd/server/main.go:21,109`、`internal/store/migrate.go:506-509`（并如实注明为何取 47：上游已注册 37~46）。
- 前端 6 处修改文件全部有标注：`main.tsx:23,49,79,147,187`、`DesktopSidebar.tsx:17,21,41,66,108`、`DashboardTopBar.tsx:14,24,52,99,110`、`app/settings/page.tsx:11,95,106,212,282`、`locales/zh-CN.ts:1371`、`locales/en.ts:1374`。
- `git status --porcelain` + `git diff --stat HEAD`（9 个修改文件 +738/-2 行）与开发汇报清单**完全一致，无越界改动**；新增文件 17 个均位于汇报目录内。仓库根另有 `.v2c/`、`comics/`（空）为工具/运行目录，非本次代码改动。

## 4. 前端自报 6 处契约假设核实

| # | 假设 | 核实结果 |
| --- | --- | --- |
| 1 | `/status` 返回 `canUse` | ✅ 成立：handler:142 返回；403 时 code=Z_FORBIDDEN，hook 双路径兼容（useZLibraryStatus.ts:39-45） |
| 2 | `GET /settings` 同路径读取 | ❌ **不成立 → DEF-1**（运行时 404 实锤） |
| 3 | 筛选选项前端内置 | ✅ 偏差已声明且自洽（format.ts:149-153）；FR-3.2 的"来自 /eapi/info"推迟到后端补端点，可接受 |
| 4 | 清空历史用聚合（逐条 DELETE 模拟） | ⚠️ 部分成立：功能可用，但仅覆盖当前加载 ≤50 条 → **DEF-3** |
| 5 | Toast 无动作按钮 | ✅ 成立：完成通知纯文本，下一步动作由任务卡「查看书籍」承接（TaskCenterDrawer.tsx:43-53）；已入库状态卡片有跳转链接 |
| 6 | remix 令牌 body `{"remixUserid","remixUserkey"}` | ✅ 成立：后端绑定字段逐字符一致（zlibrary_handler.go:219-220） |

## 5. 自动化测试覆盖清单（复跑通过）

- 适配层：`crypto_test.go`×4（AES-GCM 往返/密钥 env/持久化/base64）、`search_test.go`×11（防御解析/默认分页/登录/profile 配额/密码错→Z_AUTH_FAILED/错误分类/failover 网络错误与 5xx/全域名不可达/HTML 响应/ZBook JSON tag 锁定）、`download_test.go`×4+魔数校验。
- 业务层：`pipeline_test.go`×8（成功写映射/扫描忙退避/扫描失败保留文件/校验失败清理临时文件/扩展名不匹配/下载中取消/上游 404/过期映射清理）+ 队列（Enqueue 完成流转/并发=1/取消排队任务）。
- 存储层：`zlibrary_store_test.go`×6（KV/CRUD 与过滤/状态机/失败取消重试/中断与 200 条裁剪/映射 upsert 与清除）。

## 6. 需真实 Z-Library 账号的联调用例（留待用户执行）

| # | 用例 | 对应故事 |
| --- | --- | --- |
| E1 | 邮箱+密码登录 →「测试连接」回显账号名/类型/配额；确认日志无密码 | M1-01 |
| E2 | 粘贴 remix_userid/userkey 免密登录 | M1-01 |
| E3 | 令牌失效（改密后）→ 搜索触发全局「已断开」+ 设置页标红 + 重连引导 | M1-01/FR-1.4 |
| E4 | 搜索「三体」：语言=中文/格式=epub/年份/排序筛选、翻页加载更多、结果 <5s、封面正常、三态标注 | M1-03/10 |
| E5 | 详情面板：描述/大图/格式列表/多格式单选（epub 优先）/个人域名解析 | M1-11 |
| E6 | 一键入库全链路：下载→落盘「书名 - 作者.epub」→扫描→completed→跳转阅读；观察进度条与阶段文案（2s 轮询） | M1-05/14 |
| E7 | epub/mobi/PDF × 漫画/小说书库类型路由与 Z_EXT_MISMATCH 阻断 | M1-14 |
| E8 | 配额：下载后刷新、耗尽时 Z_QUOTA_EXCEEDED 文案与次日重试禁用 | M1-07/13 |
| E9 | 重复入库：同书再次点击 → 「已入库，点击查看」/「已在下载队列中」，配额不重复消耗 | M1-16 |
| E10 | failed 任务重试 / downloading 任务取消（临时文件清理） | M1-05/06 |
| E11 | 域名故障转移：手动将当前域名停用或等真实宕机，观察自动切换与冷却 | M1-02 |
| E12 | 全域名不可达 → Z_DOMAIN_UNREACHABLE + blackbox 指引 | M1-02 |
| E13 | 进程重启：downloading 任务 → Z_INTERRUPTED 可重试、无半截文件 | M1-05 |
| E14 | 已入库条目在书库删除后 → 再搜索显示「未入库」可再次入库 | M1-16 |
| E15 | 权限 b/c 档真实用户搜索/入库（含 canManage 校验、非 canManage 书库 403） | M1-08 |
| E16 | Z_SCAN_FAILED / Z_DISK_FULL / Z_SIZE_LIMIT 真实触发与文案 | M1-15 |

## 7. QA 说明与残留事项

- 本次验证产生并已清理：`/tmp/slib-qa-*`（服务二进制/日志/cookie jar/临时 DATA_DIR）、项目内测试产生的 `data/nowen-reader.db`（DB 落盘路径由 `DATABASE_URL`/cwd 决定而非 DATA_DIR，`config.go:573-579`——部署时请注意该路径语义）。
- 迁移日志中大量 `Skipping (already applied): duplicate column name ...` 为上游既有重试模式（migrate.go:635），非本次改动引入。
- `web/dist` 本次已被 QA 手动填充为当前前端构建产物（等价于 `npm run build:all` 的最终状态），后续正式出包仍应走 `npm run build:all`（DEF-4）。

---

## 8. 修复记录（后端开发工程师 · 2026-11-09）

> 按分工本轮仅修复后端缺陷 DEF-1 与 DEF-4（DEF-2/DEF-5 属前端文案、DEF-3 属前端循环清除或后续聚合端点、DEF-4 表内标注与任务单以 P3 归口）。均未触碰 frontend/。

- **DEF-1 已修复**：新增 `GET /api/zlibrary/settings`（仅 admin，`internal/handler/routes_zlibrary.go` 注册 + `internal/handler/zlibrary_handler.go` `ZlibraryGetSettings`）。响应体与 PUT /settings 请求绑定结构**同构**（`domains[]{url,enabled,isDefault}` + `behavior{concurrency,defaultLibraryId,allowScope}`，JSON 字段名完全一致，domains 顺序即优先级），并额外返回 `hasCredentials` 布尔值（取 `Credentials.HasToken()`，凭据已配置但失效也为 true）；本端点不含任何凭据字段，password/remixUserkey 秘密字段绝不回传。回归测试：`internal/handler/zlibrary_handler_test.go` `TestZlibraryGetSettingsEchoesSavedConfig`（PUT 后 GET 逐字段回显断言 + 秘密字段不回传断言）、`TestZlibraryGetSettingsRequiresAdmin`（普通用户 403）。
- **DEF-4 已修复**：非 failed 任务重试由 `500 Z_UPSTREAM` 改为 **409 + `{"code":"Z_INVALID_STATE"}`**（契约 §4(9)）；新增内部错误码 `Z_INVALID_STATE`（`internal/zlibrary/models.go`）并纳入 handler 的 409 映射分支（`zHTTPStatus`）。回归测试：`TestZlibraryRetryNonFailedReturns409`（completed/queued/canceled → 409+Z_INVALID_STATE；failed 且未连接凭据 → 401 Z_AUTH_REQUIRED，验证前置校验顺序未受影响）。

**GET /api/zlibrary/settings 响应示例**（前端适配基准）：

```json
{
  "domains": [
    { "url": "https://z-lib.sk", "enabled": true,  "isDefault": true  },
    { "url": "https://z-lib.fm", "enabled": false, "isDefault": false }
  ],
  "behavior": {
    "concurrency": 3,
    "defaultLibraryId": "",
    "allowScope": "all"
  },
  "hasCredentials": false
}
```

`behavior` 可整体作为下次 `PUT /settings` 的载荷回传（`lastLibraryByExt` 等内部记忆字段不在回显范围，PUT 时也不会被清除——PUT 对 behavior 做字段级合并更新，见 `ZlibrarySaveSettings`）。
