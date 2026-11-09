// s-library: 新增 — 任务卡片（来源 docs/UI-DESIGN.md §3.3）
// 进行中 = 进度条 + 阶段文案 + [取消]；失败 = danger 描边 + 人话文案 + [重试][删除记录]（Z_SCAN_FAILED 额外 [手动扫描]）；
// 完成 = success + [查看书籍]；排队中 = [取消]。管理员看全部任务时附发起人名（FR-6.4 基础）。
"use client";

import { useState } from "react";
import Link from "next/link";
import { BookOpen, CircleAlert, Loader2, RotateCw, Wrench } from "lucide-react";
import type { ZTask, ZTaskStage } from "@/api/zlibrary";
import { Z_ERROR_CODES } from "@/api/zlibrary";
import { scanManagedLibrary } from "@/api/libraries";
import { useToast } from "@/components/Toast";
import { useTranslation } from "@/lib/i18n";
import { localBookUrl, zErrorText } from "./format";

interface TaskCardProps {
  task: ZTask;
  /** 当前用户是否管理员（决定是否展示发起人名） */
  showOwner: boolean;
  onRetry: (id: number) => Promise<void>;
  onCancelOrRemove: (id: number) => Promise<void>;
}

function stageLabel(t: ReturnType<typeof useTranslation>["zlibrary"], stage: ZTaskStage): string {
  switch (stage) {
    case "fetch_info":
      return t.tasks.stage.fetchInfo;
    case "downloading":
      return t.tasks.stage.downloading;
    case "validating":
      return t.tasks.stage.validating;
    case "moving":
      return t.tasks.stage.moving;
    case "scanning":
      return t.tasks.stage.scanning;
    default:
      return "";
  }
}

