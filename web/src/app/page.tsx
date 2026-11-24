"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  CalendarClock,
  CheckCircle2,
  History,
  RefreshCw,
  Server,
  Wrench,
  XCircle,
} from "lucide-react";
import { SiteHeader } from "@/components/SiteHeader";
import { SiteFooter } from "@/components/SiteFooter";
import { MonitorRow } from "@/components/MonitorRow";
import { fetchIncidents, fetchSummary } from "@/lib/api";
import type { Incident, PublicSummary, TimeRange } from "@/lib/types";
import {
  formatDateTime,
  formatRelativeTime,
  incidentImpactMeta,
  incidentStatusLabel,
  monitorStateMeta,
  overallStatusMeta,
} from "@/lib/format";

const REFRESH_INTERVAL_SEC = 30;
const RANGE_STORAGE_KEY = "starmoe-status-range";

const TIME_RANGES: { key: TimeRange; label: string }[] = [
  { key: "24h", label: "24h" },
  { key: "7d", label: "7d" },
  { key: "30d", label: "30d" },
  { key: "90d", label: "90d" },
];

type IncidentFilter = "all" | "incident" | "maintenance";

function IncidentBanner({ incident }: { incident: Incident }) {
  const impact = incidentImpactMeta(incident.impact);
  const latestUpdate =
    incident.updates && incident.updates.length > 0
      ? incident.updates[incident.updates.length - 1]
      : null;

  return (
    <div
      className="sm-card p-4 sm:p-5 border-l-4"
      style={{ borderLeftColor: impact.color }}
    >
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span
              className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-semibold"
              style={{ color: impact.color, backgroundColor: impact.bg }}
            >
              {incident.type === "maintenance" ? "计划维护" : impact.label}
            </span>
            <span className="text-xs font-medium text-[var(--text-muted)]">
              {incidentStatusLabel(incident.status)}
            </span>
          </div>
          <h3 className="text-base font-semibold text-[var(--text)]">{incident.title}</h3>
        </div>
        <a
          href="#incidents"
          className="text-xs font-medium text-[var(--link)] hover:underline shrink-0"
        >
          查看进展
        </a>
      </div>

      {latestUpdate && (
        <p className="mt-2.5 text-sm text-[var(--text-muted)] leading-relaxed">
          {latestUpdate.message}
        </p>
      )}

      <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--text-muted)]">
        {incident.starts_at && (
          <span>
            维护窗口：{formatDateTime(incident.starts_at)}
            {incident.ends_at ? ` ~ ${formatDateTime(incident.ends_at)}` : ""}
          </span>
        )}
        <span>更新于 {formatRelativeTime(incident.updated_at)}</span>
      </div>
    </div>
  );
}

function IncidentTimelineCard({ incident }: { incident: Incident }) {
  const impact = incidentImpactMeta(incident.impact);
  const isMaintenance = incident.type === "maintenance";
  const updates = [...(incident.updates ?? [])].reverse();

  return (
    <article className="sm-card p-5 space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1.5 min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span
              className="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-[11px] font-semibold"
              style={{ color: impact.color, backgroundColor: impact.bg }}
            >
              {isMaintenance ? (
                <CalendarClock className="w-3 h-3" />
              ) : (
                <AlertTriangle className="w-3 h-3" />
              )}
              {isMaintenance ? "计划维护" : impact.label}
            </span>
            <span className="text-xs font-medium text-[var(--text-muted)]">
              {incidentStatusLabel(incident.status)}
            </span>
          </div>
          <h3 className="text-base font-semibold text-[var(--text)]">{incident.title}</h3>
        </div>

        <span className="text-xs text-[var(--text-muted)] tabular-nums shrink-0">
          {formatDateTime(incident.created_at)}
        </span>
      </div>

      {incident.starts_at && (
        <div className="text-xs text-[var(--text-muted)] bg-[var(--surface-1)]/60 border border-[var(--line-divider)] rounded-xl px-3.5 py-2 tabular-nums">
          维护窗口：{formatDateTime(incident.starts_at)}
          {incident.ends_at ? ` ~ ${formatDateTime(incident.ends_at)}` : ""}
        </div>
      )}

      {updates.length > 0 && (
        <ol className="relative border-l border-[var(--line-divider)] ml-2 space-y-3.5 pl-4">
          {updates.map((u) => (
            <li key={u.id} className="relative">
              <span
                className="absolute -left-[21px] top-1.5 h-2 w-2 rounded-full ring-4 ring-[var(--surface-1)]"
                style={{ backgroundColor: impact.color }}
              />
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="font-semibold text-[var(--text)]">
                  {incidentStatusLabel(u.status)}
                </span>
                <span className="text-[var(--text-muted)] tabular-nums">
                  {formatDateTime(u.created_at)}
                </span>
              </div>
              <p className="mt-1 text-sm text-[var(--text-muted)] leading-relaxed whitespace-pre-wrap">
                {u.message}
              </p>
            </li>
          ))}
        </ol>
      )}
    </article>
  );
}

