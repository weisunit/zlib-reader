# s-library Docker 部署测试报告

> 测试日期：2026-11-09 · 测试人：项目编排（协同 QA 结论）· 结论：**✅ 可部署**

## 1. 测试范围

| 项 | 结果 |
| --- | --- |
| `docker build` 本地镜像构建（含全部 M1 改动） | ✅ 成功（358MB 未压缩） |
| `docker run` 单容器部署 | ✅ 通过 |
| `docker-compose.yml` 编排部署（`--no-build` 用本地镜像） | ✅ 通过 |
| `docker-compose up` 文档路径（含 `platforms:[amd64,arm64]` 本地构建） | ⚠️ 见 §4 注意事项 |
| 数据持久化（容器销毁重建后数据保留） | ✅ 通过 |
| 迁移链 1→47（含 Z-Library 三张表） | ✅ 通过 |
| Z-Library 端点冒烟 | ✅ 通过 |

## 2. 运行时验证记录（真实执行）

```
health:        GET /api/health → {"status":"ok","version":"0.2.0-slibrary"}   （容器启动 ~6s 即 healthy）
前端:          GET / → 200，SPA 正常服务
迁移:          [Migrate] Applied migration 47: Add Z-Library download task,
               imported book mapping and settings tables — successfully
队列:          [ZLIBRARY] download queue started (concurrency=1)
注册:          POST /api/auth/register → 200 (role=admin，首用户即管理员)
权限:          未认证 GET /api/zlibrary/settings → 401
设置回显:      GET /api/zlibrary/settings → 5 个预置域名 + hasCredentials:false
下载预检:      未连接 POST /api/zlibrary/downloads → 401 Z_AUTH_REQUIRED（µs 级）
持久化:        /data/nowen-reader.db(+WAL) 与 zlib-secret.key(0600) 落在宿主机卷，
               容器销毁重建后用户/数据完整保留
隔离性:        compose 全新实例 needsSetup:true，与旧实例数据隔离正确
```

## 3. 测试中发现并已修复的缺陷

### DOCKER-1（P2）搜索端点缺少连接前置校验
- 现象：未配置凭据时 `POST /api/zlibrary/search` 空跑 5 域名故障转移，**耗时 30s** 返回 500 + `Z_UPSTREAM`（误导为"上游故障"）；而下载端点有预检（µs 级返回 `Z_AUTH_REQUIRED`）
- 修复：`internal/handler/zlibrary_handler.go` `ZlibrarySearch` 增加 `Connected()` 前置校验 → 实测 **8ms** 返回 401 `Z_AUTH_REQUIRED`
- 顺带修复：5 处 400 参数校验误用 `CodeUpstream`（前端会显示"服务暂时不可用"而非具体原因），改为不带回退 message
- 回归：`go build/vet/test ./...` 全绿；镜像重建后容器实测通过

## 4. 环境注意事项（colima 用户必读）

本次测试环境为 macOS + colima（非 Docker Desktop），踩坑及解法：

| 问题 | 解法 |
| --- | --- |
| 缺 buildx 插件，legacy builder 不支持 Dockerfile 的 `$BUILDPLATFORM` | `brew install docker-buildx` 并链接到 `~/.docker/cli-plugins/` |
| 拉取 Docker Hub 超时 | colima.yaml `docker:` 段配置 `registry-mirrors`（daocloud/1ms/dockerproxy），`colima restart` |
| `/tmp` 不在 colima 共享路径，绑定挂载静默落空 | 卷路径必须用 `/Users/...` 下（默认仅共享 `/Users`） |
| compose `platforms:[amd64,arm64]` 本地构建 | 经典 docker driver 会触发双架构构建（QEMU 交叉编译极慢，12min+ 无产出）。**本地验证用 `docker build`（单架构）**；Docker Desktop + containerd 存储用户可走 compose 构建路径 |

## 5. 部署指引（本 fork）

```bash
# 方式 A：本地构建 + compose 编排（推荐）
docker build -t s-library:m1 --build-arg VERSION=0.2.0-slibrary .
docker tag s-library:m1 cropflre/nowen-reader:latest   # 复用 compose 文件的 image 名
docker-compose -f docker-compose.yml up -d --no-build
# → http://localhost:6680 注册管理员 → 设置 → Z-Library 配置凭据

# 方式 B：纯 docker run（与 compose 等效配置）
docker run -d --name s-library --privileged -p 6680:3000 \
  -e DATABASE_URL=/data/nowen-reader.db -e COMICS_DIR=/app/comics \
  -e NOVELS_DIR=/app/novels -e DATA_DIR=/app/.cache -e PORT=3000 \
  -v $PWD/data:/data -v $PWD/comics:/app/comics \
  -v $PWD/novels:/app/novels -v $PWD/cache:/app/.cache s-library:m1
```

## 6. 其他记录

- 镜像实测 358MB（未压缩），README"约 30MB"应指压缩传输体积，建议后续在 README 标注口径
- `.gitignore` 已补 `novels/`、`cache/`（compose 一键部署产生的用户数据目录，此前缺失）
- `web/dist/.gitkeep` 已恢复（go:embed 在无产物时构建失败问题）
- 清理完毕：测试容器/网络/临时标签已删除，`docker rmi` 临时 tag 已撤销，仓库根目录的空卷目录已移除
