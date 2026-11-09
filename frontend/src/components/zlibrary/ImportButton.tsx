// s-library: 新增 — 「入库」按钮状态机（来源 docs/UI-DESIGN.md §3.2 状态表）
// 可点击(bg-accent) / 排队中(bg-accent/10+Loader2) / 已入库(success 描边跳本地) / 配额不足等禁用态(bg-card text-muted)。
"use client";

import { CircleAlert, Check, Download, Loader2 } from "lucide-react";
import { useTranslation } from "@/lib/i18n";

export type ImportButtonState = "idle" | "pending" | "queued" | "imported" | "blocked";

interface ImportButtonProps {
  state: ImportButtonState;
  /** blocked 时的禁用原因（title 提示） */
  reason?: string;
  fullWidth?: boolean;
  onImport?: () => void;
  onViewLocal?: () => void;
  onViewTask?: () => void;
}

export default function ImportButton({
  state,
  reason,
  fullWidth = false,
  onImport,
  onViewLocal,
  onViewTask,
}: ImportButtonProps) {
  const t = useTranslation();
  const z = t.zlibrary;
  const width = fullWidth ? "w-full" : "";

  const base =
    "inline-flex min-h-9 items-center justify-center gap-1.5 rounded-xl px-3.5 py-2 text-xs font-medium transition-all disabled:cursor-not-allowed";

  if (state === "imported") {
    return (
      <button
        type="button"
        onClick={onViewLocal}
        className={`${base} ${width} border border-success/40 bg-success/10 text-success hover:bg-success/20`}
      >
        <Check className="h-3.5 w-3.5" />
        <span className="truncate">
          {z.detail.imported} · {z.status.viewLocal}
        </span>
      </button>
    );
  }

  if (state === "queued") {
    return (
      <button
        type="button"
        onClick={onViewTask}
        title={z.error.dupQueued}
        className={`${base} ${width} bg-accent/10 text-accent hover:bg-accent/20`}
      >
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        <span className="truncate">{z.detail.importQueued}</span>
      </button>
    );
  }

  if (state === "pending") {
    return (
      <button type="button" disabled className={`${base} ${width} bg-accent/10 text-accent`}>
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        <span className="truncate">{z.detail.import}</span>
      </button>
    );
  }

  if (state === "blocked") {
    return (
      // s-library: blocked 必须显示可见原因（此前只在 title 里，用户无从得知为何禁用）
      <div className={`${width} space-y-1`}>
        <button type="button" disabled className={`${base} w-full bg-card text-muted`}>
          <Download className="h-3.5 w-3.5" />
          <span className="truncate">{z.detail.import}</span>
        </button>
        {reason && (
          <p className="flex items-start gap-1 text-xs leading-snug text-danger">
            <CircleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            {reason}
          </p>
        )}
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={onImport}
      className={`${base} ${width} bg-accent text-white hover:-translate-y-px hover:bg-accent-hover`}
    >
      <Download className="h-3.5 w-3.5" />
      <span className="truncate">{z.detail.import}</span>
    </button>
  );
}
