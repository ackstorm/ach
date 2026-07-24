// SPDX-License-Identifier: Apache-2.0

// `ach operator` is the ACH Hub Operator entrypoint (D-03). It wires every
// Phase 1 piece — env-var validation (D-08, D-09), PVC layout bootstrap
// (D-13), Postgres connection pool, LiteLLM client built from
// LiteLLMConnection (D-11), all reconcilers, namespace-scoped informer
// cache (MULTI-01), health probes — and blocks on manager.Start until
// SIGINT/SIGTERM. Body lifted from ach-old/cmd/operator/main.go and
// adapted to a cobra RunE for the single-binary layout.

package cmd

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/cachefs"
	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/connection"
	achcontroller "github.com/ackstorm/ach/internal/controller/ach"
	"github.com/ackstorm/ach/internal/credhash/pepperenv"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/featuregate"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/forwarder/litellmconn"
	achmetrics "github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/operator/jwtkeys"
	"github.com/ackstorm/ach/internal/operator/litellmconnboot"
	"github.com/ackstorm/ach/internal/operator/refreshsignal"
	"github.com/ackstorm/ach/internal/operator/resync"
	"github.com/ackstorm/ach/internal/orphan"
	"github.com/ackstorm/ach/internal/snapshot"
	// +kubebuilder:scaffold:imports
)

// resyncSourceChanCap bounds each per-Kind source.Channel so a slow
// reconciler doesn't backpressure the resync sweep into ctx-cancel.
// 64 covers a typical envtest spec; oversize sweeps (>64 CRs of one
// Kind) push out the resync horizon by one tick — acceptable safety
// net behavior.
const resyncSourceChanCap = 64

var (
	operatorScheme   = runtime.NewScheme()
	operatorSetupLog = ctrl.Log.WithName("setup")

	// Operator-subcommand flags. Registered on stdlib `flag.CommandLine`
	// (kubebuilder boilerplate) and bridged into cobra's pflag set in
	// init() so `ach operator --metrics-bind-address=:8080` actually
	// reaches the operator. Without the bridge, cobra rejects unknown
	// flags before RunE runs.
	operatorFlags struct {
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		secureMetrics        bool
		webhookCertPath      string
		webhookCertName      string
		webhookCertKey       string
		metricsCertPath      string
		metricsCertName      string
		metricsCertKey       string
		enableHTTP2          bool

		// LiteLLMConnection/default bootstrap coordinates (issue #34). The
		// chart's litellmConnection block feeds these; the operator
		// get-or-creates the canonical CR on boot (see litellmconnboot). Only
		// the Secret REFERENCE is passed here — never the master-key value.
		litellmEndpoint      string
		litellmKeySecretName string
		litellmKeySecretKey  string
	}
	operatorZapOpts = zap.Options{Development: true}
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(operatorScheme))
	utilruntime.Must(achv1alpha1.AddToScheme(operatorScheme))
	// +kubebuilder:scaffold:scheme

	flag.StringVar(&operatorFlags.metricsAddr, "metrics-bind-address",
		config.EnvOr("METRICS_BIND_ADDRESS", "0"),
		"The address the metrics endpoint binds to. "+
			"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&operatorFlags.probeAddr, "health-probe-bind-address",
		config.EnvOr("PROBE_BIND_ADDRESS", ":8081"),
		"The address the probe endpoint binds to.")
	flag.BoolVar(&operatorFlags.enableLeaderElection, "leader-elect",
		config.EnvBool("LEADER_ELECT", false),
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&operatorFlags.secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&operatorFlags.webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&operatorFlags.webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&operatorFlags.webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&operatorFlags.metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&operatorFlags.metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&operatorFlags.metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.StringVar(&operatorFlags.litellmEndpoint, "litellm-endpoint",
		config.EnvOr("ACH_LITELLM_CONNECTION_ENDPOINT", ""),
		"Base URL of the LiteLLM instance. When non-empty, the operator "+
			"bootstraps LiteLLMConnection/default with it (issue #34). Empty disables bootstrap.")
	flag.StringVar(&operatorFlags.litellmKeySecretName, "litellm-master-key-secret-name",
		config.EnvOr("ACH_LITELLM_CONNECTION_SECRET_NAME", ""),
		"Name of the same-namespace Secret holding the LiteLLM master key (referenced by the bootstrapped CR; the value is never read here).")
	flag.StringVar(&operatorFlags.litellmKeySecretKey, "litellm-master-key-secret-key",
		config.EnvOr("ACH_LITELLM_CONNECTION_SECRET_KEY", ""),
		"Data key inside the master-key Secret referenced by the bootstrapped LiteLLMConnection.")
	flag.BoolVar(&operatorFlags.enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	operatorZapOpts.BindFlags(flag.CommandLine)

	operatorCmd.Flags().AddGoFlagSet(flag.CommandLine)
	rootCmd.AddCommand(operatorCmd)
}

var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "Run the ACH Kubernetes operator (controller-runtime manager)",
	Long: `Boot the controller-runtime manager that reconciles every ACH CRD
(Environment, Plugin, PluginMarketplace, Skill, SkillMarketplace, Artifact,
Prompt, BackendIdentityPolicy, LiteLLMConnection) within the namespace named by
ACH_NAMESPACE. Health probes at :8081; metrics at the configured address.`,
	RunE: runOperator,
}

