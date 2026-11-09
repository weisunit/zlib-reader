# s-library UI/UX 设计文档（Z-Library 集成）

> 依据 `docs/REQUIREMENTS.md`（v0.1）与 `docs/PM-PLAN.md`（§4.2 设计师约束）产出。
> 文档版本 v1.0 · 日期 2026-11-09 · 维护角色：UI/UX 设计师 · 下游：前端工程师（§9）
> 所有新页面完全沿用 nowen-reader 现有设计语言，不引入任何新 UI 依赖库。

---

## 0. 前端现状调研结论（设计扎根点）

| 项 | 真相 |
| --- | --- |
| 目录 | `frontend/` 是唯一 React 源码（Docker stage1 构建产物拷入 `web/dist/` 由 go:embed 内嵌）；`web/` 仅 embed.go，不手写 |
| 路由 | `src/main.tsx` React Router v7；Shell 路由包 `AppShell`（桌面 dock + 移动底栏）；管理员守卫 `AdminRoute` 先例 |
| 导航 | `DesktopSidebar` 左侧悬浮 icon dock（primaryItems 数组 + adminOnly 过滤 + dock-footer）；`MobileBottomNav` <640px 底部 5 槽；首页顶部 `DashboardTopBar` |
| 布局 | `PageHeader`/`PageContent`（宽度档 form max-w-5xl / management 1400 / wide 1760 / full） |
| 状态管理 | 无 React Query/Redux。Context（auth/theme/i18n/toast）+ 自研 hooks（useComics 带 invalidateCache）+ 模块级单例 store（scraper-core subscribe/notify）+ localStorage |
| API | `lib/apiClient.ts` fetch 封装（credentials include、默认 30s 超时、`ApiError{status,message}`）；`src/api/*.ts` 每域一文件 |
| i18n | `lib/i18n/` context + `locales/zh-CN.ts`/`en.ts` 结构化对象；`useTranslation()` → `t.section.key`；注意 dock 标签目前硬编码中文，新入口必须走 i18n |
| 组件 | Toast(`useToast`)、Pagination、LazyImage、UploadDialog（fixed inset-0 z-50 居中弹窗）、ForbiddenPage、ErrorBoundary、ScrollReveal；图标 lucide-react；ComicCard 已有 grid/list 双视图 + viewMode localStorage 先例 |
| 代码风格 | "use client" 指令保留；`next/link`、`next/navigation` 经 `src/shims/next/` 映射到 react-router，新代码跟随 |

## 1. 设计基调（从 globals.css 提取）

1. **深色玻璃拟态 NAS 仪表盘**：暗色为默认主题（`html.dark`，背景 #070A0F），卡片 = `bg-card/72 + backdrop-blur` 玻璃层，浅色主题经 `html.light` 变量整体切换——新页面只许用语义 token（`bg-card`、`border-border`、`text-muted`、`bg-elevated`），禁止硬编码色值。
2. **蓝紫双主色 + 语义三色**：accent #3B82F6（主操作/选中态）、accent-purple #8B5CF6（点缀渐变，如移动端导航指示条）；success #22c55e / warning #f59e0b / danger #ef4444 用于「已入库/队列中/失败」三态。数字一律 `tabular-nums`。
3. **圆角/动效/阴影 token 化**：卡片 `radius-card 0.75rem`、面板 `radius-panel 1rem`、弹窗 `shadow-modal`；过渡 `duration-fast 150ms`（控件）/ `duration-panel 280ms`（抽屉）+ `ease-standard`；入场用 `animate-card-in`，骨架屏 `animate-pulse bg-card`。字体层级沿用 PageHeader（text-xl 标题 + text-xs muted 描述）与卡片（text-sm 标题 / text-xs 元信息）。

## 2. 信息架构

### 2.1 入口位置与理由

