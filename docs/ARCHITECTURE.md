# s-library 系统架构设计

> 基于 nowen-reader 真实代码的架构方案 · 所有结论均给出 文件路径:行号 级证据
> 文档版本 v1.0 · 2026-11-09 · 角色：架构师 · 上游输入：REQUIREMENTS.md v0.1、PM-PLAN.md v1.0

---

## 1. 现状速览（代码事实确认清单）

### 1.1 后端骨架

| # | 事实 | 证据 |
| --- | --- | --- |
| 1 | 入口 `main()`：InitDB → RunMigrations → 后台任务 → Gin 路由 → SPA 服务；优雅停机 | `cmd/server/main.go:31-191` |
| 2 | 全局中间件链（顺序）：Recovery → RequestLogger/QuietLogger → ErrorLogCapture → CORS → SecurityHeaders → RequestTimeout(30s) → Gzip | `cmd/server/main.go:117-130` |
| 3 | 路由注册统一入口 `SetupRoutes(r)`，按域拆分为 registerAuth/Comic/Series/Content/Metadata/Library/UserGroupRoutes | `internal/handler/router.go:19-59` |
| 4 | 鉴权：`AuthRequired()` 接受 Bearer API Key（`Authorization: Bearer`，先于 Cookie）或会话 Cookie `nowen_session`；`AdminRequired()` 校验 `user.Role == "admin"`；`GetCurrentUser()` 返回 `model.AuthUser{ID,Username,Nickname,Role,AiEnabled}` | `internal/middleware/auth.go:38,94,167`；`internal/model/models.go:21-27` |
| 5 | 限流中间件已有 token-bucket 实现，但目前**只挂在 auth 路由**（register/login/api-key） | `internal/middleware/ratelimit.go:113,128,143`；`internal/handler/routes_auth.go:17-39` |
| 6 | 默认端口：代码默认 `:5080`（`PORT` env）；容器内 3000，宿主机映射 6680 | `cmd/server/main.go:144-147`；`docker-compose.prod.yml:21,42` |

### 1.2 数据库与迁移

| # | 事实 | 证据 |
| --- | --- | --- |
| 7 | SQLite（modernc.org/sqlite 纯 Go），DSN 带 `busy_timeout(30000)`，WAL 模式，`MaxOpenConns(8)` | `internal/store/db.go:34-54` |
| 8 | 迁移机制：**代码内注册**，`store.Migrations []Migration{Version, Description, SQL}` 顺序执行，`_migrations` 表记录版本；当前最新 **Version 36**（注意 31~34 被跳过），新迁移应取 **37** | `internal/store/migrate.go:28,493,539` |
| 9 | 命名规范：表名 PascalCase 单数（`Comic`/`Library`/`UserLibraryAccess`/`ComicGroupItem`），**列名 camelCase** 并带双引号，索引 `表名_列_idx` | `migrate.go:107-121,423-427` |
| 10 | 所有 SQL 集中在 `internal/store` 包（包级 `db` 全局变量），无 ORM | `internal/store/*.go` |

### 1.3 上传 / 扫描 / 权限（一键入库的复用点）

| # | 事实 | 证据 |
| --- | --- | --- |
| 11 | `POST /api/upload`（multipart，`files` + 可选 `libraryId`/`category`）：非 admin 需 `store.UserCanManageLibrary`，目标书库需存在且 `Enabled`，写入 `lib.RootPath` | 路由 `internal/handler/routes_content.go:82`；实现 `internal/handler/upload.go:31-96` |
| 12 | 单书库扫描：`POST /api/libraries/:id/scan`（AuthRequired，非 admin 需 canManage）与 `POST /api/admin/libraries/:id/scan`（AdminRequired），handler 均为 `LibraryHandler.ScanLibrary` → **`service.SyncLibraryByID(id)`** 返回 `(added, removed, err)`；挂在 `reconcileOwnershipAfterScan()`、`rebuildSeriesAfterScan()` 中间件后 | `internal/handler/routes_library.go:23,37`；`internal/handler/library_handler.go:512-546` |
| 13 | `service.SyncLibraryByID` 使用**全局互斥** `syncInProgress`（与 `ForceSyncComicsToDatabase` 共享），同一时刻仅允许一个扫描，冲突直接返回 error | `internal/service/scanner.go:1493-1505` |
| 14 | 全库扫描 `POST /api/sync`（仅 admin）→ `service.ForceSyncComicsToDatabase()`；后台自动同步 `StartBackgroundSync()`（快/慢双 ticker，启动即跑首轮） | `internal/handler/routes_comics.go:109`；`comic.go:643-650`；`scanner.go:1206,1399,1435,1459` |
| 15 | 权限矩阵三函数均已存在且含用户组继承：`UserCanViewLibrary` / `UserCanDownloadLibrary` / `UserCanManageLibrary`（public 不自动给 canManage；UserLibraryAccess + GroupLibraryAccess 两路检查） | `internal/store/library_store.go:449,491,537` |
| 16 | **Comic ID 是确定性的**：`store.PathToID(libraryID, relativePath)` = `md5(libraryId + ":" + relativePath)`。落盘后无需"从扫描结果匹配"，直接计算预期 ID 再 `GetComicByID` 验证即可 | `internal/store/comic_store.go:20-45` |
| 17 | 书库模型：`Library{Type: "comic"\|"novel"\|"mixed", RootPath, RootPaths, Enabled, DefaultAccess}`；扩展名判定 `config.NovelExtensions`（.txt/.epub/.mobi/.azw3/.html/.htm/.pdf）与 `IsSupportedArchive`；扫描时用 `IsNovelFile` 决定类型 | `internal/model/models.go:55-71`；`internal/config/config.go:585,601,612`；`scanner.go:375` |
| 18 | 后台任务先例充足：`StartSessionCleanup`（6h/30m ticker）、`StartStorageSampler`（1h ticker）、`StartBackgroundSync`——均为 main 启动的 goroutine + time.Ticker | `session_cleanup.go:13-29`；`storage_history.go:134-143`；`scanner.go:1399` |

