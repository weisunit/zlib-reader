// s-library: 新增 — Z-Library 下载任务全局单例 store + 轮询 hook（来源 docs/UI-DESIGN.md §3.3 / §9.1）
// 参照 lib/stores/scraper-core.ts 的模块级单例 subscribe/notify 先例：
// - 任务列表与活动数角标为全局唯一状态（dock / 顶栏 / 抽屉共享）；
// - 2s 轮询仅在任务抽屉打开时运行（startTaskPolling / stopTaskPolling 由抽屉生命周期驱动）；
// - 快速入库等动作可通过 bumpTaskBadge 做乐观角标更新。
import { useEffect, useState } from "react";
import {
  fetchZDownloads,
  retryZDownload,
  deleteZDownload,
  type ZTask,
} from "@/api/zlibrary";
import { invalidateZLibraryStatus } from "@/hooks/useZLibraryStatus";

const POLL_INTERVAL_MS = 2000;

interface ZTasksState {
  tasks: ZTask[];
  total: number;
  loading: boolean;
  error: string | null;
  /** 活动任务数（queued + downloading），驱动全局角标 */
  activeCount: number;
  /** 「清空历史」进行中（期间禁用按钮防重复点击，QA DEF-3） */
  clearing: boolean;
}

let state: ZTasksState = {
  tasks: [],
  total: 0,
  loading: false,
  error: null,
  activeCount: 0,
  clearing: false,
};

let taskCenterOpen = false;
let pollTimer: ReturnType<typeof setInterval> | null = null;
let inflight = false;
const listeners = new Set<() => void>();

function emit() {
  listeners.forEach((fn) => fn());
}

function computeActiveCount(tasks: ZTask[]): number {
  return tasks.filter((t) => t.status === "queued" || t.status === "downloading").length;
}

async function fetchTasks() {
  if (inflight) return;
  inflight = true;
  const prevActiveIds = new Map(
    state.tasks
      .filter((t) => t.status === "queued" || t.status === "downloading")
      .map((t) => [t.id, t.status])
  );
  try {
    const data = await fetchZDownloads({ limit: 50 });
    const activeCount = computeActiveCount(data.tasks);
    // 检测「活动 → completed」迁移，通知调用方（toast + 配额刷新由抽屉层做）
    const newlyCompleted = data.tasks.filter(
      (t) => t.status === "completed" && prevActiveIds.has(t.id)
    );
    state = { ...state, tasks: data.tasks, total: data.total, loading: false, error: null, activeCount };
    emit();
    if (newlyCompleted.length > 0) {
      invalidateZLibraryStatus(); // 配额已消耗，刷新状态
      completionHandlers.forEach((fn) => fn(newlyCompleted));
    }
  } catch (err) {
    state = {
      ...state,
      loading: false,
      error: err instanceof Error ? err.message : "tasks request failed",
    };
    emit();
  } finally {
    inflight = false;
  }
}

/** 任务完成通知（抽屉层注册，用于 toast + 本地跳转提示） */
type CompletionHandler = (tasks: ZTask[]) => void;
const completionHandlers = new Set<CompletionHandler>();

export function registerTaskCompletionHandler(fn: CompletionHandler): () => void {
  completionHandlers.add(fn);
  return () => completionHandlers.delete(fn);
}

// ============================================================
// 抽屉开关 + 轮询生命周期
// ============================================================

export function setTaskCenterOpen(open: boolean) {
  taskCenterOpen = open;
  emit();
}

export function useTaskCenterOpen(): boolean {
  const [open, setOpen] = useState(taskCenterOpen);
  useEffect(() => {
    const listener = () => setOpen(taskCenterOpen);
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }, []);
  return open;
}

/** 2s 轮询——仅在任务抽屉打开期间运行 */
export function startTaskPolling() {
  if (pollTimer) return;
  fetchTasks();
  pollTimer = setInterval(fetchTasks, POLL_INTERVAL_MS);
}

export function stopTaskPolling() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

