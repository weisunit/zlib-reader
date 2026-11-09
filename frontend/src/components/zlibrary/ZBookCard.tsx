// s-library: 新增 — Z-Library 搜索结果卡片（来源 docs/UI-DESIGN.md §3.1）
// grid = 封面竖卡；list = 横排（复用 ComicCard 列表态结构口径）。
// 卡片右上角状态徽标三态：未入库无徽标 / 已入库(success，可点击跳本地条目) / 队列中(accent，点击开任务中心)。
"use client";

import Link from "next/link";
import { memo } from "react";
import type { ZBook } from "@/api/zlibrary";
import LazyImage from "@/components/LazyImage";
import { useTranslation } from "@/lib/i18n";
import ImportButton, { type ImportButtonState } from "./ImportButton";
import { formatFileSize, formatRating, localBookUrl, zCoverSrc } from "./format";

export type ZBookCardActionState = ImportButtonState;

interface ZBookCardProps {
  book: ZBook;
  viewMode: "grid" | "list";
  /** 快速入库按钮当前状态（idle/blocked/queued/imported/pending） */
  actionState: ZBookCardActionState;
  actionReason?: string;
  onOpenDetail: (book: ZBook) => void;
  onQuickImport: (book: ZBook) => void;
  onViewTask: () => void;
}

function StatusBadge({
  book,
  onViewTask,
}: {
  book: ZBook;
  onViewTask: () => void;
}) {
  const t = useTranslation();
  if (book.dedupStatus === "imported" && book.localBookId) {
    // 已入库徽标是唯一跳本地条目的入口，必须可点击（§8）
    return (
      <Link
        href={localBookUrl(book.localBookId)}
        onClick={(e) => e.stopPropagation()}
        className="inline-flex items-center gap-1 rounded-full border border-success/40 bg-background/80 px-2 py-0.5 text-[10px] font-medium text-success backdrop-blur transition-colors hover:bg-success/20"
      >
        {t.zlibrary.status.imported}
      </Link>
    );
  }
  if (book.dedupStatus === "queued") {
    return (
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          onViewTask();
        }}
        className="inline-flex items-center gap-1 rounded-full border border-accent/50 bg-background/80 px-2 py-0.5 text-[10px] font-medium text-accent backdrop-blur transition-colors hover:bg-accent/20"
      >
        {t.zlibrary.status.queued}
      </button>
    );
  }
  return null;
}

function MetaLine({ book, className = "" }: { book: ZBook; className?: string }) {
  const parts = [book.author, book.year ? String(book.year) : ""].filter(Boolean);
  return (
    <p className={`truncate text-xs text-muted ${className}`}>
      {parts.join(" · ") || "—"}
    </p>
  );
}

function FormatSizeLine({ book, className = "" }: { book: ZBook; className?: string }) {
  const rating = formatRating(book.rating);
  return (
    <p className={`truncate text-xs text-muted/80 tabular-nums ${className}`}>
      {(book.ext ?? "").toUpperCase() || "—"} · {formatFileSize(book.fileSize)}
      {rating && <> · ★ {rating}</>}
    </p>
  );
}

function ZBookCardInner({
  book,
  viewMode,
  actionState,
  actionReason,
  onOpenDetail,
  onQuickImport,
  onViewTask,
}: ZBookCardProps) {
  const t = useTranslation();
  const cover = zCoverSrc(book.coverUrl); // s-library: 封面走后端代理

  if (viewMode === "list") {
    return (
      <div
        role="button"
        tabIndex={0}
        onClick={() => onOpenDetail(book)}
        onKeyDown={(e) => {
          if (e.key === "Enter") onOpenDetail(book);
        }}
        className="group flex cursor-pointer items-center gap-3 rounded-xl border border-white/[0.04] bg-card/70 p-2.5 transition-all hover:bg-card-hover sm:gap-4 sm:p-3"
      >
        <div className="relative h-20 w-14 shrink-0 overflow-hidden rounded-lg bg-card">
          {cover ? (
            <LazyImage src={cover} alt={book.title} className="h-full w-full object-cover" blurEffect={false} />
          ) : (
            <div className="flex h-full w-full items-center justify-center text-muted/30">
              <span className="text-xs">{(book.ext ?? "").toUpperCase()}</span>
            </div>
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-start gap-2">
            <h3 className="min-w-0 flex-1 truncate text-sm font-medium text-foreground">{book.title}</h3>
            <StatusBadge book={book} onViewTask={onViewTask} />
          </div>
          <MetaLine book={book} className="mt-0.5" />
          <FormatSizeLine book={book} className="mt-0.5" />
        </div>
        <div className="shrink-0" onClick={(e) => e.stopPropagation()}>
          {book.dedupStatus === "imported" ? (
            <Link
              href={localBookUrl(book.localBookId)}
              className="inline-flex min-h-9 items-center justify-center rounded-xl border border-success/40 bg-success/10 px-3.5 py-2 text-xs font-medium text-success transition-colors hover:bg-success/20"
            >
              {t.zlibrary.status.viewLocal}
            </Link>
          ) : book.dedupStatus === "queued" ? (
            <ImportButton state="queued" onViewTask={onViewTask} />
          ) : (
            <ImportButton state={actionState} reason={actionReason} onImport={() => onQuickImport(book)} />
          )}
        </div>
      </div>
    );
  }

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => onOpenDetail(book)}
      onKeyDown={(e) => {
        if (e.key === "Enter") onOpenDetail(book);
      }}
      className="group relative flex cursor-pointer flex-col overflow-hidden rounded-xl border border-white/[0.04] bg-card/70 transition-all duration-200 hover:bg-card-hover hover:shadow-[var(--shadow-card-hover)]"
    >
      {/* 封面 */}
      <div className="relative aspect-[2/3] w-full overflow-hidden bg-card">
        {cover ? (
          <LazyImage
            src={cover}
            alt={book.title}
            className="h-full w-full object-cover transition-transform duration-300 group-hover:scale-[1.03]"
          />
        ) : (
          <div className="flex h-full w-full items-center justify-center text-muted/30">
            <span className="text-sm">{(book.ext ?? "").toUpperCase()}</span>
          </div>
        )}
        {/* 状态徽标 */}
        <div className="absolute right-1.5 top-1.5">
          <StatusBadge book={book} onViewTask={onViewTask} />
        </div>
      </div>
      {/* 元数据 */}
      <div className="flex min-h-0 flex-1 flex-col gap-0.5 p-2.5">
        <h3 className="line-clamp-2 text-sm font-medium leading-snug text-foreground">{book.title}</h3>
        <MetaLine book={book} />
        <FormatSizeLine book={book} />
        {/* 快速入库：不弹任何打断对话框（PM 约束） */}
        <div
          className="mt-auto pt-2"
          onClick={(e) => e.stopPropagation()}
        >
          {book.dedupStatus === "imported" ? (
            <Link
              href={localBookUrl(book.localBookId)}
              className="flex min-h-9 w-full items-center justify-center rounded-xl border border-success/40 bg-success/10 px-3.5 py-2 text-xs font-medium text-success transition-colors hover:bg-success/20"
            >
              {t.zlibrary.status.viewLocal}
            </Link>
          ) : book.dedupStatus === "queued" ? (
            <ImportButton fullWidth state="queued" onViewTask={onViewTask} />
          ) : (
            <ImportButton fullWidth state={actionState} reason={actionReason} onImport={() => onQuickImport(book)} />
          )}
        </div>
      </div>
    </div>
  );
}

export default memo(ZBookCardInner);