### 1.4 配置持久化（重要差异）

| # | 事实 | 证据 |
| --- | --- | --- |
| 19 | 站点设置**不是 SQLite 表**，而是 `site-config.json` 文件（DATA_DIR 下，5s TTL 内存缓存），`SaveSiteConfig` 原子写盘 | `internal/config/config.go:15-41,195,208,236` |
| 20 | AI 配置（含 `cloudApiKey`）存 `dataDir/ai-config.json`，**明文**，回显时仅掩码 | `internal/service/ai_config.go:88`；`internal/handler/ai_config_handler.go:25-36` |
| 21 | **代码中不存在任何 AES/凭据加密先例**（仅 archive 包的 DRM 读取用到 AES 术语）。FR-1.3 的 AES-GCM 是全新代码 | 全仓 grep 证实 |
| 22 | `DATA_DIR` env 默认 `./.cache`；docker-compose 设 `DATA_DIR=/app/.cache` | `internal/config/config.go:200-206`；`docker-compose.prod.yml:41` |

### 1.5 前端

| # | 事实 | 证据 |
| --- | --- | --- |
| 23 | 目录关系：`frontend/` 是唯一源码；构建产物由 `npm run build:all`（= `tsc -b && vite build && node scripts/copy-dist.mjs`）拷贝到 `web/dist/`；`web/embed.go` 用 `//go:embed all:dist` 内嵌；开发期可用 `FRONTEND_DIR` 直读目录。`web/` 里**没有源码** | `web/embed.go:19-42`；`frontend/scripts/copy-dist.mjs:1-27`；`cmd/server/main.go:194-221` |
| 24 | 路由：**不是** Next.js——`main.tsx` 用 react-router-dom `BrowserRouter` + `Routes`，页面组件来自 `app/*/page.tsx`（Next 目录风格），`next/*` 导入经 vite alias 重写到 `src/shims/next/*` 兼容层 | `frontend/src/main.tsx:1-173`；`frontend/vite.config.ts` alias 段 |
| 25 | 新路由挂载点：`AppRoutes()` 内 `<Route path="...">`；管理员守卫 `AdminRoute` 包裹即可 | `main.tsx:56-62,112-141` |
| 26 | 侧边栏：`DesktopSidebar.tsx` 的 `primaryItems` 数组（href/icon/label/adminOnly）；标签是**硬编码中文**（未走 i18n）；移动端 `MobileBottomNav.tsx` 同样硬编码 | `frontend/src/components/DesktopSidebar.tsx:48-56`；`MobileBottomNav.tsx:43-68` |
| 27 | 设置页：`app/settings/page.tsx`，`SettingsTab` 联合类型 + `groups: TabGroup[]`（分组渲染，admin 项用 `...(isAdmin ? [...] : [])` 条件展开），面板组件 dynamic import；tab 经 `?tab=` URL 参数定位 | `frontend/src/app/settings/page.tsx:95-105,169-224,147` |
| 28 | API client：`src/api/*.ts` 每域一文件，裸 `fetch(apiPath(...))` + 类型定义 + `safeJson`（错误取 `data.error`，可选 `data.code`）；`apiPath` 处理 BasePath | `frontend/src/api/libraries.ts:202-228,231` |
| 29 | i18n：`lib/i18n/context.tsx`（I18nProvider，`t` 为整个翻译对象，类型由 zh-CN 导出）；词条 `locales/zh-CN.ts` + `locales/en.ts`（嵌套对象，两文件同构）；locale 存 localStorage | `frontend/src/lib/i18n/context.tsx:1-58`；`index.ts` |
| 30 | dev 代理：vite server 5090 → `http://localhost:5080` 的 `/api/`；`base: "./"` | `frontend/vite.config.ts` server/build 段 |

### 1.6 API 契约风格（新端点必须沿用）

- JSON tag 全部 **camelCase**（`models.go` 所有 struct）；
- 错误响应统一 `{"error": string}`（HTTP status 表达类别），结构化错误码有先例：`{"error": ..., "code": "SOURCE_REVISION_CONFLICT", ...}`；
- 列表响应形如 `{"libraries": [...]}`、任务详情形如 `{"task": {...}}`；
- 认证复用 Cookie/Bearer，无独立 header。

