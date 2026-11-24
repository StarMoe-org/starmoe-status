package protocol

import "encoding/json"

// Monitor states.
const (
	StateUp          = "up"
	StateDegraded    = "degraded"
	StateDown        = "down"
	StateMaintenance = "maintenance"
	StateUnknown     = "unknown"
)

// Overall system statuses.
const (
	OverallOperational = "operational"
	OverallDegraded    = "degraded"
	OverallOutage      = "outage"
	OverallMaintenance = "maintenance"
)

// Monitor visibility values.
const (
	VisibilityPublic   = "public"
	VisibilityInternal = "internal"
)

// Alert severity levels.
const (
	SeverityP0 = "P0"
	SeverityP1 = "P1"
	SeverityP2 = "P2"
)

// Monitor kinds.
const (
	KindHTTP      = "http"
	KindTLS       = "tls"
	KindTCP       = "tcp"
	KindAgentNode = "agent_node"
	KindAgentDeep = "agent_deep"
)

// Incident types.
const (
	IncidentTypeIncident    = "incident"
	IncidentTypeMaintenance = "maintenance"
)

// Incident statuses.
const (
	IncidentStatusInvestigating = "investigating"
	IncidentStatusIdentified    = "identified"
	IncidentStatusMonitoring    = "monitoring"
	IncidentStatusResolved      = "resolved"
	IncidentStatusScheduled     = "scheduled"
	IncidentStatusInProgress    = "in_progress"
)

// Incident impacts.
const (
	ImpactMinor       = "minor"
	ImpactMajor       = "major"
	ImpactCritical    = "critical"
	ImpactMaintenance = "maintenance"
)

// HeartbeatPayload is pushed every 60s by starmoe-status-agent from each k3s cluster.
// It must never contain server IPs, secrets, or sensitive environment variables.
type HeartbeatPayload struct {
	Cluster         string            `json:"cluster"`
	Timestamp       int64             `json:"timestamp"`
	AgentVersion    string            `json:"agent_version,omitempty"`
	HostMetrics     HostMetrics       `json:"host_metrics"`
	NodeConditions  []NodeCondition   `json:"node_conditions,omitempty"`
	UnhealthyPods   []UnhealthyPod    `json:"unhealthy_pods,omitempty"`
	ExpiringCerts   []ExpiringCert    `json:"expiring_certs,omitempty"`
	ArgoCDUnhealthy []ArgoCDAppStatus `json:"argocd_unhealthy,omitempty"`
	DeepChecks      []DeepCheckResult `json:"deep_checks,omitempty"`
}

// HostMetrics contains aggregated host resource usage from local VictoriaMetrics.
type HostMetrics struct {
	CPULoad1        float64      `json:"cpu_load1"`
	CPUCores        int          `json:"cpu_cores,omitempty"`
	MemUsedPct      float64      `json:"mem_used_pct"`
	MemAvailablePct float64      `json:"mem_available_pct,omitempty"`
	SwapUsedPct     float64      `json:"swap_used_pct"`
	RootDiskPct     float64      `json:"root_disk_pct"`
	ExtraDiskPct    float64      `json:"extra_disk_pct,omitempty"`
	Mounts          []MountCheck `json:"mounts,omitempty"`
}

// MountCheck verifies that an expected block device is mounted at Path (e.g. /mnt/extra-storage).
type MountCheck struct {
	Path           string `json:"path"`
	Device         string `json:"device"`
	ExpectedDevice string `json:"expected_device,omitempty"`
	OK             bool   `json:"ok"`
}

// NodeCondition represents a Kubernetes Node condition.
type NodeCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// UnhealthyPod represents a pod that is not Ready or is crash-looping.
type UnhealthyPod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Ready     string `json:"ready"`
	Restarts  int    `json:"restarts"`
	Reason    string `json:"reason,omitempty"`
}

// ExpiringCert represents a cert-manager Certificate that is not Ready or expiring soon.
type ExpiringCert struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Ready     bool   `json:"ready"`
	DaysLeft  int    `json:"days_left"`
	NotAfter  int64  `json:"not_after,omitempty"`
}

// ArgoCDAppStatus represents an ArgoCD Application that is OutOfSync or Degraded.
type ArgoCDAppStatus struct {
	Name         string `json:"name"`
	SyncStatus   string `json:"sync_status"`
	HealthStatus string `json:"health_status"`
	Message      string `json:"message,omitempty"`
}

// DeepCheckResult represents an in-cluster application health check collected by an agent.
type DeepCheckResult struct {
	MonitorID string          `json:"monitor_id"`
	Name      string          `json:"name,omitempty"`
	State     string          `json:"state"`
	LatencyMs int64           `json:"latency_ms,omitempty"`
	Message   string          `json:"message,omitempty"`
	SubItems  []SubItemStatus `json:"sub_items,omitempty"`
	Details   map[string]any  `json:"details,omitempty"`
}

