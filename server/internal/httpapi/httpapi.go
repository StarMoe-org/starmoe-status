package httpapi

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/alert"
	"github.com/StarMoe-org/starmoe-status/server/internal/auth"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/ingest"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
)

// Server wires all HTTP endpoints and static SPA assets.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	auth     *auth.Authenticator
	alerts   *alert.Engine
	ingestor *ingest.Ingestor
	webFS    fs.FS
	logger   *slog.Logger
	mux      *http.ServeMux
}

// New constructs a new HTTP API server.
func New(cfg *config.Config, st *store.Store, au *auth.Authenticator, al *alert.Engine, in *ingest.Ingestor, webFS fs.FS, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:      cfg,
		store:    st,
		auth:     au,
		alerts:   al,
		ingestor: in,
		webFS:    webFS,
		logger:   logger,
		mux:      http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	// Public API (with CORS + Cache-Control)
	s.mux.HandleFunc("GET /api/v1/summary", s.withPublicCORS(s.handlePublicSummary))
	s.mux.HandleFunc("OPTIONS /api/v1/summary", s.withPublicCORS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	s.mux.HandleFunc("GET /api/v1/monitors/{id}/history", s.withPublicCORS(s.handleMonitorHistory))
	s.mux.HandleFunc("OPTIONS /api/v1/monitors/{id}/history", s.withPublicCORS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	s.mux.HandleFunc("GET /api/v1/incidents", s.withPublicCORS(s.handlePublicIncidents))
	s.mux.HandleFunc("OPTIONS /api/v1/incidents", s.withPublicCORS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	s.mux.HandleFunc("GET /rss.xml", s.handleRSS)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	// Cluster agent ingest
	s.mux.HandleFunc("POST /api/ingest/heartbeat", s.handleIngestHeartbeat)

	// Moebot plugin endpoints
	s.mux.HandleFunc("GET /api/bot/stream", s.withBotAuth(s.handleBotStream))
	s.mux.HandleFunc("GET /api/bot/events", s.withBotAuth(s.handleBotEvents))
	s.mux.HandleFunc("GET /api/bot/summary", s.withBotAuth(s.handleBotSummary))

	// Admin endpoints (Cloudflare Access JWT or ADMIN_DEV_TOKEN)
	s.mux.HandleFunc("GET /api/admin/overview", s.withAdminAuth(s.handleAdminOverview))
	s.mux.HandleFunc("POST /api/admin/incidents", s.withAdminAuth(s.handleAdminCreateIncident))
	s.mux.HandleFunc("PATCH /api/admin/incidents/{id}", s.withAdminAuth(s.handleAdminUpdateIncident))
	s.mux.HandleFunc("POST /api/admin/alerts/test", s.withAdminAuth(s.handleAdminTestAlert))

	// Static SPA fallback
	s.mux.HandleFunc("/", s.handleStatic)
}

func (s *Server) withPublicCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isAllowedPublicOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		next(w, r)
	}
}

func isAllowedPublicOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" {
		return true
	}
	allowed := []string{"star.moe", "pjsk.moe", "bdon.moe", "exmeaning.com"}
	for _, domain := range allowed {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func (s *Server) withBotAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.VerifyBotToken(r.Header.Get("Authorization")) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

type adminHandlerFunc func(w http.ResponseWriter, r *http.Request, adminEmail string)

func (s *Server) withAdminAuth(next adminHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email, err := s.auth.VerifyAdmin(r.Context(), r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r, email)
	}
}

// --- Public Handlers ---

func normalizeRange(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "24h", "7d", "30d", "90d":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "90d"
	}
}

