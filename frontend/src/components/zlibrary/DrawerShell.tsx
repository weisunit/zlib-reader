// s-library: 新增 — Z-Library 抽屉容器壳（来源 docs/UI-DESIGN.md §3.2 / §8）
// 桌面 ≥sm：右侧滑入面板；<sm：底部上滑全屏（圆角顶边，pb 预留底栏高度）。
// 统一参数：遮罩 bg-overlay、面板 bg-elevated border-border shadow-modal、duration-panel + ease-standard、Esc/点遮罩关闭。
"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { X } from "lucide-react";

interface DrawerShellProps {
  open: boolean;
  onClose: () => void;
  title: string;
  icon?: ReactNode;
  /** 桌面端面板宽度 class（默认 w-[480px] md:w-[560px]；任务抽屉传 w-[400px]） */
  widthClass?: string;
  children: ReactNode;
  /** 面板头部右侧操作区（清空历史等） */
  actions?: ReactNode;
  /** 底部固定操作区（入库按钮等） */
  footer?: ReactNode;
}

const PANEL_TRANSITION_MS = 280; // 对应 --duration-panel

export default function DrawerShell({
  open,
  onClose,
  title,
  icon,
  widthClass = "sm:w-[480px] md:w-[560px]",
  children,
  actions,
  footer,
}: DrawerShellProps) {
  const [mounted, setMounted] = useState(open);
  const [visible, setVisible] = useState(false);
  const hideTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // 挂载/可见性两段式动画：先挂载再下一帧进入，关闭时先退出再卸载
  useEffect(() => {
    if (open) {
      if (hideTimer.current) {
        clearTimeout(hideTimer.current);
        hideTimer.current = null;
      }
      setMounted(true);
      const raf = requestAnimationFrame(() => {
        requestAnimationFrame(() => setVisible(true));
      });
      return () => cancelAnimationFrame(raf);
    }
    setVisible(false);
    hideTimer.current = setTimeout(() => setMounted(false), PANEL_TRANSITION_MS);
    return () => {
      if (hideTimer.current) {
        clearTimeout(hideTimer.current);
        hideTimer.current = null;
      }
    };
  }, [open]);

  // Esc 关闭 + body 滚动锁
  useEffect(() => {
    if (!mounted) return;
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleKey);
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", handleKey);
      document.body.style.overflow = prevOverflow;
    };
  }, [mounted, onClose]);

  if (!mounted) return null;

  return (
    <div className="fixed inset-0 z-[55]" role="dialog" aria-modal="true" aria-label={title}>
      {/* 遮罩 */}
      <button
        type="button"
        aria-label="close overlay"
        onClick={onClose}
        className={`absolute inset-0 h-full w-full cursor-default bg-overlay transition-opacity ${
          visible ? "opacity-100" : "opacity-0"
        }`}
        style={{ transitionDuration: "var(--duration-panel)" }}
      />
      {/* 面板：桌面右侧滑入 / 移动端底部上滑 */}
      <div
        className={`absolute bg-elevated shadow-modal backdrop-blur-xl
          inset-x-0 bottom-0 flex h-[88dvh] flex-col rounded-t-2xl border-t border-border pb-16
          sm:inset-y-0 sm:left-auto sm:right-0 sm:h-full sm:w-auto sm:rounded-none sm:rounded-l-2xl sm:border-l sm:border-t-0 sm:pb-0
          ${widthClass}
          transition-transform ${visible ? "translate-y-0 sm:translate-x-0" : "translate-y-full sm:translate-y-0 sm:translate-x-full"}`}
        style={{ transitionDuration: "var(--duration-panel)", transitionTimingFunction: "var(--ease-standard)" }}
      >
        {/* 头部 */}
        <div className="flex shrink-0 items-center gap-3 border-b border-border/60 px-4 py-3.5 sm:px-5">
          {icon && (
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-accent/10 text-accent">
              {icon}
            </span>
          )}
          <h2 className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">{title}</h2>
          {actions}
          <button
            type="button"
            onClick={onClose}
            aria-label="close"
            className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted transition-colors hover:bg-card-hover hover:text-foreground"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        {/* 内容（长文本纵向滚动） */}
        <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
        {footer && <div className="shrink-0 border-t border-border/60 px-4 py-3 sm:px-5">{footer}</div>}
      </div>
    </div>
  );
}
