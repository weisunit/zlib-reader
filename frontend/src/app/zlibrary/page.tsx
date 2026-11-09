// s-library: 新增 — Z-Library 搜索页（来源 docs/UI-DESIGN.md §3.1 / §5 状态矩阵；路由注册见 main.tsx）
// 结构：PageHeader(+QuotaBadge) → 状态横幅（未连接/配额耗尽）→ SearchFilterBar → 结果网格/列表双视图 → 加载更多。
// 搜索历史存 localStorage 最近 10 条；viewMode 记 localStorage（沿用 books 页先例）。
"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import {
  CircleAlert,
  CloudDownload,
  Globe2,
  Loader2,
  RefreshCw,
  SearchX,
  Settings,
} from "lucide-react";
import { useTranslation } from "@/lib/i18n";
import { useAuth } from "@/lib/auth-context";
import { PageHeader, PageContent } from "@/components/PageHeader";
import { useToast } from "@/components/Toast";
import ScrollReveal from "@/components/ScrollReveal";
import { bumpTaskBadge, setTaskCenterOpen } from "@/hooks/useZLibraryTasks";
import { useZLibraryStatus } from "@/hooks/useZLibraryStatus";
import {
  createZDownload,
  searchZBooks,
  ZLibraryApiError,
  Z_ERROR_CODES,
  type ZBook,
} from "@/api/zlibrary";
import SearchFilterBar, { type ZFilterState } from "@/components/zlibrary/SearchFilterBar";
import ZBookCard from "@/components/zlibrary/ZBookCard";
import ZBookDetailDrawer from "@/components/zlibrary/ZBookDetailDrawer";
import QuotaBadge from "@/components/zlibrary/QuotaBadge";
import type { ImportButtonState } from "@/components/zlibrary/ImportButton";
import {
  formatResetTime,
  interpolate,
  quotaViewOf,
  readStringList,
  writeSearchHistory,
  ZLS_SEARCH_HISTORY,
  ZLS_VIEW_MODE,
  zErrorText,
} from "@/components/zlibrary/format";

const PAGE_SIZE = 20; // FR-3.4
const DEFAULT_FILTERS: ZFilterState = {
  message: "",
  languages: [],
  extensions: [],
  yearFrom: "",
  yearTo: "",
  order: "bestmatch",
};

