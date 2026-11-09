// s-library: 新增 — 搜索筛选栏（来源 docs/UI-DESIGN.md §3.1）
// 搜索框 + 语言/格式/排序三个下拉常驻一行（<sm 收进「筛选」按钮展开的面板）；
// 年份区间、清除筛选、搜索历史收进「更多筛选」折叠区（默认收起）。
"use client";

import { useState } from "react";
import { ChevronDown, History, LayoutGrid, List, Loader2, Search, SlidersHorizontal, X } from "lucide-react";
import { useTranslation } from "@/lib/i18n";
import type { ZSearchOrder } from "@/api/zlibrary";
import { Z_FILTER_EXTENSIONS, Z_FILTER_LANGUAGES } from "./format";

export interface ZFilterState {
  message: string;
  languages: string[];
  extensions: string[];
  yearFrom: string;
  yearTo: string;
  order: ZSearchOrder;
}

interface SearchFilterBarProps {
  value: ZFilterState;
  onChange: (next: ZFilterState) => void;
  onSubmit: () => void;
  loading: boolean;
  disabled?: boolean;
  viewMode: "grid" | "list";
  onViewModeChange: (mode: "grid" | "list") => void;
  history: string[];
  onHistorySelect: (keyword: string) => void;
  onHistoryRemove: (keyword: string) => void;
  onHistoryClear: () => void;
  onClearFilters: () => void;
}

const selectClass =
  "h-9 rounded-lg border border-border bg-card px-2.5 text-xs text-foreground outline-none transition-colors focus:border-accent/50 disabled:opacity-50";