// SubItemStatus represents a sub-component status (e.g. Sekai jp/cn/tw/kr/en regions).
type SubItemStatus struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	State          string `json:"state"`
	Message        string `json:"message,omitempty"`
	ActiveSessions *int   `json:"active_sessions,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

// AlertEvent is persisted in state_events and streamed over /api/bot/stream & /api/bot/events.
type AlertEvent struct {
	ID           int64           `json:"id"`
	MonitorID    string          `json:"monitor_id"`
	MonitorName  string          `json:"monitor_name,omitempty"`
	Cluster      string          `json:"cluster,omitempty"`
	Severity     string          `json:"severity"`
	PrevState    string          `json:"prev_state"`
	NextState    string          `json:"next_state"`
	SuppressedBy string          `json:"suppressed_by,omitempty"`
	Summary      string          `json:"summary"`
	Detail       json.RawMessage `json:"detail,omitempty"`
	CreatedAt    int64           `json:"created_at"`
}

// PublicSummary is returned by GET /api/v1/summary.
// Strictly contains visibility=public monitors and redacted infrastructure states.
type PublicSummary struct {
	OverallStatus         string            `json:"overall_status"`
	Range                 string            `json:"range,omitempty"`
	UpdatedAt             int64             `json:"updated_at"`
	Groups                []PublicGroup     `json:"groups"`
	Infra                 []PublicInfraNode `json:"infra"`
	ActiveIncidents       []Incident        `json:"active_incidents"`
	ScheduledMaintenances []Incident        `json:"scheduled_maintenances"`
}

// PublicGroup groups public monitors by product line.
type PublicGroup struct {
	Key      string          `json:"key"`
	Name     string          `json:"name"`
	Status   string          `json:"status"`
	Monitors []PublicMonitor `json:"monitors"`
}

// PublicMonitor is the public-safe representation of a single monitor.
type PublicMonitor struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	GroupKey      string          `json:"group_key"`
	State         string          `json:"state"`
	Severity      string          `json:"severity"`
	LatencyMs     *int64          `json:"latency_ms,omitempty"`
	Uptime90d     float64         `json:"uptime_90d"`
	TLSDaysLeft   *int            `json:"tls_days_left,omitempty"`
	Message       string          `json:"message,omitempty"`
	SubItems      []PublicSubItem `json:"sub_items,omitempty"`
	DailyBars     []DayUptime     `json:"daily_bars"`
	LastCheckedAt int64           `json:"last_checked_at"`
}

// PublicSubItem is the redacted public sub-component status (no session counts or internal errors).
type PublicSubItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	State string `json:"state"`
}

// PublicInfraNode is the redacted public status of a cluster node.
// Never exposes IP, hostname, or exact resource metrics.
type PublicInfraNode struct {
	ID        string `json:"id"`
	Region    string `json:"region"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Label     string `json:"label"`
	UpdatedAt int64  `json:"updated_at"`
}

// DayUptime represents one UTC calendar day in the 90-day uptime bar.
type DayUptime struct {
	Date          string  `json:"date"`
	Timestamp     int64   `json:"timestamp"`
	UptimePct     float64 `json:"uptime_pct"`
	State         string  `json:"state"` // up | degraded | down | no_data
	AvgLatencyMs  int64   `json:"avg_latency_ms"`
	DownCount     int     `json:"down_count"`
	DegradedCount int     `json:"degraded_count"`
}

// LatencyPoint represents a single latency sample in the 24h chart.
type LatencyPoint struct {
	Timestamp int64  `json:"timestamp"`
	LatencyMs int64  `json:"latency_ms"`
	State     string `json:"state"`
}

// MonitorHistory is returned by GET /api/v1/monitors/{id}/history.
type MonitorHistory struct {
	MonitorID  string         `json:"monitor_id"`
	Name       string         `json:"name"`
	Uptime90d  float64        `json:"uptime_90d"`
	Daily      []DayUptime    `json:"daily"`
	Latency24h []LatencyPoint `json:"latency_24h"`
}

// Incident represents an incident or scheduled maintenance window.
type Incident struct {
	ID               string           `json:"id"`
	Type             string           `json:"type"`
	Title            string           `json:"title"`
	Status           string           `json:"status"`
	Impact           string           `json:"impact"`
	AffectedMonitors []string         `json:"affected_monitors"`
	StartsAt         *int64           `json:"starts_at,omitempty"`
	EndsAt           *int64           `json:"ends_at,omitempty"`
	CreatedBy        string           `json:"created_by,omitempty"`
	CreatedAt        int64            `json:"created_at"`
	UpdatedAt        int64            `json:"updated_at"`
	Updates          []IncidentUpdate `json:"updates"`
}

