// s-library: 新增 — 任务中心角标（来源 docs/UI-DESIGN.md §3.3：danger 圆点角标，活动任务数，>9 显示 9+）
// dock 与 DashboardTopBar 共用；计数由 useZLibraryTasks 的全局单例状态驱动。
"use client";

export default function TaskCenterBadge({ count }: { count: number }) {
  if (count <= 0) return null;
  return (
    <span
      aria-hidden
      className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-danger px-1 text-[10px] font-semibold leading-none text-white tabular-nums"
    >
      {count > 9 ? "9+" : count}
    </span>
  );
}
