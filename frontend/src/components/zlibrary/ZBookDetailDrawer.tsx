// s-library: 新增 — 书籍详情侧滑抽屉（来源 docs/UI-DESIGN.md §3.2）
// 桌面右侧滑入（480/560px）、移动端底部上滑；封面大图 + 元数据 + 简介 + 格式选择器 + 目标书库选择器 + 入库按钮状态机。
"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { AlertTriangle, BookOpen, CircleAlert, Loader2 } from "lucide-react";
import type { ZBook, ZBookDetail, ZBookFormat, ZLibraryStatus } from "@/api/zlibrary";
import { createZDownload, fetchZBookDetail, ZLibraryApiError, Z_ERROR_CODES } from "@/api/zlibrary";
import { fetchAccessibleLibraries, type Library } from "@/api/libraries";
import { useToast } from "@/components/Toast";
import { useTranslation } from "@/lib/i18n";
import { bumpTaskBadge } from "@/hooks/useZLibraryTasks";
import DrawerShell from "./DrawerShell";
import FormatSelector from "./FormatSelector";
import LibrarySelector from "./LibrarySelector";
import ImportButton, { type ImportButtonState } from "./ImportButton";
import {
  extMatchesLibrary,
  formatFileSize,
  formatRating,
  zCoverSrc,
  libraryTypeLabel,
  localBookUrl,
  pickDefaultExt,
  quotaViewOf,
  readLastLibraryByExt,
  writeLastLibraryByExt,
  zErrorText,
  type QuotaView,
} from "./format";

interface ZBookDetailDrawerProps {
  book: ZBook | null;
  status: ZLibraryStatus | null;
  onClose: () => void;
  /** 某本书已加入队列（页面据此把卡片徽标置为「队列中」） */
  onQueued: (book: ZBook) => void;
  /** 某本书确认为已入库（竞态兜底 Z_DUP_IMPORTED） */
  onImported: (book: ZBook) => void;
  onOpenTaskCenter: () => void;
}

interface DetailState {
  loading: boolean;
  detail: ZBookDetail | null;
  errorText: string | null;
  errorCode?: string;
}