| 入口 | 位置 | 理由 |
| --- | --- | --- |
| 「Z-Library」搜索 | DesktopSidebar primaryItems，插在「推荐」之后、「阅读历史」之前 | 搜索是内容获取动作，与书库/推荐同层；**不用 adminOnly**——权限是三档（FR-10.2），可见性由后端 `/api/zlibrary/status` 的 `canUse` 决定，未授权用户不渲染 |
| 任务中心 | DesktopSidebar dock-footer 分隔线上方（「管理工具」之前），常驻图标 + 活动数角标 | PM 约束「全局入口、不藏设置里」；footer 区已有常驻项先例 |
| 移动端 | 不动 5 槽底栏；DashboardTopBar 右侧操作区加两个图标按钮（搜索 + 任务中心带角标） | 底栏无空槽；顶部栏是移动端既有全局操作位 |

### 2.2 页面清单

| 视图 | 形态 | 路由/挂载点 |
| --- | --- | --- |
| Z-Library 搜索页 | 整页 | `/zlibrary`（Shell 路由内，新 `ZLibraryRoute` 守卫：admin 或 canUse） |
| 书籍详情面板 | 右侧侧滑抽屉（见 §3.2） | 搜索页内浮层，不占路由 |
| 任务中心 | 全局右侧抽屉 | 挂 `main.tsx` AppShell 层，任意页面可达 |
| 设置「Z-Library」分区 | 设置页新 tab，内部五小节 | `/settings?tab=zlibrary`（仅 admin，走既有 AdminRoute 逻辑） |

## 3. 视图详细规格

### 3.1 搜索页 `/zlibrary`

```
┌──────────────────────────────────────────────────────────────┐
│ [⬇] Z-Library 搜索                 今日剩余 7/10 ▓▓▓▓▓▓▓░░░  │ ← PageHeader + QuotaBadge
├──────────────────────────────────────────────────────────────┤
│ 🔍 [ 搜索书名、作者、关键词…            ] [搜索]  [▦|≡] 视图  │
│ 语言:[全部▾] 格式:[全部▾] 排序:[最佳匹配▾]  更多筛选 ▾        │
│   └ 展开: 年份 [20__]–[20__]   清除筛选   历史: 三体 · 沙丘 × │
├──────────────────────────────────────────────────────────────┤
│ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐                  │
│ │  封面   │ │[已入库]│ │  封面   │ │[队列中]│  ← 网格视图       │
│ └────────┘ └────────┘ └────────┘ └────────┘                  │
│  书名        书名        书名        书名                      │
│  作者·2020   作者·2019   作者·1998   作者·2005                │
│  EPUB 2.1MB  MOBI 3.4MB  PDF 12MB   EPUB 1.8MB               │
│  [⬇ 入库]    [→ 查看本地] [⬇ 入库]   [⬇ 入库]                 │
│                        [ 加载更多 ]                           │
└──────────────────────────────────────────────────────────────┘
```

- **筛选栏**：搜索框 + 语言/格式/排序三个下拉常驻一行（<640px 收进「筛选」按钮展开的面板）；年份区间、清除筛选、搜索历史收进「更多筛选」折叠区（默认收起）。格式/语言选项来自 `/eapi/info` 缓存的后端端点。
- **结果卡片双视图**：沿用 books 页 `viewMode` localStorage 惯例；网格 = 封面竖卡（复用 library-book-card 结构），列表 = `rounded-xl bg-card/70 border border-white/[0.04]` 横排（复用 ComicCard 列表态结构）。卡片右上角状态徽标三态：未入库（无徽标）/ 已入库（success 徽标，点击跳 `/series/:id` 或 `/comic/:id`）/ 队列中（accent 徽标，点击开任务中心）。
- **快速入库**：列表卡片直接放「入库」按钮（PM 约束：不弹任何打断对话框），点击即创建任务 + toast「已加入任务队列」。多格式书用默认优先级 epub > pdf > mobi > azw3。
- **配额条**：header 右侧常显 `今日剩余 x/y` + 迷你进度条；剩余 ≤2 变 warning 色、为 0 变 danger 色。
- **分页**：不做页码，用「加载更多」追加（limit=20，FR-3.4），复用 ScrollReveal 做入场。

### 3.2 书籍详情面板（右侧侧滑抽屉）

