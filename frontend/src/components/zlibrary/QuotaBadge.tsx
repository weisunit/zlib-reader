// s-library: 新增 — Z-Library 配额条（来源 docs/UI-DESIGN.md §3.1：header 右侧常显「今日剩余 x/y」+ 迷你进度条）
// 剩余 ≤2 warning 色、为 0 danger 色；数字 tabular-nums；搜索页 header 与设置页复用。
"use client";

import type { ZLibraryStatus } from "@/api/zlibrary";
import { useTranslation } from "@/lib/i18n";
import { quotaViewOf } from "./format";

export default function QuotaBadge({ status }: { status: ZLibraryStatus | null }) {
  const t = useTranslation();
  const quota = quotaViewOf(status);
  if (!quota) return null;

  const tone = quota.exhausted
    ? "text-danger"
    : quota.warning
      ? "text-warning"
      : "text-muted";
  const barColor = quota.exhausted ? "bg-danger" : quota.warning ? "bg-warning" : "bg-accent";
  const pct = Math.min(100, Math.round((quota.remaining / quota.total) * 100));
  const label = quota.exhausted
    ? t.zlibrary.quotaExhaustedShort
    : t.zlibrary.quotaRemaining;

  return (
    <div
      className="flex items-center gap-2 rounded-lg border border-border/60 bg-card/60 px-3 py-1.5"
      title={`${quota.remaining}/${quota.total}`}
    >
      <span className="whitespace-nowrap text-xs text-muted">{label}</span>
      <span className={`whitespace-nowrap text-xs font-semibold tabular-nums ${tone}`}>
        {quota.remaining}/{quota.total}
      </span>
      <span className="h-1.5 w-20 overflow-hidden rounded-full bg-card-hover" aria-hidden>
        <span
          className={`block h-full rounded-full transition-all ${barColor}`}
          style={{ width: `${pct}%` }}
        />
      </span>
    </div>
  );
}