export default function ZBookDetailDrawer({
  book,
  status,
  onClose,
  onQueued,
  onImported,
  onOpenTaskCenter,
}: ZBookDetailDrawerProps) {
  const t = useTranslation();
  const z = t.zlibrary;
  const toast = useToast();
  const router = useRouter();
  const quota: QuotaView | null = quotaViewOf(status);

  const [detailState, setDetailState] = useState<DetailState>({ loading: false, detail: null, errorText: null });
  const [selectedExt, setSelectedExt] = useState("");
  const [libraries, setLibraries] = useState<Library[]>([]);
  const [librariesLoading, setLibrariesLoading] = useState(false);
  const [selectedLibraryId, setSelectedLibraryId] = useState("");
  const [creating, setCreating] = useState(false);
  const [localQueued, setLocalQueued] = useState(false);
  const [localImportedId, setLocalImportedId] = useState<string | null>(null);
  const [importError, setImportError] = useState<{ text: string; code?: string; localBookId?: string } | null>(null);
  const [descExpanded, setDescExpanded] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  const open = !!book;
  const bookKey = book ? `${book.zbookId}:${book.hash}` : "";

  // 打开/切换书籍：拉详情 + 可管理书库
  useEffect(() => {
    if (!book) return;
    abortRef.current?.abort();
    const ac = new AbortController();
    abortRef.current = ac;
    setDetailState({ loading: true, detail: null, errorText: null });
    setLocalQueued(book.dedupStatus === "queued");
    setLocalImportedId(book.dedupStatus === "imported" ? book.localBookId ?? null : null);
    setImportError(null);
    setDescExpanded(false);
    // s-library: 立即以搜索结果的 ext 兜底——详情请求失败时按钮不再静默禁用
    if (!selectedExt && book.ext) setSelectedExt(book.ext);

    fetchZBookDetail(book.zbookId, book.hash, ac.signal)
      .then((detail) => {
        if (ac.signal.aborted) return;
        setDetailState({ loading: false, detail, errorText: null });
        const exts = detail.formats?.length ? detail.formats.map((f) => f.ext) : [detail.ext];
        // s-library: 详情缺 ext 时保留现有选择/搜索兜底，绝不回退为空
        setSelectedExt((cur) => pickDefaultExt(exts) ?? (cur || book.ext || ""));
      })
      .catch((err) => {
        if (ac.signal.aborted) return;
        // s-library: 详情失败同样兜底 ext，保证入库可用
        setSelectedExt((cur) => cur || book.ext || "");
        if (err instanceof ZLibraryApiError && err.status === 0) {
          setDetailState({ loading: false, detail: null, errorText: z.error.timeout, errorCode: "timeout" });
          return;
        }
        const { text, code } = zErrorText(z, err);
        setDetailState({ loading: false, detail: null, errorText: text, errorCode: code });
      });

    if (libraries.length === 0) {
      setLibrariesLoading(true);
      fetchAccessibleLibraries()
        .then((libs) => {
          if (ac.signal.aborted) return;
          setLibraries(libs.filter((l) => l.canManage && l.enabled));
        })
        .catch(() => {
          if (!ac.signal.aborted) setLibraries([]);
        })
        .finally(() => {
          if (!ac.signal.aborted) setLibrariesLoading(false);
        });
    }

    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bookKey]);

  const formats = useMemo<ZBookFormat[]>(() => {
    const d = detailState.detail;
    if (!d) return [];
    if (d.formats && d.formats.length > 0) return d.formats;
    return d.ext ? [{ ext: d.ext, fileSize: d.fileSize }] : [];
  }, [detailState.detail]);

  // 默认书库：localStorage 记忆优先（FR-4.1），否则第一个与扩展名类型匹配的书库
  useEffect(() => {
    if (!selectedExt || libraries.length === 0) return;
    const remembered = readLastLibraryByExt()[selectedExt];
    if (remembered && libraries.some((l) => l.id === remembered)) {
      setSelectedLibraryId(remembered);
      return;
    }
    const matched = libraries.find((l) => extMatchesLibrary(selectedExt, l.type));
    setSelectedLibraryId((matched ?? libraries[0]).id);
  }, [selectedExt, libraries]);

  const selectedLibrary = libraries.find((l) => l.id === selectedLibraryId) ?? null;
  const extMismatch =
    !!selectedLibrary && !!selectedExt && !extMatchesLibrary(selectedExt, selectedLibrary.type);
  // s-library: 全部书库都与当前格式不兼容时，引导用户去创建对应类型书库（而非仅报错）
  const noCompatibleLibrary =
    !librariesLoading && selectedExt !== "" && libraries.length > 0 &&
    !libraries.some((l) => extMatchesLibrary(selectedExt, l.type));

  const connected = !!status?.connected;
  const quotaExhausted = !!quota?.exhausted;

  const importState: ImportButtonState = useMemo(() => {
    if (localImportedId) return "imported";
    if (localQueued || book?.dedupStatus === "queued") return "queued";
    if (!connected || quotaExhausted || extMismatch || !selectedLibrary) return "blocked";
    if (creating) return "pending";
    return "idle";
  }, [localImportedId, localQueued, book, connected, quotaExhausted, extMismatch, selectedLibrary, creating]);

  const blockedReason = !connected
    ? z.detail.notConnectedHint
    : quotaExhausted
      ? z.detail.quotaHint
      : extMismatch
        ? z.error.extMismatch.replace("{ext}", selectedExt.toUpperCase()).replace(
            "{libraryType}",
            selectedLibrary ? libraryTypeLabel(z, selectedLibrary.type) : ""
          )
        : "";

  const handleImport = async () => {
    // s-library: 缺格式时不再静默吞掉点击——给可见错误提示
    if (!book || !selectedLibrary) return;
    if (!selectedExt) {
      setImportError({ text: z.detail.noFormatSelected });
      return;
    }
    setCreating(true);
    setImportError(null);
    try {
      await createZDownload({ zbookId: book.zbookId, hash: book.hash, ext: selectedExt, libraryId: selectedLibrary.id });
      setLocalQueued(true);
      bumpTaskBadge(1);
      onQueued({ ...book, ext: selectedExt, dedupStatus: "queued" });
      // 记忆本次 ext → library 选择（FR-4.1）
      const map = readLastLibraryByExt();
      map[selectedExt] = selectedLibrary.id;
      writeLastLibraryByExt(map);
      toast.toast(`${z.detail.importQueued} · ${z.detail.viewTask}`, "success");
    } catch (err) {
      if (err instanceof ZLibraryApiError && err.code === Z_ERROR_CODES.dupImported) {
        setLocalImportedId(err.localBookId ?? "");
        onImported({ ...book, dedupStatus: "imported", localBookId: err.localBookId });
        toast.warning(z.error.dupImported);
        return;
      }
      if (err instanceof ZLibraryApiError && err.code === Z_ERROR_CODES.dupQueued) {
        setLocalQueued(true);
        onQueued({ ...book, dedupStatus: "queued" });
        toast.warning(z.error.dupQueued);
        return;
      }
      const quotaView = quotaViewOf(status);
      const { text, code } = zErrorText(z, err, {
        domain: status?.currentDomain,
        ext: selectedExt,
        libraryType: selectedLibrary ? libraryTypeLabel(z, selectedLibrary.type) : "",
        x: quotaView?.used ?? 0,
        y: quotaView?.total ?? 0,
        time: quotaView?.resetAt,
      });
      setImportError({ text, code, localBookId: err instanceof ZLibraryApiError ? err.localBookId : undefined });
      toast.error(text);
    } finally {
      setCreating(false);
    }
  };

  const displayBook: (ZBook & Partial<ZBookDetail>) | null = detailState.detail
    ? { ...book!, ...detailState.detail }
    : book;

  const metaRows = displayBook
    ? [
        { label: z.detail.author, value: displayBook.author },
        { label: z.detail.publisher, value: displayBook.publisher },
        { label: z.detail.year, value: displayBook.year ? String(displayBook.year) : "" },
        { label: z.detail.language, value: displayBook.language },
        { label: z.detail.format, value: displayBook.ext ? displayBook.ext.toUpperCase() : "" },
        { label: z.detail.size, value: displayBook.fileSize ? formatFileSize(displayBook.fileSize) : "" },
        {
          label: z.detail.rating,
          value: displayBook.rating !== undefined && displayBook.rating !== null ? `★ ${formatRating(displayBook.rating)}` : "",
        },
      ].filter((r) => !!r.value)
    : [];

  return (
    <DrawerShell
      open={open}
      onClose={onClose}
      title={z.detail.title}
      icon={<BookOpen className="h-4 w-4" />}
      footer={
        displayBook ? (
          <div className="flex flex-col gap-1.5">
            <ImportButton
              fullWidth
              state={importState}
              reason={blockedReason}
              onImport={handleImport}
              onViewLocal={() => {
                if (localImportedId) {
                  onClose();
                  router.push(localBookUrl(localImportedId));
                }
              }}
              onViewTask={onOpenTaskCenter}
            />
            {/* 禁用/失败原因行内提示（含下一步动作） */}
            {importState === "blocked" && blockedReason && (
              <p className="text-xs text-warning">{blockedReason}</p>
            )}
            {importError && (
              <p className="flex items-start gap-1 text-xs text-danger">
                <CircleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span>
                  {importError.text}
                  {importError.code === Z_ERROR_CODES.dupImported && importError.localBookId && (
                    <Link href={localBookUrl(importError.localBookId)} className="ml-1 text-accent hover:underline">
                      {z.status.viewLocal}
                    </Link>
                  )}
                  {importError.code === Z_ERROR_CODES.domainUnreachable && (
                    <Link href="/settings?tab=zlibrary" className="ml-1 text-accent hover:underline">
                      {z.action.goSettings}
                    </Link>
                  )}
                </span>
              </p>
            )}
            {importState === "imported" && localImportedId && (
              <Link href={localBookUrl(localImportedId)} onClick={onClose} className="text-xs text-accent hover:underline">
                {z.status.viewLocal} →
              </Link>
            )}
          </div>
        ) : undefined
      }
    >
      {detailState.loading && (
        <div className="flex gap-4 p-4 sm:p-5">
          <div className="h-44 w-28 shrink-0 animate-pulse rounded-xl bg-card" />
          <div className="flex-1 space-y-3">
            <div className="h-5 w-3/4 animate-pulse rounded bg-card" />
            <div className="h-4 w-1/2 animate-pulse rounded bg-card" />
            <div className="h-4 w-2/3 animate-pulse rounded bg-card" />
            <div className="h-20 animate-pulse rounded bg-card" />
          </div>
        </div>
      )}

      {!detailState.loading && detailState.errorText && (
        <div className="flex flex-col items-center gap-3 p-8 text-center">
          <AlertTriangle className="h-8 w-8 text-warning" />
          <p className="text-sm text-muted">{detailState.errorText}</p>
        </div>
      )}

      {!detailState.loading && displayBook && !detailState.errorText && (
        <div className="p-4 sm:p-5">
          {/* 封面 + 核心元数据 */}
          <div className="flex gap-4">
            <div className="h-44 w-28 shrink-0 overflow-hidden rounded-xl bg-card sm:h-52 sm:w-32">
              {zCoverSrc(displayBook.coverUrl) ? (
                <img src={zCoverSrc(displayBook.coverUrl)} alt={displayBook.title} className="h-full w-full object-cover" loading="lazy" /> // s-library: 封面走后端代理
              ) : (
                <div className="flex h-full w-full items-center justify-center text-muted/30">
                  <BookOpen className="h-8 w-8" />
                </div>
              )}
            </div>
            <div className="min-w-0 flex-1">
              <h3 className="text-sm font-semibold leading-snug text-foreground">{displayBook.title}</h3>
              {displayBook.subtitle && <p className="mt-0.5 text-xs text-muted">{displayBook.subtitle}</p>}
              <dl className="mt-2 space-y-1">
                {metaRows.map((row) => (
                  <div key={row.label} className="flex gap-2 text-xs">
                    <dt className="w-12 shrink-0 text-muted/70">{row.label}</dt>
                    <dd className="min-w-0 flex-1 truncate text-foreground/90 tabular-nums">{row.value}</dd>
                  </div>
                ))}
              </dl>
            </div>
          </div>

          {/* 简介（长文本可滚动 + 展开/收起） */}
          {displayBook.description && (
            <div className="mt-4 border-t border-border/60 pt-3">
              <h4 className="text-xs font-semibold text-muted">{z.detail.description}</h4>
              <p
                className={`mt-1.5 whitespace-pre-wrap text-xs leading-relaxed text-foreground/80 ${
                  descExpanded ? "" : "line-clamp-6"
                }`}
              >
                {displayBook.description}
              </p>
              <button
                type="button"
                onClick={() => setDescExpanded((v) => !v)}
                className="mt-1 text-xs text-accent hover:underline"
              >
                {descExpanded ? z.detail.collapseDesc : z.detail.expandDesc}
              </button>
            </div>
          )}

          {/* 格式选择器（仅多格式时显示） */}
          {formats.length > 1 && (
            <div className="mt-4 border-t border-border/60 pt-3">
              <h4 className="mb-2 text-xs font-semibold text-muted">{z.detail.formatSelector}</h4>
              <FormatSelector
                formats={formats}
                value={selectedExt}
                onChange={(ext) => {
                  setSelectedExt(ext);
                  setImportError(null);
                }}
                disabled={localQueued || !!localImportedId}
                label={z.detail.formatSelector}
              />
            </div>
          )}

          {/* 目标书库选择器 */}
          <div className="mt-4 border-t border-border/60 pt-3">
            <h4 className="mb-2 text-xs font-semibold text-muted">{z.detail.librarySelector}</h4>
            {librariesLoading ? (
              <div className="flex h-10 items-center gap-2 rounded-xl bg-card px-3 text-xs text-muted">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              </div>
            ) : (
              <LibrarySelector
                libraries={libraries}
                selectedExt={selectedExt}
                value={selectedLibraryId}
                onChange={(id) => {
                  setSelectedLibraryId(id);
                  setImportError(null);
                }}
                disabled={localQueued || !!localImportedId}
                label={z.detail.librarySelector}
              />
            )}
            {/* 格式与书库类型不匹配：danger 警告 + 禁用入库（M1 从严，不提供强制） */}
            {noCompatibleLibrary && (
              <p className="mt-1.5 flex items-center gap-1 text-xs text-warning">
                <CircleAlert className="h-3.5 w-3.5 shrink-0" />
                {z.detail.noCompatibleLibrary.replace("{ext}", selectedExt.toUpperCase())}
              </p>
            )}
            {extMismatch && !noCompatibleLibrary && selectedLibrary && (
              <p className="mt-1.5 flex items-center gap-1 text-xs text-danger">
                <CircleAlert className="h-3.5 w-3.5 shrink-0" />
                {z.error.extMismatch
                  .replace("{ext}", selectedExt.toUpperCase())
                  .replace("{libraryType}", libraryTypeLabel(z, selectedLibrary.type))}
              </p>
            )}
          </div>
        </div>
      )}
    </DrawerShell>
  );
}
