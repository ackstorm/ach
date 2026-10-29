// edition.ts — whether this deployment runs the Enterprise features
// (Helm enterprise.enabled, bootstrap `enterprise`).
import { useSessionStore } from '@/stores/session';

export function useEnterprise(): boolean {
  return useSessionStore((s) => s.me?.enterprise === true);
}
