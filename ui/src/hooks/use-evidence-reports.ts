// use-evidence-reports.ts — TanStack Query hooks for the caller's own
// observability evidence under /platform/observability/* (pk-scoped, whole
// selected window). Shapes mirror ee/observability/evidence/*.go; a
// measurement no row carries is null, never 0.
import { useQuery } from '@tanstack/react-query';
import { getJson } from '@/lib/api';
import type { EvidenceProvenance, WorkloadRuntime } from '@/lib/api-types';
import { rangeDays } from '@/lib/stats-presets';
import { useConsoleDateRange } from '@/stores/console-range';

export type { ClassificationReport, EvidenceProvenance } from '@/lib/api-types';

export interface EvidenceReport {
  window: { start: string; end: string; timezone: string };
  provenance: EvidenceProvenance;
  generation_count: number | null;
  eligibility: 'observed_generations_not_logical_requests';
}
export interface ContextReport extends EvidenceReport {
  input_summary: { mean: number | null; median: number | null; max: number | null };
  token_ratio: { input_tokens: number | null; output_tokens: number | null; value: number | null; uncached_input_tokens?: number | null; uncached_value?: number | null; coverage: number };
  cache_read_share: { value: number | null; coverage: number };
  reasoning: { tokens: number | null; share: number | null; coverage: number };
  context_bands: Array<{ name: string; count: number }>;
  input_coverage: number;
  cache_read_tokens: number | null;
  cache_coverage: number;
  invocations: Array<{ name: string; count: number }>;
  tool_provenance: EvidenceProvenance;
  invocation_count?: number | null;
  skill_invocation_count?: number | null;
  subagent_invocation_count?: number | null;
  /** Top 20 skills and subagent types by name; null when that read failed. */
  skills?: Array<{ name: string; count: number }> | null;
  subagents?: Array<{ name: string; count: number }> | null;
  cache_creation_tokens?: number | null;
  cache_creation_coverage?: number;
  invocations_truncated?: boolean;
  execution_success: null;
}
export interface WorkloadReport extends EvidenceReport {
  runtimes: WorkloadRuntime[];
  runtime_parser_version: number;
  runtime_attribution: 'same_event' | 'same_trace';
  business_classification: null;
  task_outcomes: null;
}
export interface CostReport {
  window: { start: string; end: string; timezone: string };
  provenance: EvidenceProvenance;
  priced_generations: number;
  zero_cost_generations: number;
  spend: number | null;
  cost_per_model_request: number | null;
  sessions: number;
  cost_per_session: number | null;
  /** Share of spend that carries a session id. */
  session_spend_coverage: number | null;
  cache: {
    basis: string;
    with_cache: number | null;
    without_cache: number | null;
    saving: number | null;
    saving_share: number | null;
    priced_models: number;
    unpriced_models: string[];
    reason: string | null;
  };
}
export interface GenerationLatency {
  available: boolean;
  sampled: boolean;
  row_count: number | null;
  window: { start: string; end: string; days: number };
  latency: { p50_ms: number | null; p95_ms: number | null; p99_ms: number | null; avg_ms: number | null; ttft_p50_ms: number | null; ttft_p95_ms: number | null; tokens_per_sec_p50: number | null } | null;
  outcomes: Array<{ status: 'error' | 'unknown'; count: number }>;
  by_model: Array<{ model: string; requests: number; failed: number; p50_ms: number | null; p95_ms: number | null; ttft_p50_ms?: number | null; tokens_per_sec_p50: number | null }>;
  by_model_truncated: boolean;
  by_model_reason?: string | null;
  histogram: Array<{ label: string; lower_ms: number; upper_ms: number | null; count: number }>;
  /** null: no generation reported a finish reason (not "none truncated"). */
  finish?: { coverage: number; truncated: number; content_filtered: number } | null;
  /** null: unread (see guardrails_reason); [] means no guardrail ran. */
  guardrails?: Array<{ name: string; checked: number; intervened: number }> | null;
  guardrails_reason?: string | null;
  source_coverage?: { generation_count: number; timing: number; ttft: number; tokens_per_sec: number };
  eligibility: string;
  provenance: EvidenceProvenance;
  reason?: string | null;
}
export interface TrendMetric { value: number | null; unit: string; window: string; state: string; reason: string | null }
export type FailureOwner = 'you' | 'provider' | 'tool' | 'unattributed';
export interface FailureCauses { state: string; reason: string | null; causes: Array<{ owner: FailureOwner; class: string; status: string; value: number }> }
export type PerformanceReport = Record<'latency_p95' | 'failures' | 'mcp_calls' | 'requests', TrendMetric> & { queue_p95?: TrendMetric; failure_causes?: FailureCauses };

type ReportName = 'context' | 'workloads' | 'cost' | 'latency' | 'performance' | 'classification';

// ponytail: /platform/observability/* answers 400 above 31 days (obsapi
// maxRangeDays, bounded by ClickHouse memory), so wider ranges skip the request
// and show EVIDENCE_LIMIT_NOTICE; lift it with pre-aggregated rollups (backlog #17).
export const EVIDENCE_MAX_DAYS = 31;
export const EVIDENCE_LIMIT_NOTICE = 'This view covers up to 31 days for now. Choose a shorter range.';

/** The selected range, and whether it is too wide for the evidence endpoints. */
export function useEvidenceWindow() {
  const range = useConsoleDateRange();
  return { range, tooLong: rangeDays(range) > EVIDENCE_MAX_DAYS };
}

export function useEvidenceReport<T>(report: ReportName) {
  const { range, tooLong } = useEvidenceWindow();
  return useQuery({
    queryKey: ['observability', report, range.start, range.end],
    enabled: !tooLong,
    queryFn: async ({ signal }) => {
      const response = await getJson<T>(`/platform/observability/${report}?${new URLSearchParams(range)}`, { signal });
      if (response.status !== 200 || !response.data) throw new Error(`${report}-unavailable`);
      return response.data;
    },
  });
}

export function sourceUnavailable(provenance: EvidenceProvenance): boolean {
  return provenance.state === 'not_instrumented' || Boolean(provenance.reason && !['row_cap', 'group_cap'].includes(provenance.reason));
}
