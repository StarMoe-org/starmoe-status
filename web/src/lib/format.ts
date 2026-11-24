import type {
  DayBarState,
  IncidentImpact,
  IncidentStatus,
  MonitorState,
  OverallStatus,
} from "./types";

export function overallStatusMeta(status: OverallStatus) {
  switch (status) {
    case "operational":
      return {
        title: "全站服务正常运行",
        subtitle: "所有公开监测节点与业务入口均处于健康状态",
        badge: "全站正常",
        color: "var(--success)",
        wash: "var(--success-wash)",
      };
    case "degraded":
      return {
        title: "部分服务性能降级",
        subtitle: "部分线路或子服务存在波动，我们正在持续关注",
        badge: "部分降级",
        color: "var(--warning)",
        wash: "var(--warning-wash)",
      };
    case "outage":
      return {
        title: "部分核心服务中断",
        subtitle: "检测到服务不可用，运维团队正在紧急排查恢复",
        badge: "服务中断",
        color: "var(--danger)",
        wash: "var(--danger-wash)",
      };
    case "maintenance":
      return {
        title: "计划维护进行中",
        subtitle: "正在执行计划内维护窗口，部分服务可能短暂不可用",
        badge: "维护中",
        color: "var(--info)",
        wash: "var(--info-wash)",
      };
  }
}

export function monitorStateMeta(state: MonitorState | OverallStatus | DayBarState) {
  switch (state) {
    case "up":
    case "operational":
      return {
        label: "正常",
        color: "var(--success)",
        bg: "var(--success-wash)",
        barColor: "var(--success-strong)",
      };
    case "degraded":
      return {
        label: "降级",
        color: "var(--warning)",
        bg: "var(--warning-wash)",
        barColor: "var(--warning-strong)",
      };
    case "down":
    case "outage":
      return {
        label: "中断",
        color: "var(--danger)",
        bg: "var(--danger-wash)",
        barColor: "var(--danger-strong)",
      };
    case "maintenance":
      return {
        label: "维护中",
        color: "var(--info)",
        bg: "var(--info-wash)",
        barColor: "var(--info)",
      };
    case "no_data":
      return {
        label: "无数据",
        color: "var(--text-muted)",
        bg: "var(--unknown-wash)",
        barColor: "var(--line-border)",
      };
    default:
      return {
        label: "检测中",
        color: "var(--text-muted)",
        bg: "var(--unknown-wash)",
        barColor: "var(--unknown)",
      };
  }
}

export function incidentStatusLabel(status: IncidentStatus): string {
  switch (status) {
    case "investigating":
      return "排查中";
    case "identified":
      return "已定位";
    case "monitoring":
      return "恢复观察";
    case "resolved":
      return "已解决";
    case "scheduled":
      return "计划维护";
    case "in_progress":
      return "维护进行中";
  }
}

export function incidentImpactMeta(impact: IncidentImpact) {
  switch (impact) {
    case "critical":
      return { label: "严重故障", color: "var(--danger)", bg: "var(--danger-wash)" };
    case "major":
      return { label: "主要故障", color: "var(--danger)", bg: "var(--danger-wash)" };
    case "minor":
      return { label: "轻微波动", color: "var(--warning)", bg: "var(--warning-wash)" };
    case "maintenance":
      return { label: "计划维护", color: "var(--info)", bg: "var(--info-wash)" };
  }
}

export function lineBadgeForMonitor(id: string): { label: string; variant: "cn" | "global" } | null {
  if (id.endsWith("-cn")) {
    return { label: "国内加速 · EdgeOne", variant: "cn" };
  }
  if (id.endsWith("-global")) {
    return { label: "海外线路 · CF", variant: "global" };
  }
  return null;
}

export function formatRelativeTime(unixSec?: number | null): string {
  if (!unixSec || unixSec <= 0) return "尚未上报";
  const diffSec = Math.max(0, Math.floor(Date.now() / 1000) - unixSec);
  if (diffSec < 15) return "刚刚";
  if (diffSec < 60) return `${diffSec} 秒前`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin} 分钟前`;
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return `${diffHour} 小时前`;
  const diffDay = Math.floor(diffHour / 24);
  return `${diffDay} 天前`;
}

export function formatDateTime(unixSec?: number | null): string {
  if (!unixSec || unixSec <= 0) return "-";
  const d = new Date(unixSec * 1000);
  return d.toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

export function formatShortTime(unixSec: number): string {
  const d = new Date(unixSec * 1000);
  return d.toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

export function formatUptime(pct: number): string {
  if (!Number.isFinite(pct)) return "—";
  if (pct >= 99.995) return "100%";
  return `${pct.toFixed(2)}%`;
}
