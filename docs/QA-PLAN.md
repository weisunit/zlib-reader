# s-library QA 测试计划（M1）

> 文档版本 v1.0 · 2026-11-09 · 角色：QA 测试工程师
> 上游输入：PM-PLAN.md（16 条用户故事）、REQUIREMENTS.md §5 FR / §7 错误矩阵、ARCHITECTURE.md §4 API 契约、UI-DESIGN.md
> 执行原则：**独立验证，不信任开发自报**；所有结论以真实命令输出或代码引用（文件:行号）为据；本计划只报告不修复。

---

## 1. 测试范围与分类

| 类别 | 代号 | 说明 | 本轮可执行性 |
| --- | --- | --- | --- |
| 构建与静态检查 | A | go build / go vet / go test / 前端 build+lint / embed 产物链 | ✅ 本次可执行 |
| 前后端契约核对 | B | 11 端点 + GET /settings 逐字段比对（请求/响应/错误码） | ✅ 本次可执行（静态） |
| 迁移与权限运行时 | C | 临时 DATA_DIR 起服，验证迁移 47、401/403、/status 响应结构 | ✅ 本次可执行 |
| 需求覆盖走查 | D | 16 条故事逐条找后端/前端覆盖证据 + 标注抽查 + diff 范围 | ✅ 本次可执行（静态） |
| 真实账号联调 | E | 搜索→入库→阅读全链路、配额/去重真实行为 | ⛔ 需真实 Z-Library 账号，留用例清单 |

自动化覆盖现状（复跑验证）：后端单测在 `internal/zlibrary/`（crypto/search/download）、`internal/zlibrary/service/`（pipeline）、`internal/store/`（zlibrary_store）；前端无自动化测试（仅 tsc + eslint + 手工脚本 scripts/test-*.mjs，均与本次改动无关）。

## 2. PM-PLAN 16 条用户故事 → 测试项映射

| # | 用户故事 | 映射测试项 | 执行方式 |
| --- | --- | --- | --- |
| US-M1-01 | 账号连接与凭据管理 | T-01 凭据双方式绑定字段核对（email/password、remixUserid/remixUserkey）；T-02 密码不落库/不回显（settings.go SaveCredentialsToken 仅收令牌；面板 type=password）；T-C5 未配置凭据时 /auth/test 行为 | B + C ✅；真实登录 E ⛔ |
| US-M1-02 | 域名管理与故障转移 | T-03 域名 CRUD 契约（url/enabled/isDefault）；T-04 健康检查响应字段（url/healthy/latencyMs/error）；T-C4 failover 单测复跑 | B ✅；真实域名切换 E ⛔ |
| US-M1-03 | eapi 搜索与详情 | T-05 搜索请求/响应逐字段；T-06 详情+formats 契约；T-07 防御性解析单测复跑（search_test.go） | B + A ✅；真实搜索 E ⛔ |
| US-M1-04 | 入库前置校验 | T-08 前置校验顺序与错误码（Z_AUTH_REQUIRED→Z_QUOTA_EXCEEDED→Z_LIB_NOT_FOUND→Z_FORBIDDEN→Z_EXT_MISMATCH→Z_DUP_*）；T-C6 无凭据创建任务 401 | B + C ✅（部分需真实账号确认配额语义） |
| US-M1-05 | 下载流水线 | T-09 pipeline 状态机/魔数校验/磁盘检查单测复跑（pipeline_test.go）；T-10 落盘命名「书名 - 作者.ext」与 (2) 去重 | A ✅；真实下载 E ⛔ |
| US-M1-06 | 任务中心 API | T-11 任务对象字段两端一致性（task JSON tag vs ZTask）；T-12 非 admin WHERE 注入（仅见自己）；T-13 retry 仅 failed / DELETE 语义 | B + C ✅ |
| US-M1-07 | 配额数据源 | T-14 quota 字段（used/total/resetAt）与 profile 映射；T-C3 /status 未配置时 quota 结构 | B + C ✅；真实配额数值 E ⛔ |
| US-M1-08 | 权限档与鉴权 | T-C1 未认证 401；T-C2 非管理员 PUT /settings 403；T-C7 权限三档 CanUse 逻辑（settings.go:395-410） | C ✅（三档行为切换联调 E ⛔） |
| US-M1-09 | 设置页分区 | T-15 GET /settings 假设验证（路由是否存在、降级路径、覆盖风险）；T-16 五分组 UI 静态走查 | B ✅；UI 视觉人工验收 ⚠️ |
| US-M1-10 | 搜索页与筛选 | T-17 筛选选项内置偏差声明核对（format.ts Z_FILTER_*）；T-18 历史 localStorage 上限 10 条；T-19 入口可见性 canUse | B ✅；真实搜索 E ⛔ |
| US-M1-11 | 详情面板与一键入库 | T-20 创建任务请求体（zbookId/hash/ext/libraryId）；T-21 快速入库无打断对话框；T-22 错误码→文案映射覆盖度 | B ✅ |
| US-M1-12 | 任务中心 UI | T-23 2s 轮询（抽屉打开期间）；T-24 清空历史聚合假设（循环 DELETE）；T-25 角标乐观更新 | B ✅；实时进度 E ⛔ |
| US-M1-13 | 配额展示 | T-26 quotaViewOf 解析与 /status 兼容性；T-27 配额 0 时前端拦截 | B ✅ |
| US-M1-14 | 全链路搜→入→读 | T-E1 完整链路 | ⛔ 需真实账号 |
| US-M1-15 | 错误路径文案 | T-28 错误矩阵全覆盖核对（后端 17 码 vs 前端映射表 12 码） | B ✅；真实触发 E ⛔ |
| US-M1-16 | 去重与重试专项 | T-29 (zbookId,zbookHash) 唯一索引（store_test.go 复跑）；T-30 本地删除后允许再入库（ClearZlibraryImportedBookLocal） | A ✅；端到端 E ⛔ |