---

## 2. 模块设计（新增 Go 包）

> 遵循 PM-PLAN §4.1：eapi 调用收敛到 `internal/zlibrary`；SQL 全部放 `internal/store`（项目惯例）；路由放 `internal/handler`（项目无 `internal/server`，见 §10 冲突 C2）。

### 2.1 文件清单

**适配层 `internal/zlibrary/`（纯客户端，不 import store/service）**

| 文件 | 职责 |
| --- | --- |
| `models.go` | `ZBook{ID,Hash,Title,Subtitle,Author,Publisher,Year,Language,Ext,FileSize. Rating,CoverURL,Description,Formats[],DlDomain}`、`ZProfile{UserID,Name,AccountType,DailyQuota,DailyUsed}`、`ZError{Code,...}`（映射 §7 错误矩阵：Z_AUTH_REQUIRED/Z_QUOTA_EXCEEDED/...） |
| `client.go` | HTTP 客户端：域名列表轮换与请求级 failover（网络错误/5xx/CF 特征→下一域名）、UA、15s 超时、请求间隔、冷却表；注入 `*http.Client` 便于测试 |
| `auth.go` | `Login(email,password)` 换取 remix_userid/userkey；`Profile()` 校验登录态+配额映射 |
| `search.go` | `Search(query)` 构造 form 请求（`languages[]` 等数组参数）+ **防御性解析**（未知字段宽容） |
| `book.go` | `Book(id,hash)` 详情、`Formats(id,hash)`、解析个人下载域名 `dl` |
| `download.go` | 流式下载到 `io.Writer` + 进度回调；魔数校验（EPUB/CBZ=PK、PDF=%PDF、MOBI=BOOKMOBI） |
| `crypto.go` | AES-GCM 加解密 userkey；密钥解析：`SL_ZLIB_SECRET` env → 否则读/生成 `DATA_DIR/zlib-secret.key` |
| `client_test.go` `search_test.go` `download_test.go` | httptest 固定样本单测（见 §9） |

**业务层 `internal/zlibrary/service/`（编排，可 import store/service/config）**

| 文件 | 职责 |
| --- | --- |
| `settings.go` | 设置缓存（并发 1~3、默认书库、权限档、域名列表）、权限档判定 `CanUse(uid)`（a/b/c 三档，b 档复用 `store.UserCanManageLibrary` 对任意书库的遍历） |
| `quota.go` | 配额缓存（今日已用/总量）、下载后强制刷新、本地预检 |
| `dedup.go` | `CheckDedup(zbookId,hash)` → 已入库(含 localBookId)/队列中/未入库；批量标注接口 |
| `queue.go` | 任务队列：DB 持久化 + worker goroutine + 并发信号量 + 启动恢复 + 取消/重试 |
| `pipeline.go` | 单任务流水线：fetch_info → downloading → validating → moving → scanning → 完成映射（§5 时序） |
| `queue_test.go` `pipeline_test.go` | 用 mock Client（接口注入）覆盖状态机与失败分支 |

**接入层**

| 文件 | 职责 |
| --- | --- |
| `internal/handler/zlibrary_handler.go` | 11 个端点的 handler（薄层：绑定/校验/调 service） |
| `internal/handler/routes_zlibrary.go` | `registerZlibraryRoutes(api)` + `ZlibraryAccessRequired()` 中间件（读权限档设置，复用 `middleware.GetCurrentUser`） |
| `internal/store/zlibrary_store.go` | 三张表的 CRUD + 状态机更新 + 200 条历史裁剪 |

### 2.2 依赖关系（文字版）

```
cmd/server/main.go
  └─ handler.SetupRoutes ──> handler.routes_zlibrary ──> handler.zlibrary_handler
                                                             │
internal/zlibrary/service（queue/pipeline/quota/dedup/settings）
  ├─> internal/zlibrary（适配层，唯一出网点）        ├──> internal/store（Comic/Library/新表/权限矩阵）
  │      └─> net/http（Z-Library eapi）              ├──> internal/service（仅 SyncLibraryByID）
  └─> internal/zlibrary/crypto ──> internal/config（DATA_DIR）
                                                  └──> internal/middleware（GetCurrentUser，经 handler 传入）
禁止：internal/zlibrary（适配层）import store/service/config/handler —— 保证上游变更只改适配层
```

---

## 3. 数据模型（Migration Version 37）

> 冲突说明见 §10-C1：需求文档用 snake_case 表名，项目规范是 PascalCase 表 + camelCase 列，**按项目规范执行**，字段与 FR-5.1/FR-7.1 一一对应。

