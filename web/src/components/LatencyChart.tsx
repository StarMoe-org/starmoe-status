"use client";

import { useMemo, useState } from "react";
import type { LatencyPoint } from "@/lib/types";
import { formatShortTime, monitorStateMeta } from "@/lib/format";

interface LatencyChartProps {
  points: LatencyPoint[];
  loading?: boolean;
  error?: string | null;
}

export function LatencyChart({ points, loading, error }: LatencyChartProps) {
  const [activeIdx, setActiveIdx] = useState<number | null>(null);

  const stats = useMemo(() => {
    if (!points || points.length === 0) return null;
    const vals = points.map((p) => p.latency_ms).filter((v) => v >= 0);
    if (vals.length === 0) return null;
    const sorted = [...vals].sort((a, b) => a - b);
    const min = sorted[0];
    const max = sorted[sorted.length - 1];
    const avg = Math.round(vals.reduce((a, b) => a + b, 0) / vals.length);
    const p95 = sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95))];
    return { min, max, avg, p95 };
  }, [points]);

  if (loading) {
    return (
      <div className="h-40 flex items-center justify-center text-xs text-[var(--text-muted)]">
        正在加载近 24 小时延迟数据…
      </div>
    );
  }

  if (error) {
    return (
      <div className="h-32 flex items-center justify-center text-xs text-[var(--danger)]">
        加载历史数据失败：{error}
      </div>
    );
  }

  if (!points || points.length === 0 || !stats) {
    return (
      <div className="h-32 flex items-center justify-center text-xs text-[var(--text-muted)]">
        暂无近 24 小时延迟采样数据
      </div>
    );
  }

  const width = 640;
  const height = 150;
  const padLeft = 42;
  const padRight = 14;
  const padTop = 14;
  const padBottom = 26;
  const innerW = width - padLeft - padRight;
  const innerH = height - padTop - padBottom;

  const yMax = Math.max(stats.max * 1.15, 50);
  const xStep = points.length > 1 ? innerW / (points.length - 1) : 0;

  const coords = points.map((pt, i) => {
    const x = padLeft + (points.length > 1 ? i * xStep : innerW / 2);
    const y = padTop + innerH - (Math.max(0, pt.latency_ms) / yMax) * innerH;
    return { x, y, pt };
  });

  const linePath = coords
    .map((c, i) => `${i === 0 ? "M" : "L"} ${c.x.toFixed(1)} ${c.y.toFixed(1)}`)
    .join(" ");

  const areaPath =
    coords.length > 1
      ? `${linePath} L ${coords[coords.length - 1].x.toFixed(1)} ${(padTop + innerH).toFixed(1)} L ${coords[0].x.toFixed(1)} ${(padTop + innerH).toFixed(1)} Z`
      : "";

  const active = activeIdx !== null ? coords[activeIdx] : null;

  const yTicks = [0, Math.round(yMax / 2), Math.round(yMax)];
  const xLabelIndices =
    points.length >= 4
      ? [0, Math.floor(points.length / 3), Math.floor((points.length * 2) / 3), points.length - 1]
      : points.length === 1
      ? [0]
      : [0, points.length - 1];

  return (
    <div className="space-y-3">
      {/* Stats pills */}
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
        <span className="font-medium text-[var(--text)]">近 24 小时响应延迟</span>
        <div className="flex flex-wrap items-center gap-3 text-[var(--text-muted)] tabular-nums">
          <span>
            平均 <strong className="text-[var(--text)]">{stats.avg} ms</strong>
          </span>
          <span>
            P95 <strong className="text-[var(--text)]">{stats.p95} ms</strong>
          </span>
          <span>
            最小 <strong className="text-[var(--text)]">{stats.min} ms</strong>
          </span>
          <span>
            最大 <strong className="text-[var(--text)]">{stats.max} ms</strong>
          </span>
        </div>
      </div>

      {/* SVG Chart */}
      <div className="relative w-full">
        {active && (
          <div
            className="pointer-events-none absolute top-1 z-10 rounded-lg border border-[var(--card-edge)] bg-[var(--card-solid)] px-2.5 py-1.5 text-[11px] shadow-[var(--shadow-float)] tabular-nums"
            style={{
              left: `${Math.min(78, Math.max(8, (active.x / width) * 100))}%`,
              transform: "translateX(-50%)",
            }}
          >
            <span className="text-[var(--text-muted)] mr-2">{formatShortTime(active.pt.timestamp)}</span>
            <strong className="text-[var(--text)] mr-2">{active.pt.latency_ms} ms</strong>
            <span style={{ color: monitorStateMeta(active.pt.state).color }}>
              {monitorStateMeta(active.pt.state).label}
            </span>
          </div>
        )}

        <svg
          viewBox={`0 0 ${width} ${height}`}
          className="w-full h-auto overflow-visible"
          onMouseLeave={() => setActiveIdx(null)}
          onMouseMove={(e) => {
            const rect = e.currentTarget.getBoundingClientRect();
            const relX = ((e.clientX - rect.left) / rect.width) * width;
            let best = 0;
            let bestDist = Infinity;
            for (let i = 0; i < coords.length; i++) {
              const d = Math.abs(coords[i].x - relX);
              if (d < bestDist) {
                bestDist = d;
                best = i;
              }
            }
            setActiveIdx(best);
          }}
        >
          <defs>
            <linearGradient id="starmoe-latency-grad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--success-strong)" stopOpacity="0.30" />
              <stop offset="100%" stopColor="var(--success-strong)" stopOpacity="0.02" />
            </linearGradient>
          </defs>

          {/* Horizontal grid lines */}
          {yTicks.map((tick) => {
            const y = padTop + innerH - (tick / yMax) * innerH;
            return (
              <g key={tick}>
                <line
                  x1={padLeft}
                  y1={y}
                  x2={width - padRight}
                  y2={y}
                  stroke="var(--line-divider)"
                  strokeDasharray="3 3"
                />
                <text
                  x={padLeft - 6}
                  y={y + 3.5}
                  textAnchor="end"
                  fontSize="10"
                  fill="var(--text-muted)"
                >
                  {tick}ms
                </text>
              </g>
            );
          })}

          {/* Area & Line */}
          {areaPath && <path d={areaPath} fill="url(#starmoe-latency-grad)" />}
          <path
            d={linePath}
            fill="none"
            stroke="var(--success-strong)"
            strokeWidth="1.8"
            strokeLinejoin="round"
            strokeLinecap="round"
          />

          {/* Highlight down/degraded points */}
          {coords.map((c, idx) =>
            c.pt.state !== "up" ? (
              <circle
                key={idx}
                cx={c.x}
                cy={c.y}
                r="3"
                fill={monitorStateMeta(c.pt.state).barColor}
              />
            ) : null
          )}

          {/* Active hover marker */}
          {active && (
            <g>
              <line
                x1={active.x}
                y1={padTop}
                x2={active.x}
                y2={padTop + innerH}
                stroke="var(--brand)"
                strokeOpacity="0.45"
                strokeDasharray="2 2"
              />
              <circle
                cx={active.x}
                cy={active.y}
                r="4"
                fill="var(--brand)"
                stroke="var(--card-solid)"
                strokeWidth="1.5"
              />
            </g>
          )}

          {/* X-axis labels */}
          {xLabelIndices.map((idx) => {
            const c = coords[idx];
            if (!c) return null;
            return (
              <text
                key={idx}
                x={c.x}
                y={height - 6}
                textAnchor={idx === 0 ? "start" : idx === points.length - 1 ? "end" : "middle"}
                fontSize="10"
                fill="var(--text-muted)"
              >
                {formatShortTime(c.pt.timestamp)}
              </text>
            );
          })}
        </svg>
      </div>
    </div>
  );
}
