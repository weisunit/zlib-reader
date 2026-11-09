// s-library: 新增 — 任务中心全局抽屉（来源 docs/UI-DESIGN.md §3.3）
// 抽屉打开时 2s 轮询（useZLibraryTasks），关闭即停；任务完成触发 toast「《书名》已入库」并刷新配额。
// 挂载于 main.tsx AppShell 层，任意页面可达。
"use client";

import { useEffect } from "react";
import { CloudDownload, Loader2 } from "lucide-react";
import { useRouter } from "next/navigation";
import { useToast } from "@/components/Toast";
import { useTranslation } from "@/lib/i18n";
import { useAuth } from "@/lib/auth-context";
import {
  registerTaskCompletionHandler,
  setTaskCenterOpen,
  startTaskPolling,
  stopTaskPolling,
  useTaskCenterOpen,
  useZLibraryTasks,
} from "@/hooks/useZLibraryTasks";
import DrawerShell from "./DrawerShell";
import TaskCard from "./TaskCard";
import { interpolate } from "./format";

export default function TaskCenterDrawer() {
  const open = useTaskCenterOpen();
  const t = useTranslation();
  const z = t.zlibrary;
  const toast = useToast();
  const router = useRouter();
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";
  const { tasks, loading, error, activeCount, clearing, retry, cancelOrRemove, clearFinished } = useZLibraryTasks();

  // 打开 → 2s 轮询；关闭 → 停止
  useEffect(() => {
    if (!open) return;
    startTaskPolling();
    return () => {
      stopTaskPolling();
    };
  }, [open]);

  // 任务完成 → toast（下一步动作由任务卡「查看书籍」承接）+ 配额已由 store 刷新
  useEffect(() => {
    const off = registerTaskCompletionHandler((completed) => {
      for (const task of completed) {
        toast.toast(interpolate(z.tasks.importedToast, { title: task.title }), "success");
      }
    });
    return () => {
      off();
    };
  }, [toast, z]);

  const hasFinished = tasks.some(
    (task) => task.status === "completed" || task.status === "failed" || task.status === "canceled"
  );

  return (
    <DrawerShell
      open={open}
      onClose={() => setTaskCenterOpen(false)}
      title={
        activeCount > 0
          ? `${z.tasks.title} · ${interpolate(z.tasks.activeCount, { n: activeCount })}`
          : z.tasks.title
      }
      icon={<CloudDownload className="h-4 w-4" />}
      widthClass="sm:w-[400px]"
      actions={
        hasFinished && (
          // s-library: QA DEF-3 — 清空期间禁用防重复点击（后端无聚合端点，前端分页逐条删除需数秒）
          <button
            type="button"
            disabled={clearing}
            onClick={() => clearFinished().catch(() => toast.error(z.error.timeout))}
            className="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 text-xs text-muted/70 transition-colors hover:text-danger disabled:cursor-not-allowed disabled:opacity-50"
          >
            {clearing && <Loader2 className="h-3 w-3 animate-spin" />}
            {z.tasks.clearHistory}
          </button>
        )
      }
    >
      {loading && tasks.length === 0 ? (
        /* 首次打开骨架 ×3 */
        <div className="space-y-3 p-4">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-24 animate-pulse rounded-xl bg-card" />
          ))}
        </div>
      ) : tasks.length === 0 ? (
        /* 空状态 */
        <div className="flex flex-col items-center gap-3 px-8 py-16 text-center">
          <span className="flex h-16 w-16 items-center justify-center rounded-full bg-card text-muted/40">
            <CloudDownload className="h-8 w-8" />
          </span>
          <p className="text-sm font-medium text-foreground">{z.tasks.emptyTitle}</p>
          <button
            type="button"
            onClick={() => {
              setTaskCenterOpen(false);
              router.push("/zlibrary");
            }}
            className="text-xs text-accent hover:underline"
          >
            {z.tasks.emptyHint} →
          </button>
          {error && <p className="text-xs text-danger">{error}</p>}
        </div>
      ) : (
        <div className="space-y-2.5 p-4">
          {tasks.map((task) => (
            <TaskCard
              key={task.id}
              task={task}
              showOwner={!!isAdmin}
              onRetry={retry}
              onCancelOrRemove={cancelOrRemove}
            />
          ))}
          {error && <p className="pt-2 text-center text-xs text-danger">{error}</p>}
        </div>
      )}
    </DrawerShell>
  );
}