// ============================================================
// 角标（抽屉关闭时无轮询，靠乐观更新 + 最近一次拉取结果驱动）
// ============================================================

/** 快速入库等场景的乐观角标更新 */
export function bumpTaskBadge(delta: number) {
  const next = Math.max(0, state.activeCount + delta);
  if (next !== state.activeCount) {
    state = { ...state, activeCount: next };
    emit();
  }
}

/** 全局角标 hook：dock / DashboardTopBar 使用，不发起轮询 */
export function useZLibraryTaskBadge(): number {
  const [activeCount, setActiveCount] = useState(state.activeCount);
  useEffect(() => {
    const listener = () => setActiveCount(state.activeCount);
    listeners.add(listener);
    listener();
    // 首次挂载时若尚无数据则拉一次（不启动轮询）
    if (!state.tasks.length && !state.loading && !inflight) {
      fetchTasks();
    }
    return () => {
      listeners.delete(listener);
    };
  }, []);
  return activeCount;
}

// ============================================================
// 任务列表 hook（任务抽屉内使用）
// ============================================================

export interface UseZLibraryTasksResult {
  tasks: ZTask[];
  total: number;
  loading: boolean;
  error: string | null;
  activeCount: number;
  /** 清空历史进行中 */
  clearing: boolean;
  refresh: () => Promise<void>;
  retry: (id: number) => Promise<void>;
  cancelOrRemove: (id: number) => Promise<void>;
  clearFinished: () => Promise<void>;
}

export function useZLibraryTasks(): UseZLibraryTasksResult {
  const [snap, setSnap] = useState<ZTasksState>(state);

  useEffect(() => {
    const listener = () => setSnap({ ...state });
    listeners.add(listener);
    listener();
    return () => {
      listeners.delete(listener);
    };
  }, []);

  const refresh = async () => {
    await fetchTasks();
  };

  const retry = async (id: number) => {
    await retryZDownload(id);
    bumpTaskBadge(1);
    await fetchTasks();
  };

  const cancelOrRemove = async (id: number) => {
    const task = state.tasks.find((t) => t.id === id);
    await deleteZDownload(id);
    if (task && (task.status === "queued" || task.status === "downloading")) {
      bumpTaskBadge(-1);
    }
    await fetchTasks();
  };

  /**
   * 清空全部终态任务历史（QA DEF-3：FR-5.4 要求覆盖最近 200 条，而非仅已加载页）。
   * 后端 GET /downloads 仅支持单值 status 过滤且 limit≤100，故按
   * completed/failed/canceled 三种状态各从第 1 页拉取（limit=100，删除后页码前移），
   * 循环至某一轮三种状态均无返回或全部删除失败为止（后端历史裁剪上限 200 条，
   * 正常 2~3 轮收敛；轮数硬上限防病态循环）。终态任务删除不影响进行中任务。
   */
  const clearFinished = async () => {
    if (state.clearing) return;
    const TERMINAL_STATUSES = ["completed", "failed", "canceled"] as const;
    const MAX_ROUNDS = 10;
    state = { ...state, clearing: true };
    emit();
    try {
      for (let round = 0; round < MAX_ROUNDS; round++) {
        let roundDeleted = 0;
        for (const status of TERMINAL_STATUSES) {
          const data = await fetchZDownloads({ status, page: 1, limit: 100 });
          for (const task of data.tasks) {
            try {
              await deleteZDownload(task.id);
              roundDeleted += 1;
            } catch {
              // 单条删除失败（如权限竞争）不中断整体清空
            }
          }
        }
        if (roundDeleted === 0) break;
      }
    } finally {
      state = { ...state, clearing: false };
      emit();
      await fetchTasks();
    }
  };

  return {
    tasks: snap.tasks,
    total: snap.total,
    loading: snap.loading,
    error: snap.error,
    activeCount: snap.activeCount,
    clearing: snap.clearing,
    refresh,
    retry,
    cancelOrRemove,
    clearFinished,
  };
}
