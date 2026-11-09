// s-library: 新增 — 格式单选 chips（来源 docs/UI-DESIGN.md §3.2：来自 /formats 端点；仅单格式时不显示由调用方控制）
"use client";

import type { ZBookFormat } from "@/api/zlibrary";
import { formatFileSize } from "./format";

interface FormatSelectorProps {
  formats: ZBookFormat[];
  value: string;
  onChange: (ext: string) => void;
  disabled?: boolean;
  /** 单选 chips 上的无障碍组名 */
  label?: string;
}

export default function FormatSelector({ formats, value, onChange, disabled, label }: FormatSelectorProps) {
  if (formats.length === 0) return null;
  return (
    <div role="radiogroup" aria-label={label} className="flex flex-wrap gap-2">
      {formats.map((f) => {
        const active = f.ext === value;
        return (
          <button
            key={f.ext}
            type="button"
            role="radio"
            aria-checked={active}
            disabled={disabled}
            onClick={() => onChange(f.ext)}
            className={`inline-flex min-h-9 items-center gap-1.5 rounded-xl border px-3 py-1.5 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
              active
                ? "border-accent/60 bg-accent/10 text-accent"
                : "border-border bg-card text-muted hover:border-accent/40 hover:text-foreground"
            }`}
          >
            <span className="uppercase">{f.ext}</span>
            {f.fileSize > 0 && <span className="text-[10px] opacity-70 tabular-nums">{formatFileSize(f.fileSize)}</span>}
          </button>
        );
      })}
    </div>
  );
}