```
 搜索结果列表(被遮罩压暗)                ┌────────────────────────┐
 ┌──────────────────────┐               │ 书籍详情            [×] │
 │ ┌────┐ ┌────┐        │               ├────────────────────────┤
 │ │封面│ │封面│ …      │               │ ┌────────┐ 书名        │
 │ └────┘ └────┘        │               │ │封面大图 │ 副标题      │
 └──────────────────────┘               │ │        │ 作者        │
                                        │ └────────┘ 出版社·年份 │
                                        │ 语言·格式·大小·评分     │
                                        │ ────────────────────── │
                                        │ 简介（长文本可滚动）     │
                                        │ ────────────────────── │
                                        │ 格式  (•)EPUB ( )MOBI  │
                                        │ 目标书库 [小说库     ▾] │
                                        │ ⚠ epub 无法加入漫画书库 │
                                        │ ┌────────────────────┐ │
                                        │ │        ⬇ 入库        │ │
                                        │ └────────────────────┘ │
                                        └────────────────────────┘
```

- **形态选择：抽屉而非弹窗**。理由：① 浏览比较是多书行为，抽屉保留结果列表可见，点击其他卡片可原位切换详情；② Z-Library 描述是长文本，右侧 480px（md 以上 560px）纵向滚动天然适配；③ 入库是轻量高频操作，居中弹窗打断感过重（现有 UploadDialog 弹窗仅用于单次上传，语义不符）。移动端抽屉改为底部上滑全屏（圆角顶边，符合 `duration-panel` 动效）。
- **格式选择器**：单选 chips，来自 `/formats` 端点；仅单格式时不显示。默认按 epub > pdf > mobi > azw3 预选。
- **目标书库选择器**：下拉，选项 = 用户 canManage 的书库（复用 `fetchAccessibleLibraries`），默认 = 当前格式扩展名匹配类型的第一个书库（FR-4.1），选择写入 localStorage 记忆。格式与书库类型不匹配时显示 danger 警告并禁用入库按钮（提示换书库，不提供「强制」——M1 从严）。
- **「入库」按钮状态机**：

| 状态 | 视觉 | 行为 |
| --- | --- | --- |
| 可点击 | `bg-accent text-white`，hover 提升 | 点击 → 前置校验 → 创建任务 |
| 排队中/下载中 | `bg-accent/10 text-accent` + Loader2 旋转 + 「已加入队列」 | 禁用，点击徽标跳任务中心 |
| 已入库 | success 描边 + 「已入库 · 查看本地」 | 按钮变跳转本地条目链接 |
| 配额不足/未连接/格式冲突 | `bg-card text-muted` 禁用态 | 禁用 + 下方 danger/warning 行内原因与下一步动作（重连 → 设置；超限 → 显示重置时间） |

### 3.3 任务中心（全局抽屉）

```
 dock                          ┌──────────────────────────────┐
 ┌──┐                          │ 下载任务 (2)       清空 [×]   │
 │🏠│                          ├──────────────────────────────┤
 │📚│                          │ 三体 - 刘慈欣        EPUB     │
 │❤️│                          │ ▓▓▓▓▓▓▓░░░ 63%  下载中        │
 │⋯ │                          │ 目标: 小说库 · 发起: 我       │
 ├──┤                          │ [取消]                       │
 │⬇³│ ← 任务图标 + 角标         ├──────────────────────────────┤
 │⚙️│                          │ 沙丘 - …              ✗ 失败  │
 └──┘                          │ 访问被拦截，请在浏览器打开 …   │
                               │ [重试] [删除记录]             │
                               ├──────────────────────────────┤
                               │ 挪威的森林          ✓ 已完成  │
                               │ → 查看书籍 · 14:32           │
                               └──────────────────────────────┘
```