// IncidentUpdate is a timeline entry belonging to an Incident.
type IncidentUpdate struct {
	ID         int64  `json:"id"`
	IncidentID string `json:"incident_id"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	CreatedBy  string `json:"created_by,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// BotSummary is returned by GET /api/bot/summary for moebot /status queries.
type BotSummary struct {
	OverallStatus    string              `json:"overall_status"`
	UpdatedAt        int64               `json:"updated_at"`
	TotalMonitors    int                 `json:"total_monitors"`
	UpMonitors       int                 `json:"up_monitors"`
	AbnormalMonitors []AdminMonitor      `json:"abnormal_monitors"`
	Nodes            []AdminNodeOverview `json:"nodes"`
	SekaiRegions     []SubItemStatus     `json:"sekai_regions,omitempty"`
	ActiveIncidents  []Incident          `json:"active_incidents"`
}

// AdminOverview is returned by GET /api/admin/overview (Cloudflare Access protected).
type AdminOverview struct {
	OverallStatus string              `json:"overall_status"`
	UpdatedAt     int64               `json:"updated_at"`
	BotOnline     bool                `json:"bot_online"`
	BotClients    int                 `json:"bot_clients"`
	BotLastSeenAt int64               `json:"bot_last_seen_at,omitempty"`
	Monitors      []AdminMonitor      `json:"monitors"`
	Nodes         []AdminNodeOverview `json:"nodes"`
	Incidents     []Incident          `json:"incidents"`
	RecentEvents  []AlertEvent        `json:"recent_events"`
}

// AdminMonitor is the full internal monitor view shown in /admin and /api/bot/summary.
type AdminMonitor struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	GroupKey         string          `json:"group_key"`
	Kind             string          `json:"kind"`
	Cluster          string          `json:"cluster,omitempty"`
	Visibility       string          `json:"visibility"`
	Severity         string          `json:"severity"`
	State            string          `json:"state"`
	ConsecutiveFails int             `json:"consecutive_fails"`
	LatencyMs        *int64          `json:"latency_ms,omitempty"`
	TLSDaysLeft      *int            `json:"tls_days_left,omitempty"`
	Message          string          `json:"message,omitempty"`
	Meta             json.RawMessage `json:"meta,omitempty"`
	LastCheckedAt    int64           `json:"last_checked_at"`
}

// AdminNodeOverview aggregates the latest heartbeat snapshot for a cluster.
type AdminNodeOverview struct {
	Cluster         string            `json:"cluster"`
	State           string            `json:"state"`
	LastHeartbeatAt int64             `json:"last_heartbeat_at"`
	HostMetrics     *HostMetrics      `json:"host_metrics,omitempty"`
	NodeConditions  []NodeCondition   `json:"node_conditions,omitempty"`
	UnhealthyPods   []UnhealthyPod    `json:"unhealthy_pods,omitempty"`
	ExpiringCerts   []ExpiringCert    `json:"expiring_certs,omitempty"`
	ArgoCDUnhealthy []ArgoCDAppStatus `json:"argocd_unhealthy,omitempty"`
	DeepChecks      []DeepCheckResult `json:"deep_checks,omitempty"`
}

// CreateIncidentRequest is the payload for POST /api/admin/incidents.
type CreateIncidentRequest struct {
	Type             string   `json:"type"`
	Title            string   `json:"title"`
	Status           string   `json:"status"`
	Impact           string   `json:"impact"`
	AffectedMonitors []string `json:"affected_monitors"`
	Message          string   `json:"message"`
	StartsAt         *int64   `json:"starts_at,omitempty"`
	EndsAt           *int64   `json:"ends_at,omitempty"`
}

// UpdateIncidentRequest is the payload for PATCH /api/admin/incidents/{id}.
type UpdateIncidentRequest struct {
	Title            *string  `json:"title,omitempty"`
	Status           *string  `json:"status,omitempty"`
	Impact           *string  `json:"impact,omitempty"`
	AffectedMonitors []string `json:"affected_monitors,omitempty"`
	Message          string   `json:"message,omitempty"`
	StartsAt         *int64   `json:"starts_at,omitempty"`
	EndsAt           *int64   `json:"ends_at,omitempty"`
}

// TestAlertRequest is the payload for POST /api/admin/alerts/test.
type TestAlertRequest struct {
	Channel  string `json:"channel"`  // bot | email | all
	Severity string `json:"severity"` // P0 | P1 | P2
	Message  string `json:"message,omitempty"`
}