```sql
-- 1) 任务表（FR-5.1 全字段）
CREATE TABLE IF NOT EXISTS "ZlibraryDownloadTask" (
    "id"             INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "userId"         TEXT NOT NULL,                    -- 发起人（users.id, uuid）
    "zbookId"        TEXT NOT NULL,                    -- Z-Library 书籍 id
    "zbookHash"      TEXT NOT NULL,                    -- Z-Library 书籍 hash（复合主键之一）
    "title"          TEXT NOT NULL DEFAULT '',         -- 快照
    "author"         TEXT NOT NULL DEFAULT '',
    "ext"            TEXT NOT NULL DEFAULT '',
    "fileSize"       INTEGER NOT NULL DEFAULT 0,
    "libraryId"      TEXT NOT NULL DEFAULT '',
    "localBookId"    TEXT NOT NULL DEFAULT '',         -- completed 后回填
    "status"         TEXT NOT NULL DEFAULT 'queued',   -- queued|downloading|completed|failed|canceled
    "progress"       INTEGER NOT NULL DEFAULT 0,       -- 0-100
    "stage"          TEXT NOT NULL DEFAULT 'fetch_info', -- fetch_info|downloading|validating|moving|scanning
    "errorCode"      TEXT NOT NULL DEFAULT '',
    "errorMsg"       TEXT NOT NULL DEFAULT '',
    "quotaConsumed"  BOOLEAN NOT NULL DEFAULT 0,       -- 失败时标注配额是否已消耗（PM §3.3-1）
    "retryCount"     INTEGER NOT NULL DEFAULT 0,
    "metaJson"       TEXT NOT NULL DEFAULT '{}',       -- FR-8.1：Z-Library 元数据随任务落库（M2 回填用）
    "createdAt"      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt"      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "finishedAt"     DATETIME
);
CREATE INDEX IF NOT EXISTS "ZlibraryDownloadTask_user_created_idx"
    ON "ZlibraryDownloadTask"("userId", "createdAt" DESC);
CREATE INDEX IF NOT EXISTS "ZlibraryDownloadTask_status_idx"
    ON "ZlibraryDownloadTask"("status");

-- 2) 去重映射表（FR-7.1）
CREATE TABLE IF NOT EXISTS "ZlibraryImportedBook" (
    "zbookId"      TEXT NOT NULL,
    "zbookHash"    TEXT NOT NULL,
    "localBookId"  TEXT NOT NULL DEFAULT '',
    "libraryId"    TEXT NOT NULL DEFAULT '',
    "importedBy"   TEXT NOT NULL,
    "importedAt"   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY ("zbookId", "zbookHash")              -- FR-7.2 唯一约束兜底
);
CREATE INDEX IF NOT EXISTS "ZlibraryImportedBook_library_idx"
    ON "ZlibraryImportedBook"("libraryId", "localBookId");

-- 3) 配置表（FR-1.3：SQLite 配置表；KV 结构便于扩展）
CREATE TABLE IF NOT EXISTS "ZlibrarySetting" (
    "key"        TEXT NOT NULL PRIMARY KEY,           -- credentials|domains|behavior|personalDomain|quotaCache
    "value"      TEXT NOT NULL DEFAULT '',            -- JSON 序列化值；userkey 字段 AES-GCM 密文
    "updatedAt"  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

- `credentials` value JSON：`{"remixUserid":"...","remixUserkeyEnc":"<base64 AES-GCM>","connected":bool,"failedAt":ts}` —— **密码永不落库**；
- `domains` value JSON：`[{"url":"https://z-lib.sk","enabled":true,"isDefault":true,"healthy":true,"lastCheckedAt":ts,"cooldownUntil":ts}, ...]`（顺序即优先级）；
- `behavior` value JSON：`{"concurrency":1,"defaultLibraryId":"","allowScope":"admin"|"canManage"|"all","lastLibraryByExt":{...}}`（FR-4.1 记忆上次选择）。

---

## 4. API 契约（11 端点）

统一前缀 `/api/zlibrary`，全部经 `middleware.AuthRequired()`。错误响应统一：

```json
{ "error": "人话描述", "code": "Z_QUOTA_EXCEEDED" }        // 4xx/5xx
```

| # | 方法 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| 1 | GET `/api/zlibrary/status` | 授权用户* | 连接状态+配额+当前域名+各域名健康 |
| 2 | PUT `/api/zlibrary/settings` | admin | 保存域名/行为/权限档配置 |
| 3 | POST `/api/zlibrary/auth/test` | admin | 测试连接（或直接用 body 凭据登录换令牌） |
| 4 | DELETE `/api/zlibrary/auth` | admin | 解绑（清除 credentials） |
| 5 | POST `/api/zlibrary/search` | 授权用户* | 代理搜索+去重标注 |
| 6 | GET `/api/zlibrary/book/:id/:hash` | 授权用户* | 详情+格式列表+已入库标注 |
| 7 | POST `/api/zlibrary/downloads` | 授权用户* | 创建任务（前置校验 <1s 同步返回） |
| 8 | GET `/api/zlibrary/downloads` | 授权用户* | 任务列表（admin 见全部，含 `user` 字段） |
| 9 | POST `/api/zlibrary/downloads/:id/retry` | 发起人或 admin | 重试（重新前置校验） |
| 10 | DELETE `/api/zlibrary/downloads/:id` | 发起人或 admin | 取消/删除 |
| 11 | POST `/api/zlibrary/domains/health-check` | admin | 手动探测全部启用域名 |

\* 授权用户 = `ZlibraryAccessRequired()`：按 `behavior.allowScope` 三档判定（admin 恒通过；`canManage` 档遍历 accessible libraries 有任一 canManage；`all` 档仅登录）。凭据/域名/设置类端点（2/3/4/11）恒为 admin。

**字段 Schema（camelCase，与现有一致）**

```
(1) GET /status → 200 {"connected":bool, "accountName":string, "accountType":string,
      "quota":{"used":int,"total":int,"resetAt":string},
      "currentDomain":string, "personalDomain":string,
      "domains":[{"url":string,"enabled":bool,"isDefault":bool,"healthy":bool,
                  "lastCheckedAt":string|null,"latencyMs":int|null}]}
    401 未登录；403 {"error","code":"Z_FORBIDDEN"}