- 入口：dock-footer 图标（lucide `CloudDownload`）+ danger 圆点角标（活动任务数，>9 显示 9+）；移动端挂 DashboardTopBar。抽屉宽 400px，右侧滑入，与详情抽屉同动效参数。
- **任务卡片**：标题（截断）+ 格式徽标；进行中 = 进度条（accent）+ 阶段文案（获取详情/下载中/校验中/落盘中/扫描中，映射 stage 字段）+ [取消]；失败 = danger 描边 + §7 人话文案 + [重试] [删除记录]，`Z_SCAN_FAILED` 额外给 [手动扫描] 按钮；完成 = success + [查看书籍]；排队中 = [取消]。管理员看全部任务时卡片附发起人名（FR-6.4 基础）。
- **刷新**：抽屉打开时 2s 轮询（`useZLibraryTasks`），关闭即停；角标由全局状态驱动，任务完成后触发 toast「《书名》已入库」，点击跳书籍详情。
- **空状态**：居中插画位（CloudDownload 图标）+「暂无下载任务」+「去 Z-Library 搜一本书试试」链接。

### 3.4 设置页「Z-Library」分区（仅管理员）

```
 设置 · 分组「自动化」 · Tab: Z-Library（icon Globe2，排在 AI 服务之前）
 ┌──────────────────────────────────────────────────────────────┐
 │ ① 连接   状态: ● 已连接  test@example.com · 免费 · 7/10      │
 │          凭据方式: (•) 邮箱+密码   ( ) remix 令牌             │
 │          邮箱 [________________]  密码 [●●●●●●●●] (不回显)    │
 │          remix_userid [______]  remix_userkey [______] 粘贴框 │
 │          获取指引: 打开 z-lib.sk → F12 → Cookie 复制 (链接)   │
 │          [测试连接] [保存凭据] [解绑]（二次确认弹窗）          │
 │ ② 域名   ☰ z-lib.sk   [默认] · ● 可用 · 3 分钟前       [删]  │
 │          ☰ z-lib.fm          ○ 未探测                 [删]  │
 │          [＋ 添加域名]  [健康检查]   当前生效: z-lib.sk        │
 │          个人下载域名: dl-xxx.example.com（自动获取，只读）    │
 │ ③ 下载行为   并发数 [1▾]   默认目标书库 [小说库▾]             │
 │ ④ 权限       允许使用范围 [仅管理员▾]（三档说明文字）          │
 │ ⑤ 兜底指引   全部域名不可达时，向 blackbox@z-library.so 发任意 │
 │              邮件获取新域名。[复制邮箱] / 手动填入: [____▾]    │
 └──────────────────────────────────────────────────────────────┘
```

- 五个小节用 `rounded-2xl border bg-card/80` 卡片分区（对齐 LibraryManagementPanel 面板惯例），节标题 text-sm font-semibold + 编号。
- **凭据安全交互**：密码 `type="password"` 永不回显（已连接时只显示「已加密保存」占位）；remix token 用等宽字体粘贴框，粘贴时 trim；「解绑」复用 UploadDialog 式居中确认弹窗；连接失败时状态行变 danger + 重连引导。
- 表单控件统一 `rounded-xl border-border bg-card px-3 py-2.5 text-sm focus:border-accent/50 focus:ring-2 focus:ring-accent/20`；域名行拖拽排序即优先级（复用 ResizeDivider 场景外简单 HTML5 drag 或上下移按钮，M1 用 ↑↓ 按钮降低实现成本）。

## 4. 关键交互流

### 4.1 搜索 → 入库 → 查看任务 → 跳转书库

1. 用户经 dock 进入 `/zlibrary`，页面即从 `/status` 拉连接与配额（QuotaBadge 常显）。
2. 输入关键词 + 筛选 → 搜索；结果卡片批量带去重标注（未入库/已入库/队列中）。
3. 点卡片「入库」（或详情面板内选格式/书库后入库）→ 前置校验失败则行内报 §7 文案；成功 → toast「已加入任务队列」+ 卡片徽标变「队列中」。
4. 用户点 dock 任务图标（角标 +1）→ 抽屉看进度条与阶段；完成后 toast「《书名》已入库」→ 点通知或任务卡「查看书籍」→ 跳 `/series/:id` 阅读页。全程未离开平台。

### 4.2 失败重试流

