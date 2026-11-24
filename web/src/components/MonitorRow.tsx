"use client";

import { useState } from "react";
import { ChevronDown, ChevronUp, Clock, Globe, Lock, Zap } from "lucide-react";
import type { MonitorHistory, PublicMonitor, TimeRange } from "@/lib/types";
import { fetchMonitorHistory } from "@/lib/api";
import {
  formatRelativeTime,
  formatUptime,
  lineBadgeForMonitor,
  monitorStateMeta,
} from "@/lib/format";
import { UptimeBar } from "./UptimeBar";
import { LatencyChart } from "./LatencyChart";

interface MonitorRowProps {
  monitor: PublicMonitor;
  range: TimeRange;
}

export function MonitorRow({ monitor, range }: MonitorRowProps) {
  const [expanded, setExpanded] = useState(false);
  const [history, setHistory] = useState<MonitorHistory | null>(null);
  const [loadingHistory, setLoadingHistory] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);

  const meta = monitorStateMeta(monitor.state);
  const lineBadge = lineBadgeForMonitor(monitor.id);

  const toggleExpand = async () => {
    const next = !expanded;
    setExpanded(next);
    if (next && !history && !loadingHistory) {
      setLoadingHistory(true);
      setHistoryError(null);
      try {
        const data = await fetchMonitorHistory(monitor.id);
        setHistory(data);
      } catch (err) {
        setHistoryError(err instanceof Error ? err.message : "未知错误");
      } finally {
        setLoadingHistory(false);
      }
    }
  };

  return (
    <div className="border-t border-[var(--line-divider)] first:border-t-0">
      <div
        role="button"
        tabIndex={0}
        onClick={toggleExpand}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            toggleExpand();
          }
        }}
        className="w-full px-5 py-4 text-left hover:bg-[var(--overlay-hover)] transition-colors cursor-pointer"
      >
        {/* Top info line */}
        <div className="flex flex-wrap items-center justify-between gap-2 mb-3">
          <div className="flex items-center gap-2.5 flex-wrap min-w-0">
            <span
              className="h-2.5 w-2.5 rounded-full shrink-0"
              style={{ backgroundColor: meta.barColor }}
            />
            <span className="font-medium text-sm sm:text-[15px] text-[var(--text)]">
              {monitor.name}
            </span>

            {lineBadge && (
              <span
                className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium border ${
                  lineBadge.variant === "cn"
                    ? "bg-[var(--brand-wash)] text-[var(--brand)] border-[var(--card-edge)]"
                    : "bg-[var(--info-wash)] text-[var(--info)] border-transparent"
                }`}
              >
                {lineBadge.variant === "cn" ? (
                  <Zap className="w-3 h-3" />
                ) : (
                  <Globe className="w-3 h-3" />
                )}
                {lineBadge.label}
              </span>
            )}

            {monitor.tls_days_left != null && monitor.tls_days_left <= 30 && (
              <span
                className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium tabular-nums"
                style={{
                  color: monitor.tls_days_left <= 14 ? "var(--warning)" : "var(--text-muted)",
                  backgroundColor:
                    monitor.tls_days_left <= 14 ? "var(--warning-wash)" : "var(--unknown-wash)",
                }}
              >
                <Lock className="w-3 h-3" />
                TLS {monitor.tls_days_left} 天
              </span>
            )}
          </div>

          <div className="flex items-center gap-3 text-xs tabular-nums shrink-0">
            {monitor.latency_ms != null && monitor.latency_ms > 0 && (
              <span className="text-[var(--text-muted)] hidden sm:inline">
                {monitor.latency_ms} ms
              </span>
            )}
            <span className="text-[var(--text-muted)] font-medium">
              {formatUptime(monitor.uptime_90d)}
            </span>
            <span
              className="inline-flex items-center px-2.5 py-0.5 rounded-full font-semibold text-[11px]"
              style={{ color: meta.color, backgroundColor: meta.bg }}
            >
              {meta.label}
            </span>
            <span className="text-[var(--text-muted)]">
              {expanded ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
            </span>
          </div>
        </div>

        {/* Sub-items (e.g. Sekai 5-server JP/CN/TW/KR/EN) */}
        {monitor.sub_items && monitor.sub_items.length > 0 && (
          <div className="mb-3 flex flex-wrap gap-1.5">
            {monitor.sub_items.map((sub) => {
              const sm = monitorStateMeta(sub.state);
              return (
                <span
                  key={sub.key}
                  className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs border border-[var(--line-divider)] bg-[var(--surface-1)]/70"
                >
                  <span
                    className="h-2 w-2 rounded-full"
                    style={{ backgroundColor: sm.barColor }}
                  />
                  <span className="font-medium text-[var(--text)]">{sub.label}</span>
                  <span className="text-[11px]" style={{ color: sm.color }}>
                    {sm.label}
                  </span>
                </span>
              );
            })}
          </div>
        )}

        {/* Uptime bar */}
        <UptimeBar bars={monitor.daily_bars} uptimePct={monitor.uptime_90d} range={range} />
      </div>

      {/* Expandable 24h latency chart */}
      {expanded && (
        <div className="px-5 pb-5 pt-2 bg-[var(--surface-1)]/45 border-t border-[var(--line-divider)]">
          <div className="flex flex-wrap items-center justify-between gap-2 mb-3 text-xs text-[var(--text-muted)]">
            <div className="flex items-center gap-2">
              <Clock className="w-3.5 h-3.5" />
              <span>最近检测：{formatRelativeTime(monitor.last_checked_at)}</span>
              {monitor.latency_ms != null && monitor.latency_ms > 0 && (
                <span>· 当前延迟 {monitor.latency_ms} ms</span>
              )}
              {monitor.tls_days_left != null && monitor.tls_days_left > 0 && (
                <span>· TLS 证书剩余 {monitor.tls_days_left} 天</span>
              )}
            </div>
            {monitor.message && (
              <span className="text-[var(--warning)]">{monitor.message}</span>
            )}
          </div>

          <LatencyChart
            points={history?.latency_24h ?? []}
            loading={loadingHistory}
            error={historyError}
          />
        </div>
      )}
    </div>
  );
}
