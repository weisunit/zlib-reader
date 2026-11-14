// s-library: 新增 — Z-Library API 封装（来源 docs/ARCHITECTURE.md §4 十一端点契约 + docs/UI-DESIGN.md §9.1）
// 沿用 libraries.ts 的 apiPath + safeJson + 错误 code 字段模式；搜索超时 15s。
import { apiPath } from "@/lib/base-path";

// ============================================================
// 类型（与后端契约 §4 schema 一一对应，camelCase）
// ============================================================

export type ZDedupStatus = "none" | "imported" | "queued";

export interface ZQuota {
  used: number;
  total: number;
  /** ISO 时间字符串，配额重置时间（可能缺省） */
  resetAt?: string;
}

export interface ZDomainHealth {
  url: string;
  enabled: boolean;
  isDefault: boolean;
  healthy: boolean;
  lastCheckedAt: string | null;
  latencyMs: number | null;
}

export interface ZLibraryStatus {
  connected: boolean;
  accountName?: string;
  accountType?: string;
  quota?: ZQuota;
  currentDomain?: string;
  personalDomain?: string;
  domains?: ZDomainHealth[];
  /** 防御性字段：契约未定，若后端提供则优先使用；否则以 HTTP 200/403 判定 */
  canUse?: boolean;
}

export interface ZBook {
  zbookId: string;
  hash: string;
  title: string;
  subtitle?: string;
  author?: string;
  publisher?: string;
  year: number | null;
  language?: string;
  ext?: string;
  fileSize: number;
  rating?: number | null;
  coverUrl?: string;
  dedupStatus: ZDedupStatus;
  /** 仅 dedupStatus === "imported" 时存在 */
  localBookId?: string;
}

export type ZSearchOrder = "bestmatch" | "newest" | "rating";

export interface ZSearchRequest {
  message: string;
  languages?: string[];
  extensions?: string[];
  yearFrom?: number | null;
  yearTo?: number | null;
  order?: ZSearchOrder;
  page?: number;
  limit?: number;
}

export interface ZSearchResponse {
  total: number;
  page: number;
  limit: number;
  books: ZBook[];
}

export interface ZBookFormat {
  ext: string;
  fileSize: number;
}

export interface ZBookDetail extends ZBook {
  description?: string;
  formats?: ZBookFormat[];
  dlDomain?: string;
}

export type ZTaskStatus = "queued" | "downloading" | "completed" | "failed" | "canceled";
export type ZTaskStage = "fetch_info" | "downloading" | "validating" | "moving" | "scanning" | "";

export interface ZTask {
  id: number;
  userId: string;
  /** 管理员可见的发起人用户名 */
  user?: string;
  zbookId: string;
  zbookHash: string;
  title: string;
  author?: string;
  ext?: string;
  fileSize?: number;
  libraryId?: string;
  localBookId?: string;
  status: ZTaskStatus;
  progress: number;
  stage: ZTaskStage;
  errorCode?: string;
  errorMsg?: string;
  quotaConsumed?: boolean;
  retryCount?: number;
  createdAt: string;
  finishedAt?: string | null;
}

export interface ZSettingsDomainInput {
  url: string;
  enabled: boolean;
  isDefault: boolean;
}

export type ZAllowScope = "admin" | "canManage" | "all";

export interface ZSettingsPayload {
  /** 顺序即优先级 */
  domains: ZSettingsDomainInput[];
  behavior: {
    concurrency: number;
    defaultLibraryId: string;
    allowScope: ZAllowScope;
  };
  /** s-library: 浏览器辅助通行（下载通道）。cookieString 空=保留已存；clear=true 显式清除 */
  browserAssist?: {
    cookieString?: string;
    userAgent?: string;
    clear?: boolean;
  };
}

// ============================================================
// 错误码（契约 §4 + 需求 §7 错误矩阵）
// ============================================================