任务卡「重试」→ 后端重新前置校验（配额/登录态/去重）→ 通过则回队列、卡片回「排队中」；校验失败则卡片直接显示原因（如配额用尽 + 重置时间）。`Z_QUOTA_EXCEEDED` 的重试按钮文案在次日重置前显示「次日可重试」并禁用。重试成功不重复扣配额（未发起下载的失败）。

### 4.3 重复入库拦截

搜索结果渲染时批量查询去重状态：已入库卡片无「入库」按钮，仅「查看本地」；队列中卡片入库按钮禁用并标「已在队列中」。若用户在详情面板仍触发（竞态），后端 `Z_DUP_IMPORTED/Z_DUP_QUEUED` 兜底 → toast 提示 + 「点击查看」跳本地条目/任务中心。

## 5. 状态清单（loading / empty / error / disabled 矩阵）

| 视图 | loading | empty | error（§7 文案） | disabled |
| --- | --- | --- | --- | --- |
| 搜索页首屏 | 骨架卡片 ×8（animate-pulse） | 「输入关键词开始搜索」引导 | `Z_AUTH_REQUIRED`「Z-Library 未连接，请在设置中登录」+ [去设置]；`Z_DOMAIN_UNREACHABLE`「所有 Z-Library 域名均无法访问…」+ 兜底指引；`Z_BLOCKED`「访问被拦截…」+ [重试]；15s 超时「搜索超时，请重试」 | 未授权 → ForbiddenPage；未连接时搜索框禁用 + 顶部重连横幅 |
| 结果区 | 追加时按钮 Loader2 | 「未找到匹配结果」+「试试其他关键词」 | 同上（换关键词建议区分） | 配额为 0：入库按钮全灰 +「今日下载额度已用完（x/y），将于每日 xx 重置」 |
| 详情面板 | 骨架 + 封面 LazyImage 模糊占位 | — | `Z_NOT_FOUND`「该书在 Z-Library 已不可用」，面板内错误条 | 格式冲突：入库禁用 +「epub 无法加入漫画书库」；`Z_SIZE_LIMIT` 超上限禁用 + 文案 |
| 任务中心 | 首次开抽屉骨架 ×3 | 「暂无下载任务」+ 去搜索引导 | 每条失败卡显示对应错误码文案（§7 全量）+ 重试/删除 | 排队中无重试；completed 无取消；`Z_QUOTA_EXCEEDED` 重试禁用至次日 |
| 设置分区 | LoadingSkeleton（dynamic 惯例） | 未配置：连接节展开为引导态 | `Z_AUTH_FAILED`「登录失败：邮箱或密码错误」；保存失败 toast | 非管理员不渲染 tab；已连接时密码框禁用输入 |

## 6. i18n 词条表（命名空间 `t.zlibrary.*`，zh/en 成对，共 135 条）

