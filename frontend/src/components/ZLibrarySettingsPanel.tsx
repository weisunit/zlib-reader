// s-library: 新增 — 设置页「Z-Library」分区面板（来源 docs/UI-DESIGN.md §3.4；注册见 app/settings/page.tsx）
// 五个小节：① 连接 ② 域名 ③ 下载行为 ④ 权限 ⑤ 兜底指引（rounded-2xl border bg-card/80 分区）。
// 凭据安全交互：密码 type=password 永不回显（已连接显示「已加密保存」占位）；remix token 等宽粘贴框；解绑二次确认弹窗。
"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Check,
  CircleAlert,
  CircleCheck,
  Copy,
  Loader2,
  Plus,
  RefreshCw,
  Trash2,
  Unplug,
} from "lucide-react";
import { useTranslation } from "@/lib/i18n";
import { useToast } from "@/components/Toast";
import { useZLibraryStatus, invalidateZLibraryStatus } from "@/hooks/useZLibraryStatus";
import QuotaBadge from "@/components/zlibrary/QuotaBadge";
import {
  fetchZLibrarySettings,
  healthCheckZDomains,
  saveZLibrarySettings,
  testZLibraryConnection,
  unbindZLibrary,
  type ZAllowScope,
} from "@/api/zlibrary";
import { fetchAccessibleLibraries, fetchLibraries, type Library } from "@/api/libraries";
import { zErrorText } from "@/components/zlibrary/format";

const FALLBACK_EMAIL = "blackbox@z-library.so";

const inputClass =
  "w-full rounded-xl border border-border bg-card px-3 py-2.5 text-sm text-foreground outline-none transition-colors placeholder:text-muted/50 focus:border-accent/50 focus:ring-2 focus:ring-accent/20 disabled:opacity-60";

interface DomainDraft {
  url: string;
  enabled: boolean;
  isDefault: boolean;
  healthy?: boolean;
  lastCheckedAt?: string | null;
  latencyMs?: number | null;
}

interface BehaviorDraft {
  concurrency: number;
  defaultLibraryId: string;
  allowScope: ZAllowScope;
}

function SectionCard({
  index,
  title,
  children,
}: {
  index: number;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-2xl border border-border/60 bg-card/80 p-4 sm:p-5">
      <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
        <span className="flex h-5 w-5 items-center justify-center rounded-full bg-accent/10 text-[10px] font-bold text-accent">
          {index}
        </span>
        {title}
      </h3>
      {children}
    </section>
  );
}