(2) PUT /settings ← {"domains":[{"url","enabled","isDefault"}],     // 顺序即优先级
                     "behavior":{"concurrency":1|2|3,"defaultLibraryId":string,
                                 "allowScope":"admin"|"canManage"|"all"}}
    → 200 {"success":true}；400 参数非法
    凭据不在此端点（走 3/4），避免误覆盖令牌

(3) POST /auth/test ← 二选一：{"email","password"}（登录换令牌，密码用后即弃）
                       或 {} （用已存令牌验证）
    → 200 {"connected":true,"accountName","accountType","quota":{used,total}}
    401/200 包裹式失败均可，约定 → 422 {"error","code":"Z_AUTH_FAILED"}；网络类 → Z_DOMAIN_UNREACHABLE/Z_BLOCKED

(4) DELETE /auth → 200 {"success":true}（清除 credentials；任务不清理）

(5) POST /search ← {"message":string(必填,1-256), "languages":[string], "extensions":[string],
                    "yearFrom":int|null, "yearTo":int|null,
                    "order":"bestmatch"|"newest"|"rating"(默认bestmatch),
                    "page":int(默认1), "limit":int(默认20,≤100)}
    → 200 {"total":int, "page":int, "limit":int,
           "books":[{"zbookId":string,"hash":string,"title","subtitle","author","publisher",
                     "year":int|null,"language","ext","fileSize":int,"rating":float|null,
                     "coverUrl":string,
                     "dedupStatus":"none"|"imported"|"queued",
                     "localBookId":string(仅 imported)}]}
    上游失败 → 502 {"error","code":Z_DOMAIN_UNREACHABLE|Z_AUTH_REQUIRED|Z_BLOCKED}

(6) GET /book/:id/:hash → 200 {"book":{...(5)全字段, "description":string,
                                "formats":[{"ext","fileSize"}], "dlDomain":string,
                                "dedupStatus", "localBookId"}}
    404 → Z_NOT_FOUND；502 → 同(5)

(7) POST /downloads ← {"zbookId":string,"hash":string,"ext":string|null,"libraryId":string|null}
    同步预检（顺序）：登录态→配额(本地计数)→书库存在且Enabled→扩展名与书库类型匹配
    （novelExt=config.NovelExtensions；comic=SupportedExtensions；mixed 均可）→去重
    → 202 {"task":{...(8)task对象}}          // 立即返回任务 ID
    400/403/409 对应错误码：Z_AUTH_REQUIRED|Z_QUOTA_EXCEEDED|Z_EXT_MISMATCH|
                           Z_DUP_IMPORTED(附localBookId)|Z_DUP_QUEUED|Z_DISK_FULL(预检可选)
    注：磁盘预检在落盘前(流水线内)再查一次；创建时仅做书库类型校验

(8) GET /downloads?status=&page=&limit= → 200 {"tasks":[task], "total":int}
    task = {"id":int,"userId","user":string(admin可见用户名),"zbookId","zbookHash",
            "title","author","ext","fileSize","libraryId","localBookId",
            "status","progress","stage","errorCode","errorMsg","quotaConsumed",
            "retryCount","createdAt","finishedAt"|null}
    非管理员强制 userId=自己（WHERE 注入，非过滤）

(9) POST /downloads/:id/retry → 202 {"task"}；failed 才可重试：completed → 409；
    重试前重新执行(7)的前置校验，retryCount+1
(10) DELETE /downloads/:id → 200 {"success":true}；queued 直接删、downloading 取消
     （ctx 中断+清理临时文件→canceled）、终态任务删除记录（200 条内自由清理）