| key | zh | en |
| --- | --- | --- |
| navLabel | Z-Library | Z-Library |
| tasksNavLabel | 下载任务 | Downloads |
| searchTitle | Z-Library 搜索 | Z-Library Search |
| searchDescription | 搜书并一键入库到本地书库 | Search and import books to your library |
| searchPlaceholder | 搜索书名、作者、关键词… | Search title, author, keyword… |
| searchButton | 搜索 | Search |
| searching | 搜索中… | Searching… |
| quotaRemaining | 今日剩余 | Remaining today |
| quotaExhaustedShort | 额度已用完 | Quota exhausted |
| filter.language | 语言 | Language |
| filter.format | 格式 | Format |
| filter.sort | 排序 | Sort by |
| filter.yearFrom | 出版年起 | Year from |
| filter.yearTo | 出版年止 | Year to |
| filter.more | 更多筛选 | More filters |
| filter.less | 收起筛选 | Fewer filters |
| filter.clear | 清除筛选 | Clear filters |
| sort.bestmatch | 最佳匹配 | Best match |
| sort.newest | 最新 | Newest |
| sort.popular | 最受欢迎 | Most popular |
| view.grid | 网格 | Grid |
| view.list | 列表 | List |
| results.loadMore | 加载更多 | Load more |
| results.noMore | 没有更多了 | No more results |
| results.count | 共 {n} 项 | {n} results |
| status.notImported | 未入库 | Not imported |
| status.imported | 已入库 | Imported |
| status.queued | 队列中 | Queued |
| status.viewLocal | 查看本地 | View local |
| history.title | 搜索历史 | Search history |
| history.clear | 清空历史 | Clear history |
| empty.searchReady | 输入关键词开始搜索 | Type a keyword to start |
| empty.noResults | 未找到匹配结果 | No results found |
| empty.tryDifferent | 试试其他关键词 | Try different keywords |
| error.authRequired | Z-Library 未连接，请在设置中登录 | Z-Library is not connected. Sign in from Settings |
| error.authFailed | 登录失败：邮箱或密码错误 | Sign-in failed: incorrect email or password |
| error.quotaExceeded | 今日下载额度已用完（{x}/{y}），将于每日 {time} 重置 | Daily quota used up ({x}/{y}), resets at {time} |
| error.sizeLimit | 该文件超过当前账号的大小上限 | File exceeds your account's size limit |
| error.domainUnreachable | 所有 Z-Library 域名均无法访问，可尝试邮件获取新域名 | All Z-Library domains are unreachable; get a new domain by email |
| error.blocked | 访问被拦截，请在浏览器打开 {domain} 完成验证，或稍后重试 | Blocked; open {domain} in a browser to verify, or retry later |
| error.notFound | 该书在 Z-Library 已不可用 | This book is no longer available on Z-Library |
| error.extMismatch | {ext} 无法加入{libraryType} | {ext} cannot be added to {libraryType} |
| error.diskFull | 目标书库所在磁盘空间不足 | Not enough disk space on the target library |
| error.dupImported | 已入库，点击查看 | Already imported, view it |
| error.dupQueued | 已在下载队列中 | Already in the download queue |
| error.scanFailed | 文件已下载但入库失败，请手动扫描或查看日志 | Downloaded but import failed; scan manually or check logs |
| error.timeout | 请求超时，请重试 | Request timed out, please retry |
| error.quotaConsumed | 已消耗今日配额，次日恢复后可重试 | Daily quota consumed; retry after reset |
| action.goSettings | 去设置 | Go to Settings |
| action.retry | 重试 | Retry |
| action.retryTomorrow | 次日可重试 | Retry after reset |
| action.cancel | 取消 | Cancel |
| action.removeRecord | 删除记录 | Remove record |
| action.scanNow | 手动扫描 | Scan now |
| detail.title | 书籍详情 | Book details |
| detail.author | 作者 | Author |
| detail.publisher | 出版社 | Publisher |
| detail.year | 年份 | Year |
| detail.language | 语言 | Language |
| detail.format | 格式 | Format |
| detail.size | 大小 | Size |
| detail.rating | 评分 | Rating |
| detail.description | 简介 | Description |
| detail.expandDesc | 展开全部 | Show more |
| detail.collapseDesc | 收起 | Show less |
| detail.formatSelector | 选择格式 | Format |
| detail.librarySelector | 目标书库 | Target library |
| detail.import | 入库 | Import |
| detail.importQueued | 已加入任务队列 | Added to download queue |
| detail.imported | 已入库 | Imported |
| detail.viewTask | 查看任务 | View task |
| detail.notConnectedHint | 未连接，请先在设置中登录 | Not connected; sign in from Settings |
| detail.quotaHint | 今日额度已用完 | Daily quota exhausted |
| tasks.title | 下载任务 | Downloads |
| tasks.activeCount | {n} 个进行中 | {n} active |
| tasks.emptyTitle | 暂无下载任务 | No downloads yet |
| tasks.emptyHint | 去 Z-Library 搜一本书试试 | Search a book on Z-Library |
| tasks.clearHistory | 清空历史 | Clear history |
| tasks.by | 发起人 | Initiated by |
| tasks.stage.fetchInfo | 获取详情 | Fetching info |
| tasks.stage.downloading | 下载中 | Downloading |
| tasks.stage.validating | 校验中 | Validating |
| tasks.stage.moving | 落盘中 | Moving file |
| tasks.stage.scanning | 扫描中 | Scanning |
| tasks.status.queued | 排队中 | Queued |
| tasks.status.completed | 已完成 | Completed |
| tasks.status.failed | 失败 | Failed |
| tasks.status.canceled | 已取消 | Canceled |
| tasks.viewBook | 查看书籍 | View book |
| tasks.importedToast | 《{title}》已入库 | "{title}" imported |
| settings.tabLabel | Z-Library | Z-Library |
| settings.section.connection | 连接 | Connection |
| settings.section.domain | 域名 | Domains |
| settings.section.download | 下载行为 | Download behavior |
| settings.section.permission | 权限 | Permissions |
| settings.section.fallback | 兜底指引 | Fallback guide |
| settings.status.connected | 已连接 | Connected |
| settings.status.disconnected | 已断开 | Disconnected |
| settings.status.notConfigured | 未配置 | Not configured |
| settings.credential.email | 邮箱 | Email |
| settings.credential.password | 密码 | Password |
| settings.credential.passwordHint | 仅用于登录换取令牌，用后即弃、不落盘 | Used once to obtain a token, never stored |
| settings.credential.encrypted | 凭据已加密保存 | Credentials saved (encrypted) |
| settings.credential.modePassword | 邮箱 + 密码 | Email + password |
| settings.credential.modeToken | remix 令牌 | remix token |
| settings.credential.remixUserId | remix_userid | remix_userid |
| settings.credential.remixUserKey | remix_userkey | remix_userkey |
| settings.credential.tokenHint | 打开 Z-Library 站点 → 浏览器开发者工具 → Application → Cookies 复制 | Open Z-Library → DevTools → Application → Cookies, then copy |
| settings.action.testConnection | 测试连接 | Test connection |
| settings.action.testing | 测试中… | Testing… |
| settings.action.unbind | 解绑 | Unbind |
| settings.unbindConfirm | 确认解绑 Z-Library 账号？进行中的任务将失败 | Unbind the Z-Library account? Active tasks will fail |
| settings.accountType.free | 免费 | Free |
| settings.accountType.donor | 捐赠者 | Donor |
| settings.domain.add | 添加域名 | Add domain |
| settings.domain.default | 默认 | Default |
| settings.domain.setDefault | 设为默认 | Set default |
| settings.domain.healthCheck | 健康检查 | Health check |
| settings.domain.healthy | 可用 | Healthy |
| settings.domain.unhealthy | 不可用 | Unhealthy |
| settings.domain.unknown | 未探测 | Not checked |
| settings.domain.current | 当前生效 | Active domain |
| settings.domain.personal | 个人下载域名（自动获取） | Personal download domain (auto) |
| settings.domain.dragHint | 上下移动调整优先级 | Order sets priority |
| settings.download.concurrency | 下载并发数 | Concurrent downloads |
| settings.download.defaultLibrary | 默认目标书库 | Default target library |
| settings.permission.scope | 允许使用 Z-Library 的用户范围 | Who can use Z-Library |
| settings.permission.adminsOnly | 仅管理员 | Admins only |
| settings.permission.canManage | 拥有书库管理权限的用户 | Users with library manage rights |
| settings.permission.all | 所有登录用户 | All signed-in users |
| settings.fallback.desc | 全部域名不可达时，向 blackbox@z-library.so 发送任意邮件，系统将自动回复最新可用域名 | If all domains fail, email blackbox@z-library.so for the latest domain |
| settings.fallback.copyEmail | 复制邮箱 | Copy email |
| settings.fallback.emailCopied | 已复制 | Copied |
| settings.fallback.addDomain | 手动填入新域名 | Enter new domain manually |
| settings.saved | 已保存 | Saved |