export default function ZLibrarySettingsPanel() {
  const t = useTranslation();
  const z = t.zlibrary;
  const toast = useToast();
  const { status, refresh: refreshStatus } = useZLibraryStatus();

  const [loading, setLoading] = useState(true);
  const [domains, setDomains] = useState<DomainDraft[]>([]);
  const [behavior, setBehavior] = useState<BehaviorDraft>({ concurrency: 1, defaultLibraryId: "", allowScope: "admin" });
  const [saving, setSaving] = useState(false);
  const [libraries, setLibraries] = useState<Library[]>([]);

  // 连接小节
  const [credMode, setCredMode] = useState<"password" | "token">("password");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [remixUserid, setRemixUserid] = useState("");
  const [remixUserkey, setRemixUserkey] = useState("");
  const [testing, setTesting] = useState(false);
  const [savingCreds, setSavingCreds] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [unbindOpen, setUnbindOpen] = useState(false);
  const [unbinding, setUnbinding] = useState(false);

  // 域名 / 兜底
  const [healthChecking, setHealthChecking] = useState(false);
  const [newDomain, setNewDomain] = useState("");
  const [fallbackDomain, setFallbackDomain] = useState("");
  const [copied, setCopied] = useState(false);

  const connected = !!status?.connected;
  // s-library: QA DEF-1 — GET /settings 返回 hasCredentials（是否已配置凭据；秘密字段不回传）
  const [hasCredentials, setHasCredentials] = useState(false);

  const loadSettings = useCallback(async () => {
    setLoading(true);
    try {
      // GET /settings 与 PUT 请求体同构（domains/behavior）+ hasCredentials（QA DEF-1 契约）。
      // 后端未部署该路由时（404）走 catch 降级，面板仍可用但 behavior 回显默认值。
      const data = await fetchZLibrarySettings();
      setDomains(
        (data.domains || []).map((d) => ({
          url: d.url,
          enabled: d.enabled,
          isDefault: d.isDefault,
          healthy: undefined,
          lastCheckedAt: null,
          latencyMs: null,
        }))
      );
      setBehavior({
        concurrency: data.behavior?.concurrency ?? 1,
        defaultLibraryId: data.behavior?.defaultLibraryId ?? "",
        allowScope: data.behavior?.allowScope ?? "admin",
      });
      setHasCredentials(data.hasCredentials === true);
    } catch {
      // 降级：从 /status 的域名健康数据推导域名列表（域名列表在契约 (1) 中可见）；
      // behavior（并发/默认书库/权限档）无法回显，保持默认值——待后端 GET /settings 就绪后恢复。
      setHasCredentials(connected); // 无 GET 时以连接状态近似判断是否已有凭据
      const fromStatus = (status?.domains || []).map((d) => ({
        url: d.url,
        enabled: d.enabled,
        isDefault: d.isDefault,
        healthy: d.healthy,
        lastCheckedAt: d.lastCheckedAt,
        latencyMs: d.latencyMs,
      }));
      if (fromStatus.length > 0) setDomains(fromStatus);
    } finally {
      setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    loadSettings();
    // 管理员取全部书库作为默认目标书库选项；失败降级为可管理书库
    fetchLibraries()
      .then(setLibraries)
      .catch(() =>
        fetchAccessibleLibraries()
          .then(setLibraries)
          .catch(() => setLibraries([]))
      );
  }, [loadSettings]);

  const accountTypeLabel = useMemo(() => {
    const type = status?.accountType ?? "";
    if (type === "free") return z.settings.accountType.free;
    if (type === "donor") return z.settings.accountType.donor;
    return type;
  }, [status?.accountType, z]);

  // ============ 域名操作（顺序即优先级，M1 用 ↑↓ 按钮） ============
  const moveDomain = (index: number, dir: -1 | 1) => {
    setDomains((prev) => {
      const next = [...prev];
      const target = index + dir;
      if (target < 0 || target >= next.length) return prev;
      [next[index], next[target]] = [next[target], next[index]];
      return next;
    });
  };

  const setDefaultDomain = (url: string) => {
    setDomains((prev) => prev.map((d) => ({ ...d, isDefault: d.url === url, enabled: d.url === url ? true : d.enabled })));
  };

  const removeDomain = (url: string) => {
    setDomains((prev) => {
      const next = prev.filter((d) => d.url !== url);
      if (!next.some((d) => d.isDefault) && next.length > 0) next[0] = { ...next[0], isDefault: true };
      return next;
    });
  };

  const addDomain = (raw: string) => {
    const url = raw.trim().replace(/\/+$/, "");
    if (!url) return;
    const normalized = /^https?:\/\//i.test(url) ? url : `https://${url}`;
    if (domains.some((d) => d.url.toLowerCase() === normalized.toLowerCase())) {
      toast.warning(z.settings.domain.duplicate);
      return;
    }
    setDomains((prev) => [
      ...prev,
      { url: normalized, enabled: true, isDefault: prev.length === 0 },
    ]);
  };

  const handleHealthCheck = async () => {
    setHealthChecking(true);
    try {
      const results = await healthCheckZDomains();
      setDomains((prev) =>
        prev.map((d) => {
          const hit = results.find((r) => r.url === d.url);
          return hit ? { ...d, healthy: hit.healthy, latencyMs: hit.latencyMs, lastCheckedAt: new Date().toISOString() } : d;
        })
      );
      refreshStatus();
      toast.success(z.settings.domain.healthCheck);
    } catch (err) {
      toast.error(zErrorText(z, err).text);
    } finally {
      setHealthChecking(false);
    }
  };

  // ============ 保存域名 + 行为 + 权限 ============
  const handleSave = async () => {
    setSaving(true);
    try {
      await saveZLibrarySettings({
        domains: domains.map((d) => ({ url: d.url, enabled: d.enabled, isDefault: d.isDefault })),
        behavior,
      });
      toast.success(z.settings.saved);
      refreshStatus();
    } catch (err) {
      toast.error(zErrorText(z, err).text);
    } finally {
      setSaving(false);
    }
  };

  // ============ 连接小节 ============
  const handleTestConnection = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const res = await testZLibraryConnection();
      setTestResult({
        ok: true,
        text: `${z.settings.status.connected}${res.accountName ? ` · ${res.accountName}` : ""}${
          res.quota ? ` · ${res.quota.used}/${res.quota.total}` : ""
        }`,
      });
      invalidateZLibraryStatus();
    } catch (err) {
      const { text } = zErrorText(z, err);
      setTestResult({ ok: false, text });
    } finally {
      setTesting(false);
    }
  };

  const handleSaveCredentials = async () => {
    // s-library: QA DEF-1 — 秘密字段为空时直接拒绝发送：凭据经 POST /auth/test 登录换令牌保存，
    // 空 email/password 或空 remix 字段会以「空凭据」覆盖已有令牌，必须整体不发送。
    if (!credsReady) return;
    setSavingCreds(true);
    setTestResult(null);
    try {
      const creds =
        credMode === "password"
          ? { email: email.trim(), password }
          : { remixUserid: remixUserid.trim(), remixUserkey: remixUserkey.trim() };
      // 契约 (3)：带凭据 = 登录换令牌并保存（密码用后即弃）；remix 字段形状为假设（见汇报偏差）
      const res = await testZLibraryConnection(creds);
      setTestResult({
        ok: true,
        text: `${z.settings.status.connected}${res.accountName ? ` · ${res.accountName}` : ""}`,
      });
      setPassword("");
      setRemixUserkey("");
      invalidateZLibraryStatus();
      loadSettings(); // 刷新 hasCredentials 回显
      toast.success(z.settings.saved);
    } catch (err) {
      const { text } = zErrorText(z, err);
      setTestResult({ ok: false, text });
    } finally {
      setSavingCreds(false);
    }
  };

  const handleUnbind = async () => {
    setUnbinding(true);
    try {
      await unbindZLibrary();
      setUnbindOpen(false);
      setTestResult(null);
      invalidateZLibraryStatus();
      loadSettings(); // 解绑后刷新 hasCredentials（QA DEF-1），凭据输入恢复可编辑
      toast.success(z.settings.saved);
    } catch (err) {
      toast.error(zErrorText(z, err).text);
    } finally {
      setUnbinding(false);
    }
  };

  const handleCopyEmail = async () => {
    try {
      await navigator.clipboard.writeText(FALLBACK_EMAIL);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error(z.settings.fallback.copyEmail);
    }
  };

  const credsReady =
    credMode === "password"
      ? !!email.trim() && !!password
      : !!remixUserid.trim() && !!remixUserkey.trim();

  return (
    <div className="space-y-5">
      {loading && (
        <div className="space-y-4">
          <div className="h-32 animate-pulse rounded-2xl bg-card" />
          <div className="h-48 animate-pulse rounded-2xl bg-card" />
        </div>
      )}

      {!loading && (
        <>
          {/* ① 连接 */}
          <SectionCard index={1} title={z.settings.section.connection}>
            {/* 状态行 */}
            <div className="flex flex-wrap items-center gap-3">
              <span
                className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ${
                  connected
                    ? "bg-success/10 text-success"
                    : status === null
                      ? "bg-card text-muted"
                      : "bg-danger/10 text-danger"
                }`}
              >
                <span
                  className={`h-1.5 w-1.5 rounded-full ${
                    connected ? "bg-success" : status === null ? "bg-muted" : "bg-danger"
                  }`}
                />
                {connected ? z.settings.status.connected : status === null ? z.settings.status.notConfigured : z.settings.status.disconnected}
              </span>
              {connected && (
                <span className="text-xs text-muted tabular-nums">
                  {status?.accountName}
                  {accountTypeLabel ? ` · ${accountTypeLabel}` : ""}
                </span>
              )}
              {connected && <QuotaBadge status={status} />}
            </div>

            {/* 凭据方式 */}
            <div className="mt-4 flex flex-wrap items-center gap-2">
              {(["password", "token"] as const).map((mode) => (
                <button
                  key={mode}
                  type="button"
                  onClick={() => setCredMode(mode)}
                  aria-pressed={credMode === mode}
                  className={`inline-flex min-h-9 items-center rounded-xl border px-3 py-1.5 text-xs font-medium transition-colors ${
                    credMode === mode
                      ? "border-accent/60 bg-accent/10 text-accent"
                      : "border-border bg-card text-muted hover:text-foreground"
                  }`}
                >
                  {mode === "password" ? z.settings.credential.modePassword : z.settings.credential.modeToken}
                </button>
              ))}
            </div>

            {/* 凭据输入：已连接时禁用输入；hasCredentials（已配置但可能未连接）显示「已加密保存」占位，
                秘密字段永不回显；空秘密字段不发送（见 handleSaveCredentials 守卫与按钮禁用条件） */}
            {credMode === "password" ? (
              <div className="mt-3 grid gap-3 sm:grid-cols-2">
                <label className="block">
                  <span className="mb-1 block text-xs text-muted">{z.settings.credential.email}</span>
                  <input
                    type="email"
                    value={email}
                    disabled={connected}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder={connected || hasCredentials ? z.settings.credential.encrypted : "test@example.com"}
                    className={inputClass}
                    autoComplete="off"
                  />
                </label>
                <label className="block">
                  <span className="mb-1 block text-xs text-muted">{z.settings.credential.password}</span>
                  <input
                    type="password"
                    value={password}
                    disabled={connected}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder={connected || hasCredentials ? z.settings.credential.encrypted : "••••••••"}
                    className={inputClass}
                    autoComplete="new-password"
                  />
                </label>
                <p className="text-xs text-muted/70 sm:col-span-2">{z.settings.credential.passwordHint}</p>
              </div>
            ) : (
              <div className="mt-3 grid gap-3 sm:grid-cols-2">
                <label className="block">
                  <span className="mb-1 block text-xs text-muted">{z.settings.credential.remixUserId}</span>
                  <input
                    type="text"
                    value={remixUserid}
                    disabled={connected}
                    onChange={(e) => setRemixUserid(e.target.value.trim())}
                    placeholder={connected || hasCredentials ? z.settings.credential.encrypted : ""}
                    className={`${inputClass} font-mono`}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </label>
                <label className="block">
                  <span className="mb-1 block text-xs text-muted">{z.settings.credential.remixUserKey}</span>
                  <input
                    type="password"
                    value={remixUserkey}
                    disabled={connected}
                    onChange={(e) => setRemixUserkey(e.target.value.trim())}
                    placeholder={connected || hasCredentials ? z.settings.credential.encrypted : ""}
                    className={`${inputClass} font-mono`}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </label>
                <p className="text-xs text-muted/70 sm:col-span-2">{z.settings.credential.tokenHint}</p>
              </div>
            )}

            {/* 操作按钮 */}
            <div className="mt-4 flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={handleTestConnection}
                disabled={!connected || testing}
                className="inline-flex min-h-10 items-center gap-1.5 rounded-xl border border-border bg-card px-4 py-2 text-sm text-foreground transition-colors hover:border-accent/40 hover:text-accent disabled:cursor-not-allowed disabled:opacity-50"
              >
                {testing ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                {testing ? z.settings.action.testing : z.settings.action.testConnection}
              </button>
              {/* 保存凭据：已连接时先解绑；秘密字段为空时禁用——保证空秘密永不覆盖已有凭据（QA DEF-1） */}
              <button
                type="button"
                onClick={handleSaveCredentials}
                disabled={connected || !credsReady || savingCreds}
                className="inline-flex min-h-10 items-center gap-1.5 rounded-xl bg-accent px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
              >
                {savingCreds && <Loader2 className="h-4 w-4 animate-spin" />}
                {z.settings.action.saveCredentials}
              </button>
              {connected && (
                <button
                  type="button"
                  onClick={() => setUnbindOpen(true)}
                  className="inline-flex min-h-10 items-center gap-1.5 rounded-xl border border-danger/30 px-4 py-2 text-sm text-danger transition-colors hover:bg-danger/10"
                >
                  <Unplug className="h-4 w-4" />
                  {z.settings.action.unbind}
                </button>
              )}
            </div>

            {/* 测试结果行内反馈 */}
            {testResult && (
              <p
                className={`mt-2 flex items-center gap-1.5 text-xs ${
                  testResult.ok ? "text-success" : "text-danger"
                }`}
              >
                {testResult.ok ? <CircleCheck className="h-3.5 w-3.5" /> : <CircleAlert className="h-3.5 w-3.5" />}
                {testResult.text}
              </p>
            )}
          </SectionCard>

          {/* ② 域名 */}
          <SectionCard index={2} title={z.settings.section.domain}>
            <div className="space-y-2">
              {domains.length === 0 && (
                <p className="rounded-xl border border-dashed border-border px-4 py-6 text-center text-xs text-muted">
                  {z.settings.status.notConfigured}
                </p>
              )}
              {domains.map((d, i) => (
                <div
                  key={d.url}
                  className="flex flex-wrap items-center gap-2 rounded-xl border border-border/60 bg-card px-3 py-2"
                >
                  <span className="min-w-0 flex-1 truncate font-mono text-xs text-foreground">{d.url}</span>
                  {d.isDefault && (
                    <span className="rounded-md bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                      {z.settings.domain.default}
                    </span>
                  )}
                  {!d.isDefault && (
                    <button
                      type="button"
                      onClick={() => setDefaultDomain(d.url)}
                      className="rounded-md px-1.5 py-0.5 text-[10px] text-muted transition-colors hover:text-accent"
                    >
                      {z.settings.domain.setDefault}
                    </button>
                  )}
                  <span
                    className={`inline-flex items-center gap-1 text-[10px] ${
                      d.healthy === undefined ? "text-muted/60" : d.healthy ? "text-success" : "text-danger"
                    }`}
                  >
                    {d.healthy === undefined ? (
                      <>○ {z.settings.domain.unknown}</>
                    ) : d.healthy ? (
                      <>● {z.settings.domain.healthy}{d.latencyMs !== null && d.latencyMs !== undefined ? ` · ${d.latencyMs}ms` : ""}</>
                    ) : (
                      <>● {z.settings.domain.unhealthy}</>
                    )}
                  </span>
                  <div className="flex items-center gap-0.5">
                    <button
                      type="button"
                      aria-label={z.settings.domain.dragHint}
                      disabled={i === 0}
                      onClick={() => moveDomain(i, -1)}
                      className="flex h-6 w-6 items-center justify-center rounded text-muted transition-colors hover:text-foreground disabled:opacity-30"
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      aria-label={z.settings.domain.dragHint}
                      disabled={i === domains.length - 1}
                      onClick={() => moveDomain(i, 1)}
                      className="flex h-6 w-6 items-center justify-center rounded text-muted transition-colors hover:text-foreground disabled:opacity-30"
                    >
                      ↓
                    </button>
                    <button
                      type="button"
                      aria-label={`${z.action.removeRecord}: ${d.url}`}
                      onClick={() => removeDomain(d.url)}
                      className="flex h-6 w-6 items-center justify-center rounded text-muted transition-colors hover:text-danger"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>
                </div>
              ))}
            </div>

            {/* 添加域名 + 健康检查 */}
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <input
                type="text"
                value={newDomain}
                onChange={(e) => setNewDomain(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    addDomain(newDomain);
                    setNewDomain("");
                  }
                }}
                placeholder="z-lib.sk"
                className={`${inputClass} h-10 w-auto min-w-48 flex-1 py-0 font-mono text-xs`}
                spellCheck={false}
              />
              <button
                type="button"
                onClick={() => {
                  addDomain(newDomain);
                  setNewDomain("");
                }}
                className="inline-flex h-10 items-center gap-1.5 rounded-xl border border-border bg-card px-3 text-xs text-muted transition-colors hover:border-accent/40 hover:text-accent"
              >
                <Plus className="h-3.5 w-3.5" />
                {z.settings.domain.add}
              </button>
              <button
                type="button"
                onClick={handleHealthCheck}
                disabled={healthChecking || domains.length === 0}
                className="inline-flex h-10 items-center gap-1.5 rounded-xl border border-border bg-card px-3 text-xs text-muted transition-colors hover:border-accent/40 hover:text-accent disabled:opacity-50"
              >
                {healthChecking ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
                {z.settings.domain.healthCheck}
              </button>
            </div>
            <p className="mt-2 text-xs text-muted/70">{z.settings.domain.dragHint}</p>

            {/* 当前生效 / 个人下载域名 */}
            <div className="mt-3 grid gap-2 text-xs sm:grid-cols-2">
              <p className="text-muted">
                {z.settings.domain.current}:{" "}
                <span className="font-mono text-foreground">{status?.currentDomain || "-"}</span>
              </p>
              <p className="text-muted">
                {z.settings.domain.personal}:{" "}
                <span className="font-mono text-foreground">{status?.personalDomain || "-"}</span>
              </p>
            </div>
          </SectionCard>

          {/* ③ 下载行为 */}
          <SectionCard index={3} title={z.settings.section.download}>
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="block">
                <span className="mb-1 block text-xs text-muted">{z.settings.download.concurrency}</span>
                <select
                  value={behavior.concurrency}
                  onChange={(e) => setBehavior((prev) => ({ ...prev, concurrency: Number(e.target.value) }))}
                  className={`${inputClass} py-0`}
                >
                  {[1, 2, 3].map((n) => (
                    <option key={n} value={n}>{n}</option>
                  ))}
                </select>
              </label>
              <label className="block">
                <span className="mb-1 block text-xs text-muted">{z.settings.download.defaultLibrary}</span>
                <select
                  value={behavior.defaultLibraryId}
                  onChange={(e) => setBehavior((prev) => ({ ...prev, defaultLibraryId: e.target.value }))}
                  className={`${inputClass} py-0`}
                >
                  <option value="">{z.settings.download.autoByFormat}</option>
                  {libraries.map((lib) => (
                    <option key={lib.id} value={lib.id}>{lib.name}</option>
                  ))}
                </select>
                <span className="mt-1 block text-[11px] leading-snug text-muted">{z.settings.download.defaultLibraryHint}</span>
              </label>
            </div>
          </SectionCard>

          {/* ④ 权限 */}
          <SectionCard index={4} title={z.settings.section.permission}>
            <p className="mb-2 text-xs text-muted">{z.settings.permission.scope}</p>
            <div className="flex flex-col gap-2">
              {(
                [
                  { value: "admin" as ZAllowScope, label: z.settings.permission.adminsOnly },
                  { value: "canManage" as ZAllowScope, label: z.settings.permission.canManage },
                  { value: "all" as ZAllowScope, label: z.settings.permission.all },
                ]
              ).map((opt) => (
                <label
                  key={opt.value}
                  className={`flex cursor-pointer items-center gap-2.5 rounded-xl border px-3 py-2.5 text-sm transition-colors ${
                    behavior.allowScope === opt.value
                      ? "border-accent/60 bg-accent/10 text-accent"
                      : "border-border bg-card text-muted hover:text-foreground"
                  }`}
                >
                  <input
                    type="radio"
                    name="zlibrary-allow-scope"
                    value={opt.value}
                    checked={behavior.allowScope === opt.value}
                    onChange={() => setBehavior((prev) => ({ ...prev, allowScope: opt.value }))}
                    className="accent-[var(--color-accent)]"
                  />
                  {opt.label}
                </label>
              ))}
            </div>
          </SectionCard>

          {/* ⑤ 兜底指引 */}
          <SectionCard index={5} title={z.settings.section.fallback}>
            <p className="text-xs leading-relaxed text-muted">{z.settings.fallback.desc}</p>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={handleCopyEmail}
                className="inline-flex min-h-9 items-center gap-1.5 rounded-xl border border-border bg-card px-3 py-2 text-xs text-muted transition-colors hover:border-accent/40 hover:text-accent"
              >
                {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
                {copied ? z.settings.fallback.emailCopied : z.settings.fallback.copyEmail}
              </button>
              <input
                type="text"
                value={fallbackDomain}
                onChange={(e) => setFallbackDomain(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    addDomain(fallbackDomain);
                    setFallbackDomain("");
                  }
                }}
                placeholder={z.settings.fallback.addDomain}
                className={`${inputClass} h-9 w-auto min-w-48 flex-1 py-0 font-mono text-xs`}
                spellCheck={false}
              />
              <button
                type="button"
                onClick={() => {
                  addDomain(fallbackDomain);
                  setFallbackDomain("");
                }}
                className="inline-flex h-9 items-center gap-1.5 rounded-xl border border-border bg-card px-3 text-xs text-muted transition-colors hover:border-accent/40 hover:text-accent"
              >
                <Plus className="h-3.5 w-3.5" />
                {z.settings.domain.add}
              </button>
            </div>
          </SectionCard>

          {/* 保存（域名 + 行为 + 权限） */}
          <div className="sticky bottom-4 flex justify-end">
            <button
              type="button"
              onClick={handleSave}
              disabled={saving}
              className="inline-flex min-h-10 items-center gap-1.5 rounded-xl bg-accent px-5 py-2.5 text-sm font-medium text-white shadow-[var(--shadow-float)] transition-colors hover:bg-accent-hover disabled:opacity-50"
            >
              {saving && <Loader2 className="h-4 w-4 animate-spin" />}
              {z.settings.saved}
            </button>
          </div>
        </>
      )}

      {/* 解绑二次确认弹窗（复用 UploadDialog 的 fixed inset-0 z-50 居中弹窗模式） */}
      {unbindOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <button
            type="button"
            aria-label="close"
            onClick={() => setUnbindOpen(false)}
            className="absolute inset-0 h-full w-full cursor-default bg-overlay"
          />
          <div
            className="relative w-full max-w-sm rounded-2xl border border-border bg-elevated p-5 shadow-modal"
            role="alertdialog"
            aria-label={z.settings.action.unbind}
          >
            <div className="flex items-start gap-3">
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-danger/10 text-danger">
                <Unplug className="h-4 w-4" />
              </span>
              <p className="text-sm leading-relaxed text-foreground">{z.settings.unbindConfirm}</p>
            </div>
            <div className="mt-4 flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setUnbindOpen(false)}
                className="inline-flex min-h-9 items-center rounded-xl border border-border px-4 py-2 text-sm text-muted transition-colors hover:text-foreground"
              >
                {t.common.cancel}
              </button>
              <button
                type="button"
                onClick={handleUnbind}
                disabled={unbinding}
                className="inline-flex min-h-9 items-center gap-1.5 rounded-xl bg-danger px-4 py-2 text-sm font-medium text-white transition-colors hover:opacity-90 disabled:opacity-50"
              >
                {unbinding && <Loader2 className="h-4 w-4 animate-spin" />}
                {z.settings.action.unbind}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 解绑确认弹窗挂载于面板尾部 */}
    </div>
  );
}