export default function ZLibraryPage() {
  const t = useTranslation();
  const z = t.zlibrary;
  const toast = useToast();
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";
  const { status, loading: statusLoading, refresh: refreshStatus } = useZLibraryStatus();
  const quota = quotaViewOf(status);
  const connected = !!status?.connected;

  const [filters, setFilters] = useState<ZFilterState>(DEFAULT_FILTERS);
  const [viewMode, setViewMode] = useState<"grid" | "list">(() =>
    typeof window !== "undefined" && localStorage.getItem(ZLS_VIEW_MODE) === "list" ? "list" : "grid"
  );
  const [books, setBooks] = useState<ZBook[]>([]);
  const [total, setTotal] = useState(0);
  // s-library: 上游不回 total 时（实测 z-lib.by）用最后一批是否满页判断有无下一页
  const [lastBatch, setLastBatch] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [searched, setSearched] = useState(false);
  const [error, setError] = useState<{ text: string; code?: string; status?: number } | null>(null);
  const [history, setHistory] = useState<string[]>(() => readStringList(ZLS_SEARCH_HISTORY, 10));
  const [detailBook, setDetailBook] = useState<ZBook | null>(null);

  // viewMode 持久化（沿用 books 页惯例）
  useEffect(() => {
    try {
      localStorage.setItem(ZLS_VIEW_MODE, viewMode);
    } catch {
      // storage unavailable
    }
  }, [viewMode]);

  const pushHistory = useCallback((keyword: string) => {
    setHistory((prev) => {
      const next = [keyword, ...prev.filter((k) => k !== keyword)].slice(0, 10);
      writeSearchHistory(next);
      return next;
    });
  }, []);

  const runSearch = useCallback(
    async (nextPage: number, overrideFilters?: ZFilterState) => {
      const active = overrideFilters ?? filters;
      const message = active.message.trim();
      if (!message) return;
      if (nextPage === 1) setLoading(true);
      else setLoadingMore(true);
      setError(null);
      try {
        const res = await searchZBooks({
          message,
          languages: active.languages,
          extensions: active.extensions,
          yearFrom: active.yearFrom ? Number.parseInt(active.yearFrom, 10) || null : null,
          yearTo: active.yearTo ? Number.parseInt(active.yearTo, 10) || null : null,
          order: active.order,
          page: nextPage,
          limit: PAGE_SIZE,
        });
        setBooks((prev) => (nextPage === 1 ? res.books : [...prev, ...res.books]));
        setTotal(res.total);
        setLastBatch(res.books?.length ?? 0);
        setPage(nextPage);
        setSearched(true);
        if (nextPage === 1) pushHistory(message);
      } catch (err) {
        const { text, code } = zErrorText(z, err, { domain: status?.currentDomain });
        setError({ text, code, status: err instanceof ZLibraryApiError ? err.status : undefined });
        setSearched(true);
        if (nextPage === 1) setBooks([]);
      } finally {
        setLoading(false);
        setLoadingMore(false);
      }
    },
    [filters, pushHistory, status?.currentDomain, z]
  );

  // ============ 去重状态本地更新 ============
  const markBook = useCallback(
    (target: ZBook, patch: Partial<ZBook>) => {
      setBooks((prev) =>
        prev.map((b) => (b.zbookId === target.zbookId && b.hash === target.hash ? { ...b, ...patch } : b))
      );
    },
    []
  );

  // ============ 快速入库（列表卡片直达，无打断对话框） ============
  const quickImport = useCallback(
    async (book: ZBook) => {
      if (!connected) {
        toast.warning(z.detail.notConnectedHint);
        return;
      }
      if (quota?.exhausted) {
        toast.error(
          interpolate(z.error.quotaExceeded, {
            x: quota.used,
            y: quota.total,
            time: formatResetTime(quota.resetAt),
          })
        );
        return;
      }
      try {
        await createZDownload({
          zbookId: book.zbookId,
          hash: book.hash,
          ext: book.ext ?? null,
          libraryId: null, // 后端按默认目标书库解析（契约 ext/libraryId 可空）
        });
        markBook(book, { dedupStatus: "queued" });
        bumpTaskBadge(1);
        toast.toast(`${z.detail.importQueued} · ${z.detail.viewTask}`, "success");
      } catch (err) {
        if (err instanceof ZLibraryApiError && err.code === Z_ERROR_CODES.dupImported) {
          markBook(book, { dedupStatus: "imported", localBookId: err.localBookId });
          toast.warning(z.error.dupImported);
          return;
        }
        if (err instanceof ZLibraryApiError && err.code === Z_ERROR_CODES.dupQueued) {
          markBook(book, { dedupStatus: "queued" });
          toast.warning(z.error.dupQueued);
          return;
        }
        const { text } = zErrorText(z, err, { domain: status?.currentDomain, ext: book.ext });
        toast.error(text);
      }
    },
    [connected, quota, markBook, toast, z, status?.currentDomain]
  );

  const cardActionState = useCallback(
    (book: ZBook): ImportButtonState => {
      if (book.dedupStatus === "imported") return "imported";
      if (book.dedupStatus === "queued") return "queued";
      if (!connected || quota?.exhausted) return "blocked";
      return "idle";
    },
    [connected, quota]
  );

  const cardActionReason = useCallback(
    (book: ZBook): string | undefined => {
      if (book.dedupStatus !== "none") return undefined;
      if (!connected) return z.detail.notConnectedHint;
      if (quota?.exhausted)
        return interpolate(z.error.quotaExceeded, {
          x: quota.used,
          y: quota.total,
          time: formatResetTime(quota.resetAt),
        });
      return undefined;
    },
    [connected, quota, z]
  );

  const hasMore = searched && books.length > 0 && (total > 0 ? books.length < total : lastBatch >= PAGE_SIZE);
  const activeSearchDisabled = statusLoading || !connected;

  // ============ 错误横幅（§5 状态矩阵：每条错误带下一步动作） ============
  const renderErrorActions = () => {
    if (!error) return null;
    if (error.code === Z_ERROR_CODES.authRequired && isAdmin) {
      return (
        <Link
          href="/settings?tab=zlibrary"
          className="inline-flex min-h-8 items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-accent-hover"
        >
          <Settings className="h-3.5 w-3.5" />
          {z.action.goSettings}
        </Link>
      );
    }
    if (error.code === Z_ERROR_CODES.domainUnreachable && isAdmin) {
      return (
        <Link
          href="/settings?tab=zlibrary"
          className="inline-flex min-h-8 items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-xs text-muted transition-colors hover:text-foreground"
        >
          {z.action.goSettings}
        </Link>
      );
    }
    return (
      <button
        type="button"
        onClick={() => runSearch(1)}
        className="inline-flex min-h-8 items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-xs text-muted transition-colors hover:text-foreground"
      >
        <RefreshCw className="h-3.5 w-3.5" />
        {z.action.retry}
      </button>
    );
  };

  return (
    <>
      <PageHeader
        title={z.searchTitle}
        description={z.searchDescription}
        icon={Globe2}
        actions={<QuotaBadge status={status} />}
      />

      <PageContent width="wide" className="space-y-4">
        {/* 未连接横幅：搜索框禁用 + 重连引导 */}
        {!statusLoading && status && !connected && (
          <div className="flex flex-wrap items-center gap-3 rounded-xl border border-warning/30 bg-warning/10 px-4 py-3">
            <CircleAlert className="h-4 w-4 shrink-0 text-warning" />
            <p className="min-w-0 flex-1 text-xs text-foreground">{z.error.authRequired}</p>
            {isAdmin && (
              <Link
                href="/settings?tab=zlibrary"
                className="inline-flex min-h-8 shrink-0 items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-accent-hover"
              >
                <Settings className="h-3.5 w-3.5" />
                {z.action.goSettings}
              </Link>
            )}
          </div>
        )}

        {/* 配额耗尽横幅 */}
        {quota?.exhausted && (
          <div className="flex flex-wrap items-center gap-3 rounded-xl border border-danger/30 bg-danger/10 px-4 py-3">
            <CircleAlert className="h-4 w-4 shrink-0 text-danger" />
            <p className="min-w-0 flex-1 text-xs text-foreground tabular-nums">
              {interpolate(z.error.quotaExceeded, {
                x: quota.used,
                y: quota.total,
                time: formatResetTime(quota.resetAt) || "-",
              })}
            </p>
          </div>
        )}

        <SearchFilterBar
          value={filters}
          onChange={setFilters}
          onSubmit={() => runSearch(1)}
          loading={loading}
          disabled={activeSearchDisabled}
          viewMode={viewMode}
          onViewModeChange={setViewMode}
          history={history}
          onHistorySelect={(keyword) => {
            // 点击历史词条：填入并立即以该关键词搜索
            const next = { ...filters, message: keyword };
            setFilters(next);
            runSearch(1, next);
          }}
          onHistoryRemove={(keyword) => {
            setHistory((prev) => {
              const next = prev.filter((k) => k !== keyword);
              writeSearchHistory(next);
              return next;
            });
          }}
          onHistoryClear={() => {
            setHistory([]);
            writeSearchHistory([]);
          }}
          onClearFilters={() =>
            setFilters((prev) => ({ ...DEFAULT_FILTERS, message: prev.message }))
          }
        />

        {/* ============ 结果区 ============ */}
        {loading ? (
          /* 首屏骨架 ×8 */
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 sm:gap-3 md:grid-cols-4 lg:grid-cols-6">
            {Array.from({ length: 8 }).map((_, i) => (
              <div key={i} className="overflow-hidden rounded-xl border border-border/50">
                <div className="aspect-[2/3] animate-pulse bg-card" />
                <div className="space-y-2 p-2.5">
                  <div className="h-4 w-3/4 animate-pulse rounded bg-card" />
                  <div className="h-3 w-1/2 animate-pulse rounded bg-card" />
                  <div className="h-8 animate-pulse rounded-xl bg-card" />
                </div>
              </div>
            ))}
          </div>
        ) : error ? (
          /* 错误态（含下一步动作） */
          <div className="flex flex-col items-center gap-3 rounded-2xl border border-border/60 bg-card/40 px-6 py-14 text-center">
            <CircleAlert className="h-8 w-8 text-warning" />
            <p className="max-w-md text-sm text-muted">{error.text}</p>
            <div className="flex items-center gap-2">{renderErrorActions()}</div>
          </div>
        ) : !searched ? (
          /* 首屏引导 */
          <div className="flex flex-col items-center gap-3 rounded-2xl border border-dashed border-border/60 px-6 py-20 text-center">
            <span className="flex h-14 w-14 items-center justify-center rounded-full bg-card text-muted/50">
              <SearchX className="h-7 w-7" />
            </span>
            <p className="text-sm text-muted">{z.empty.searchReady}</p>
          </div>
        ) : books.length === 0 ? (
          /* 空结果 */
          <div className="flex flex-col items-center gap-2 rounded-2xl border border-dashed border-border/60 px-6 py-16 text-center">
            <SearchX className="h-7 w-7 text-muted/40" />
            <p className="text-sm text-foreground">{z.empty.noResults}</p>
            <p className="text-xs text-muted">{z.empty.tryDifferent}</p>
          </div>
        ) : (
          <>
            {/* 结果计数 */}
            <p className="text-xs text-muted tabular-nums">
              {interpolate(z.results.count, { n: total > 0 ? total : books.length })}
            </p>

            {viewMode === "grid" ? (
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 sm:gap-3 md:grid-cols-4 lg:grid-cols-6">
                {books.map((book, i) => (
                  <ScrollReveal key={`${book.zbookId}:${book.hash}`} delay={Math.min(i, 12) * 30}>
                    <ZBookCard
                      book={book}
                      viewMode="grid"
                      actionState={cardActionState(book)}
                      actionReason={cardActionReason(book)}
                      onOpenDetail={setDetailBook}
                      onQuickImport={quickImport}
                      onViewTask={() => setTaskCenterOpen(true)}
                    />
                  </ScrollReveal>
                ))}
              </div>
            ) : (
              <div className="grid grid-cols-1 gap-2 sm:gap-3">
                {books.map((book, i) => (
                  <ScrollReveal key={`${book.zbookId}:${book.hash}`} delay={Math.min(i, 12) * 30}>
                    <ZBookCard
                      book={book}
                      viewMode="list"
                      actionState={cardActionState(book)}
                      actionReason={cardActionReason(book)}
                      onOpenDetail={setDetailBook}
                      onQuickImport={quickImport}
                      onViewTask={() => setTaskCenterOpen(true)}
                    />
                  </ScrollReveal>
                ))}
              </div>
            )}

            {/* 加载更多（不做页码，FR-3.4） */}
            <div className="flex justify-center pt-2">
              {hasMore ? (
                <button
                  type="button"
                  onClick={() => runSearch(page + 1)}
                  disabled={loadingMore}
                  className="inline-flex min-h-10 items-center gap-2 rounded-xl border border-border bg-card px-5 py-2.5 text-sm text-muted transition-colors hover:border-accent/40 hover:text-accent disabled:opacity-50"
                >
                  {loadingMore && <Loader2 className="h-4 w-4 animate-spin" />}
                  {z.results.loadMore}
                </button>
              ) : (
                <span className="text-xs text-muted/50">{z.results.noMore}</span>
              )}
            </div>
          </>
        )}

        {/* 任务中心快捷入口（结果区内轻量提示位） */}
        {searched && books.some((b) => b.dedupStatus === "queued") && (
          <button
            type="button"
            onClick={() => setTaskCenterOpen(true)}
            className="mx-auto flex items-center gap-1.5 text-xs text-accent hover:underline"
          >
            <CloudDownload className="h-3.5 w-3.5" />
            {z.detail.viewTask}
          </button>
        )}
      </PageContent>

      {/* 详情侧滑抽屉（不占路由） */}
      <ZBookDetailDrawer
        book={detailBook}
        status={status}
        onClose={() => setDetailBook(null)}
        onQueued={(book) => markBook(book, { dedupStatus: "queued" })}
        onImported={(book) =>
          markBook(book, { dedupStatus: "imported", localBookId: book.localBookId })
        }
        onOpenTaskCenter={() => {
          setDetailBook(null);
          setTaskCenterOpen(true);
        }}
      />
    </>
  );
}