## 7. 响应式断点

| 断点 | 布局 |
| --- | --- |
| ≥640px（桌面，sm+） | 桌面 dock 常驻；筛选一行展开；网格 4–6 列（沿用 books 网格列数惯例）；详情/任务抽屉右滑 480–560px/400px |
| 640–1024px（平板） | 同桌面但网格 3–4 列；详情抽屉 480px；筛选可收进「筛选」按钮 |
| <640px（窄屏） | dock 隐藏、底栏照旧；入口移至 DashboardTopBar 图标；筛选全部收进展开面板，结果单列列表态优先；详情面板与任务抽屉改为底部上滑全屏（`pb-16` 预留底栏高度，阅读器/详情页隐藏惯例保持） |

## 8. 交互统一细则

- 所有反馈走 `useToast()`（success/error/warning/info 四型已有），错误 toast 必须携带「下一步动作」按钮或行内链接，禁止裸错误码（PM §4.2）。
- 抽屉统一参数：遮罩 `bg-overlay`（rgba(0,0,0,.6)）、面板 `bg-elevated border-l border-border shadow-modal`、时长 `duration-panel` + `ease-standard`、Esc/点遮罩关闭。
- 数字（进度、配额、大小）一律 `tabular-nums`；进度条高度 h-1.5 rounded-full。
- 已入库徽标是唯一跳本地条目的入口，必须可点击（不同于装饰徽标）。