(11) POST /domains/health-check → 200 {"results":[{"url","healthy","latencyMs","error"}]}
```

---

## 5. 一键入库流水线时序（文字版）

```
用户点击入库
 │
 ├─ [同步, <1s] POST /api/zlibrary/downloads（handler.zlibrary_handler.CreateTask）
 │    1. ZlibraryAccessRequired → 权限档判定（新增，settings.CanUse）
 │    2. settings 凭据有效？ → 否：同步 401 Z_AUTH_REQUIRED（新增）
 │    3. quota.Precheck()（新增，本地缓存计数）→ 0：409 Z_QUOTA_EXCEEDED
 │    4. store.GetLibraryByID + config.IsNovelFile/IsSupportedArchive 类型匹配（现有函数）
 │       → 不匹配：400 Z_EXT_MISMATCH
 │    5. dedup.CheckDedup（新增；store 查两表）→ Z_DUP_IMPORTED / Z_DUP_QUEUED
 │    6. store.CreateZlibraryDownloadTask(status=queued)（新增）→ 立即 202 返回
 │
 └─ [异步] queue worker（新增，并发信号量 ≤ behavior.concurrency）
      7. status=downloading, stage=fetch_info
        → zlibrary.Client.Book(id,hash)（新增）取 dl 链接/最新大小
        失败：Z_NOT_FOUND/Z_AUTH_REQUIRED → failed；未发起下载不耗配额
        多格式未指定：按 epub>pdf>mobi>azw3>其他 选 ext（新增 pipeline.resolveExt）
      8. stage=downloading，流式下载（zlibrary.Client.DownloadToFile，新增）
        → 临时文件 DATA_DIR/tmp/zlib-{taskId}.{ext}；进度回调节流写 progress
        中断：清理临时文件 → failed（quotaConsumed 如实标注，PM §3.3-1）
      9. stage=validating：魔数+扩展名校验（zlibrary.ValidateMagic，新增）
        失败重试下载 1 次 → 再失败：failed + 清理
     10. 落盘前：磁盘剩余空间 > fileSize×1.1（新增 pipeline.checkDisk）
        → 不足：failed Z_DISK_FULL；书库被删：failed（重试不耗配额，PM §3.3-3）
     11. stage=moving：文件名「书名 - 作者.ext」（新增 pipeline.sanitizeFilename：
        非法字符清洗/长度截断/重名加 (2)）；move 临时文件 → lib.RootPath（现有字段）
     12. stage=scanning：**进程内直接调用** service.SyncLibraryByID(libraryId)
        （现有 scanner.go:1493，不绕过扫描器；不发 HTTP 自调用）
        注意全局互斥：若 "another library sync is already running"
        → 指数退避重试（最多 3 次/60s），仍失败 → failed Z_SCAN_FAILED，文件保留（PM §3.3-2）
     13. 匹配本地条目：localBookId = store.PathToID(libraryId, relativePath)
        （现有 comic_store.go:20，确定性 md5）→ store.GetComicByID 验证（现有）
        验证失败 → failed Z_SCAN_FAILED（文件保留 + 手动扫描指引）
     14. 写映射 store.UpsertZlibraryImportedBook（新增，唯一索引兜底）
        quota.Refresh()（新增）→ status=completed, localBookId 回填
        M1 不做元数据回填（D2）：metaJson 已随任务落库，文件名规范兜底（FR-8.3）
     15. 任务历史裁剪：保留最近 200 条（新增 store 帮助函数）
     16. 前端 2s 轮询 GET /downloads 得知 completed → 通知跳转 /comic/{localBookId}
```

失败分支汇总：任何一步失败 = `failed` + `errorCode/errorMsg`（§7 矩阵）+ 临时文件清理 + 不产生半截文件；completed 不重复下载（重试仅对 failed）。

---

## 6. 任务队列设计

- **并发控制**：带缓冲 channel 信号量，容量 = `behavior.concurrency`（1~3，默认 1）；修改设置后对新任务生效。下载串行 + eapi 请求间最小间隔（适配层内 sleep），失败指数退避（防风控 R2）。
- **状态机**：`queued → downloading(阶段: fetch_info→downloading→validating→moving→scanning) → completed | failed | canceled`。状态迁移全部经 `store` 单点函数更新（乐观检查当前状态，防止取消与完成竞争）。
- **持久化**：任务即数据库行；progress 更新**节流**（≥2% 或 ≥500ms 才写盘，SQLite 单写者），阶段/状态变化立即写。
- **进程重启恢复**：启动时（`registerZlibraryRoutes` 或 main 显式调用 `service.StartZlibraryQueue()`）执行：`status IN (downloading) → failed(Z_INTERRUPTED, retryable)`，临时文件目录全清（`DATA_DIR/tmp/zlib-*`）；`queued` 保持排队。PM §4.3 要求重启不留半截文件。
- **取消**：worker 持有每任务的 `context.Context`；DELETE 时 store 标记取消意图 + cancel ctx，下载循环经 `io.Copy` 错误返回感知，清理临时文件后置 `canceled`。
- **进度上报**：无推送（SSE 为 M2）；前端 2s 轮询 `GET /downloads`（FR-5.3）。配额变化经 `GET /status` 同轮询可见。
- **扫描互斥**：流水线的 scan 步骤与全局扫描共用 `syncInProgress` 互斥（scanner.go:1493），必须退避重试而非并发排队——防止与后台自动同步死锁。

---

## 7. 前端接入点

| 接入点 | 位置 | 改动 |
| --- | --- | --- |
| 搜索页路由 | `frontend/src/main.tsx:112-141`（`<Route path="zlibrary">` + lazy import）；页面文件 `frontend/src/app/zlibrary/page.tsx` | 新增文件 + main.tsx 注册 |
| 桌面侧边栏入口 | `frontend/src/components/DesktopSidebar.tsx:48-56` `primaryItems` 追加 `{href:"/zlibrary", icon:Globe|BookOpen, label:t.zlibrary.nav}`；可见性按 `useAuth` + `/status` 的 connected/权限判断（无权限不渲染） | 修改一处 |
| 任务中心入口 | `DesktopSidebar.tsx` dock-footer 区（`/settings` 上方）新增图标 + 活动角标（轮询 tasks 计数）；抽屉组件 `frontend/src/components/ZlibraryTaskDrawer.tsx` 挂到 `main.tsx` 全局（AppShell 同级） | 新增文件 + 修改一处 |
| 移动端入口 | `frontend/src/components/MobileBottomNav.tsx:43-68` 追加（若底部导航超 5 项则 M1 仅保留搜索页入口） | 可选 |
| 设置页 Tab | `frontend/src/app/settings/page.tsx:95` `SettingsTab` 联合类型加 `"zlibrary"`；`:169` groups 的「站点」组 isAdmin 条件展开中追加 tab；面板 `frontend/src/components/ZlibrarySettingsPanel.tsx`（连接/域名/下载行为/权限/兜底指引五分组，dynamic import） | 修改两处 + 新增文件 |
| API client | `frontend/src/api/zlibrary.ts`（沿用 `apiPath` + `safeJson` + 错误 `code` 字段模式，参照 libraries.ts:202） | 新增文件 |
| i18n | `frontend/src/lib/i18n/locales/zh-CN.ts` + `en.ts` 各加 `zlibrary: {...}` 命名空间；`zh-CN.ts` 导出类型自动约束 en 同构 | 修改两个词条文件 |
| 书籍详情跳转 | completed 通知跳 `/comic/{localBookId}`（现有路由）；series 场景走现有 `seriesIdFromShelfId` 转发逻辑（main.tsx:64-71） | 无需改动 |

---

## 8. 侵入性改动清单（`// s-library:` 标记策略）