export default function StatusPage() {
  const [range, setRange] = useState<TimeRange>("90d");
  const [rangeReady, setRangeReady] = useState(false);
  const [summary, setSummary] = useState<PublicSummary | null>(null);
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [incidentFilter, setIncidentFilter] = useState<IncidentFilter>("all");
  const [loading, setLoading] = useState(true);
  const [switchingRange, setSwitchingRange] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [countdown, setCountdown] = useState(REFRESH_INTERVAL_SEC);

  useEffect(() => {
    try {
      const saved = localStorage.getItem(RANGE_STORAGE_KEY);
      if (saved === "24h" || saved === "7d" || saved === "30d" || saved === "90d") {
        setRange(saved);
      }
    } catch {
      // ignore storage errors
    }
    setRangeReady(true);
  }, []);

  const load = useCallback(
    async (targetRange: TimeRange, opts?: { manual?: boolean; switching?: boolean }) => {
      if (opts?.manual) setRefreshing(true);
      if (opts?.switching) setSwitchingRange(true);
      try {
        const [sumData, incData] = await Promise.all([
          fetchSummary(targetRange),
          fetchIncidents().catch(() => null),
        ]);
        setSummary(sumData);
        if (incData !== null) {
          setIncidents(incData);
        }
        setError(null);
        setCountdown(REFRESH_INTERVAL_SEC);
      } catch (err) {
        setError(err instanceof Error ? err.message : "获取状态数据失败");
      } finally {
        setLoading(false);
        setRefreshing(false);
        setSwitchingRange(false);
      }
    },
    []
  );

  useEffect(() => {
    if (!rangeReady) return;
    void load(range);
    const timer = setInterval(() => {
      setCountdown((prev) => {
        if (prev <= 1) {
          void load(range);
          return REFRESH_INTERVAL_SEC;
        }
        return prev - 1;
      });
    }, 1000);
    return () => clearInterval(timer);
  }, [load, range, rangeReady]);

  const handleRangeChange = (next: TimeRange) => {
    if (next === range) return;
    setRange(next);
    try {
      localStorage.setItem(RANGE_STORAGE_KEY, next);
    } catch {
      // ignore
    }
    void load(next, { switching: true });
  };

  const filteredIncidents = useMemo(() => {
    if (incidentFilter === "all") return incidents;
    return incidents.filter((inc) => inc.type === incidentFilter);
  }, [incidents, incidentFilter]);

  const overall = overallStatusMeta(summary?.overall_status ?? "operational");

  const OverallIcon =
    summary?.overall_status === "outage"
      ? XCircle
      : summary?.overall_status === "degraded"
        ? AlertTriangle
        : summary?.overall_status === "maintenance"
          ? Wrench
          : CheckCircle2;

  return (
    <>
      <SiteHeader />

      <main className="flex-1 max-w-4xl w-full mx-auto px-4 sm:px-6 py-6 sm:py-8 space-y-8">
        {/* Overall Status Hero Card */}
        <section className="sm-card p-6 sm:p-7 relative overflow-hidden">
          <div
            className="pointer-events-none absolute -right-12 -top-12 h-44 w-44 rounded-full blur-3xl opacity-25"
            style={{ backgroundColor: overall.color }}
          />
          <div className="relative flex flex-col sm:flex-row sm:items-center justify-between gap-4">
            <div className="flex items-start sm:items-center gap-4">
              <div
                className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl border"
                style={{
                  color: overall.color,
                  backgroundColor: overall.wash,
                  borderColor: "var(--card-edge)",
                }}
              >
                <OverallIcon className="w-6 h-6" />
              </div>
              <div>
                <div className="flex items-center gap-2.5">
                  <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-[var(--text)]">
                    {loading ? "正在获取服务状态..." : overall.title}
                  </h1>
                  {!loading && (
                    <span
                      className="h-2.5 w-2.5 rounded-full animate-pulse-soft"
                      style={{ backgroundColor: overall.color }}
                    />
                  )}
                </div>
                <p className="text-sm text-[var(--text-muted)] mt-1">
                  {loading ? "连接 StarMoe 独立监测节点" : overall.subtitle}
                </p>
              </div>
            </div>

            <div className="flex items-center gap-3 self-end sm:self-center text-xs text-[var(--text-muted)] tabular-nums">
              {summary?.updated_at && (
                <span className="hidden md:inline">
                  更新于 {formatRelativeTime(summary.updated_at)}
                </span>
              )}
              <button
                type="button"
                onClick={() => void load(range, { manual: true })}
                disabled={refreshing}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full border border-[var(--line-divider)] bg-[var(--surface-1)]/70 text-[var(--text)] hover:bg-[var(--overlay-hover)] transition-colors cursor-pointer disabled:opacity-60"
              >
                <RefreshCw className={`w-3.5 h-3.5 ${refreshing ? "animate-spin" : ""}`} />
                <span>{refreshing ? "刷新中" : `${countdown}s`}</span>
              </button>
            </div>
          </div>
        </section>

        {/* Error notice */}
        {error && (
          <div className="sm-card p-4 border-[var(--danger)]/40 bg-[var(--danger-wash)] text-sm text-[var(--danger)] flex items-center justify-between gap-3">
            <span>状态数据拉取异常：{error}</span>
            <button
              type="button"
              onClick={() => void load(range, { manual: true })}
              className="px-3 py-1 rounded-full bg-[var(--surface-1)] text-xs text-[var(--text)] cursor-pointer"
            >
              重试
            </button>
          </div>
        )}

        {/* Active Incidents & Scheduled Maintenances */}
        {summary &&
          (summary.active_incidents.length > 0 || summary.scheduled_maintenances.length > 0) && (
            <section className="space-y-3">
              {summary.active_incidents.map((inc) => (
                <IncidentBanner key={inc.id} incident={inc} />
              ))}
              {summary.scheduled_maintenances.map((inc) => (
                <IncidentBanner key={inc.id} incident={inc} />
              ))}
            </section>
          )}

        {/* Service Monitors Toolbar + Groups */}
        <section className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3 px-1">
            <div>
              <h2 className="text-xs font-medium tracking-wider uppercase text-[var(--text-muted)]">
                服务运行状态
              </h2>
            </div>

            {/* 24h / 7d / 30d / 90d Segmented Range Selector */}
            <div
              role="group"
              aria-label="统计周期切换"
              className="inline-flex items-center p-0.5 rounded-full border border-[var(--line-divider)] bg-[var(--card)] backdrop-blur-md"
            >
              {TIME_RANGES.map((item) => {
                const active = range === item.key;
                return (
                  <button
                    key={item.key}
                    type="button"
                    onClick={() => handleRangeChange(item.key)}
                    aria-pressed={active}
                    className={`px-3 py-1 rounded-full text-xs font-medium tabular-nums transition-colors cursor-pointer ${
                      active
                        ? "bg-[var(--brand-wash)] text-[var(--brand)] border border-[var(--card-edge)]"
                        : "text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--overlay-hover)] border border-transparent"
                    }`}
                  >
                    {item.label}
                  </button>
                );
              })}
            </div>
          </div>

          {loading && !summary && (
            <div className="space-y-4">
              {[1, 2, 3].map((n) => (
                <div key={n} className="sm-card p-6 animate-pulse space-y-4">
                  <div className="h-5 w-40 rounded bg-[var(--overlay-hover)]" />
                  <div className="h-12 w-full rounded bg-[var(--overlay-hover)]" />
                  <div className="h-12 w-full rounded bg-[var(--overlay-hover)]" />
                </div>
              ))}
            </div>
          )}

          <div className={`space-y-5 transition-opacity duration-150 ${switchingRange ? "opacity-65" : "opacity-100"}`}>
            {summary &&
              summary.groups.map((group) => {
                const gMeta = monitorStateMeta(group.status);
                return (
                  <div key={group.key} className="sm-card overflow-hidden">
                    <div className="px-5 py-3.5 border-b border-[var(--line-divider)] flex items-center justify-between gap-3">
                      <div className="flex items-center gap-2.5">
                        <h3 className="text-[15px] font-semibold text-[var(--text)]">
                          {group.name}
                        </h3>
                        <span className="text-xs text-[var(--text-muted)] tabular-nums">
                          {group.monitors.length} 项
                        </span>
                      </div>
                      <span
                        className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-semibold"
                        style={{ color: gMeta.color, backgroundColor: gMeta.bg }}
                      >
                        <span
                          className="h-1.5 w-1.5 rounded-full"
                          style={{ backgroundColor: gMeta.barColor }}
                        />
                        {gMeta.label}
                      </span>
                    </div>

                    <div>
                      {group.monitors.map((monitor) => (
                        <MonitorRow key={monitor.id} monitor={monitor} range={range} />
                      ))}
                    </div>
                  </div>
                );
              })}
          </div>
        </section>

        {/* Redacted 4-Node Infrastructure Summary */}
        {summary && summary.infra && summary.infra.length > 0 && (
          <section className="space-y-3">
            <div className="flex items-center justify-between px-1">
              <div className="flex items-center gap-2">
                <Server className="w-3.5 h-3.5 text-[var(--brand)]" />
                <h2 className="text-xs font-medium tracking-wider uppercase text-[var(--text-muted)]">
                  基础设施节点摘要
                </h2>
              </div>
              <span className="text-xs text-[var(--text-muted)]">
                全球 4 区域只读心跳监测
              </span>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
              {summary.infra.map((node) => {
                const nMeta = monitorStateMeta(node.state);
                return (
                  <div
                    key={node.id}
                    className="sm-card p-4 flex flex-col justify-between gap-3"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div>
                        <div className="text-[11px] font-mono-code uppercase tracking-wider text-[var(--text-muted)]">
                          {node.region}
                        </div>
                        <div className="text-sm font-semibold text-[var(--text)] mt-0.5">
                          {node.name}
                        </div>
                      </div>
                      <span
                        className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-semibold shrink-0"
                        style={{ color: nMeta.color, backgroundColor: nMeta.bg }}
                      >
                        <span
                          className="h-1.5 w-1.5 rounded-full"
                          style={{ backgroundColor: nMeta.barColor }}
                        />
                        {node.label || nMeta.label}
                      </span>
                    </div>
                    <div className="text-[11px] text-[var(--text-muted)] tabular-nums">
                      心跳同步：{formatRelativeTime(node.updated_at)}
                    </div>
                  </div>
                );
              })}
            </div>
          </section>
        )}

        {/* Merged Incidents & Maintenance Timeline */}
        <section id="incidents" className="space-y-3 scroll-mt-8">
          <div className="flex flex-wrap items-center justify-between gap-3 px-1">
            <div className="flex items-center gap-2">
              <History className="w-3.5 h-3.5 text-[var(--brand)]" />
              <h2 className="text-xs font-medium tracking-wider uppercase text-[var(--text-muted)]">
                故障与维护记录
              </h2>
            </div>

            {incidents.length > 0 && (
              <div
                role="group"
                aria-label="事件筛选"
                className="inline-flex items-center p-0.5 rounded-full border border-[var(--line-divider)] bg-[var(--card)] backdrop-blur-md"
              >
                {(
                  [
                    { key: "all", label: "全部" },
                    { key: "incident", label: "故障" },
                    { key: "maintenance", label: "维护" },
                  ] as { key: IncidentFilter; label: string }[]
                ).map((item) => {
                  const active = incidentFilter === item.key;
                  return (
                    <button
                      key={item.key}
                      type="button"
                      onClick={() => setIncidentFilter(item.key)}
                      aria-pressed={active}
                      className={`px-2.5 py-0.5 rounded-full text-xs font-medium transition-colors cursor-pointer ${
                        active
                          ? "bg-[var(--brand-wash)] text-[var(--brand)]"
                          : "text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--overlay-hover)]"
                      }`}
                    >
                      {item.label}
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          {filteredIncidents.length === 0 ? (
            <div className="sm-card px-5 py-6 text-center text-sm text-[var(--text-muted)]">
              近期暂无故障或计划维护记录，所有服务平稳运行中。
            </div>
          ) : (
            <div className="space-y-3">
              {filteredIncidents.map((inc) => (
                <IncidentTimelineCard key={inc.id} incident={inc} />
              ))}
            </div>
          )}
        </section>
      </main>

      <SiteFooter />
    </>
  );
}
