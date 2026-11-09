// s-library: 新增 — Z-Library 连接/配额/域名状态 hook（来源 docs/UI-DESIGN.md §9.1）
// 模块级单例缓存 + invalidate，参照 useAIStatus / useComics 的缓存先例。
import { useEffect, useState } from "react";
import {
  fetchZLibraryStatus,
  ZLibraryApiError,
  Z_ERROR_CODES,
  type ZLibraryStatus,
} from "@/api/zlibrary";

interface ZStatusState {
  status: ZLibraryStatus | null;
  /** 请求被 Z_FORBIDDEN 拒绝（或显式 canUse=false）：当前用户无权使用 */
  forbidden: boolean;
  loading: boolean;
  /** 最近一次请求的失败信息（非 403） */
  error: string | null;
}

let state: ZStatusState = {
  status: null,
  forbidden: false,
  loading: false,
  error: null,
};

let fetchPromise: Promise<void> | null = null;
const listeners = new Set<() => void>();

function emit() {
  listeners.forEach((fn) => fn());
}

async function requestStatus() {
  state = { ...state, loading: true, error: null };
  emit();
  try {
    const status = await fetchZLibraryStatus();
    const canUse =
      typeof status.canUse === "boolean"
        ? status.canUse
        : true; // HTTP 200 即视为有权（契约：无权时返回 403 Z_FORBIDDEN）
    state = { status: { ...status, canUse }, forbidden: !canUse, loading: false, error: null };
  } catch (err) {
    if (err instanceof ZLibraryApiError && (err.code === Z_ERROR_CODES.forbidden || err.status === 403)) {
      state = { status: null, forbidden: true, loading: false, error: null };
    } else {
      state = {
        status: null,
        forbidden: false,
        loading: false,
        error: err instanceof Error ? err.message : "status request failed",
      };
    }
  }
  emit();
}

function load() {
  if (fetchPromise) return fetchPromise;
  fetchPromise = requestStatus().finally(() => {
    fetchPromise = null;
  });
  return fetchPromise;
}

/** 使缓存失效（设置变更 / 任务完成影响配额后调用） */
export function invalidateZLibraryStatus(options?: { refetch?: boolean }) {
  state = { status: null, forbidden: false, loading: false, error: null };
  emit();
  if (options?.refetch !== false) load();
}

export interface UseZLibraryStatusResult extends ZStatusState {
  /** 当前用户是否可使用 Z-Library（管理员恒真；普通用户由 /status 200/403 判定） */
  canUse: boolean;
  refresh: () => void;
}

export function useZLibraryStatus(): UseZLibraryStatusResult {
  const [snap, setSnap] = useState<ZStatusState>(state);

  useEffect(() => {
    const listener = () => setSnap({ ...state });
    listeners.add(listener);
    if (!state.status && !state.forbidden && !state.loading && !fetchPromise) {
      load();
    } else {
      // 立即对齐最新缓存，避免闪烁
      listener();
    }
    return () => {
      listeners.delete(listener);
    };
  }, []);

  return {
    status: snap.status,
    forbidden: snap.forbidden,
    loading: snap.loading,
    error: snap.error,
    // 仅在确认拿到 200 响应后才视为可用，避免入口先闪现再消失
    canUse: !snap.forbidden && !!snap.status,
    refresh: () => {
      state = { status: null, forbidden: false, loading: false, error: null };
      emit();
      load();
    },
  };
}