## 9. 给前端开发的实现提示

### 9.1 组件拆分清单（文件名级别）

```
frontend/src/
├── api/zlibrary.ts                      # 全部端点封装（复用 lib/apiClient，超时搜索 15s）
├── hooks/useZLibraryStatus.ts           # 连接/配额/域名状态（模块级单例 + invalidate，参照 useComics 缓存模式）
├── hooks/useZLibraryTasks.ts            # 任务列表 + 2s 轮询 + 角标全局状态（抽屉卸载即停）
├── app/zlibrary/page.tsx                # 搜索页（lazy import 注册进 main.tsx Shell 路由）
├── components/zlibrary/
│   ├── QuotaBadge.tsx                   # 配额条（搜索页 header 与设置页复用）
│   ├── SearchFilterBar.tsx              # 筛选栏 + 更多筛选折叠 + 历史
│   ├── ZBookCard.tsx                    # 结果卡片（grid/list 两态，参照 ComicCard 双模式）
│   ├── ZBookDetailDrawer.tsx            # 详情侧滑抽屉
│   ├── FormatSelector.tsx               # 格式单选 chips
│   ├── LibrarySelector.tsx              # 目标书库下拉（复用 fetchAccessibleLibraries）
│   ├── ImportButton.tsx                 # 入库按钮状态机（§3.2 表）
│   ├── TaskCenterDrawer.tsx / TaskCard.tsx / TaskCenterBadge.tsx
├── components/ZLibrarySettingsPanel.tsx # 设置面板（settings page dynamic import + LoadingSkeleton）
```

### 9.2 必须修改的现有文件（集中入口，标注 `// s-library:`）

`main.tsx`（路由 + TaskCenterDrawer 全局挂载）、`DesktopSidebar.tsx`（primaryItems 一项 + dock-footer 任务按钮，**新标签必须走 i18n**）、`DashboardTopBar.tsx`（移动端两个图标位）、`app/settings/page.tsx`（SettingsTab union + groups「自动化」组 + lazy panel）、`lib/i18n/locales/zh-CN.ts` 与 `en.ts`（新增 `zlibrary` 命名空间，两文件同步提交）。其余全部为新增文件，遵循需求 §6.4 隔离策略。

### 9.3 避免自造轮子

复用 `Toast`、`PageHeader/PageContent`、`LazyImage`（封面）、`ForbiddenPage`（未授权态）、`ErrorBoundary`、`ScrollReveal`、`Pagination` 不适用则不引（用加载更多）；弹窗确认复用 UploadDialog 的 `fixed inset-0 z-50` 模式；图标全部 lucide-react（推荐 Download/CloudDownload/Globe2/ListOrdered/RefreshCw/X）；数据获取自研 hook（项目无 React Query，勿引入）；`next/link`、`next/navigation` 经既有 shims 使用。

### 9.4 验收自查清单

配额常显于搜索页顶部；密码/remix 输入框不回显；列表卡片快速入库无打断弹窗；结果三态徽标视觉可区分且「已入库」可点击跳转；每个错误文案 zh/en 成对且带下一步动作；窄屏详情/任务为底部上滑全屏。
