export type MonitorState = "up" | "degraded" | "down" | "maintenance" | "unknown";
export type OverallStatus = "operational" | "degraded" | "outage" | "maintenance";
export type DayBarState = "up" | "degraded" | "down" | "no_data";
export type TimeRange = "24h" | "7d" | "30d" | "90d";
export type Severity = "P0" | "P1" | "P2";
export type IncidentType = "incident" | "maintenance";
export type IncidentStatus =
  | "investigating"
  | "identified"
  | "monitoring"
  | "resolved"
  | "scheduled"
  | "in_progress";
export type IncidentImpact = "minor" | "major" | "critical" | "maintenance";

export interface PublicSubItem {
  key: string;
  label: string;
  state: MonitorState;
}

export interface DayUptime {
  date: string;
  timestamp: number;
  uptime_pct: number;
  state: DayBarState;
  avg_latency_ms: number;
  down_count: number;
  degraded_count: number;
}

export interface PublicMonitor {
  id: string;
  name: string;
  group_key: string;
  state: MonitorState;
  severity: Severity;
  latency_ms?: number | null;
  uptime_90d: number;
  tls_days_left?: number | null;
  message?: string;
  sub_items?: PublicSubItem[];
  daily_bars: DayUptime[];
  last_checked_at: number;
}

export interface PublicGroup {
  key: string;
  name: string;
  status: OverallStatus | MonitorState;
  monitors: PublicMonitor[];
}

export interface PublicInfraNode {
  id: string;
  region: string;
  name: string;
  state: MonitorState;
  label: string;
  updated_at: number;
}

export interface IncidentUpdate {
  id: number;
  incident_id: string;
  status: IncidentStatus;
  message: string;
  created_by?: string;
  created_at: number;
}

export interface Incident {
  id: string;
  type: IncidentType;
  title: string;
  status: IncidentStatus;
  impact: IncidentImpact;
  affected_monitors: string[];
  starts_at?: number | null;
  ends_at?: number | null;
  created_by?: string;
  created_at: number;
  updated_at: number;
  updates: IncidentUpdate[];
}

export interface PublicSummary {
  overall_status: OverallStatus;
  range?: TimeRange;
  updated_at: number;
  groups: PublicGroup[];
  infra: PublicInfraNode[];
  active_incidents: Incident[];
  scheduled_maintenances: Incident[];
}

export interface LatencyPoint {
  timestamp: number;
  latency_ms: number;
  state: MonitorState;
}

export interface MonitorHistory {
  monitor_id: string;
  name: string;
  uptime_90d: number;
  daily: DayUptime[];
  latency_24h: LatencyPoint[];
}

export interface MountCheck {
  path: string;
  device: string;
  expected_device?: string;
  ok: boolean;
}

export interface HostMetrics {
  cpu_load1: number;
  cpu_cores?: number;
  mem_used_pct: number;
  mem_available_pct?: number;
  swap_used_pct: number;
  root_disk_pct: number;
  extra_disk_pct?: number;
  mounts?: MountCheck[];
}

export interface NodeCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
}

export interface UnhealthyPod {
  namespace: string;
  name: string;
  phase: string;
  ready: string;
  restarts: number;
  reason?: string;
}

export interface ExpiringCert {
  namespace: string;
  name: string;
  ready: boolean;
  days_left: number;
  not_after?: number;
}

export interface ArgoCDAppStatus {
  name: string;
  sync_status: string;
  health_status: string;
  message?: string;
}

export interface SubItemStatus {
  key: string;
  label: string;
  state: MonitorState;
  message?: string;
  active_sessions?: number | null;
  last_error?: string;
}

export interface DeepCheckResult {
  monitor_id: string;
  name?: string;
  state: MonitorState;
  latency_ms?: number;
  message?: string;
  sub_items?: SubItemStatus[];
  details?: Record<string, unknown>;
}

export interface AlertEvent {
  id: number;
  monitor_id: string;
  monitor_name?: string;
  cluster?: string;
  severity: Severity;
  prev_state: MonitorState;
  next_state: MonitorState;
  suppressed_by?: string;
  summary: string;
  created_at: number;
}

export interface AdminMonitor {
  id: string;
  name: string;
  group_key: string;
  kind: string;
  cluster?: string;
  visibility: "public" | "internal";
  severity: Severity;
  state: MonitorState;
  consecutive_fails: number;
  latency_ms?: number | null;
  tls_days_left?: number | null;
  message?: string;
  last_checked_at: number;
}

export interface AdminNodeOverview {
  cluster: string;
  state: MonitorState;
  last_heartbeat_at: number;
  host_metrics?: HostMetrics | null;
  node_conditions?: NodeCondition[];
  unhealthy_pods?: UnhealthyPod[];
  expiring_certs?: ExpiringCert[];
  argocd_unhealthy?: ArgoCDAppStatus[];
  deep_checks?: DeepCheckResult[];
}

export interface AdminOverview {
  overall_status: OverallStatus;
  updated_at: number;
  bot_online: boolean;
  bot_clients: number;
  bot_last_seen_at?: number;
  monitors: AdminMonitor[];
  nodes: AdminNodeOverview[];
  incidents: Incident[];
  recent_events: AlertEvent[];
}

export interface CreateIncidentRequest {
  type: IncidentType;
  title: string;
  status: IncidentStatus;
  impact: IncidentImpact;
  affected_monitors: string[];
  message: string;
  starts_at?: number;
  ends_at?: number;
}

export interface UpdateIncidentRequest {
  title?: string;
  status?: IncidentStatus;
  impact?: IncidentImpact;
  affected_monitors?: string[];
  message?: string;
  starts_at?: number;
  ends_at?: number;
}

export interface TestAlertRequest {
  channel: "bot" | "email" | "all";
  severity: Severity;
  message?: string;
}

export interface TestAlertResponse {
  ok: boolean;
  results: Record<string, string>;
}