export const Z_ERROR_CODES = {
  authRequired: "Z_AUTH_REQUIRED",
  authFailed: "Z_AUTH_FAILED",
  quotaExceeded: "Z_QUOTA_EXCEEDED",
  sizeLimit: "Z_SIZE_LIMIT",
  domainUnreachable: "Z_DOMAIN_UNREACHABLE",
  blocked: "Z_BLOCKED",
  notFound: "Z_NOT_FOUND",
  extMismatch: "Z_EXT_MISMATCH",
  diskFull: "Z_DISK_FULL",
  dupImported: "Z_DUP_IMPORTED",
  dupQueued: "Z_DUP_QUEUED",
  scanFailed: "Z_SCAN_FAILED",
  forbidden: "Z_FORBIDDEN",
  interrupted: "Z_INTERRUPTED",
  // s-library: DEF-2 补齐后端扩展码（internal/zlibrary/models.go 全集，含并行新增的 Z_INVALID_STATE）
  libNotFound: "Z_LIB_NOT_FOUND",
  rateLimited: "Z_RATE_LIMITED",
  upstream: "Z_UPSTREAM",
  invalidState: "Z_INVALID_STATE",
} as const;

export class ZLibraryApiError extends Error {
  status: number;
  code?: string;
  /** Z_DUP_IMPORTED 时附带的本地条目 id */
  localBookId?: string;

  constructor(message: string, status: number, details?: { code?: string; localBookId?: string }) {
    super(message);
    this.name = "ZLibraryApiError";
    this.status = status;
    this.code = details?.code;
    this.localBookId = details?.localBookId;
  }
}

export async function safeJson<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    let code: string | undefined;
    let localBookId: string | undefined;
    try {
      const data = await res.json();
      msg = data.error || msg;
      if (typeof data.code === "string") code = data.code;
      if (typeof data.localBookId === "string") localBookId = data.localBookId;
    } catch {
      // ignore non-JSON error body
    }
    throw new ZLibraryApiError(msg, res.status, { code, localBookId });
  }
  return res.json() as Promise<T>;
}

/** 带超时的 fetch（AbortController 合并外部 signal），参照 apiClient.ts 的超时策略；内部统一走 apiPath */
export async function fetchWithTimeout(
  url: string,
  init: RequestInit = {},
  timeoutMs = 30000
): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  const signal = init.signal
    ? AbortSignal.any?.([init.signal, controller.signal]) || controller.signal
    : controller.signal;
  try {
    return await fetch(apiPath(url), { ...init, signal });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError" && !init.signal?.aborted) {
      throw new ZLibraryApiError("timeout", 0);
    }
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

export function jsonInit(method: string, body: unknown, signal?: AbortSignal): RequestInit {
  return {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  };
}

// ============================================================
// 端点封装（对应契约 1~11）
// ============================================================

/** (1) GET /api/zlibrary/status —— 连接状态 + 配额 + 域名健康 */
export async function fetchZLibraryStatus(signal?: AbortSignal): Promise<ZLibraryStatus> {
  const res = await fetchWithTimeout("/api/zlibrary/status", { signal });
  return safeJson<ZLibraryStatus>(res);
}

/**
 * GET /api/zlibrary/settings —— 读取域名/行为/权限配置（admin，后端并行补齐中，QA DEF-1）。
 * 响应与 PUT 请求体同构（domains/behavior），另含 hasCredentials 布尔表示是否已配置凭据；
 * 秘密字段（password/remixUserkey）不回传（空串），前端据此显示「已加密保存」占位且不发送空串覆盖。
 */
export interface ZSettingsSnapshot extends ZSettingsPayload {
  hasCredentials?: boolean;
  /** s-library: cookie 串不回传，仅回显配置状态 + UA */
  browserAssist?: { configured?: boolean; userAgent?: string };
  /** s-library: 通行证自动同步令牌（用于生成油猴脚本） */
  assistSyncToken?: string;
}

export async function fetchZLibrarySettings(): Promise<ZSettingsSnapshot> {
  const res = await fetchWithTimeout("/api/zlibrary/settings");
  return safeJson<ZSettingsSnapshot>(res);
}

/** (2) PUT /api/zlibrary/settings —— 保存域名/行为/权限配置（admin） */
export async function saveZLibrarySettings(payload: ZSettingsPayload): Promise<void> {
  const res = await fetchWithTimeout("/api/zlibrary/settings", jsonInit("PUT", payload));
  await safeJson<{ success: boolean }>(res);
}

/**
 * (3) POST /api/zlibrary/auth/test —— 测试连接 / 保存凭据
 * - 传凭据：登录换令牌并保存（密码用后即弃）
 * - 空对象：用已存令牌验证
 * - remix 令牌方式契约未明确 body 形状，按 {"remixUserid","remixUserkey"} 假设实现
 */
