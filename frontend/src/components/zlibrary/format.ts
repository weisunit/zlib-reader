// s-library: 新增 — Z-Library 前端共享工具（i18n 插值 / 错误码文案映射 / 筛选选项常量）
// 来源 docs/UI-DESIGN.md §3.1（筛选选项）、§7（错误矩阵）、REQUIREMENTS FR-3.2（格式清单）。
import type { Translations } from "@/lib/i18n/locales/zh-CN";
import { Z_ERROR_CODES, ZLibraryApiError, type ZLibraryStatus } from "@/api/zlibrary";

type ZlibI18n = Translations["zlibrary"];

/** 极简 i18n 插值：`{name}` 占位符替换（项目 i18n 为纯字符串，无内置插值器） */
export function interpolate(template: string, params: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) =>
    key in params ? String(params[key]) : match
  );
}

/** 文件大小展示（与 UploadDialog 的 formatFileSize 口径一致） */
export function formatFileSize(bytes?: number | null): string {
  if (bytes === undefined || bytes === null || Number.isNaN(bytes)) return "-";
  if (bytes >= 1024 * 1024 * 1024) return (bytes / (1024 * 1024 * 1024)).toFixed(1) + " GB";
  if (bytes >= 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + " MB";
  if (bytes >= 1024) return (bytes / 1024).toFixed(1) + " KB";
  return String(bytes) + " B";
}

/** 评分展示 */
export function formatRating(rating?: number | null): string {
  if (rating === undefined || rating === null) return "";
  return rating.toFixed(1);
}

/** 本地条目跳转：契约约定 completed/已入库统一走 /comic/{localBookId}（series 场景由路由转发） */
export function localBookUrl(localBookId?: string): string {
  return `/comic/${localBookId ?? ""}`;
}

/** 配额重置时间展示 */
export function formatResetTime(resetAt: string | undefined, locale?: string): string {
  if (!resetAt) return "";
  const date = new Date(resetAt);
  if (Number.isNaN(date.getTime())) return resetAt;
  return date.toLocaleTimeString(locale === "en" ? "en-US" : "zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * 后端错误码 → i18n 文案（需求 §7 错误矩阵）。
 * 后端 error 字段本身是人话描述，作为未知码的兜底。
 */
export function zErrorText(
  t: ZlibI18n,
  err: unknown,
  params?: { domain?: string; ext?: string; libraryType?: string; x?: number; y?: number; time?: string }
): { text: string; code?: string } {
  if (err instanceof ZLibraryApiError) {
    if (err.status === 0) return { text: t.error.timeout, code: "timeout" };
    const p = params ?? {};
    switch (err.code) {
      case Z_ERROR_CODES.authRequired:
        return { text: t.error.authRequired, code: err.code };
      case Z_ERROR_CODES.authFailed:
        // s-library: 后端已透传上游真实拒绝原因（验证码/超限/密码错误），优先展示
        return { text: err.message || t.error.authFailed, code: err.code };
      case Z_ERROR_CODES.quotaExceeded:
        return {
          text: interpolate(t.error.quotaExceeded, { x: p.x ?? 0, y: p.y ?? 0, time: p.time ?? "" }),
          code: err.code,
        };
      case Z_ERROR_CODES.sizeLimit:
        return { text: t.error.sizeLimit, code: err.code };
      case Z_ERROR_CODES.domainUnreachable:
        return { text: t.error.domainUnreachable, code: err.code };
      case Z_ERROR_CODES.blocked:
        return { text: interpolate(t.error.blocked, { domain: p.domain ?? "" }), code: err.code };
      case Z_ERROR_CODES.notFound:
        return { text: t.error.notFound, code: err.code };
      case Z_ERROR_CODES.extMismatch:
        return {
          text: interpolate(t.error.extMismatch, {
            ext: (p.ext ?? "").toUpperCase(),
            libraryType: p.libraryType ?? "",
          }),
          code: err.code,
        };
      case Z_ERROR_CODES.diskFull:
        return { text: t.error.diskFull, code: err.code };
      case Z_ERROR_CODES.dupImported:
        return { text: t.error.dupImported, code: err.code };
      case Z_ERROR_CODES.dupQueued:
        return { text: t.error.dupQueued, code: err.code };
      case Z_ERROR_CODES.scanFailed:
        return { text: t.error.scanFailed, code: err.code };
      // s-library: DEF-2 补齐后端扩展码（internal/zlibrary/models.go 全集，含并行新增的 Z_INVALID_STATE）
      case Z_ERROR_CODES.libNotFound:
        return { text: t.error.libNotFound, code: err.code };
      case Z_ERROR_CODES.rateLimited:
        return { text: t.error.rateLimited, code: err.code };
      case Z_ERROR_CODES.upstream:
        return { text: t.error.upstream, code: err.code };
      case Z_ERROR_CODES.forbidden:
        return { text: t.error.forbidden, code: err.code };
      case Z_ERROR_CODES.interrupted:
        return { text: t.error.interrupted, code: err.code };
      case Z_ERROR_CODES.invalidState:
        return { text: t.error.invalidState, code: err.code };
      default:
        return { text: err.message, code: err.code };
    }
  }
  return {
    text: err instanceof Error ? err.message : String(err),
  };
}

// ============================================================
// 目标书库类型 × 扩展名匹配（镜像 UploadDialog 的 EXTENSIONS_BY_LIBRARY_TYPE 口径）
// ============================================================

export type LibraryType = "comic" | "novel" | "mixed";

export const EXTS_BY_LIBRARY_TYPE: Record<LibraryType, string[]> = {
  comic: ["zip", "cbz", "cbr", "rar", "7z", "cb7", "pdf", "azw3"],
  novel: ["txt", "epub", "mobi", "azw3", "html", "htm", "pdf"],
  mixed: ["zip", "cbz", "cbr", "rar", "7z", "cb7", "pdf", "azw3", "txt", "epub", "mobi", "html", "htm"],
};

export function extMatchesLibrary(ext: string | undefined | null, type: LibraryType): boolean {
  if (!ext) return true;
  return EXTS_BY_LIBRARY_TYPE[type].includes(ext.toLowerCase().replace(/^\./, ""));
}

/** 书库类型 i18n（用于 extMismatch 文案中的 {libraryType}） */
export function libraryTypeLabel(t: ZlibI18n, type: LibraryType): string {
  switch (type) {
    case "comic":
      return t.settings.libraryTypeComic;
    case "novel":
      return t.settings.libraryTypeNovel;
    default:
      return t.settings.libraryTypeMixed;
  }
}

// ============================================================
// 快速入库默认格式优先级（FR-4.2：epub > pdf > mobi > azw3）
// ============================================================

const DEFAULT_EXT_PRIORITY = ["epub", "pdf", "mobi", "azw3"];

export function pickDefaultExt(available: Array<string | undefined | null>): string | undefined {
  for (const ext of DEFAULT_EXT_PRIORITY) {
    const hit = available.find((e) => (e ?? "").toLowerCase() === ext);
    if (hit) return hit.toLowerCase();
  }
  const first = available.find(Boolean);
  return first ? String(first).toLowerCase() : undefined;
}

// ============================================================
// 筛选选项常量
// ============================================================

/**
 * 语言/格式下拉选项。
 * 偏差说明：UI 设计稿要求选项来自后端 /eapi/info/languages|extensions 的缓存，
 * 但前端契约（11 端点）暂未暴露该端点，M1 先用内置常用列表，后端补端点后切换。
 */
export const Z_FILTER_LANGUAGES = [
  "chinese",
  "english",
  "japanese",
  "russian",
  "spanish",
  "french",
  "german",
  "portuguese",
  "italian",
  "korean",
] as const;

export const Z_FILTER_EXTENSIONS = [
  "epub",
  "pdf",
  "mobi",
  "azw3",
  "txt",
  "html",
  "cbz",
  "cbr",
  "zip",
  "rar",
] as const;

// ============================================================
// localStorage 键与读写（搜索历史 / 双视图 / 目标书库记忆）
// ============================================================

export const ZLS_VIEW_MODE = "zlibrary:viewMode";
export const ZLS_SEARCH_HISTORY = "zlibrary:searchHistory";
export const ZLS_LAST_LIBRARY_BY_EXT = "zlibrary:lastLibraryByExt";

export function readStringList(key: string, max: number): string[] {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((v): v is string => typeof v === "string").slice(0, max);
  } catch {
    return [];
  }
}

export function writeSearchHistory(items: string[]) {
  try {
    localStorage.setItem(ZLS_SEARCH_HISTORY, JSON.stringify(items.slice(0, 10)));
  } catch {
    // storage unavailable
  }
}

export function readLastLibraryByExt(): Record<string, string> {
  try {
    const raw = localStorage.getItem(ZLS_LAST_LIBRARY_BY_EXT);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null) return {};
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof v === "string") out[k] = v;
    }
    return out;
  } catch {
    return {};
  }
}

