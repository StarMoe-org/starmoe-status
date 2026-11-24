"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  AlertTriangle,
  BellRing,
  Bot,
  CheckCircle2,
  Cpu,
  GitBranch,
  HardDrive,
  KeyRound,
  Plus,
  Radio,
  RefreshCw,
  Send,
  Server,
  ShieldAlert,
  ShieldCheck,
} from "lucide-react";
import { SiteHeader } from "@/components/SiteHeader";
import { SiteFooter } from "@/components/SiteFooter";
import {
  ApiError,
  createIncident,
  fetchAdminOverview,
  sendTestAlert,
  updateIncident,
} from "@/lib/api";
import type {
  AdminNodeOverview,
  AdminOverview,
  ArgoCDAppStatus,
  ExpiringCert,
  Incident,
  IncidentImpact,
  IncidentStatus,
  IncidentType,
  Severity,
  SubItemStatus,
  UnhealthyPod,
} from "@/lib/types";
import {
  formatDateTime,
  formatRelativeTime,
  incidentImpactMeta,
  incidentStatusLabel,
  monitorStateMeta,
} from "@/lib/format";

function pctColor(pct: number, warn = 75, crit = 90): string {
  if (pct >= crit) return "var(--danger-strong)";
  if (pct >= warn) return "var(--warning-strong)";
  return "var(--success-strong)";
}

function WatermarkBar({
  label,
  value,
  displayText,
  warn = 75,
  crit = 90,
}: {
  label: string;
  value: number;
  displayText: string;
  warn?: number;
  crit?: number;
}) {
  const clamped = Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0));
  const color = pctColor(clamped, warn, crit);
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-xs tabular-nums">
        <span className="text-[var(--text-muted)]">{label}</span>
        <span className="font-medium text-[var(--text)]">{displayText}</span>
      </div>
      <div className="h-1.5 w-full rounded-full bg-[var(--line-divider)] overflow-hidden">
        <div
          className="h-full rounded-full transition-all duration-300"
          style={{ width: `${clamped}%`, backgroundColor: color }}
        />
      </div>
    </div>
  );
}

function NodeCard({ node }: { node: AdminNodeOverview }) {
  const meta = monitorStateMeta(node.state);
  const hm = node.host_metrics;
  const cpuPct =
    hm && hm.cpu_cores && hm.cpu_cores > 0
      ? Math.min(100, (hm.cpu_load1 / hm.cpu_cores) * 100)
      : hm
      ? Math.min(100, hm.cpu_load1 * 25)
      : 0;

  return (
    <div className="sm-card p-5 space-y-4">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Server className="w-4 h-4 text-[var(--brand)]" />
          <h3 className="text-base font-bold font-mono-code text-[var(--text)]">{node.cluster}</h3>
        </div>
        <span
          className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-semibold"
          style={{ color: meta.color, backgroundColor: meta.bg }}
        >
          <span
            className="h-1.5 w-1.5 rounded-full"
            style={{ backgroundColor: meta.barColor }}
          />
          {meta.label}
        </span>
      </div>

      {hm ? (
        <div className="space-y-2.5">
          <WatermarkBar
            label="CPU Load1"
            value={cpuPct}
            displayText={
              hm.cpu_cores
                ? `${hm.cpu_load1.toFixed(2)} / ${hm.cpu_cores}c`
                : hm.cpu_load1.toFixed(2)
            }
            warn={75}
            crit={95}
          />
          <WatermarkBar
            label="内存使用"
            value={hm.mem_used_pct}
            displayText={`${hm.mem_used_pct.toFixed(1)}%`}
            warn={80}
            crit={92}
          />
          <WatermarkBar
            label="Swap 使用"
            value={hm.swap_used_pct}
            displayText={`${hm.swap_used_pct.toFixed(1)}%`}
            warn={70}
            crit={90}
          />
          <WatermarkBar
            label="根磁盘 (/)"
            value={hm.root_disk_pct}
            displayText={`${hm.root_disk_pct.toFixed(1)}%`}
            warn={75}
            crit={85}
          />
          {hm.extra_disk_pct != null && hm.extra_disk_pct > 0 && (
            <WatermarkBar
              label="扩展磁盘"
              value={hm.extra_disk_pct}
              displayText={`${hm.extra_disk_pct.toFixed(1)}%`}
              warn={80}
              crit={90}
            />
          )}
        </div>
      ) : (
        <div className="py-4 text-xs text-[var(--text-muted)] text-center">
          尚未收到该节点 Agent 心跳
        </div>
      )}

      {hm?.mounts && hm.mounts.length > 0 && (
        <div className="pt-2 border-t border-[var(--line-divider)] space-y-1">
          {hm.mounts.map((m) => (
            <div key={m.path} className="flex items-center justify-between text-[11px]">
              <span className="font-mono-code text-[var(--text-muted)] truncate">{m.path}</span>
              <span
                className="font-semibold shrink-0 ml-2"
                style={{ color: m.ok ? "var(--success)" : "var(--danger)" }}
              >
                {m.ok ? "挂载正常" : "挂载异常"}
              </span>
            </div>
          ))}
        </div>
      )}

      <div className="pt-2 border-t border-[var(--line-divider)] flex items-center justify-between text-[11px] text-[var(--text-muted)] tabular-nums">
        <span>心跳：{formatRelativeTime(node.last_heartbeat_at)}</span>
        <span>
          异常 Pod：{node.unhealthy_pods?.length ?? 0}
        </span>
      </div>
    </div>
  );
}