export interface ZAuthTestResult {
  connected: boolean;
  accountName?: string;
  accountType?: string;
  quota?: Pick<ZQuota, "used" | "total">;
}

export async function testZLibraryConnection(
  creds?: { email?: string; password?: string; remixUserid?: string; remixUserkey?: string }
): Promise<ZAuthTestResult> {
  const res = await fetchWithTimeout("/api/zlibrary/auth/test", jsonInit("POST", creds ?? {}));
  return safeJson<ZAuthTestResult>(res);
}

/** (4) DELETE /api/zlibrary/auth —— 解绑（清除凭据，任务不清理） */
export async function unbindZLibrary(): Promise<void> {
  const res = await fetchWithTimeout("/api/zlibrary/auth", { method: "DELETE" });
  await safeJson<{ success: boolean }>(res);
}

/** (5) POST /api/zlibrary/search —— 代理搜索 + 去重标注（15s 超时） */
export async function searchZBooks(req: ZSearchRequest, signal?: AbortSignal): Promise<ZSearchResponse> {
  const res = await fetchWithTimeout("/api/zlibrary/search", jsonInit("POST", req, signal), 15000);
  return safeJson<ZSearchResponse>(res);
}

/** (6) GET /api/zlibrary/book/:id/:hash —— 详情 + 格式列表 + 已入库标注 */
export async function fetchZBookDetail(id: string, hash: string, signal?: AbortSignal): Promise<ZBookDetail> {
  const res = await fetchWithTimeout(
    `/api/zlibrary/book/${encodeURIComponent(id)}/${encodeURIComponent(hash)}`,
    { signal }
  );
  return safeJson<ZBookDetail>(res);
}

/** (7) POST /api/zlibrary/downloads —— 创建任务（前置校验 <1s 同步返回 202） */
export async function createZDownload(req: {
  zbookId: string;
  hash: string;
  ext?: string | null;
  libraryId?: string | null;
}): Promise<ZTask> {
  const res = await fetchWithTimeout("/api/zlibrary/downloads", jsonInit("POST", req));
  const data = await safeJson<{ task: ZTask }>(res);
  return data.task;
}

/** (8) GET /api/zlibrary/downloads —— 任务列表（admin 见全部，含 user 字段） */
export async function fetchZDownloads(params?: {
  status?: string;
  page?: number;
  limit?: number;
  signal?: AbortSignal;
}): Promise<{ tasks: ZTask[]; total: number }> {
  const qs = new URLSearchParams();
  if (params?.status) qs.set("status", params.status);
  if (params?.page) qs.set("page", String(params.page));
  if (params?.limit) qs.set("limit", String(params.limit));
  const query = qs.toString();
  const res = await fetchWithTimeout(`/api/zlibrary/downloads${query ? `?${query}` : ""}`, {
    signal: params?.signal,
  });
  return safeJson<{ tasks: ZTask[]; total: number }>(res);
}

/** (9) POST /api/zlibrary/downloads/:id/retry —— 重试（重新前置校验） */
export async function retryZDownload(id: number): Promise<ZTask> {
  const res = await fetchWithTimeout(`/api/zlibrary/downloads/${id}/retry`, jsonInit("POST", {}));
  const data = await safeJson<{ task: ZTask }>(res);
  return data.task;
}

/** (10) DELETE /api/zlibrary/downloads/:id —— 取消（进行中）/ 删除记录（终态） */
export async function deleteZDownload(id: number): Promise<void> {
  const res = await fetchWithTimeout(`/api/zlibrary/downloads/${id}`, { method: "DELETE" });
  await safeJson<{ success: boolean }>(res);
}

/** (11) POST /api/zlibrary/domains/health-check —— 手动探测全部启用域名（admin） */
export interface ZHealthCheckResult {
  url: string;
  healthy: boolean;
  latencyMs: number | null;
  error?: string;
}

export async function healthCheckZDomains(): Promise<ZHealthCheckResult[]> {
  const res = await fetchWithTimeout("/api/zlibrary/domains/health-check", jsonInit("POST", {}));
  const data = await safeJson<{ results: ZHealthCheckResult[] }>(res);
  return data.results || [];
}
