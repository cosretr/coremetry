// aiThinking.ts — v0.10.2458: AiThinkingPanel pure helpers (stats line, inherit summary).
import type { AIThinkingProfile, AIThinkingStat } from '@/lib/types';

/** One compact line per surface: "off 40 · 0.9 s avg | on 12 · 3.4 s avg (1 err)". */
export function thinkingStatsLine(stats: AIThinkingStat[], surface: string): string {
  const fmt = (ms: number) => (ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`);
  return stats
    .filter(s => s.surface === surface && s.calls > 0)
    .map(s => `${s.thinking} ${s.calls} · ${fmt(s.avgMs)} avg${s.errors ? ` (${s.errors} err)` : ''}`)
    .join(' | ');
}

/** What "inherit" means today, per profile (profile thinking + whether the key is sent). */
export function inheritSummary(profiles: AIThinkingProfile[]): string {
  return profiles
    .map(p => `${p.label || p.id}: ${p.switch ? (p.thinking ? `thinking=${p.thinking}` : 'model default') : 'no thinking key (ignored)'}`)
    .join(' · ');
}
