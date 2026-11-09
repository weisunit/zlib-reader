# 安全说明（Security Policy）

## 已评估的扫描器误报（上游继承，非本分支引入）

静态扫描器对本仓库标记了若干「高危」项，经人工评估均为误报，
全部位于**上游 NowenReader 的既有代码**（本分支未改动这些文件的相关逻辑）：

| 位置 | 扫描器判定 | 评估结论 |
| --- | --- | --- |
| `internal/archive/archive.go` 多处 | 命令注入 | `exec.CommandContext(ctx, name, args...)` 为 Go 参数数组直传，**不经 shell 解释**，文件名中的特殊字符仅作为单参数传递，无命令注入面 |
| `internal/archive/archive.go` `ContentMD5` | 弱加密算法（MD5） | MD5 用于**文件内容指纹与缓存键**（幂等去重标识），非密码学用途；更换算法会破坏既有缓存键与去重标识的兼容性 |
| `internal/handler/handler_test.go` | 硬编码凭据 | 单元测试夹具中的假凭据（`password123`），仅存在于测试进程内存，非真实凭据 |

如需修复其中的命令注入面（例如切换为参数白名单校验），应在上游仓库进行，
以免本分支与上游产生功能性分歧。

## Z-Library 插件的安全设计

- `remix_userkey` 以 AES-256-GCM 加密落库（密钥来自 `SL_ZLIB_SECRET` 或自动生成持久化，0600）
- 用户密码仅用于登录换取令牌，内存传递、不落库、不落日志
- 封面代理端点仅允许 Z-Library 系域名（域名后缀白名单，防 SSRF）
- 所有 Z-Library 上游流量仅由服务端发起，浏览器不直连

## 报告安全问题

请通过 GitHub Issues（标记 security）或邮件联系仓库所有者。