按需求 §6.4：新增文件为主；被修改的原文件在改动行附近加 `// s-library: <说明>` 注释，fork 基线 commit 记录在本文档 §10 后补。

| # | 文件 | 改动 |
| --- | --- | --- |
| 1 | `internal/store/migrate.go` | Migrations 切片追加 Version 37（三张新表），`// s-library:` |
| 2 | `internal/handler/router.go:52-58` | `registerZlibraryRoutes(api)` 一行 |
| 3 | `cmd/server/main.go:96-106` 附近 | `zlibservice.StartZlibraryQueue()`（启动恢复+worker）一行 |
| 4 | `internal/handler/routes_*.go` | 无改动（新端点全部在新文件） |
| 5 | `frontend/src/main.tsx` | +1 lazy import、+1 Route、任务抽屉挂载 3 处，均带标记 |
| 6 | `frontend/src/components/DesktopSidebar.tsx` | primaryItems + 任务中心图标 2 处 |
| 7 | `frontend/src/app/settings/page.tsx` | SettingsTab 类型 + groups 追加 2 处 |
| 8 | `frontend/src/lib/i18n/locales/zh-CN.ts` / `en.ts` | 各追加 `zlibrary` 命名空间 |
| 9 | `docker-compose*.yml` | 可选 env `SL_ZLIB_SECRET` 注释行（D5：不新增必需配置） |
| 10 | `docs/` | API.md 增补（可选） |

不改动：上传/扫描/权限/阅读器全部现有代码路径；`site-config.json` 结构；现有中间件链。

---

## 9. 构建与测试策略

**后端**
- eapi mock：`internal/zlibrary` 的 `Client` 持有可注入 `*http.Client` 与 baseURL；单测用 `httptest.NewServer` 返回固定样本 JSON（登录/搜索/详情/格式/配额/CF 403 页/下架 404），断言防御性解析与 failover 顺序（`client_test.go`，先例：`internal/handler/api_key_test.go`、`spa_test.go` 已用 httptest）。
- 流水线单测：`pipeline.go` 依赖 `ZClient` 接口（生产实现 + mock），书库根目录用 `t.TempDir()`；覆盖：成功、校验失败重试、磁盘满、扫描互斥退避、扫描匹配失败（Z_SCAN_FAILED）、取消清理。队列测试用内存 SQLite（`store.InitDB(":memory:")` 模式，参照 `store/db_test.go`）。
- handler 测试：`gin.SetMode(TestMode)` + 真实路由注册，验证权限档 401/403 与错误码契约。
- 命令：`go test ./...`；`go build ./cmd/server`（Makefile `make build`，LDFLAGS 注入版本）。

**前端**
- 构建：`npm --prefix frontend run build:all`（tsc 类型检查 → vite build → 拷贝 `web/dist` 供 embed）；dev：根目录 `npm run dev`（并发起 Go 5080 + vite 5090 代理）。
- 类型即契约：`api/zlibrary.ts` 的 interface 与 §4 schema 一一对应；`tsc -b` 保证 i18n 两文件同构（en 缺 key 即编译失败）。

---

## 10. 需求/PM 计划与代码现实的冲突清单