export default function SearchFilterBar({
  value,
  onChange,
  onSubmit,
  loading,
  disabled,
  viewMode,
  onViewModeChange,
  history,
  onHistorySelect,
  onHistoryRemove,
  onHistoryClear,
  onClearFilters,
}: SearchFilterBarProps) {
  const t = useTranslation();
  const z = t.zlibrary;
  const [moreOpen, setMoreOpen] = useState(false);
  const [mobileFilterOpen, setMobileFilterOpen] = useState(false);

  const hasFilters =
    value.languages.length > 0 ||
    value.extensions.length > 0 ||
    !!value.yearFrom ||
    !!value.yearTo ||
    value.order !== "bestmatch";

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onSubmit();
  };

  const toggleIn = (list: string[], v: string) =>
    list.includes(v) ? list.filter((x) => x !== v) : [...list, v];

  /* 常驻三个下拉（桌面） */
  const filterSelects = (
    <>
      <label className="flex items-center gap-1.5 text-xs text-muted">
        {z.filter.language}
        <select
          className={selectClass}
          value={value.languages[0] ?? ""}
          disabled={disabled}
          onChange={(e) => onChange({ ...value, languages: e.target.value ? [e.target.value] : [] })}
        >
          <option value="">{t.common.all}</option>
          {Z_FILTER_LANGUAGES.map((lang) => (
            <option key={lang} value={lang}>{lang}</option>
          ))}
        </select>
      </label>
      <label className="flex items-center gap-1.5 text-xs text-muted">
        {z.filter.format}
        <select
          className={selectClass}
          value={value.extensions[0] ?? ""}
          disabled={disabled}
          onChange={(e) => onChange({ ...value, extensions: e.target.value ? [e.target.value] : [] })}
        >
          <option value="">{t.common.all}</option>
          {Z_FILTER_EXTENSIONS.map((ext) => (
            <option key={ext} value={ext}>{ext.toUpperCase()}</option>
          ))}
        </select>
      </label>
      <label className="flex items-center gap-1.5 text-xs text-muted">
        {z.filter.sort}
        <select
          className={selectClass}
          value={value.order}
          disabled={disabled}
          onChange={(e) => onChange({ ...value, order: e.target.value as ZSearchOrder })}
        >
          <option value="bestmatch">{z.sort.bestmatch}</option>
          <option value="newest">{z.sort.newest}</option>
          <option value="rating">{z.sort.popular}</option>
        </select>
      </label>
    </>
  );

  /* 年份区间 */
  const yearInputs = (
    <div className="flex items-center gap-2">
      <span className="text-xs text-muted">{z.filter.yearFrom}</span>
      <input
        type="number"
        inputMode="numeric"
        min={0}
        max={9999}
        value={value.yearFrom}
        disabled={disabled}
        onChange={(e) => onChange({ ...value, yearFrom: e.target.value })}
        className="h-9 w-20 rounded-lg border border-border bg-card px-2.5 text-xs text-foreground outline-none transition-colors focus:border-accent/50 disabled:opacity-50 tabular-nums"
      />
      <span className="text-xs text-muted">–</span>
      <span className="text-xs text-muted">{z.filter.yearTo}</span>
      <input
        type="number"
        inputMode="numeric"
        min={0}
        max={9999}
        value={value.yearTo}
        disabled={disabled}
        onChange={(e) => onChange({ ...value, yearTo: e.target.value })}
        className="h-9 w-20 rounded-lg border border-border bg-card px-2.5 text-xs text-foreground outline-none transition-colors focus:border-accent/50 disabled:opacity-50 tabular-nums"
      />
    </div>
  );

  /* 搜索历史 chips */
  const historyChips = history.length > 0 && (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="flex items-center gap-1 text-xs text-muted">
        <History className="h-3.5 w-3.5" />
        {z.history.title}
      </span>
      {history.map((keyword) => (
        <span
          key={keyword}
          className="inline-flex items-center gap-1 rounded-full border border-border bg-card px-2.5 py-1 text-xs text-muted transition-colors hover:border-accent/40 hover:text-foreground"
        >
          <button type="button" onClick={() => onHistorySelect(keyword)} className="max-w-40 truncate">
            {keyword}
          </button>
          <button
            type="button"
            aria-label={`${t.common.delete}: ${keyword}`}
            onClick={() => onHistoryRemove(keyword)}
            className="text-muted/60 transition-colors hover:text-danger"
          >
            <X className="h-3 w-3" />
          </button>
        </span>
      ))}
      <button
        type="button"
        onClick={onHistoryClear}
        className="text-xs text-muted/70 transition-colors hover:text-danger"
      >
        {z.history.clear}
      </button>
    </div>
  );

  return (
    <div className="rounded-2xl border border-border/60 bg-card/50 p-3 sm:p-4">
      {/* 第一行：搜索框 + 搜索按钮 + 视图切换 */}
      <form onSubmit={handleSubmit} className="flex items-center gap-2">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
          <input
            type="search"
            value={value.message}
            disabled={disabled}
            onChange={(e) => onChange({ ...value, message: e.target.value })}
            placeholder={z.searchPlaceholder}
            aria-label={z.searchTitle}
            className="h-10 w-full rounded-xl border border-border bg-card pl-9 pr-3 text-sm text-foreground outline-none transition-colors placeholder:text-muted/60 focus:border-accent/50 focus:ring-2 focus:ring-accent/20 disabled:opacity-50"
          />
        </div>
        <button
          type="submit"
          disabled={disabled || loading || !value.message.trim()}
          className="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-xl bg-accent px-4 text-sm font-medium text-white transition-all hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
        >
          {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}
          <span className="hidden sm:inline">{loading ? z.searching : z.searchButton}</span>
        </button>
        <div className="flex shrink-0 items-center rounded-lg border border-border/60 bg-card/50 p-0.5" role="group" aria-label={`${z.view.grid} / ${z.view.list}`}>
          <button
            type="button"
            onClick={() => onViewModeChange("grid")}
            aria-label={z.view.grid}
            aria-pressed={viewMode === "grid"}
            className={`flex h-7 w-7 items-center justify-center rounded-md transition-all duration-200 ${
              viewMode === "grid" ? "bg-accent text-white shadow-sm" : "text-muted hover:text-foreground"
            }`}
          >
            <LayoutGrid className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            onClick={() => onViewModeChange("list")}
            aria-label={z.view.list}
            aria-pressed={viewMode === "list"}
            className={`flex h-7 w-7 items-center justify-center rounded-md transition-all duration-200 ${
              viewMode === "list" ? "bg-accent text-white shadow-sm" : "text-muted hover:text-foreground"
            }`}
          >
            <List className="h-3.5 w-3.5" />
          </button>
        </div>
      </form>

      {/* 第二行：语言/格式/排序（≥sm 常驻）+ 更多筛选；<sm 收进「筛选」按钮 */}
      <div className="mt-3 flex items-center gap-3">
        <div className="hidden flex-wrap items-center gap-3 sm:flex">{filterSelects}</div>
        <button
          type="button"
          onClick={() => setMobileFilterOpen((v) => !v)}
          aria-expanded={mobileFilterOpen}
          className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-border bg-card px-2.5 text-xs text-muted transition-colors hover:text-foreground sm:hidden"
        >
          <SlidersHorizontal className="h-3.5 w-3.5" />
          {z.filter.more}
        </button>
        <button
          type="button"
          onClick={() => setMoreOpen((v) => !v)}
          aria-expanded={moreOpen}
          className="hidden h-9 items-center gap-1 rounded-lg px-2 text-xs text-muted transition-colors hover:text-foreground sm:inline-flex"
        >
          {moreOpen ? z.filter.less : z.filter.more}
          <ChevronDown className={`h-3.5 w-3.5 transition-transform ${moreOpen ? "rotate-180" : ""}`} />
        </button>
      </div>

      {/* 移动端筛选面板 */}
      {mobileFilterOpen && (
        <div className="mt-3 flex flex-col gap-3 rounded-xl border border-border/60 bg-background/40 p-3 sm:hidden">
          {filterSelects}
          {yearInputs}
          {hasFilters && (
            <button type="button" onClick={onClearFilters} className="self-start text-xs text-accent hover:underline">
              {z.filter.clear}
            </button>
          )}
        </div>
      )}

      {/* 更多筛选折叠区（年份 + 清除筛选 + 历史） */}
      {moreOpen && (
        <div className="mt-3 hidden flex-col gap-3 rounded-xl border border-border/60 bg-background/40 p-3 sm:flex">
          <div className="flex flex-wrap items-center justify-between gap-2">
            {yearInputs}
            {hasFilters && (
              <button type="button" onClick={onClearFilters} className="text-xs text-accent hover:underline">
                {z.filter.clear}
              </button>
            )}
          </div>
          {historyChips}
        </div>
      )}
      {mobileFilterOpen && historyChips && <div className="mt-3 sm:hidden">{historyChips}</div>}
      {!moreOpen && !mobileFilterOpen && historyChips && (
        <div className="mt-3">{historyChips}</div>
      )}
    </div>
  );
}