## 3. 最高风险测试场景（QA 判定）

| # | 场景 | 风险点 | 验证方式 |
| --- | --- | --- | --- |
| R-Q1 | **前端调用 GET /api/zlibrary/settings**（api/zlibrary.ts:233） | 后端 routes_zlibrary.go:45 只注册 PUT；面板回显依赖 /status 降级，behavior（并发/默认书库/权限档）将回退默认值，**保存时可能覆盖真实配置** | B 静态核对 + C 运行时确认 404/405 |
| R-Q2 | **迁移版本号冲突**：架构文档称取 37，实际取 47 | 若 37~46 已在上游注册则 47 正确；若迁移表已存在不同版本集合则升级路径断裂 | C 临时目录全新迁移 + 检查 _migrations 记录 |
| R-Q3 | **错误码映射缺口**：后端产生 Z_LIB_NOT_FOUND/Z_RATE_LIMITED/Z_UPSTREAM/Z_INTERRUPTED/Z_FORBIDDEN（models.go:17-34），前端 zErrorText（format.ts:58-93）仅显式映射 12 码 | 裸码/英文堆栈暴露风险（US-M1-15 要求"无裸错误码"）；Z_INTERRUPTED 的 errorMsg 是否人话待查 | B 核对 + 引用 queue.go 实际 msg |
| R-Q4 | **权限档默认 a 档 + /status 对非授权用户 403**：前端 DesktopSidebar/DashboardTopBar 依赖 canUse/forbidden 判定入口可见性 | 普通用户打开站点即触发 403 请求（噪音/状态污染）；useZLibraryStatus 单例缓存 forbidden 后无自动恢复 | C 运行时 401/403 验证 + 前端逻辑走查 |
| R-Q5 | **并发队列与取消竞争**：Cancel 与 Complete 竞争（乐观检查 store 层 StartZlibraryTask/FailZlibraryTask 返回 ok） | 半截文件、双重扣配额、状态机漂移 | A 复跑 pipeline_test.go；真实并发 E ⛔ |
| R-Q6 | **embed 产物链**：`npm run build` 不拷贝 web/dist（仅 build:all 才拷） | 开发按任务说明只跑 build 时 embed 旧前端，出现"后端新页面 404"假缺陷 | A 实测 copy-dist 链路 |
| R-Q7 | **remix 令牌登录请求体字段名**（前端假设 remixUserid/remixUserkey） | 后端 zlibrary_handler.go:219-220 同名绑定 → 需逐字核对大小写 | B 逐字段核对 |
| R-Q8 | **重启恢复**：MarkZlibraryTasksInterrupted（store:324）将 downloading→failed | Z_INTERRUPTED 文案、临时文件清理（queue.go 启动时清理 DATA_DIR/tmp） | A 代码走查 + C 复核启动日志 |

## 4. 执行清单（本轮）

1. **A 构建**：`GOPROXY=https://goproxy.cn,direct go build ./...` / `go vet ./...` / `go test ./... -count=1`；`cd frontend && npm run build`、`npm run lint`；验证 `scripts/copy-dist.mjs` 触发方式（package.json:8-9，`build` 不含拷贝）→ 手动 copy-dist → 复跑 `go build`。
2. **B 契约**：以 handler 结构体 JSON tag ↔ 前端 api/zlibrary.ts 类型逐端点建表核对；错误码全集 diff；task/user/quota/domain 对象两端命名核对。
3. **C 运行时**：`DATA_DIR=$(mktemp -d) PORT=6799 go run ./cmd/server`（后台）→ 验证迁移 47 应用 → 未认证 /status 401 → 注册管理员后 /status 200 且结构与 useZLibraryStatus 兼容 → 非管理员 PUT /settings 403 → 停进程清目录。
4. **D 走查**：16 条故事逐条标注证据（文件:行）；`// s-library:` 抽查（后端 3 + 前端 6）；`git status --porcelain` + `git diff --stat` 比对汇报清单。
5. **E 联调清单**：产出需真实账号的用例列表（详见 QA-REPORT §6）。

## 5. 通过标准

- A：全部命令 0 退出；embed 链路闭环。
- B：11 端点逐字段一致；所有不一致记录为缺陷（含文件:行号）。
- C：迁移 47 在全新库成功应用；401/403 行为符合契约；/status 未配置凭据时响应可被前端安全解析。
- D：16 条故事均有代码证据或明确缺口记录；无越界改动。
- 缺陷分级：P0 阻断（功能不可用/数据破坏）、P1 重要（主流程受损但可绕过）、P2 一般（体验/一致性）、P3 建议。