| # | 冲突 | 建议 |
| --- | --- | --- |
| C1 | FR-5.1/FR-7.1 用 snake_case 表名（`zlibrary_download_tasks`），项目规范是 PascalCase 表 + camelCase 列（migrate.go 全部先例） | 按项目规范命名（本文 §3 DDL），字段语义与 FR 一一对应；不改需求文档，仅在 PR 描述注明映射 |
| C2 | 需求 §6.1 提出 `internal/server/api/zlibrary*`，实际项目路由层在 `internal/handler`（无 internal/server 包） | 落为 `internal/handler/zlibrary_handler.go` + `routes_zlibrary.go`，模块划分意图不变 |
| C3 | FR-1.3 要求凭据存「SQLite 配置表」，但现有站点设置/AI 配置均为 JSON 文件且无加密先例 | 遵循需求用 SQLite 表（§3 的 ZlibrarySetting）——凭据需事务性与加密，JSON 文件方案放弃；AI 配置明文密钥的旧模式不复制 |
| C4 | PM-PLAN US-M1-05 说「触发扫描（复用现有 scan 路径）」——HTTP 自调用 `POST /api/libraries/:id/scan` 会引入回环与鉴权复杂度 | 进程内直接调 `service.SyncLibraryByID`（同一函数、同一互斥），语义等价且可获取错误 |
| C5 | FR-4 步骤 5「从扫描结果中匹配新增条目」——`SyncLibraryByID` 只返回 added/removed 数值，不含条目 | 用确定性 ID 方案：`store.PathToID(libraryId, relativePath)`（comic_store.go:20）计算 + `GetComicByID` 验证，比快照 diff 更可靠 |
| C6 | 需求 §2.1 称「默认端口 6680」，代码默认 5080、容器 3000→宿主 6680 | 无需动作，文档口径以 docker 映射为准 |
| C7 | FR-5.1 的 stage 枚举含 `metadata`，D2 决策元数据回填不进 M1 | M1 保留枚举值但流水线不进入该阶段；metaJson 落库保证 M2 免重新下载 |
| C8 | 现有侧边栏/底部导航标签是硬编码中文（DesktopSidebar.tsx:49-55），与「新增文案 zh/en 双语」要求不一致 | 新增的 Z-Library 入口走 i18n（t.zlibrary.nav），不顺手重构旧标签（保持侵入面最小） |

---

## 11. 分步实施顺序（给开发工程师）

| 步 | 内容 | 产出 | 规模 |
| --- | --- | --- | --- |
| 1 | 迁移 + 存储：Version 37 DDL、`store/zlibrary_store.go` 全部 CRUD（含 200 条裁剪、状态机乐观更新） | 表可读写，单测过 | M（1~1.5d） |
| 2 | 适配层：`models.go` + `client.go`（域名轮换/failover）+ `crypto.go`，httptest 样本单测 | 登录/profile 走通，解析单测绿 | M（1.5~2d） |
| 3 | 适配层：`search.go` + `book.go` + `download.go`（流式+魔数校验），补样本单测 | 搜索/详情/下载可独立验证 | M（1.5~2d） |
| 4 | 设置与权限：`service/settings.go` + handler 端点 2/3/4/11 + `ZlibraryAccessRequired`；AES-GCM 落库、密码用后即弃、日志脱敏 | US-M1-01/02/08 后端完成 | M（1.5d） |
| 5 | 搜索/详情/状态端点：5/6/1 + `quota.go` + `dedup.go` | US-M1-03/07 后端完成 | M（1d） |
| 6 | 任务队列：`queue.go`（并发/取消/重启恢复/节流进度）+ 端点 7/8/9/10 | US-M1-04/06 后端完成 | L（2~2.5d） |
| 7 | 流水线：`pipeline.go` 全 8 步 + 失败分支 + `SyncLibraryByID` 退避调用 + PathToID 匹配 + 映射回写；流水线单测 | US-M1-05 完成，curl 可全链路 | L（2~2.5d） |
| 8 | 前端地基：`api/zlibrary.ts` + i18n 命名空间 + 路由/侧边栏/任务抽屉挂载 | 页面骨架可导航 | M（1d） |
| 9 | 搜索页 + 详情面板 + 入库交互（含格式选择、书库记忆、三态标注、配额条、历史 localStorage） | US-M1-10/11/13 | L（2~3d） |
| 10 | 任务中心 UI + 设置面板五分组（含 blackbox 指引） | US-M1-09/12 | M（1.5~2d） |
| 11 | 联调收尾：§7 错误矩阵逐条演练（断连/超限/全域名不可达/重复/扫描失败/磁盘满）、去重重试专项、`docker-compose.prod.yml` 验证 | US-M1-14/15/16 | M（1.5~2d） |

关键路径：1→2→3→(4‖5)→6→7→(8→9‖10)→11；总估约 17~21 人日。步骤 4/5 可并行、9/10 可并行。

---

## 附：fork 基线

实施前执行 `git rev-parse HEAD` 记录基线 commit于此（当前仓库尚未初始化 git，见 §8 策略）。