// nolint:gocyclo
func runOperator(_ *cobra.Command, _ []string) error {
	metricsAddr := operatorFlags.metricsAddr
	probeAddr := operatorFlags.probeAddr
	enableLeaderElection := operatorFlags.enableLeaderElection
	secureMetrics := operatorFlags.secureMetrics
	webhookCertPath := operatorFlags.webhookCertPath
	webhookCertName := operatorFlags.webhookCertName
	webhookCertKey := operatorFlags.webhookCertKey
	metricsCertPath := operatorFlags.metricsCertPath
	metricsCertName := operatorFlags.metricsCertName
	metricsCertKey := operatorFlags.metricsCertKey
	enableHTTP2 := operatorFlags.enableHTTP2
	var tlsOpts []func(*tls.Config)

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&operatorZapOpts)))

	// ─── Multi-tenancy: namespace-scoped informer cache (MULTI-01) ───
	watchNS := config.EnvOr("ACH_NAMESPACE", "ach-system")

	// ─── D-09: credential-hash pepper fail-fast ───
	pepper, err := pepperenv.Load()
	if err != nil {
		return fmt.Errorf("credential-hash pepper invalid (D-09 / Hub §16.1): %w", err)
	}
	_ = pepper

	// ─── D-08: ACH_DB_URL fail-fast ───
	dbURL, err := config.MustEnvNonEmpty("ACH_DB_URL")
	if err != nil {
		return fmt.Errorf("ACH_DB_URL is required (D-08): %w", err)
	}

	// ─── OP-09 forward-compat: ACH_PLUGIN_MAX_SIZE_MIB ───
	pluginMaxSizeMiB, err := config.MustEnvIntPositive("ACH_PLUGIN_MAX_SIZE_MIB", 50)
	if err != nil {
		return fmt.Errorf("ACH_PLUGIN_MAX_SIZE_MIB must be a positive integer (OP-09 / Hub §11): %w", err)
	}
	operatorSetupLog.Info("plugin size limit configured", "ACH_PLUGIN_MAX_SIZE_MIB", pluginMaxSizeMiB)

	// ─── ACH_SKILL_MAX_SIZE_MIB (agent-skill content kind) ───
	skillMaxSizeMiB, err := config.MustEnvIntPositive("ACH_SKILL_MAX_SIZE_MIB", 50)
	if err != nil {
		return fmt.Errorf("ACH_SKILL_MAX_SIZE_MIB must be a positive integer: %w", err)
	}
	operatorSetupLog.Info("skill size limit configured", "ACH_SKILL_MAX_SIZE_MIB", skillMaxSizeMiB)

	// ─── Phase 2: ACH_ORPHAN_CLEANUP_INTERVAL (OP-15 / D-15) ───
	orphanInterval, err := config.MustEnvDurationAtLeast("ACH_ORPHAN_CLEANUP_INTERVAL", time.Hour, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("ACH_ORPHAN_CLEANUP_INTERVAL invalid (OP-15 / D-15): %w", err)
	}
	operatorSetupLog.Info("orphan-cleanup interval configured", "interval", orphanInterval)

	// ─── orphan-cleanup defense-in-depth guardrails (B1/B2/B3) ───
	// DryRun = reversible image-level neutralize; MaxRevoke = per-tick
	// circuit-breaker cap (a double-digit batch is itself the alarm).
	// MustEnvBool (not EnvBool): a malformed value must fail startup, never
	// silently fall back to false and re-enable real revocation — this is the
	// emergency neutralize for a destructive loop.
	orphanDryRun, err := config.MustEnvBool("ACH_ORPHAN_CLEANUP_DRY_RUN", false)
	if err != nil {
		return fmt.Errorf("ACH_ORPHAN_CLEANUP_DRY_RUN invalid (B3 dry-run safety toggle): %w", err)
	}
	orphanMaxRevoke, err := config.MustEnvIntPositive("ACH_ORPHAN_CLEANUP_MAX_REVOKE", orphan.DefaultMaxRevoke)
	if err != nil {
		return fmt.Errorf("ACH_ORPHAN_CLEANUP_MAX_REVOKE must be a positive integer (B2 circuit-breaker): %w", err)
	}
	operatorSetupLog.Info("orphan-cleanup guardrails configured",
		"dry_run", orphanDryRun, "max_revoke", orphanMaxRevoke)

	// ─── Postgres connection pool ───
	dbCtx, dbCancel := context.WithCancel(context.Background())
	defer dbCancel()
	dbPool, err := db.Open(dbCtx, dbURL)
	if err != nil {
		return fmt.Errorf("unable to open Postgres pool: %w", err)
	}
	defer dbPool.Close()
	operatorSetupLog.Info("Postgres pool opened", "maxConns", 10)

	// ─── D-13: PVC layout bootstrap ───
	cacheRoot := config.EnvOr("ACH_CACHE_ROOT", "/var/cache/ach")
	if err := cachefs.EnsureLayout(cacheRoot); err != nil {
		return fmt.Errorf("cache layout init failed (D-13 / OP-10) cacheRoot=%s: %w", cacheRoot, err)
	}
	operatorSetupLog.Info("PVC cache layout ready", "cacheRoot", cacheRoot)

	// ─── OP-11: empty-PVC recovery ───
	cacheWasEmpty, err := cachefs.IsEmpty(cacheRoot)
	if err != nil {
		return fmt.Errorf("cachefs.IsEmpty failed (OP-11) cacheRoot=%s: %w", cacheRoot, err)
	}
	if cacheWasEmpty {
		if err := db.ResetExternalRefRefreshOnEmptyCache(context.Background(), dbPool); err != nil {
			return fmt.Errorf("ResetExternalRefRefreshOnEmptyCache failed (OP-11): %w", err)
		}
		if err := db.ResetMarketplacePluginsRefreshOnEmptyCache(context.Background(), dbPool); err != nil {
			return fmt.Errorf("ResetMarketplacePluginsRefreshOnEmptyCache failed (OP-11): %w", err)
		}
		if err := db.ResetSkillMarketplaceSkillsRefreshOnEmptyCache(context.Background(), dbPool); err != nil {
			return fmt.Errorf("ResetSkillMarketplaceSkillsRefreshOnEmptyCache failed (OP-11): %w", err)
		}
		operatorSetupLog.Info("PVC was empty on startup — external_refs + marketplace_plugins + skill_marketplace_skills last_successful_refresh reset (OP-11)")
	}

	// ─── Phase 2: LiteLLMConnection-backed client ───
	connCache := connection.NewCache()
	realLiteLLM := connection.NewClient(connCache)

	// ─── Phase 2: audit logger (D-17) ───
	auditLog := audit.NewLogger(os.Stdout)

	disableHTTP2 := func(c *tls.Config) {
		operatorSetupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher
	webhookTLSOpts := tlsOpts

	if len(webhookCertPath) > 0 {
		operatorSetupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			return fmt.Errorf("failed to initialize webhook certificate watcher: %w", err)
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// ─── Phase 5 D-10 / OBS-05: register the shared
	//     ach_litellm_unreachable_total counter on controller-runtime's
	//     global metrics Registry (typed as a RegistererGatherer, which
	//     satisfies the prometheus.Registerer parameter of the single
	//     MustRegisterLitellmUnreachable in internal/metrics/shared.go).
	//     The Operator keeps controller-runtime's metricsserver
	//     (:8443 HTTPS by default) — adding the ACH-namespaced shared
	//     counter to crmetrics.Registry means /metrics on that server
	//     surfaces the §18.5 cross-component metric with
	//     caller="operator" dimension pre-declared.
	//
	//     Inc instrumentation is REGISTERED-BUT-UNUSED at end of Phase 5
	//     (see Plan 05-06 spec_divergence): existing
	//     internal/controller/ach/ reconcilers consume litellm.Client
	//     for §10.3 force-refresh and the error branches log + retry
	//     via the controller-runtime workqueue but do NOT emit a
	//     litellm_unreachable counter. Retrofitting Inc hooks across
	//     every reconciler error branch is a structural Phase 2/3
	//     change and out of scope for Phase 5. The pre-declared
	//     caller dimension means a future phase can add Inc hooks
	//     without re-registering the metric.
	operatorLitellmUnreachable := achmetrics.MustRegisterLitellmUnreachable(crmetrics.Registry)
	operatorLitellmUnreachable.WithLabelValues("operator").Add(0) // expose family at 0 (§18.5)
	operatorSetupLog.Info("operator: registered ACH-namespaced collectors on controller-runtime metrics registry",
		"metric_count", 1, "metric", "ach_litellm_unreachable_total")

	if len(metricsCertPath) > 0 {
		operatorSetupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			return fmt.Errorf("failed to initialize metrics certificate watcher: %w", err)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  operatorScheme,
		Metrics: metricsServerOptions,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				watchNS: {},
			},
		},
		WebhookServer:           webhookServer,
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "c86cb6c7.ackstorm.ai",
		LeaderElectionNamespace: watchNS,
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	// ─── Mint the forwarder's JWT signing keys (FWD-09) if absent ───
	// The operator owns ach-jwt-signing-keys: a fresh random Ed25519 seed is
	// minted once and persisted. Run with a direct uncached client BEFORE
	// mgr.Start (the cache is not up yet). Idempotent — never overwrites an
	// existing key, so the key persists across operator restarts; and because
	// the Secret is operator-owned (not Helm-owned) it survives helm uninstall.
	// The forwarder refuse-to-starts until this Secret exists.
	bootClient, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return fmt.Errorf("unable to build bootstrap client for jwt signing keys: %w", err)
	}
	if err := jwtkeys.EnsureSigningKeys(context.Background(), bootClient, watchNS, jwt.SecretName, ctrl.Log.WithName("jwtkeys")); err != nil {
		return fmt.Errorf("unable to ensure jwt signing keys: %w", err)
	}

	// ─── Bootstrap LiteLLMConnection/default (issue #34) ───
	// The chart renders ACH CRDs as templates (managed-upgrade strategy), so
	// Helm cannot map the LiteLLMConnection kind within the install release —
	// a CR template/hook fails "no matches for kind". The operator creates it
	// here instead, with its live-discovery client. Idempotent + drift-aware;
	// no-op when --litellm-endpoint is empty (litellmConnection disabled).
	if err := litellmconnboot.EnsureConnection(context.Background(), bootClient, watchNS,
		litellmconn.CRName, operatorFlags.litellmEndpoint,
		operatorFlags.litellmKeySecretName, operatorFlags.litellmKeySecretKey,
		ctrl.Log.WithName("litellmconnboot")); err != nil {
		return fmt.Errorf("unable to bootstrap LiteLLMConnection: %w", err)
	}

	// ─── Phase 2: pre-warm corev1.Secret informer (D-11) ───
	if _, err := mgr.GetCache().GetInformer(context.Background(), &corev1.Secret{}); err != nil {
		return fmt.Errorf("unable to install Secret informer pre-warm: %w", err)
	}

	// ─── Issue #34 (A10/A11): per-Kind source.Channel feeds ───
	//
	// Each reconciler that owns one of the seven ACH CR Kinds picks up
	// GenericEvents from a dedicated buffered channel via
	// WatchesRawSource(source.Channel(...)). Two upstream producers push
	// into these channels:
	//
	//   - resync.Resync (this file) — 5-minute periodic full re-list of
	//     every Kind in watchNS. Safety net for missed events, operator
	//     restart drift, and rare DB-NOTIFY drops.
	//
	//   - refreshsignal.Listener (this file) — Postgres LISTEN
	//     ach_refresh consumer. Replaces the legacy platform-api
	//     annotation-patch path; each payload "<kind>/<name>" is
	//     resolved against the namespaced cache and a GenericEvent
	//     is pushed into the matching channel.
	//
	// Capacity 64 (resyncSourceChanCap) bounds per-Kind backpressure.
	envCh := make(chan event.GenericEvent, resyncSourceChanCap)
	promptCh := make(chan event.GenericEvent, resyncSourceChanCap)
	artifactCh := make(chan event.GenericEvent, resyncSourceChanCap)
	skillCh := make(chan event.GenericEvent, resyncSourceChanCap)
	smpCh := make(chan event.GenericEvent, resyncSourceChanCap)
	bipCh := make(chan event.GenericEvent, resyncSourceChanCap)
	llmCh := make(chan event.GenericEvent, resyncSourceChanCap)

	// Plugin + PluginMarketplace CRDs are NOT installed while
	// featuregate.PluginsEnabled is false, so their channels stay nil:
	// resync.sweepKind and refreshsignal.Listener both no-op on a nil
	// channel, which is what keeps the 5-minute sweep from Listing a Kind
	// the apiserver does not know ("no matches for kind \"PluginList\"").
	var pluginCh, mpCh chan event.GenericEvent
	if featuregate.PluginsEnabled {
		pluginCh = make(chan event.GenericEvent, resyncSourceChanCap)
		mpCh = make(chan event.GenericEvent, resyncSourceChanCap)
	}

	if err = (&achcontroller.LiteLLMConnectionReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		Cache:        connCache,
		Namespace:    watchNS,
		Log:          ctrl.Log.WithName("controller").WithName("LiteLLMConnection"),
		Pool:         dbPool,
		ResyncSource: llmCh,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller LiteLLMConnection: %w", err)
	}

	snapshotter := snapshot.NewSnapshotter(realLiteLLM, ctrl.Log.WithName("litellm-snapshot")).
		EnableCatalog(dbPool, watchNS, "default")
	if err := mgr.Add(snapshotter); err != nil {
		return fmt.Errorf("unable to add LiteLLM snapshot Runnable: %w", err)
	}

	orphanRunnable := orphan.NewRunnable(realLiteLLM, dbPool, auditLog, orphanInterval,
		orphanDryRun, orphanMaxRevoke, ctrl.Log.WithName("orphan-cleanup"))
	// Register the orphan-cleanup collectors on controller-runtime's
	// global metrics Registry so the operator /metrics surfaces them.
	orphanRunnable.Metrics = orphan.NewMetrics(crmetrics.Registry)
	if err := mgr.Add(orphanRunnable); err != nil {
		return fmt.Errorf("unable to add orphan-cleanup Runnable: %w", err)
	}

	// G7: register the ACH-domain operator collectors (ach_environment_available
	// + ach_operator_external_ref_refresh_total) on controller-runtime's metrics
	// Registry so the operator /metrics surfaces them.
	opMetrics := achmetrics.NewOperatorCollectors(crmetrics.Registry)

	if err = (&achcontroller.EnvironmentReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		LiteLLM:      realLiteLLM,
		Namespace:    watchNS,
		Log:          ctrl.Log.WithName("controller").WithName("Environment"),
		DB:           dbPool,
		Snapshotter:  snapshotter,
		ResyncSource: envCh,
		Metrics:      opMetrics,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller Environment: %w", err)
	}
	if featuregate.PluginsEnabled {
		if err = (&achcontroller.PluginReconciler{
			Client:           mgr.GetClient(),
			Scheme:           mgr.GetScheme(),
			Namespace:        watchNS,
			Log:              ctrl.Log.WithName("controller").WithName("Plugin"),
			CacheRoot:        cacheRoot,
			DB:               dbPool,
			PluginMaxSizeMiB: pluginMaxSizeMiB,
			Fetchers:         nil,
			ResyncSource:     pluginCh,
			Metrics:          opMetrics,
		}).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create controller Plugin: %w", err)
		}
		if err = (&achcontroller.PluginMarketplaceReconciler{
			Client:           mgr.GetClient(),
			Scheme:           mgr.GetScheme(),
			Namespace:        watchNS,
			Log:              ctrl.Log.WithName("controller").WithName("PluginMarketplace"),
			CacheRoot:        cacheRoot,
			DB:               dbPool,
			PluginMaxSizeMiB: pluginMaxSizeMiB,
			Fetchers:         nil,
			ResyncSource:     mpCh,
		}).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create controller PluginMarketplace: %w", err)
		}
	}
	if err = (&achcontroller.ArtifactReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		Namespace:    watchNS,
		Log:          ctrl.Log.WithName("controller").WithName("Artifact"),
		CacheRoot:    cacheRoot,
		DB:           dbPool,
		Fetchers:     nil,
		ResyncSource: artifactCh,
		Metrics:      opMetrics,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller Artifact: %w", err)
	}
	if err = (&achcontroller.PromptReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		Namespace:    watchNS,
		Log:          ctrl.Log.WithName("controller").WithName("Prompt"),
		CacheRoot:    cacheRoot,
		DB:           dbPool,
		Fetchers:     nil,
		ResyncSource: promptCh,
		Metrics:      opMetrics,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller Prompt: %w", err)
	}
	if err = (&achcontroller.SkillReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Namespace:       watchNS,
		Log:             ctrl.Log.WithName("controller").WithName("Skill"),
		CacheRoot:       cacheRoot,
		DB:              dbPool,
		SkillMaxSizeMiB: skillMaxSizeMiB,
		Fetchers:        nil,
		ResyncSource:    skillCh,
		Metrics:         opMetrics,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller Skill: %w", err)
	}
	if err = (&achcontroller.SkillMarketplaceReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Namespace:       watchNS,
		Log:             ctrl.Log.WithName("controller").WithName("SkillMarketplace"),
		CacheRoot:       cacheRoot,
		DB:              dbPool,
		SkillMaxSizeMiB: skillMaxSizeMiB,
		Fetchers:        nil,
		ResyncSource:    smpCh,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller SkillMarketplace: %w", err)
	}
	if err = (&achcontroller.BackendIdentityPolicyReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		Namespace:    watchNS,
		Log:          ctrl.Log.WithName("controller").WithName("BackendIdentityPolicy"),
		Pool:         dbPool,
		ResyncSource: bipCh,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller BackendIdentityPolicy: %w", err)
	}
	if err = (&achcontroller.ACHAgentReconciler{
		Client:            mgr.GetClient(),
		APIReader:         mgr.GetAPIReader(),
		Scheme:            mgr.GetScheme(),
		DB:                dbPool,
		PublicBaseURL:     config.EnvOr("ACH_PUBLIC_BASE_URL", config.EnvOr("ACH_BASE_URL", "")),
		DefaultAchBaseURL: config.EnvOr("ACH_BASE_URL", ""),
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller ACHAgent: %w", err)
	}
	// +kubebuilder:scaffold:builder

	// ─── Issue #34 A10: periodic full resync runnable. ───
	resyncRunnable := &resync.Resync{
		Client:    mgr.GetClient(),
		Namespace: watchNS,
		Log:       ctrl.Log.WithName("resync"),
		Channels: resync.Channels{
			Environment:       envCh,
			Plugin:            pluginCh,
			Prompt:            promptCh,
			Artifact:          artifactCh,
			Skill:             skillCh,
			Marketplace:       mpCh,
			SkillMarketplace:  smpCh,
			BIP:               bipCh,
			LiteLLMConnection: llmCh,
		},
	}
	if err := mgr.Add(resyncRunnable); err != nil {
		return fmt.Errorf("unable to add resync Runnable: %w", err)
	}

	// ─── Issue #34 A11: refreshsignal listener (LISTEN ach_refresh). ───
	//
	// Consumes ach_refresh NOTIFY events fired by the platform-api's
	// /admin/refresh handler (db.SetForceRefresh) and pushes a
	// GenericEvent for the named CR into the matching per-Kind
	// source.Channel. Replaces the pre-issue-34 annotation-patching
	// path on the platform-api side. Only the "external-ref" Kinds —
	// plugin, prompt, artifact, skill, pluginmarketplace, skillmarketplace —
	// receive signals; the listener silently drops payloads for any other kind.
	refreshListener := &refreshsignal.Listener{
		Pool:      dbPool,
		Namespace: watchNS,
		Log:       ctrl.Log.WithName("refresh-signal"),
		Client:    mgr.GetClient(),
		Channels:  refreshChannels(pluginCh, promptCh, artifactCh, skillCh, mpCh, smpCh),
	}
	if err := mgr.Add(refreshListener); err != nil {
		return fmt.Errorf("unable to add refresh-signal Runnable: %w", err)
	}

	if metricsCertWatcher != nil {
		operatorSetupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			return fmt.Errorf("unable to add metrics certificate watcher to manager: %w", err)
		}
	}

	if webhookCertWatcher != nil {
		operatorSetupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			return fmt.Errorf("unable to add webhook certificate watcher to manager: %w", err)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up ready check: %w", err)
	}

	operatorSetupLog.Info("starting manager", "watchNS", watchNS)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("problem running manager: %w", err)
	}
	return nil
}

// refreshChannels builds the refresh-signal kind→channel map, omitting the
// Plugin kinds when featuregate.PluginsEnabled is false. The Listener is
// nil-tolerant, so omitting them is belt-and-braces — it also keeps the
// V(1) "no channel wired" log honest instead of implying a wired-but-nil
// feed.
func refreshChannels(pluginCh, promptCh, artifactCh, skillCh, mpCh, smpCh chan event.GenericEvent) map[string]chan<- event.GenericEvent {
	m := map[string]chan<- event.GenericEvent{
		"prompt":           promptCh,
		"artifact":         artifactCh,
		"skill":            skillCh,
		"skillmarketplace": smpCh,
	}
	if featuregate.PluginsEnabled {
		m["plugin"] = pluginCh
		m["pluginmarketplace"] = mpCh
	}
	return m
}
