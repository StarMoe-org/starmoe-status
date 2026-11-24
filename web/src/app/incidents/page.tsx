"use client";

import { useCallback, useEffect, useState } from "react";
import { AlertTriangle, CalendarClock, CheckCircle2, RefreshCw, Rss } from "lucide-react";
import { SiteHeader } from "@/components/SiteHeader";
import { SiteFooter } from "@/components/SiteFooter";
import { fetchIncidents } from "@/lib/api";
import type { Incident } from "@/lib/types";
import { formatDateTime, incidentImpactMeta, incidentStatusLabel } from "@/lib/format";

type FilterType = "all" | "incident" | "maintenance";

export default function IncidentsPage() {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<FilterType>("all");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const list = await fetchIncidents();
      setIncidents(list);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "加载故障公告失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const filtered = incidents.filter((item) =>
    filter === "all" ? true : item.type === filter
  );

  return (
    <>
      <SiteHeader />

      <main className="flex-1 max-w-4xl w-full mx-auto px-4 sm:px-6 py-8 space-y-6">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
          <div>
            <h1 className="text-2xl font-bold tracking-tight text-[var(--text)]">
              故障与计划维护时间线
            </h1>
            <p className="text-sm text-[var(--text-muted)] mt-1">
              透明公开 StarMoe 各项服务的历史异常排查与维护公告
            </p>
          </div>

          <div className="flex items-center gap-2">
            <a
              href="/rss.xml"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-full text-xs font-medium border border-[var(--line-border)] bg-[var(--surface-1)] text-[var(--text)] hover:bg-[var(--overlay-hover)] transition-colors"
            >
              <Rss className="w-3.5 h-3.5 text-[var(--brand)]" />
              RSS 订阅
            </a>
            <button
              type="button"
              onClick={load}
              className="inline-flex items-center justify-center h-9 w-9 rounded-full border border-[var(--line-border)] bg-[var(--surface-1)] text-[var(--text)] hover:bg-[var(--overlay-hover)] transition-colors cursor-pointer"
              title="刷新"
            >
              <RefreshCw className={`w-4 h-4 ${loading ? "animate-spin" : ""}`} />
            </button>
          </div>
        </div>

        {/* Filter tabs */}
        <div className="flex items-center gap-2">
          {(
            [
              { key: "all", label: "全部记录" },
              { key: "incident", label: "故障公告" },
              { key: "maintenance", label: "计划维护" },
            ] as const
          ).map((tab) => (
            <button
              key={tab.key}
              type="button"
              onClick={() => setFilter(tab.key)}
              className={`px-3.5 py-1.5 rounded-full text-xs font-medium transition-colors cursor-pointer ${
                filter === tab.key
                  ? "bg-[var(--brand)] text-[var(--on-brand)]"
                  : "border border-[var(--line-border)] bg-[var(--surface-1)] text-[var(--text-muted)] hover:text-[var(--text)]"
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>

        {/* Content */}
        {loading && incidents.length === 0 ? (
          <div className="sm-card p-8 text-center text-sm text-[var(--text-muted)]">
            正在加载时间线…
          </div>
        ) : error ? (
          <div className="sm-card p-6 text-sm text-[var(--danger)] flex items-center gap-2">
            <AlertTriangle className="w-4 h-4 shrink-0" />
            {error}
          </div>
        ) : filtered.length === 0 ? (
          <div className="sm-card p-10 text-center space-y-2">
            <CheckCircle2 className="w-8 h-8 mx-auto text-[var(--success)]" />
            <div className="text-base font-semibold text-[var(--text)]">暂无相关记录</div>
            <p className="text-xs text-[var(--text-muted)]">
              所有服务运行平稳，未记录到符合条件的故障或计划维护事件。
            </p>
          </div>
        ) : (
          <div className="space-y-4">
            {filtered.map((inc) => {
              const impact = incidentImpactMeta(inc.impact);
              const isResolved = inc.status === "resolved";
              return (
                <article
                  key={inc.id}
                  className="sm-card p-5 sm:p-6 space-y-4 border-l-4"
                  style={{ borderLeftColor: isResolved ? "var(--success)" : impact.color }}
                >
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="space-y-1.5">
                      <div className="flex flex-wrap items-center gap-2">
                        <span
                          className="inline-flex items-center px-2.5 py-0.5 rounded-full text-[11px] font-semibold"
                          style={{ color: impact.color, backgroundColor: impact.bg }}
                        >
                          {inc.type === "maintenance" ? "计划维护" : impact.label}
                        </span>
                        <span
                          className="inline-flex items-center px-2.5 py-0.5 rounded-full text-[11px] font-semibold"
                          style={{
                            color: isResolved ? "var(--success)" : "var(--text)",
                            backgroundColor: isResolved
                              ? "var(--success-wash)"
                              : "var(--overlay-hover)",
                          }}
                        >
                          {incidentStatusLabel(inc.status)}
                        </span>
                      </div>
                      <h2 className="text-lg font-semibold text-[var(--text)]">{inc.title}</h2>
                    </div>

                    <div className="text-xs text-[var(--text-muted)] tabular-nums">
                      发布于 {formatDateTime(inc.created_at)}
                    </div>
                  </div>

                  {inc.starts_at && (
                    <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[var(--info-wash)] text-[var(--info)] text-xs font-medium tabular-nums">
                      <CalendarClock className="w-3.5 h-3.5" />
                      <span>
                        维护时间：{formatDateTime(inc.starts_at)}
                        {inc.ends_at ? ` — ${formatDateTime(inc.ends_at)}` : ""}
                      </span>
                    </div>
                  )}

                  {inc.affected_monitors && inc.affected_monitors.length > 0 && (
                    <div className="flex flex-wrap items-center gap-1.5 text-xs">
                      <span className="text-[var(--text-muted)]">受影响组件：</span>
                      {inc.affected_monitors.map((mid) => (
                        <span
                          key={mid}
                          className="px-2 py-0.5 rounded-md font-mono-code text-[11px] bg-[var(--surface-2)] text-[var(--text)] border border-[var(--line-divider)]"
                        >
                          {mid}
                        </span>
                      ))}
                    </div>
                  )}

                  {/* Timeline Updates */}
                  {inc.updates && inc.updates.length > 0 && (
                    <ol className="relative border-l border-[var(--line-border)] ml-2 space-y-4 pt-1">
                      {[...inc.updates].reverse().map((u) => (
                        <li key={u.id} className="pl-5 relative">
                          <span className="absolute -left-[5px] top-1.5 h-2.5 w-2.5 rounded-full bg-[var(--brand)] ring-4 ring-[var(--card-solid)]" />
                          <div className="flex flex-wrap items-center gap-2 text-xs">
                            <span className="font-semibold text-[var(--text)]">
                              {incidentStatusLabel(u.status)}
                            </span>
                            <span className="text-[var(--text-muted)] tabular-nums">
                              {formatDateTime(u.created_at)}
                            </span>
                          </div>
                          <p className="mt-1 text-sm text-[var(--text-muted)] whitespace-pre-wrap leading-relaxed">
                            {u.message}
                          </p>
                        </li>
                      ))}
                    </ol>
                  )}
                </article>
              );
            })}
          </div>
        )}
      </main>

      <SiteFooter />
    </>
  );
}
