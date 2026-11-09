# Z-Library 插件架构与上游升级手册

> ZlibReader 对上游 [NowenReader](https://github.com/cropflre/nowen-reader) 的升级兼容性设计。

## 设计原则

Z-Library 集成是一个**代码级插件模块**（Go 无跨平台运行时插件机制，采用"自包含模块 + 行级接线点"方案）：

1. **纯新增文件零冲突** —— 插件的绝大多数代码位于全新文件，`git merge` 上游时不会产生任何冲突；
2. **上游修改面最小化** —— 对上游已有文件的改动压缩到 **后端 3 处 + 前端 4 个文件**的行级接线点，且全部带 `// s-library:` 注释标记；
3. **迁移独立注册** —— 插件的三张表迁移放在独立文件，以 `Migrations = append(...)` 注册（与上游自身的 `migrate_*.go` 惯例一致），上游在其迁移切片中新增条目不会与本插件冲突；
4. **可拆卸** —— 删除纯新增目录 + 移除接线点即可完全摘除插件，不影响上游功能。

## 文件清单

### 纯新增（升级 merge 零冲突）

```
internal/zlibrary/                        # eapi 适配层（client/auth/search/book/download/models/crypto + 单测）
internal/zlibrary/service/                # settings/quota/dedup/queue/pipeline
internal/store/zlibrary_store.go          # 三张表 CRUD
internal/store/zlibrary_store_test.go
internal/store/migrate_zlibrary_plugin.go # 迁移 Version 47（独立注册）
internal/model/zlibrary.go
internal/handler/zlibrary_handler.go
internal/handler/zlibrary_handler_test.go
internal/handler/routes_zlibrary.go
frontend/src/api/zlibrary.ts
frontend/src/hooks/useZLibraryStatus.ts
frontend/src/hooks/useZLibraryTasks.ts
frontend/src/app/zlibrary/page.tsx
frontend/src/components/zlibrary/         # 11 个组件 + format.ts
frontend/src/components/ZLibrarySettingsPanel.tsx
docs/PLUGIN.md / docs/DOCKER-TEST.md / docs/REQUIREMENTS.md
```

### 接线点（升级时需核对的全部上游改动）

| # | 文件 | 位置 | 内容 | 冲突风险 |
| --- | --- | --- | --- | --- |
| 1 | `internal/handler/router.go` | `SetupRoutes` 末尾 | `registerZlibraryRoutes(api)` 1 行 | 极低 |
| 2 | `cmd/server/main.go` | import 块 + 队列启动 | `zlibservice` import + `StartQueue()` 2 行 | 极低 |
| 3 | `frontend/src/main.tsx` | 5 处 `// s-library:` | 路由/守卫/抽屉挂载 | 低 |
| 4 | `frontend/src/components/DesktopSidebar.tsx` | 5 处 | dock 入口 + 任务角标 | 低 |
| 5 | `frontend/src/components/DashboardTopBar.tsx` | 5 处 | 移动端入口 | 低 |
| 6 | `frontend/src/app/settings/page.tsx` | 5 处 | 设置 tab + 面板注册 + 关于页二开声明 | 低 |
| 7 | `frontend/src/lib/i18n/locales/zh-CN.ts` / `en.ts` | 尾部 `zlibrary` 块 + `about` 区块 | 约 150 个词条（纯新增键） | 低 |
| 8 | `frontend/index.html` / `public/manifest.json` / `useSiteSettings.ts` 等 | 品牌名 ZlibReader | 更名点 | 极低 |
| 9 | `README.md` | 整文件 | 本 fork 自有 README（上游版保留为 `README.upstream.md`） | 预期冲突，直接取本分支版本 |
| 10 | `docker-compose.yml` | environment 段 | `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY` + 注释 | 低 |
| 11 | `.gitignore` | 新增 `novels/`、`cache/` | compose 挂载目录 | 极低 |

> 除上表之外无任何对上游文件的修改。上游大版本重构上述文件时，冲突解决原则：**保留上游的新结构，把带 `// s-library:` 标记的行按语义重新放入新位置**。

## 上游升级操作手册

```bash
# 0. 建议分支：main 跟随 zlib-reader 开发，upstream/main 跟随原作者
git remote add upstream https://github.com/cropflre/nowen-reader.git
git fetch upstream

# 1. 合并上游
git merge upstream/main
#   预期冲突点：README.md（取本分支）、上述接线点文件（小冲突，按标记重放）
#   纯新增文件不会出现在冲突列表

# 2. 核对接线点（逐条 grep，标记行是否仍在正确语义位置）
grep -rn "s-library:" internal/ cmd/ frontend/src/ | wc -l    # 数量应 >= 升级前
grep -n "registerZlibraryRoutes" internal/handler/router.go
grep -n "StartQueue" cmd/server/main.go
grep -c "s-library" frontend/src/main.tsx frontend/src/components/DesktopSidebar.tsx \
  frontend/src/components/DashboardTopBar.tsx frontend/src/app/settings/page.tsx

# 3. 上游若新增迁移：版本号若与 47 冲突
#    _migrations 表以 version 为主键。若上游也注册了 47（不太可能，上游版本号连续递增），
#    将 migrate_zlibrary_plugin.go 的 Version 改为未占用值，并对已应用库手工执行
#    UPDATE "_migrations" SET version=<新值> WHERE version=47;（仅当 47 已应用且语义一致）

# 4. 构建与测试
GOPROXY=https://goproxy.cn,direct go build ./... && GOPROXY=https://goproxy.cn,direct go test ./...
cd frontend && npm run build:all && npm run lint

# 5. 冒烟清单（真实账号）
#    设置 → Z-Library：测试连接（配额显示 x/10）
#    搜索 → 详情 → 入库（一本 epub + 一本 cbz）→ 任务中心 completed
#    书库出现两本书且可阅读
```

## 可拆卸性

完全移除 Z-Library 插件：

1. 删除「纯新增」清单中的全部文件；
2. 移除接线点表中 1~6 的标记行；
3. 移除 i18n 中的 `zlibrary` 命名空间与 `about` 二开声明卡；
4. 数据库清理（可选）：`DROP TABLE ZlibraryDownloadTask; DROP TABLE ZlibraryImportedBook; DELETE FROM ZlibrarySetting;`（应用前先停服）。

上游功能不受任何影响。