func (s *Server) handlePublicSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	rangeKey := normalizeRange(r.URL.Query().Get("range"))

	monitors, err := s.store.ListMonitors(ctx, true)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load monitors")
		return
	}

	var serviceIDs []string
	for _, m := range monitors {
		if m.GroupKey != "infra" {
			serviceIDs = append(serviceIDs, m.ID)
		}
	}
	barsMap, uptimeMap, err := s.store.GetRangeBarsBatch(ctx, serviceIDs, rangeKey, now)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load uptime history")
		return
	}

	incidents, err := s.store.ListIncidents(ctx, true, true)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load incidents")
		return
	}

	activeIncidents := make([]protocol.Incident, 0)
	scheduledMaint := make([]protocol.Incident, 0)
	hasActiveMaint := false
	for _, inc := range incidents {
		if inc.Type == protocol.IncidentTypeMaintenance {
			scheduledMaint = append(scheduledMaint, inc)
			if inc.Status == protocol.IncidentStatusInProgress {
				hasActiveMaint = true
			}
		} else {
			activeIncidents = append(activeIncidents, inc)
		}
	}

	groupOrder := s.cfg.Groups
	if len(groupOrder) == 0 {
		groupOrder = []config.GroupConfig{
			{Key: "platform", Name: "StarMoe 平台"},
			{Key: "moesekai", Name: "MoeSekai (PJSK)"},
			{Key: "moenotes", Name: "MoeNotes (bdon)"},
		}
	}
	groupMons := make(map[string][]protocol.PublicMonitor, len(groupOrder))
	infraNodes := make([]protocol.PublicInfraNode, 0, 4)

	for _, m := range monitors {
		if m.GroupKey == "infra" {
			infraNodes = append(infraNodes, protocol.PublicInfraNode{
				ID:        m.ID,
				Region:    m.Cluster,
				Name:      m.Name,
				State:     m.CurrentState,
				Label:     infraPublicLabel(m.CurrentState),
				UpdatedAt: m.LastCheckedAt,
			})
			continue
		}

		tlsDays, pubSubs := parsePublicMeta(m.MetaJSON)
		pm := protocol.PublicMonitor{
			ID:            m.ID,
			Name:          m.Name,
			GroupKey:      m.GroupKey,
			State:         m.CurrentState,
			Severity:      m.Severity,
			LatencyMs:     m.LastLatencyMs,
			Uptime90d:     uptimeMap[m.ID],
			TLSDaysLeft:   tlsDays,
			SubItems:      pubSubs,
			DailyBars:     barsMap[m.ID],
			LastCheckedAt: m.LastCheckedAt,
		}
		if pm.DailyBars == nil {
			pm.DailyBars = []protocol.DayUptime{}
		}
		groupMons[m.GroupKey] = append(groupMons[m.GroupKey], pm)
	}

	groups := make([]protocol.PublicGroup, 0, len(groupOrder))
	for _, g := range groupOrder {
		mons := groupMons[g.Key]
		if mons == nil {
			mons = []protocol.PublicMonitor{}
		}
		groups = append(groups, protocol.PublicGroup{
			Key:      g.Key,
			Name:     g.Name,
			Status:   computeGroupStatus(mons),
			Monitors: mons,
		})
	}

	summary := protocol.PublicSummary{
		OverallStatus:         computeOverallStatus(monitors, hasActiveMaint),
		Range:                 rangeKey,
		UpdatedAt:             now.Unix(),
		Groups:                groups,
		Infra:                 infraNodes,
		ActiveIncidents:       activeIncidents,
		ScheduledMaintenances: scheduledMaint,
	}

	w.Header().Set("Cache-Control", "public, max-age=15, s-maxage=30")
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleMonitorHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	mon, err := s.store.GetMonitor(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (mon != nil && mon.Visibility != protocol.VisibilityPublic) {
		writeJSONError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}

	now := time.Now()
	barsMap, uptimeMap, err := s.store.GetDailyBarsBatch(ctx, []string{mon.ID}, 90, now)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load daily bars")
		return
	}
	lat24, err := s.store.GetLatency24h(ctx, mon.ID, now)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load 24h latency")
		return
	}

	resp := protocol.MonitorHistory{
		MonitorID:  mon.ID,
		Name:       mon.Name,
		Uptime90d:  uptimeMap[mon.ID],
		Daily:      barsMap[mon.ID],
		Latency24h: lat24,
	}
	w.Header().Set("Cache-Control", "public, max-age=15, s-maxage=30")
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePublicIncidents(w http.ResponseWriter, r *http.Request) {
	incidents, err := s.store.ListIncidents(r.Context(), false, true)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load incidents")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=15, s-maxage=30")
	writeJSON(w, http.StatusOK, map[string]any{"incidents": incidents})
}

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	PubDate     string    `xml:"pubDate"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

func (s *Server) handleRSS(w http.ResponseWriter, r *http.Request) {
	incidents, err := s.store.ListIncidents(r.Context(), false, true)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load incidents")
		return
	}

	items := make([]rssItem, 0, len(incidents))
	for _, inc := range incidents {
		var desc strings.Builder
		desc.WriteString(fmt.Sprintf("[%s / %s] %s", inc.Type, inc.Status, inc.Title))
		for _, u := range inc.Updates {
			desc.WriteString(fmt.Sprintf("\n- (%s) %s", u.Status, u.Message))
		}
		items = append(items, rssItem{
			Title:       fmt.Sprintf("[%s] %s", strings.ToUpper(inc.Status), inc.Title),
			Link:        s.cfg.PublicBaseURL + "/incidents",
			GUID:        inc.ID + "-" + strconv.FormatInt(inc.UpdatedAt, 10),
			PubDate:     time.Unix(inc.UpdatedAt, 0).UTC().Format(time.RFC1123Z),
			Description: desc.String(),
		})
	}

	feed := rssFeed{
		Version: "2.0",
		Channel: rssChannel{
			Title:       "StarMoe Status - Incidents & Maintenance",
			Link:        s.cfg.PublicBaseURL,
			Description: "Real-time service status, incidents, and scheduled maintenance for StarMoe.",
			PubDate:     time.Now().UTC().Format(time.RFC1123Z),
			Items:       items,
		},
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=30, s-maxage=60")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(feed)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.CheckWritable(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok":    false,
			"db":    "error",
			"error": err.Error(),
		})
		return
	}
	lastProbe, _ := s.store.LastProbeTime(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"db":            "ok",
		"last_probe_at": lastProbe,
	})
}

// --- Cluster Ingest ---

func (s *Server) handleIngestHeartbeat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var payload protocol.HeartbeatPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	cluster := strings.ToUpper(strings.TrimSpace(payload.Cluster))
	if cluster == "" {
		writeJSONError(w, http.StatusBadRequest, "cluster is required")
		return
	}
	if !s.auth.VerifyClusterToken(r.Header.Get("Authorization"), cluster) {
		writeJSONError(w, http.StatusUnauthorized, "invalid cluster token")
		return
	}

	now := time.Now().Unix()
	if err := s.ingestor.ProcessHeartbeat(r.Context(), payload, now); err != nil {
		s.logger.Error("process heartbeat failed", "cluster", cluster, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to process heartbeat")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"cluster":     cluster,
		"received_at": now,
	})
}

// --- Bot Endpoints ---

func (s *Server) handleBotStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	afterID, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)

	// Subscribe BEFORE replaying so no event is missed between replay and live stream.
	subID, ch := s.alerts.Subscribe()
	defer s.alerts.Unsubscribe(subID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	var lastSentID int64 = afterID
	if afterID > 0 {
		missed, err := s.store.ListStateEventsAfter(r.Context(), afterID, false, 100)
		if err == nil && len(missed) > 0 {
			cutoff := time.Now().Add(-30 * time.Minute).Unix()
			var stale []protocol.AlertEvent
			var recent []protocol.AlertEvent
			for _, ev := range missed {
				if ev.CreatedAt < cutoff {
					stale = append(stale, ev)
				} else {
					recent = append(recent, ev)
				}
			}
			// Merge stale missed events (>30m old) into one summary to prevent spam.
			if len(stale) > 0 {
				lastStale := stale[len(stale)-1]
				merged := protocol.AlertEvent{
					ID:          lastStale.ID,
					MonitorID:   "summary",
					MonitorName: "Missed Alerts Summary",
					Severity:    protocol.SeverityP1,
					PrevState:   protocol.StateUnknown,
					NextState:   protocol.StateDegraded,
					Summary:     fmt.Sprintf("%d older alert event(s) occurred while offline", len(stale)),
					CreatedAt:   lastStale.CreatedAt,
				}
				writeSSEEvent(w, merged)
				flusher.Flush()
				lastSentID = merged.ID
			}
			for _, ev := range recent {
				writeSSEEvent(w, ev)
				flusher.Flush()
				if ev.ID > lastSentID {
					lastSentID = ev.ID
				}
			}
		}
	}

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			if ev.ID > 0 && ev.ID <= lastSentID {
				continue
			}
			writeSSEEvent(w, ev)
			flusher.Flush()
			if ev.ID > lastSentID {
				lastSentID = ev.ID
			}
		case <-heartbeat.C:
			_, _ = fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, ev protocol.AlertEvent) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: alert\nid: %d\ndata: %s\n\n", ev.ID, b)
}

func (s *Server) handleBotEvents(w http.ResponseWriter, r *http.Request) {
	s.alerts.TouchBotSeen()
	afterID, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	events, err := s.store.ListStateEventsAfter(r.Context(), afterID, false, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleBotSummary(w http.ResponseWriter, r *http.Request) {
	s.alerts.TouchBotSeen()
	ctx := r.Context()
	monitors, err := s.store.ListMonitors(ctx, false)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load monitors")
		return
	}
	nodes, err := s.buildAdminNodes(ctx, monitors)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load nodes")
		return
	}
	activeIncidents, err := s.store.ListIncidents(ctx, true, false)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load incidents")
		return
	}

	var abnormal []protocol.AdminMonitor
	var sekaiRegions []protocol.SubItemStatus
	upCount := 0
	for _, m := range monitors {
		if m.CurrentState == protocol.StateUp {
			upCount++
		} else if m.CurrentState == protocol.StateDown || m.CurrentState == protocol.StateDegraded {
			abnormal = append(abnormal, toAdminMonitor(m))
		}
		if m.ID == "pjsk-servers" && m.MetaJSON != "" {
			sekaiRegions = parseFullSubItems(m.MetaJSON)
		}
	}
	if abnormal == nil {
		abnormal = []protocol.AdminMonitor{}
	}

	writeJSON(w, http.StatusOK, protocol.BotSummary{
		OverallStatus:    computeOverallStatus(monitors, false),
		UpdatedAt:        time.Now().Unix(),
		TotalMonitors:    len(monitors),
		UpMonitors:       upCount,
		AbnormalMonitors: abnormal,
		Nodes:            nodes,
		SekaiRegions:     sekaiRegions,
		ActiveIncidents:  activeIncidents,
	})
}

// --- Admin Endpoints ---

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request, _ string) {
	ctx := r.Context()
	monitors, err := s.store.ListMonitors(ctx, false)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load monitors")
		return
	}
	nodes, err := s.buildAdminNodes(ctx, monitors)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load nodes")
		return
	}
	incidents, err := s.store.ListIncidents(ctx, false, false)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load incidents")
		return
	}
	events, err := s.store.ListRecentStateEvents(ctx, 50)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load state events")
		return
	}

	adminMons := make([]protocol.AdminMonitor, 0, len(monitors))
	for _, m := range monitors {
		adminMons = append(adminMons, toAdminMonitor(m))
	}

	online, clients, lastSeen := s.alerts.BotStatus()
	writeJSON(w, http.StatusOK, protocol.AdminOverview{
		OverallStatus: computeOverallStatus(monitors, false),
		UpdatedAt:     time.Now().Unix(),
		BotOnline:     online,
		BotClients:    clients,
		BotLastSeenAt: lastSeen,
		Monitors:      adminMons,
		Nodes:         nodes,
		Incidents:     incidents,
		RecentEvents:  events,
	})
}

func (s *Server) handleAdminCreateIncident(w http.ResponseWriter, r *http.Request, adminEmail string) {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var req protocol.CreateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeJSONError(w, http.StatusBadRequest, "title is required")
		return
	}
	inc, err := s.store.CreateIncident(r.Context(), req, adminEmail, time.Now().Unix())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create incident")
		return
	}
	writeJSON(w, http.StatusCreated, inc)
}

func (s *Server) handleAdminUpdateIncident(w http.ResponseWriter, r *http.Request, adminEmail string) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var req protocol.UpdateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	inc, err := s.store.UpdateIncident(r.Context(), id, req, adminEmail, time.Now().Unix())
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "incident not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to update incident")
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

func (s *Server) handleAdminTestAlert(w http.ResponseWriter, r *http.Request, _ string) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req protocol.TestAlertRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	results := s.alerts.SendTestAlert(r.Context(), req)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"results": results,
	})
}

// --- Static SPA Serving ---

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if s.webFS == nil {
		http.NotFound(w, r)
		return
	}

	cleaned := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if cleaned == "" {
		cleaned = "index.html"
	}

	candidates := []string{
		cleaned,
		cleaned + ".html",
		path.Join(cleaned, "index.html"),
		"index.html",
	}

	for _, cand := range candidates {
		info, err := fs.Stat(s.webFS, cand)
		if err == nil && !info.IsDir() {
			data, err := fs.ReadFile(s.webFS, cand)
			if err != nil {
				break
			}
			if strings.HasPrefix(cand, "_next/static/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else if strings.HasSuffix(cand, ".html") {
				w.Header().Set("Cache-Control", "public, max-age=60")
			}
			http.ServeContent(w, r, path.Base(cand), info.ModTime(), strings.NewReader(string(data)))
			return
		}
	}
	http.NotFound(w, r)
}

// --- Helpers ---

func (s *Server) buildAdminNodes(ctx context.Context, monitors []store.MonitorRow) ([]protocol.AdminNodeOverview, error) {
	hbs, err := s.store.GetLatestHeartbeats(ctx)
	if err != nil {
		return nil, err
	}
	nodeStates := make(map[string]string)
	for _, m := range monitors {
		if m.Kind == protocol.KindAgentNode && m.Cluster != "" {
			nodeStates[m.Cluster] = m.CurrentState
		}
	}

	clusters := []string{"DE", "JP1", "JP2", "US"}
	out := make([]protocol.AdminNodeOverview, 0, len(clusters))
	for _, c := range clusters {
		state := nodeStates[c]
		if state == "" {
			state = protocol.StateUnknown
		}
		ov := protocol.AdminNodeOverview{
			Cluster: c,
			State:   state,
		}
		if hb, ok := hbs[c]; ok {
			ov.LastHeartbeatAt = hb.ReceivedAt
			var p protocol.HeartbeatPayload
			if err := json.Unmarshal([]byte(hb.PayloadJSON), &p); err == nil {
				hm := p.HostMetrics
				ov.HostMetrics = &hm
				ov.NodeConditions = p.NodeConditions
				ov.UnhealthyPods = p.UnhealthyPods
				ov.ExpiringCerts = p.ExpiringCerts
				ov.ArgoCDUnhealthy = p.ArgoCDUnhealthy
				ov.DeepChecks = p.DeepChecks
			}
		}
		out = append(out, ov)
	}
	return out, nil
}

func toAdminMonitor(m store.MonitorRow) protocol.AdminMonitor {
	tlsDays, _ := parsePublicMeta(m.MetaJSON)
	var meta json.RawMessage
	if m.MetaJSON != "" {
		meta = json.RawMessage(m.MetaJSON)
	}
	return protocol.AdminMonitor{
		ID:               m.ID,
		Name:             m.Name,
		GroupKey:         m.GroupKey,
		Kind:             m.Kind,
		Cluster:          m.Cluster,
		Visibility:       m.Visibility,
		Severity:         m.Severity,
		State:            m.CurrentState,
		ConsecutiveFails: m.ConsecutiveFails,
		LatencyMs:        m.LastLatencyMs,
		TLSDaysLeft:      tlsDays,
		Message:          m.LastMessage,
		Meta:             meta,
		LastCheckedAt:    m.LastCheckedAt,
	}
}

func parsePublicMeta(metaJSON string) (*int, []protocol.PublicSubItem) {
	if metaJSON == "" {
		return nil, nil
	}
	var raw struct {
		TLSDaysLeft *int                     `json:"tls_days_left"`
		SubItems    []protocol.SubItemStatus `json:"sub_items"`
	}
	if err := json.Unmarshal([]byte(metaJSON), &raw); err != nil {
		return nil, nil
	}
	var subs []protocol.PublicSubItem
	for _, sub := range raw.SubItems {
		subs = append(subs, protocol.PublicSubItem{
			Key:   sub.Key,
			Label: sub.Label,
			State: sub.State,
		})
	}
	return raw.TLSDaysLeft, subs
}

func parseFullSubItems(metaJSON string) []protocol.SubItemStatus {
	var raw struct {
		SubItems []protocol.SubItemStatus `json:"sub_items"`
	}
	if err := json.Unmarshal([]byte(metaJSON), &raw); err != nil {
		return nil
	}
	return raw.SubItems
}

func infraPublicLabel(state string) string {
	switch state {
	case protocol.StateUp:
		return "正常"
	case protocol.StateDegraded:
		return "负载偏高"
	case protocol.StateDown:
		return "异常"
	default:
		return "等待上报"
	}
}

func computeGroupStatus(mons []protocol.PublicMonitor) string {
	hasDownP0 := false
	hasIssue := false
	for _, m := range mons {
		if m.State == protocol.StateDown {
			if m.Severity == protocol.SeverityP0 {
				hasDownP0 = true
			}
			hasIssue = true
		} else if m.State == protocol.StateDegraded {
			hasIssue = true
		}
	}
	if hasDownP0 {
		return protocol.OverallOutage
	}
	if hasIssue {
		return protocol.OverallDegraded
	}
	return protocol.OverallOperational
}

func computeOverallStatus(monitors []store.MonitorRow, hasActiveMaint bool) string {
	hasDownP0 := false
	hasIssue := false
	for _, m := range monitors {
		if m.CurrentState == protocol.StateDown {
			if m.Severity == protocol.SeverityP0 {
				hasDownP0 = true
			}
			hasIssue = true
		} else if m.CurrentState == protocol.StateDegraded {
			hasIssue = true
		}
	}
	if hasDownP0 {
		return protocol.OverallOutage
	}
	if hasIssue {
		return protocol.OverallDegraded
	}
	if hasActiveMaint {
		return protocol.OverallMaintenance
	}
	return protocol.OverallOperational
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