export default function TaskCard({ task, showOwner, onRetry, onCancelOrRemove }: TaskCardProps) {
  const t = useTranslation();
  const z = t.zlibrary;
  const toast = useToast();
  const [busy, setBusy] = useState<"retry" | "cancel" | "scan" | null>(null);

  const isActive = task.status === "queued" || task.status === "downloading";
  const isFailed = task.status === "failed";
  const isCompleted = task.status === "completed";
  const isCanceled = task.status === "canceled";

  // Z_QUOTA_EXCEEDED 的重试按钮在次日重置前禁用（§4.2）
  const retryBlocked = isFailed && (task.errorCode === Z_ERROR_CODES.quotaExceeded || task.quotaConsumed === true);
  const canRetry = isFailed && !retryBlocked;

  const failureText = isFailed
    ? task.errorCode === Z_ERROR_CODES.scanFailed
      ? z.error.scanFailed
      : task.errorMsg || z.error.scanFailed
    : "";

  const handleRetry = async () => {
    setBusy("retry");
    try {
      await onRetry(task.id);
    } catch (err) {
      // s-library: QA DEF-2 — 错误码统一走 i18n 映射（如重试竞态返回 Z_INVALID_STATE）
      toast.error(zErrorText(z, err).text);
    } finally {
      setBusy(null);
    }
  };

  const handleCancelOrRemove = async () => {
    setBusy("cancel");
    try {
      await onCancelOrRemove(task.id);
    } catch (err) {
      toast.error(zErrorText(z, err).text);
    } finally {
      setBusy(null);
    }
  };

  const handleScanNow = async () => {
    if (!task.libraryId) return;
    setBusy("scan");
    try {
      await scanManagedLibrary(task.libraryId);
      toast.success(z.tasks.status.completed);
    } catch (err) {
      toast.error(zErrorText(z, err).text);
    } finally {
      setBusy(null);
    }
  };

  const finishedTime = task.finishedAt
    ? new Date(task.finishedAt).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
    : "";

  return (
    <div
      className={`rounded-xl border bg-card/70 p-3 ${
        isFailed ? "border-danger/40" : isCompleted ? "border-success/30" : "border-border"
      }`}
    >
      {/* 标题行 */}
      <div className="flex items-start gap-2">
        <h4 className="min-w-0 flex-1 truncate text-sm font-medium text-foreground">{task.title || "-"}</h4>
        {task.ext && (
          <span className="shrink-0 rounded-md bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium uppercase text-accent">
            {task.ext}
          </span>
        )}
        {isCompleted && (
          <span className="shrink-0 text-[10px] font-medium text-success">{z.tasks.status.completed}</span>
        )}
        {isFailed && (
          <span className="flex shrink-0 items-center gap-0.5 text-[10px] font-medium text-danger">
            <CircleAlert className="h-3 w-3" />
            {z.tasks.status.failed}
          </span>
        )}
        {isCanceled && <span className="shrink-0 text-[10px] font-medium text-muted">{z.tasks.status.canceled}</span>}
      </div>

      {/* 进行中：进度条 + 阶段文案 */}
      {task.status === "downloading" && (
        <div className="mt-2 flex items-center gap-2">
          <span className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-card-hover">
            <span
              className="block h-full rounded-full bg-accent transition-all"
              style={{ width: `${Math.min(100, Math.max(2, task.progress))}%` }}
            />
          </span>
          <span className="shrink-0 text-xs text-muted tabular-nums">{task.progress}%</span>
          <span className="shrink-0 text-xs text-accent">{stageLabel(z, task.stage)}</span>
        </div>
      )}

      {/* 元信息 */}
      <p className="mt-1.5 truncate text-xs text-muted/80 tabular-nums">
        {showOwner && task.user && (
          <>
            {z.tasks.by}: {task.user} ·{" "}
          </>
        )}
        {finishedTime && <>{finishedTime}</>}
        {!finishedTime && task.author}
      </p>

      {/* 失败原因（人话文案） */}
      {isFailed && failureText && (
        <p className="mt-1.5 text-xs leading-relaxed text-danger">{failureText}</p>
      )}

      {/* 操作区 */}
      <div className="mt-2 flex flex-wrap items-center gap-2">
        {isActive && (
          <button
            type="button"
            onClick={handleCancelOrRemove}
            disabled={busy !== null}
            className="inline-flex min-h-7 items-center gap-1 rounded-lg border border-border px-2.5 py-1 text-xs text-muted transition-colors hover:border-danger/40 hover:text-danger disabled:opacity-50"
          >
            {busy === "cancel" && <Loader2 className="h-3 w-3 animate-spin" />}
            {z.action.cancel}
          </button>
        )}
        {isFailed && (
          <>
            <button
              type="button"
              onClick={handleRetry}
              disabled={!canRetry || busy !== null}
              title={retryBlocked ? (task.quotaConsumed ? z.error.quotaConsumed : z.action.retryTomorrow) : undefined}
              className="inline-flex min-h-7 items-center gap-1 rounded-lg bg-accent/10 px-2.5 py-1 text-xs font-medium text-accent transition-colors hover:bg-accent/20 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {busy === "retry" ? <Loader2 className="h-3 w-3 animate-spin" /> : <RotateCw className="h-3 w-3" />}
              {retryBlocked ? z.action.retryTomorrow : z.action.retry}
            </button>
            {task.errorCode === Z_ERROR_CODES.scanFailed && task.libraryId && (
              <button
                type="button"
                onClick={handleScanNow}
                disabled={busy !== null}
                className="inline-flex min-h-7 items-center gap-1 rounded-lg border border-border px-2.5 py-1 text-xs text-muted transition-colors hover:border-accent/40 hover:text-accent disabled:opacity-50"
              >
                {busy === "scan" ? <Loader2 className="h-3 w-3 animate-spin" /> : <Wrench className="h-3 w-3" />}
                {z.action.scanNow}
              </button>
            )}
            <button
              type="button"
              onClick={handleCancelOrRemove}
              disabled={busy !== null}
              className="inline-flex min-h-7 items-center rounded-lg px-2 py-1 text-xs text-muted/70 transition-colors hover:text-danger disabled:opacity-50"
            >
              {z.action.removeRecord}
            </button>
          </>
        )}
        {isCompleted && task.localBookId && (
          <Link
            href={localBookUrl(task.localBookId)}
            className="inline-flex min-h-7 items-center gap-1 rounded-lg bg-success/10 px-2.5 py-1 text-xs font-medium text-success transition-colors hover:bg-success/20"
          >
            <BookOpen className="h-3 w-3" />
            {z.tasks.viewBook}
          </Link>
        )}
        {isCanceled && (
          <button
            type="button"
            onClick={handleCancelOrRemove}
            disabled={busy !== null}
            className="inline-flex min-h-7 items-center rounded-lg px-2 py-1 text-xs text-muted/70 transition-colors hover:text-danger disabled:opacity-50"
          >
            {z.action.removeRecord}
          </button>
        )}
      </div>
    </div>
  );
}
