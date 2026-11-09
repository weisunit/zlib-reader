// s-library: 新增 — 目标书库下拉（来源 docs/UI-DESIGN.md §3.2）
// 选项 = 用户 canManage 的书库（复用 fetchAccessibleLibraries）；默认 = 当前格式扩展名匹配类型的第一个书库（FR-4.1）。
"use client";

import { useMemo } from "react";
import type { Library } from "@/api/libraries";
import { useTranslation } from "@/lib/i18n";

import { extMatchesLibrary } from "./format";

interface LibrarySelectorProps {
  libraries: Library[];
  value: string;
  onChange: (libraryId: string) => void;
  disabled?: boolean;
  label?: string;
  /** s-library: 当前书籍格式——用于兼容性排序与禁用标记（全部书库仍可见） */
  selectedExt?: string;
}

export default function LibrarySelector({ libraries, value, onChange, disabled, label, selectedExt }: LibrarySelectorProps) {
  const t = useTranslation();
  const typeLabels = useMemo(
    () => ({
      comic: t.zlibrary.settings.libraryTypeComic,
      novel: t.zlibrary.settings.libraryTypeNovel,
      mixed: t.zlibrary.settings.libraryTypeMixed,
    }),
    [t]
  );

  // s-library: 用户要求可选全部已创建书库——兼容的排前面，不兼容的
  // 仍可见但标记「不支持 {ext}」并禁用（选了扫描器也不会收）
  const ordered = useMemo(() => {
    if (!selectedExt) return libraries;
    const compat = libraries.filter((l) => extMatchesLibrary(selectedExt, l.type));
    const rest = libraries.filter((l) => !extMatchesLibrary(selectedExt, l.type));
    return [...compat, ...rest];
  }, [libraries, selectedExt]);

  return (
    <select
      value={value}
      disabled={disabled || libraries.length === 0}
      onChange={(e) => onChange(e.target.value)}
      aria-label={label}
      className="h-10 w-full rounded-xl border border-border bg-card px-3 text-sm text-foreground outline-none transition-colors focus:border-accent/50 focus:ring-2 focus:ring-accent/20 disabled:opacity-50"
    >
      {libraries.length === 0 && <option value="">--</option>}
      {ordered.map((lib) => {
        const typeLabel = typeLabels[lib.type] ?? lib.type;
        const compatible = !selectedExt || extMatchesLibrary(selectedExt, lib.type);
        // s-library: 名称已含类型词时不再重复拼接（如「小说书库 · 小说书库」）
        const name = lib.name.includes(typeLabel) ? lib.name : `${lib.name} · ${typeLabel}`;
        return (
          <option key={lib.id} value={lib.id} disabled={!compatible}>
            {compatible ? name : `${name}（${t.zlibrary.libraryNotCompatible.replace("{ext}", selectedExt.toUpperCase())}）`}
          </option>
        );
      })}
    </select>
  );
}
