package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/StarMoe-org/starmoe-status/agent/internal/deep"
	"github.com/StarMoe-org/starmoe-status/agent/internal/k8s"
	"github.com/StarMoe-org/starmoe-status/agent/internal/reporter"
	"github.com/StarMoe-org/starmoe-status/agent/internal/vm"
	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
)

var version = "dev"

type agentConfig struct {
	ClusterName      string
	IngestURL        string
	ClusterToken     string
	VMURL            string
	PollInterval     time.Duration
	EnableArgoCD     bool
	CertWarnDays     int
	ExtraMountPath   string
	ExtraMountDevice string
	ExcludeWorkloads []string
	DeepChecks       []deep.CheckConfig
}

func loadConfig() agentConfig {
	cfg := agentConfig{
		ClusterName:      strings.TrimSpace(os.Getenv("CLUSTER_NAME")),
		IngestURL:        strings.TrimSpace(os.Getenv("STATUS_INGEST_URL")),
		ClusterToken:     strings.TrimSpace(os.Getenv("CLUSTER_TOKEN")),
		VMURL:            strings.TrimSpace(os.Getenv("VM_URL")),
		PollInterval:     60 * time.Second,
		CertWarnDays:     14,
		ExtraMountPath:   strings.TrimSpace(os.Getenv("EXTRA_MOUNT_PATH")),
		ExtraMountDevice: strings.TrimSpace(os.Getenv("EXTRA_MOUNT_DEVICE")),
		ExcludeWorkloads: append([]string{}, k8s.DefaultExcludedWorkloads...),
	}

	if cfg.IngestURL == "" {
		cfg.IngestURL = "https://status.star.moe/api/ingest/heartbeat"
	}
	if cfg.VMURL == "" {
		cfg.VMURL = "http://vm.monitoring.svc.cluster.local:8428"
	}
	if raw := strings.TrimSpace(os.Getenv("POLL_INTERVAL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 10*time.Second {
			cfg.PollInterval = d
		}
	}
	if raw := strings.TrimSpace(os.Getenv("ENABLE_ARGOCD")); raw != "" {
		cfg.EnableArgoCD, _ = strconv.ParseBool(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("CERT_WARN_DAYS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			cfg.CertWarnDays = n
		}
	}
	if raw := strings.TrimSpace(os.Getenv("EXCLUDE_WORKLOADS")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			if s := strings.TrimSpace(item); s != "" {
				cfg.ExcludeWorkloads = append(cfg.ExcludeWorkloads, s)
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("DEEP_CHECKS_JSON")); raw != "" && raw != "[]" {
		var checks []deep.CheckConfig
		if err := json.Unmarshal([]byte(raw), &checks); err != nil {
			log.Printf("warn: invalid DEEP_CHECKS_JSON: %v", err)
		} else {
			cfg.DeepChecks = checks
		}
	}

	return cfg
}

func collectAndPush(
	ctx context.Context,
	cfg agentConfig,
	vmClient *vm.Client,
	k8sClient *k8s.Client,
	deepChecker *deep.Checker,
	rep *reporter.Reporter,
) {
	payload := protocol.HeartbeatPayload{
		Cluster:      cfg.ClusterName,
		Timestamp:    time.Now().Unix(),
		AgentVersion: version,
	}

	if vmClient != nil {
		metrics, err := vmClient.CollectHostMetrics(ctx, cfg.ExtraMountPath, cfg.ExtraMountDevice)
		if err != nil {
			log.Printf("warn: vm collect failed: %v", err)
		}
		payload.HostMetrics = metrics
	}

	if k8sClient != nil {
		if conds, err := k8sClient.ListNodeConditions(ctx); err != nil {
			log.Printf("warn: k8s nodes failed: %v", err)
		} else {
			payload.NodeConditions = conds
		}

		if pods, err := k8sClient.ListUnhealthyPods(ctx, cfg.ExcludeWorkloads); err != nil {
			log.Printf("warn: k8s pods failed: %v", err)
		} else {
			payload.UnhealthyPods = pods
		}

		if certs, err := k8sClient.ListExpiringCerts(ctx, cfg.CertWarnDays); err != nil {
			log.Printf("warn: k8s certs failed: %v", err)
		} else {
			payload.ExpiringCerts = certs
		}

		if cfg.EnableArgoCD {
			if apps, err := k8sClient.ListArgoCDUnhealthy(ctx); err != nil {
				log.Printf("warn: k8s argocd failed: %v", err)
			} else {
				payload.ArgoCDUnhealthy = apps
			}
		}
	}

	if deepChecker != nil && len(cfg.DeepChecks) > 0 {
		payload.DeepChecks = deepChecker.RunAll(ctx, cfg.DeepChecks)
	}

	if err := rep.Push(ctx, payload); err != nil {
		log.Printf("error: push heartbeat failed: %v", err)
		return
	}

	log.Printf(
		"heartbeat pushed cluster=%s cpu=%.2f mem=%.1f%% root=%.1f%% unhealthy_pods=%d certs=%d argocd=%d deep=%d",
		payload.Cluster,
		payload.HostMetrics.CPULoad1,
		payload.HostMetrics.MemUsedPct,
		payload.HostMetrics.RootDiskPct,
		len(payload.UnhealthyPods),
		len(payload.ExpiringCerts),
		len(payload.ArgoCDUnhealthy),
		len(payload.DeepChecks),
	)
}

func main() {
	cfg := loadConfig()
	if cfg.ClusterName == "" {
		log.Fatal("CLUSTER_NAME is required (e.g. DE, JP1, JP2, US)")
	}
	if cfg.ClusterToken == "" {
		log.Fatal("CLUSTER_TOKEN is required")
	}

	vmClient := vm.NewClient(cfg.VMURL, nil)

	k8sClient, err := k8s.NewInClusterClient()
	if err != nil {
		log.Printf("warn: k8s in-cluster client disabled: %v", err)
	}

	var podChecker deep.PodAliveChecker
	if k8sClient != nil {
		podChecker = k8sClient
	}
	deepChecker := deep.NewChecker(nil, podChecker)
	rep := reporter.New(cfg.IngestURL, cfg.ClusterToken, version, nil)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("starmoe-status-agent starting cluster=%s interval=%s argocd=%v deep_checks=%d",
		cfg.ClusterName, cfg.PollInterval, cfg.EnableArgoCD, len(cfg.DeepChecks))

	runOnce := func() {
		rctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		collectAndPush(rctx, cfg, vmClient, k8sClient, deepChecker, rep)
	}

	runOnce()

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("starmoe-status-agent stopping")
			return
		case <-ticker.C:
			runOnce()
		}
	}
}