export default function AdminPage() {
  const [overview, setOverview] = useState<AdminOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [authRequired, setAuthRequired] = useState(false);
  const [devToken, setDevToken] = useState("");
  const [tokenInput, setTokenInput] = useState("");
  const [error, setError] = useState<string | null>(null);

  // Test alert panel state
  const [showTestAlert, setShowTestAlert] = useState(false);
  const [testChannel, setTestChannel] = useState<"bot" | "email" | "all">("all");
  const [testSeverity, setTestSeverity] = useState<Severity>("P1");
  const [testMessage, setTestMessage] = useState("来自 StarMoe Status 运维台的手动告警连通性测试");
  const [testSending, setTestSending] = useState(false);
  const [testResult, setTestResult] = useState<string | null>(null);

  // Monitor filter
  const [monFilter, setMonFilter] = useState<"all" | "internal" | "abnormal">("all");

  // Create incident form state
  const [incType, setIncType] = useState<IncidentType>("incident");
  const [incTitle, setIncTitle] = useState("");
  const [incStatus, setIncStatus] = useState<IncidentStatus>("investigating");
  const [incImpact, setIncImpact] = useState<IncidentImpact>("minor");
  const [incAffected, setIncAffected] = useState("");
  const [incMessage, setIncMessage] = useState("");
  const [incStartsAt, setIncStartsAt] = useState("");
  const [incEndsAt, setIncEndsAt] = useState("");
  const [incSubmitting, setIncSubmitting] = useState(false);
  const [incFeedback, setIncFeedback] = useState<string | null>(null);

  // Update existing incident state
  const [updatingId, setUpdatingId] = useState<string | null>(null);
  const [updateStatus, setUpdateStatus] = useState<IncidentStatus>("monitoring");
  const [updateMessage, setUpdateMessage] = useState("");

  useEffect(() => {
    try {
      const saved = sessionStorage.getItem("starmoe-admin-dev-token") || "";
      if (saved) setDevToken(saved);
    } catch {
      // ignore
    }
  }, []);

  const load = useCallback(
    async (tokenOverride?: string, isManual = false) => {
      if (isManual) setRefreshing(true);
      const tok = tokenOverride !== undefined ? tokenOverride : devToken;
      try {
        const data = await fetchAdminOverview(tok);
        setOverview(data);
        setAuthRequired(false);
        setError(null);
      } catch (err) {
        if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
          setAuthRequired(true);
          setError(err.message);
        } else {
          setError(err instanceof Error ? err.message : "加载运维大盘失败");
        }
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [devToken]
  );

  useEffect(() => {
    load();
    const timer = setInterval(() => {
      void load();
    }, 30000);
    return () => clearInterval(timer);
  }, [load]);

  // Derived tables
  const allUnhealthyPods = useMemo(() => {
    const rows: Array<{ cluster: string; pod: UnhealthyPod }> = [];
    for (const n of overview?.nodes ?? []) {
      for (const p of n.unhealthy_pods ?? []) {
        rows.push({ cluster: n.cluster, pod: p });
      }
    }
    return rows;
  }, [overview]);

  const allArgoIssues = useMemo(() => {
    const rows: Array<{ cluster: string; app: ArgoCDAppStatus }> = [];
    for (const n of overview?.nodes ?? []) {
      for (const app of n.argocd_unhealthy ?? []) {
        rows.push({ cluster: n.cluster, app });
      }
    }
    return rows;
  }, [overview]);

  const allExpiringCerts = useMemo(() => {
    const rows: Array<{ cluster: string; cert: ExpiringCert }> = [];
    for (const n of overview?.nodes ?? []) {
      for (const cert of n.expiring_certs ?? []) {
        rows.push({ cluster: n.cluster, cert });
      }
    }
    return rows;
  }, [overview]);

  const sekaiSessions = useMemo(() => {
    const items: SubItemStatus[] = [];
    for (const n of overview?.nodes ?? []) {
      for (const dc of n.deep_checks ?? []) {
        if (
          dc.monitor_id === "pjsk-servers" ||
          dc.monitor_id.includes("sekai") ||
          (dc.sub_items && dc.sub_items.length > 0)
        ) {
          for (const sub of dc.sub_items ?? []) {
            items.push(sub);
          }
        }
      }
    }
    return items;
  }, [overview]);

  const filteredMonitors = useMemo(() => {
    const list = overview?.monitors ?? [];
    if (monFilter === "internal") return list.filter((m) => m.visibility === "internal");
    if (monFilter === "abnormal") return list.filter((m) => m.state !== "up");
    return list;
  }, [overview, monFilter]);

  const handleTokenSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const clean = tokenInput.trim();
    setDevToken(clean);
    try {
      sessionStorage.setItem("starmoe-admin-dev-token", clean);
    } catch {
      // ignore
    }
    void load(clean, true);
  };

  const handleTestAlert = async (e: React.FormEvent) => {
    e.preventDefault();
    setTestSending(true);
    setTestResult(null);
    try {
      const res = await sendTestAlert(
        {
          channel: testChannel,
          severity: testSeverity,
          message: testMessage.trim() || undefined,
        },
        devToken
      );
      const summary = Object.entries(res.results || {})
        .map(([k, v]) => `${k}: ${v}`)
        .join(" · ");
      setTestResult(`测试告警已发送（${summary || "ok"}）`);
      void load(devToken);
    } catch (err) {
      setTestResult(`发送失败：${err instanceof Error ? err.message : "未知错误"}`);
    } finally {
      setTestSending(false);
    }
  };

  const handleCreateIncident = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!incTitle.trim() || !incMessage.trim()) return;
    setIncSubmitting(true);
    setIncFeedback(null);
    try {
      const affected = incAffected
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      const startsAt = incStartsAt ? Math.floor(new Date(incStartsAt).getTime() / 1000) : undefined;
      const endsAt = incEndsAt ? Math.floor(new Date(incEndsAt).getTime() / 1000) : undefined;
      await createIncident(
        {
          type: incType,
          title: incTitle.trim(),
          status: incStatus,
          impact: incType === "maintenance" ? "maintenance" : incImpact,
          affected_monitors: affected,
          message: incMessage.trim(),
          starts_at: startsAt,
          ends_at: endsAt,
        },
        devToken
      );
      setIncTitle("");
      setIncMessage("");
      setIncAffected("");
      setIncStartsAt("");
      setIncEndsAt("");
      setIncFeedback("公告已发布");
      void load(devToken);
    } catch (err) {
      setIncFeedback(`发布失败：${err instanceof Error ? err.message : "未知错误"}`);
    } finally {
      setIncSubmitting(false);
    }
  };

  const handleUpdateIncident = async (inc: Incident) => {
    if (!updateMessage.trim()) return;
    try {
      await updateIncident(
        inc.id,
        {
          status: updateStatus,
          message: updateMessage.trim(),
        },
        devToken
      );
      setUpdatingId(null);
      setUpdateMessage("");
      void load(devToken);
    } catch (err) {
      setIncFeedback(`更新失败：${err instanceof Error ? err.message : "未知错误"}`);
    }
  };

  return (
    <>
      <SiteHeader />

      <main className="flex-1 max-w-6xl w-full mx-auto px-4 sm:px-6 py-8 space-y-6">
        {/* Top bar */}
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-2xl font-bold tracking-tight text-[var(--text)]">
                内部运维控制台
              </h1>
              <span className="px-2 py-0.5 rounded-full text-[11px] font-semibold bg-[var(--brand-wash)] text-[var(--brand)] border border-[var(--card-edge)]">
                Cloudflare Access
              </span>
            </div>
            <p className="text-xs text-[var(--text-muted)] mt-1">
              4 节点资源水位、异常 Pod、ArgoCD 同步、Sekai 五服会话与公告/告警管理
            </p>
          </div>

          {overview && (
            <div className="flex flex-wrap items-center gap-2.5">
              {/* Moebot SSE Indicator */}
              <div
                className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full text-xs font-medium border border-[var(--line-border)] bg-[var(--surface-1)]"
                title={
                  overview.bot_last_seen_at
                    ? `最近活跃：${formatDateTime(overview.bot_last_seen_at)}`
                    : "尚无 Bot 连接记录"
                }
              >
                <Bot className="w-3.5 h-3.5 text-[var(--brand)]" />
                <span
                  className="h-2 w-2 rounded-full"
                  style={{
                    backgroundColor: overview.bot_online
                      ? "var(--success-strong)"
                      : "var(--danger-strong)",
                  }}
                />
                <span className="text-[var(--text)]">
                  Moebot SSE {overview.bot_online ? `在线 (${overview.bot_clients})` : "离线"}
                </span>
              </div>

              {/* Test Alert Button */}
              <button
                type="button"
                onClick={() => setShowTestAlert((v) => !v)}
                className="inline-flex items-center gap-1.5 px-3.5 py-1.5 rounded-full text-xs font-semibold bg-[var(--brand)] text-[var(--on-brand)] hover:opacity-90 transition-opacity cursor-pointer"
              >
                <BellRing className="w-3.5 h-3.5" />
                测试告警
              </button>

              <button
                type="button"
                onClick={() => load(devToken, true)}
                disabled={refreshing}
                title="立即刷新"
                className="inline-flex items-center justify-center h-9 w-9 rounded-full border border-[var(--line-border)] bg-[var(--surface-1)] text-[var(--text)] hover:bg-[var(--overlay-hover)] transition-colors cursor-pointer"
              >
                <RefreshCw className={`w-4 h-4 ${refreshing ? "animate-spin" : ""}`} />
              </button>
            </div>
          )}
        </div>

        {/* Auth Gate when 401/403 */}
        {authRequired && !overview && (
          <section className="sm-card p-6 sm:p-8 max-w-lg mx-auto space-y-4">
            <div className="flex items-center gap-3">
              <div className="h-10 w-10 rounded-xl bg-[var(--warning-wash)] text-[var(--warning)] flex items-center justify-center">
                <KeyRound className="w-5 h-5" />
              </div>
              <div>
                <h2 className="text-base font-semibold text-[var(--text)]">需要管理员鉴权</h2>
                <p className="text-xs text-[var(--text-muted)]">
                  生产环境通过 Cloudflare Access 自动鉴权；本地调试可输入开发令牌。
                </p>
              </div>
            </div>
            {error && <div className="text-xs text-[var(--danger)]">{error}</div>}
            <form onSubmit={handleTokenSubmit} className="flex gap-2">
              <input
                type="password"
                value={tokenInput}
                onChange={(e) => setTokenInput(e.target.value)}
                placeholder="输入 ADMIN_DEV_TOKEN（仅存当前会话）"
                className="flex-1 px-3 py-2 rounded-xl text-sm bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)] focus:outline-none focus:border-[var(--brand)]"
              />
              <button
                type="submit"
                className="px-4 py-2 rounded-xl text-sm font-semibold bg-[var(--brand)] text-[var(--on-brand)] cursor-pointer"
              >
                验证
              </button>
            </form>
          </section>
        )}

        {/* Loading / Generic Error */}
        {loading && !overview && !authRequired && (
          <div className="sm-card p-8 text-center text-sm text-[var(--text-muted)]">
            正在加载内部运维数据…
          </div>
        )}

        {error && !authRequired && !overview && (
          <div className="sm-card p-6 text-sm text-[var(--danger)] flex items-center gap-2">
            <AlertTriangle className="w-4 h-4" /> {error}
          </div>
        )}

        {/* Main Dashboard */}
        {overview && (
          <>
            {/* Test Alert Drawer */}
            {showTestAlert && (
              <section className="sm-card p-5 border border-[var(--card-edge-hover)] space-y-4">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2 text-sm font-semibold text-[var(--text)]">
                    <BellRing className="w-4 h-4 text-[var(--brand)]" />
                    发送测试告警（验证 Moebot SSE 与邮件通道）
                  </div>
                  <button
                    type="button"
                    onClick={() => setShowTestAlert(false)}
                    className="text-xs text-[var(--text-muted)] hover:text-[var(--text)] cursor-pointer"
                  >
                    收起
                  </button>
                </div>

                <form onSubmit={handleTestAlert} className="grid grid-cols-1 sm:grid-cols-4 gap-3">
                  <select
                    value={testChannel}
                    onChange={(e) => setTestChannel(e.target.value as "bot" | "email" | "all")}
                    className="px-3 py-2 rounded-xl text-xs bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                  >
                    <option value="all">全部通道 (Bot + Email)</option>
                    <option value="bot">仅 Moebot SSE</option>
                    <option value="email">仅邮件通道</option>
                  </select>

                  <select
                    value={testSeverity}
                    onChange={(e) => setTestSeverity(e.target.value as Severity)}
                    className="px-3 py-2 rounded-xl text-xs bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                  >
                    <option value="P0">P0（严重）</option>
                    <option value="P1">P1（警告）</option>
                    <option value="P2">P2（提示）</option>
                  </select>

                  <input
                    type="text"
                    value={testMessage}
                    onChange={(e) => setTestMessage(e.target.value)}
                    placeholder="测试告警内容"
                    className="sm:col-span-2 px-3 py-2 rounded-xl text-xs bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                  />

                  <div className="sm:col-span-4 flex items-center justify-between gap-3">
                    <span className="text-xs text-[var(--text-muted)]">
                      {testResult || "发送后将立即通过 SSE 推送给在线 Moebot 或按策略触发邮件"}
                    </span>
                    <button
                      type="submit"
                      disabled={testSending}
                      className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl text-xs font-semibold bg-[var(--brand)] text-[var(--on-brand)] cursor-pointer disabled:opacity-50"
                    >
                      <Send className="w-3.5 h-3.5" />
                      {testSending ? "发送中…" : "立即发送"}
                    </button>
                  </div>
                </form>
              </section>
            )}

            {/* 4-Node Watermark Cards */}
            <section className="space-y-3">
              <div className="flex items-center justify-between px-1">
                <div className="flex items-center gap-2 text-sm font-semibold text-[var(--text)]">
                  <Cpu className="w-4 h-4 text-[var(--brand)]" />
                  4 节点实时资源水位（DE / JP1 / JP2 / US）
                </div>
                <span className="text-xs text-[var(--text-muted)] tabular-nums">
                  数据时间：{formatRelativeTime(overview.updated_at)}
                </span>
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                {overview.nodes.map((node) => (
                  <NodeCard key={node.cluster} node={node} />
                ))}
              </div>
            </section>

            {/* Sekai 5-Server Active Sessions */}
            <section className="sm-card overflow-hidden">
              <div className="px-5 py-4 border-b border-[var(--line-divider)] flex items-center justify-between">
                <div className="flex items-center gap-2 text-sm font-semibold text-[var(--text)]">
                  <Radio className="w-4 h-4 text-[var(--brand)]" />
                  Sekai 五服登录会话明细（JP / CN / TW / KR / EN）
                </div>
                <span className="text-xs text-[var(--text-muted)]">
                  来自 US 集群深层探针
                </span>
              </div>

              {sekaiSessions.length === 0 ? (
                <div className="p-6 text-center text-xs text-[var(--text-muted)]">
                  尚未收到 Sekai 五服会话上报数据（等待 US Agent 心跳）
                </div>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-left text-xs">
                    <thead className="bg-[var(--surface-1)]/60 text-[var(--text-muted)]">
                      <tr>
                        <th className="px-5 py-2.5 font-medium">区服</th>
                        <th className="px-5 py-2.5 font-medium">状态</th>
                        <th className="px-5 py-2.5 font-medium">活跃会话数</th>
                        <th className="px-5 py-2.5 font-medium">说明 / 最近错误</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-[var(--line-divider)] tabular-nums">
                      {sekaiSessions.map((sub) => {
                        const sm = monitorStateMeta(sub.state);
                        return (
                          <tr key={sub.key} className="hover:bg-[var(--overlay-hover)]">
                            <td className="px-5 py-3 font-semibold text-[var(--text)]">
                              {sub.label}{" "}
                              <span className="font-mono-code text-[11px] text-[var(--text-muted)]">
                                ({sub.key})
                              </span>
                            </td>
                            <td className="px-5 py-3">
                              <span
                                className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-semibold"
                                style={{ color: sm.color, backgroundColor: sm.bg }}
                              >
                                {sm.label}
                              </span>
                            </td>
                            <td className="px-5 py-3 font-mono-code font-semibold text-[var(--text)]">
                              {sub.active_sessions != null ? sub.active_sessions : "—"}
                            </td>
                            <td className="px-5 py-3 text-[var(--text-muted)]">
                              {sub.last_error ? (
                                <span className="text-[var(--danger)]">{sub.last_error}</span>
                              ) : (
                                sub.message || "正常"
                              )}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
            </section>

            {/* Unhealthy Pods & ArgoCD Status */}
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              {/* Unhealthy Pods */}
              <section className="sm-card overflow-hidden">
                <div className="px-5 py-4 border-b border-[var(--line-divider)] flex items-center justify-between">
                  <div className="flex items-center gap-2 text-sm font-semibold text-[var(--text)]">
                    <ShieldAlert className="w-4 h-4 text-[var(--warning)]" />
                    异常 Pod 列表
                  </div>
                  <span className="text-xs text-[var(--text-muted)] tabular-nums">
                    {allUnhealthyPods.length} 个异常
                  </span>
                </div>

                {allUnhealthyPods.length === 0 ? (
                  <div className="p-6 text-center text-xs text-[var(--success)] flex items-center justify-center gap-2">
                    <CheckCircle2 className="w-4 h-4" /> 所有集群 Pod 运行健康
                  </div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-left text-xs">
                      <thead className="bg-[var(--surface-1)]/60 text-[var(--text-muted)]">
                        <tr>
                          <th className="px-4 py-2.5 font-medium">节点</th>
                          <th className="px-4 py-2.5 font-medium">Namespace / Pod</th>
                          <th className="px-4 py-2.5 font-medium">状态</th>
                          <th className="px-4 py-2.5 font-medium">重启</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-[var(--line-divider)] tabular-nums">
                        {allUnhealthyPods.map(({ cluster, pod }, idx) => (
                          <tr key={`${cluster}-${pod.namespace}-${pod.name}-${idx}`}>
                            <td className="px-4 py-2.5 font-mono-code font-semibold text-[var(--text)]">
                              {cluster}
                            </td>
                            <td className="px-4 py-2.5">
                              <div className="font-mono-code text-[var(--text)]">{pod.name}</div>
                              <div className="text-[11px] text-[var(--text-muted)]">
                                {pod.namespace} {pod.reason ? `· ${pod.reason}` : ""}
                              </div>
                            </td>
                            <td className="px-4 py-2.5 text-[var(--danger)]">
                              {pod.phase} ({pod.ready})
                            </td>
                            <td className="px-4 py-2.5 font-mono-code text-[var(--warning)]">
                              {pod.restarts}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </section>

              {/* ArgoCD Status */}
              <section className="sm-card overflow-hidden">
                <div className="px-5 py-4 border-b border-[var(--line-divider)] flex items-center justify-between">
                  <div className="flex items-center gap-2 text-sm font-semibold text-[var(--text)]">
                    <GitBranch className="w-4 h-4 text-[var(--brand)]" />
                    ArgoCD 同步状态
                  </div>
                  <span className="text-xs text-[var(--text-muted)] tabular-nums">
                    {allArgoIssues.length} 个异常应用
                  </span>
                </div>

                {allArgoIssues.length === 0 ? (
                  <div className="p-6 text-center text-xs text-[var(--success)] flex items-center justify-center gap-2">
                    <ShieldCheck className="w-4 h-4" /> 所有 ArgoCD 应用已同步且健康
                  </div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-left text-xs">
                      <thead className="bg-[var(--surface-1)]/60 text-[var(--text-muted)]">
                        <tr>
                          <th className="px-4 py-2.5 font-medium">节点</th>
                          <th className="px-4 py-2.5 font-medium">Application</th>
                          <th className="px-4 py-2.5 font-medium">Sync</th>
                          <th className="px-4 py-2.5 font-medium">Health</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-[var(--line-divider)]">
                        {allArgoIssues.map(({ cluster, app }, idx) => (
                          <tr key={`${cluster}-${app.name}-${idx}`}>
                            <td className="px-4 py-2.5 font-mono-code font-semibold text-[var(--text)]">
                              {cluster}
                            </td>
                            <td className="px-4 py-2.5 font-mono-code text-[var(--text)]">
                              {app.name}
                            </td>
                            <td className="px-4 py-2.5 text-[var(--warning)]">{app.sync_status}</td>
                            <td className="px-4 py-2.5 text-[var(--danger)]">{app.health_status}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </section>
            </div>

            {/* Expiring Certs (only shown when present) */}
            {allExpiringCerts.length > 0 && (
              <section className="sm-card overflow-hidden">
                <div className="px-5 py-4 border-b border-[var(--line-divider)] flex items-center gap-2 text-sm font-semibold text-[var(--warning)]">
                  <HardDrive className="w-4 h-4" />
                  临期或异常 TLS 证书 ({allExpiringCerts.length})
                </div>
                <div className="overflow-x-auto">
                  <table className="w-full text-left text-xs">
                    <thead className="bg-[var(--surface-1)]/60 text-[var(--text-muted)]">
                      <tr>
                        <th className="px-5 py-2.5 font-medium">节点</th>
                        <th className="px-5 py-2.5 font-medium">Namespace / Certificate</th>
                        <th className="px-5 py-2.5 font-medium">Ready</th>
                        <th className="px-5 py-2.5 font-medium">剩余天数</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-[var(--line-divider)] tabular-nums">
                      {allExpiringCerts.map(({ cluster, cert }, idx) => (
                        <tr key={`${cluster}-${cert.namespace}-${cert.name}-${idx}`}>
                          <td className="px-5 py-2.5 font-mono-code font-semibold">{cluster}</td>
                          <td className="px-5 py-2.5 font-mono-code">
                            {cert.namespace}/{cert.name}
                          </td>
                          <td className="px-5 py-2.5">{cert.ready ? "Yes" : "No"}</td>
                          <td className="px-5 py-2.5 text-[var(--warning)] font-semibold">
                            {cert.days_left} 天
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>
            )}

            {/* Full Monitors Table (Public + Internal) */}
            <section className="sm-card overflow-hidden">
              <div className="px-5 py-4 border-b border-[var(--line-divider)] flex flex-wrap items-center justify-between gap-3">
                <div className="flex items-center gap-2 text-sm font-semibold text-[var(--text)]">
                  <Activity className="w-4 h-4 text-[var(--brand)]" />
                  全量监控项（含内部监控）
                </div>
                <div className="flex items-center gap-1.5 text-xs">
                  {(
                    [
                      { key: "all", label: `全部 (${overview.monitors.length})` },
                      {
                        key: "internal",
                        label: `仅内部 (${overview.monitors.filter((m) => m.visibility === "internal").length})`,
                      },
                      {
                        key: "abnormal",
                        label: `仅异常 (${overview.monitors.filter((m) => m.state !== "up").length})`,
                      },
                    ] as const
                  ).map((tab) => (
                    <button
                      key={tab.key}
                      type="button"
                      onClick={() => setMonFilter(tab.key)}
                      className={`px-3 py-1 rounded-full transition-colors cursor-pointer ${
                        monFilter === tab.key
                          ? "bg-[var(--brand)] text-[var(--on-brand)] font-semibold"
                          : "bg-[var(--surface-1)] text-[var(--text-muted)] hover:text-[var(--text)]"
                      }`}
                    >
                      {tab.label}
                    </button>
                  ))}
                </div>
              </div>

              <div className="overflow-x-auto">
                <table className="w-full text-left text-xs">
                  <thead className="bg-[var(--surface-1)]/60 text-[var(--text-muted)]">
                    <tr>
                      <th className="px-4 py-2.5 font-medium">组件</th>
                      <th className="px-4 py-2.5 font-medium">可见性</th>
                      <th className="px-4 py-2.5 font-medium">级别 / 节点</th>
                      <th className="px-4 py-2.5 font-medium">状态</th>
                      <th className="px-4 py-2.5 font-medium">延迟</th>
                      <th className="px-4 py-2.5 font-medium">连续失败 / 信息</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[var(--line-divider)] tabular-nums">
                    {filteredMonitors.map((m) => {
                      const sm = monitorStateMeta(m.state);
                      return (
                        <tr key={m.id} className="hover:bg-[var(--overlay-hover)]">
                          <td className="px-4 py-2.5">
                            <div className="font-medium text-[var(--text)]">{m.name}</div>
                            <div className="font-mono-code text-[11px] text-[var(--text-muted)]">
                              {m.id}
                            </div>
                          </td>
                          <td className="px-4 py-2.5">
                            <span
                              className={`px-2 py-0.5 rounded-full text-[10px] font-semibold ${
                                m.visibility === "internal"
                                  ? "bg-[var(--warning-wash)] text-[var(--warning)]"
                                  : "bg-[var(--overlay-hover)] text-[var(--text-muted)]"
                              }`}
                            >
                              {m.visibility === "internal" ? "内部" : "公开"}
                            </span>
                          </td>
                          <td className="px-4 py-2.5 font-mono-code text-[var(--text-muted)]">
                            {m.severity} {m.cluster ? `· ${m.cluster}` : ""}
                          </td>
                          <td className="px-4 py-2.5">
                            <span
                              className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-semibold"
                              style={{ color: sm.color, backgroundColor: sm.bg }}
                            >
                              {sm.label}
                            </span>
                          </td>
                          <td className="px-4 py-2.5 text-[var(--text-muted)]">
                            {m.latency_ms != null && m.latency_ms > 0 ? `${m.latency_ms} ms` : "—"}
                          </td>
                          <td className="px-4 py-2.5 text-[var(--text-muted)] max-w-xs truncate">
                            {m.consecutive_fails > 0 && (
                              <span className="text-[var(--danger)] mr-1.5">
                                ({m.consecutive_fails}次)
                              </span>
                            )}
                            {m.message || "—"}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </section>

            {/* Incident / Maintenance Management */}
            <section className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              {/* Create Form */}
              <div className="sm-card p-5 space-y-4">
                <div className="flex items-center gap-2 text-sm font-semibold text-[var(--text)]">
                  <Plus className="w-4 h-4 text-[var(--brand)]" />
                  发布故障公告 / 创建计划维护窗
                </div>

                <form onSubmit={handleCreateIncident} className="space-y-3 text-xs">
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="block text-[var(--text-muted)] mb-1">类型</label>
                      <select
                        value={incType}
                        onChange={(e) => {
                          const t = e.target.value as IncidentType;
                          setIncType(t);
                          setIncStatus(t === "maintenance" ? "scheduled" : "investigating");
                          setIncImpact(t === "maintenance" ? "maintenance" : "minor");
                        }}
                        className="w-full px-3 py-2 rounded-xl bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                      >
                        <option value="incident">故障公告 (Incident)</option>
                        <option value="maintenance">计划维护 (Maintenance)</option>
                      </select>
                    </div>
                    <div>
                      <label className="block text-[var(--text-muted)] mb-1">状态</label>
                      <select
                        value={incStatus}
                        onChange={(e) => setIncStatus(e.target.value as IncidentStatus)}
                        className="w-full px-3 py-2 rounded-xl bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                      >
                        {incType === "incident" ? (
                          <>
                            <option value="investigating">排查中 (investigating)</option>
                            <option value="identified">已定位 (identified)</option>
                            <option value="monitoring">恢复观察 (monitoring)</option>
                            <option value="resolved">已解决 (resolved)</option>
                          </>
                        ) : (
                          <>
                            <option value="scheduled">计划维护 (scheduled)</option>
                            <option value="in_progress">维护进行中 (in_progress)</option>
                            <option value="resolved">维护完成 (resolved)</option>
                          </>
                        )}
                      </select>
                    </div>
                  </div>

                  <div>
                    <label className="block text-[var(--text-muted)] mb-1">标题</label>
                    <input
                      type="text"
                      required
                      value={incTitle}
                      onChange={(e) => setIncTitle(e.target.value)}
                      placeholder="例如：MoeSekai 国内加速线临时波动排查"
                      className="w-full px-3 py-2 rounded-xl bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                    />
                  </div>

                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="block text-[var(--text-muted)] mb-1">影响程度</label>
                      <select
                        value={incImpact}
                        onChange={(e) => setIncImpact(e.target.value as IncidentImpact)}
                        className="w-full px-3 py-2 rounded-xl bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                      >
                        <option value="minor">轻微波动 (minor)</option>
                        <option value="major">主要故障 (major)</option>
                        <option value="critical">严重故障 (critical)</option>
                        <option value="maintenance">计划维护 (maintenance)</option>
                      </select>
                    </div>
                    <div>
                      <label className="block text-[var(--text-muted)] mb-1">
                        受影响组件 ID（逗号分隔）
                      </label>
                      <input
                        type="text"
                        value={incAffected}
                        onChange={(e) => setIncAffected(e.target.value)}
                        placeholder="pjsk-ranking-cn, pjsk-storage-cn"
                        className="w-full px-3 py-2 rounded-xl font-mono-code bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                      />
                    </div>
                  </div>

                  {incType === "maintenance" && (
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className="block text-[var(--text-muted)] mb-1">维护开始时间</label>
                        <input
                          type="datetime-local"
                          value={incStartsAt}
                          onChange={(e) => setIncStartsAt(e.target.value)}
                          className="w-full px-3 py-2 rounded-xl bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                        />
                      </div>
                      <div>
                        <label className="block text-[var(--text-muted)] mb-1">维护结束时间</label>
                        <input
                          type="datetime-local"
                          value={incEndsAt}
                          onChange={(e) => setIncEndsAt(e.target.value)}
                          className="w-full px-3 py-2 rounded-xl bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                        />
                      </div>
                    </div>
                  )}

                  <div>
                    <label className="block text-[var(--text-muted)] mb-1">公告内容</label>
                    <textarea
                      required
                      rows={3}
                      value={incMessage}
                      onChange={(e) => setIncMessage(e.target.value)}
                      placeholder="描述当前现象、排查进展或预计恢复时间…"
                      className="w-full px-3 py-2 rounded-xl bg-[var(--surface-1)] border border-[var(--line-border)] text-[var(--text)]"
                    />
                  </div>

                  <div className="flex items-center justify-between gap-2 pt-1">
                    <span className="text-[var(--text-muted)]">{incFeedback}</span>
                    <button
                      type="submit"
                      disabled={incSubmitting}
                      className="px-4 py-2 rounded-xl font-semibold bg-[var(--brand)] text-[var(--on-brand)] cursor-pointer disabled:opacity-50"
                    >
                      {incSubmitting ? "发布中…" : "发布公告"}
                    </button>
                  </div>
                </form>
              </div>

              {/* Existing Incidents & Inline Updates */}
              <div className="sm-card p-5 space-y-4">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-semibold text-[var(--text)]">
                    近期公告与状态更新 ({overview.incidents.length})
                  </span>
                </div>

                {overview.incidents.length === 0 ? (
                  <div className="py-8 text-center text-xs text-[var(--text-muted)]">
                    暂无已发布的故障公告或维护窗口
                  </div>
                ) : (
                  <div className="space-y-3 max-h-[420px] overflow-y-auto pr-1">
                    {overview.incidents.map((inc) => {
                      const impact = incidentImpactMeta(inc.impact);
                      const isEditing = updatingId === inc.id;
                      return (
                        <div
                          key={inc.id}
                          className="p-3.5 rounded-xl border border-[var(--line-divider)] bg-[var(--surface-1)]/60 space-y-2"
                        >
                          <div className="flex items-start justify-between gap-2">
                            <div>
                              <div className="flex items-center gap-1.5 flex-wrap">
                                <span
                                  className="px-2 py-0.5 rounded-full text-[10px] font-semibold"
                                  style={{ color: impact.color, backgroundColor: impact.bg }}
                                >
                                  {incidentStatusLabel(inc.status)}
                                </span>
                                <span className="text-[11px] text-[var(--text-muted)] tabular-nums">
                                  {formatDateTime(inc.updated_at)}
                                </span>
                              </div>
                              <div className="text-xs font-semibold text-[var(--text)] mt-1">
                                {inc.title}
                              </div>
                            </div>

                            <button
                              type="button"
                              onClick={() => {
                                if (isEditing) {
                                  setUpdatingId(null);
                                } else {
                                  setUpdatingId(inc.id);
                                  setUpdateStatus(
                                    inc.status === "resolved" ? "resolved" : "monitoring"
                                  );
                                  setUpdateMessage("");
                                }
                              }}
                              className="px-2.5 py-1 rounded-lg text-[11px] font-medium border border-[var(--line-border)] text-[var(--text)] hover:bg-[var(--overlay-hover)] cursor-pointer shrink-0"
                            >
                              {isEditing ? "取消" : "追加更新"}
                            </button>
                          </div>

                          {isEditing && (
                            <div className="pt-2 border-t border-[var(--line-divider)] space-y-2 text-xs">
                              <select
                                value={updateStatus}
                                onChange={(e) => setUpdateStatus(e.target.value as IncidentStatus)}
                                className="w-full px-2.5 py-1.5 rounded-lg bg-[var(--surface-2)] border border-[var(--line-border)] text-[var(--text)]"
                              >
                                <option value="investigating">排查中 (investigating)</option>
                                <option value="identified">已定位 (identified)</option>
                                <option value="monitoring">恢复观察 (monitoring)</option>
                                <option value="in_progress">维护进行中 (in_progress)</option>
                                <option value="resolved">已解决 / 完成 (resolved)</option>
                              </select>
                              <textarea
                                rows={2}
                                value={updateMessage}
                                onChange={(e) => setUpdateMessage(e.target.value)}
                                placeholder="输入最新进展…"
                                className="w-full px-2.5 py-1.5 rounded-lg bg-[var(--surface-2)] border border-[var(--line-border)] text-[var(--text)]"
                              />
                              <button
                                type="button"
                                onClick={() => handleUpdateIncident(inc)}
                                className="px-3 py-1.5 rounded-lg font-semibold bg-[var(--brand)] text-[var(--on-brand)] cursor-pointer"
                              >
                                提交更新
                              </button>
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </section>
          </>
        )}
      </main>

      <SiteFooter />
    </>
  );
}
