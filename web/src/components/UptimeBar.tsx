"use client";

import { useState } from "react";
import type { DayUptime, TimeRange } from "@/lib/types";
import { formatUptime, monitorStateMeta } from "@/lib/format";

interface UptimeBarProps {
  bars: DayUptime[];
  uptimePct: number;
  range: TimeRange;
}

const RANGE_META: Record<
  TimeRange,
  { startLabel: string; endLabel: string; titleLabel: string; fallbackCount: number }
> = {
  "24h": { startLabel: "24 小时前", endLabel: "现在", titleLabel: "24 小时可用率", fallbackCount: 24 },
  "7d": { startLabel: "7 天前", endLabel: "现在", titleLabel: "7 天可用率", fallbackCount: 28 },
  "30d": { startLabel: "30 天前", endLabel: "今日", titleLabel: "30 天可用率", fallbackCount: 30 },
  "90d": { startLabel: "90 天前", endLabel: "今日", titleLabel: "90 天可用率", fallbackCount: 90 },
};

export function UptimeBar({ bars, uptimePct, range }: UptimeBarProps) {
  const [hoveredIdx, setHoveredIdx] = useState<number | null>(null);
  const metaCfg = RANGE_META[range] ?? RANGE_META["90d"];

  const displayBars: DayUptime[] =
    bars && bars.length > 0
      ? bars
      : Array.from({ length: metaCfg.fallbackCount }, () => ({
          date: "",
          timestamp: 0,
          uptime_pct: 0,
          state: "no_data" as const,
          avg_latency_ms: 0,
          down_count: 0,
          degraded_count: 0,
        }));

  const hovered = hoveredIdx !== null ? displayBars[hoveredIdx] : null;
  const hoverPct = hoveredIdx !== null ? ((hoveredIdx + 0.5) / displayBars.length) * 100 : 50;

  return (
    <div className="relative select-none">
      {/* Tooltip */}
      {hovered && hovered.date && (
        <div
          className="pointer-events-none absolute -top-2 z-20 -translate-y-full -translate-x-1/2 whitespace-nowrap rounded-xl border border-[var(--card-edge)] bg-[var(--card-solid)] px-3 py-2 text-xs shadow-[var(--shadow-float)]"
          style={{
            left: `${Math.min(88, Math.max(12, hoverPct))}%`,
          }}
        >
          <div className="flex items-center justify-between gap-3 mb-1">
            <span className="font-mono-code text-[var(--text)] font-medium">{hovered.date}</span>
            <span
              className="font-semibold"
              style={{ color: monitorStateMeta(hovered.state).color }}
            >
              {monitorStateMeta(hovered.state).label}
            </span>
          </div>
          {hovered.state === "no_data" ? (
            <div className="text-[var(--text-muted)]">该时段暂无探测记录</div>
          ) : (
            <div className="space-y-0.5 text-[var(--text-muted)] tabular-nums">
              <div>
                可用率：<span className="text-[var(--text)] font-medium">{formatUptime(hovered.uptime_pct)}</span>
                {hovered.avg_latency_ms > 0 && (
                  <>
                    {" · "}平均 <span className="text-[var(--text)]">{hovered.avg_latency_ms} ms</span>
                  </>
                )}
              </div>
              {(hovered.down_count > 0 || hovered.degraded_count > 0) && (
                <div>
                  {hovered.down_count > 0 && (
                    <span className="text-[var(--danger)] mr-2">中断 {hovered.down_count} 次</span>
                  )}
                  {hovered.degraded_count > 0 && (
                    <span className="text-[var(--warning)]">降级 {hovered.degraded_count} 次</span>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Uptime bars */}
      <div
        className={`flex items-stretch h-7 w-full ${
          displayBars.length <= 30 ? "gap-[3px] sm:gap-1" : "gap-[1.5px] sm:gap-[2px]"
        }`}
        onMouseLeave={() => setHoveredIdx(null)}
      >
        {displayBars.map((bar, idx) => {
          const meta = monitorStateMeta(bar.state);
          const isHovered = hoveredIdx === idx;
          return (
            <button
              key={bar.date || idx}
              type="button"
              tabIndex={-1}
              aria-label={bar.date ? `${bar.date} ${meta.label}` : "无数据"}
              onMouseEnter={() => setHoveredIdx(idx)}
              onFocus={() => setHoveredIdx(idx)}
              onClick={(e) => {
                e.stopPropagation();
                setHoveredIdx(idx === hoveredIdx ? null : idx);
              }}
              className={`flex-1 transition-transform duration-150 cursor-pointer border-0 p-0 ${
                displayBars.length <= 30 ? "rounded-[4px]" : "rounded-[2px]"
              }`}
              style={{
                backgroundColor: meta.barColor,
                opacity: bar.state === "no_data" ? 0.30 : isHovered ? 1 : 0.88,
                transform: isHovered ? "scaleY(1.16)" : "scaleY(1)",
              }}
            />
          );
        })}
      </div>

      {/* Legend */}
      <div className="mt-1.5 flex items-center justify-between text-[11px] text-[var(--text-muted)] tabular-nums">
        <span>{metaCfg.startLabel}</span>
        <span className="hidden sm:inline">
          {metaCfg.titleLabel} {formatUptime(uptimePct)}
        </span>
        <span>{metaCfg.endLabel}</span>
      </div>
    </div>
  );
}