export function writeLastLibraryByExt(map: Record<string, string>) {
  try {
    localStorage.setItem(ZLS_LAST_LIBRARY_BY_EXT, JSON.stringify(map));
  } catch {
    // storage unavailable
  }
}

// ============================================================
// 配额视图模型
// ============================================================

export interface QuotaView {
  remaining: number;
  used: number;
  total: number;
  exhausted: boolean;
  warning: boolean;
  resetAt?: string;
}

export function quotaViewOf(status: ZLibraryStatus | null | undefined): QuotaView | null {
  const quota = status?.quota;
  if (!quota || typeof quota.total !== "number" || quota.total <= 0) return null;
  const used = quota.used ?? 0;
  const remaining = Math.max(0, quota.total - used);
  return {
    remaining,
    used,
    total: quota.total,
    exhausted: remaining <= 0,
    warning: remaining > 0 && remaining <= 2,
    resetAt: quota.resetAt,
  };
}

/**
 * s-library: 封面统一走后端代理（/api/zlibrary/cover），规避浏览器侧
 * CSP 与 CDN 可达性问题（需求 §8：上游流量仅由服务端发起）；
 * 上游占位图（cover-not-exists）视为无封面，返回空串由本地占位样式兜底。
 */
export function zCoverSrc(coverUrl?: string): string {
  if (!coverUrl || coverUrl.includes("cover-not-exists")) return "";
  return `/api/zlibrary/cover?u=${encodeURIComponent(coverUrl)}`;
}
