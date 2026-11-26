// AiThinkingPanel.tsx — v0.10.2458 (operator decision 2026-11-22: Qwen thinking
// mode configurable per surface). v0.10.2471 (operator 2026-11-26): default is
// ON for every surface except the intent / router classifier. Own GET/PUT
// (/api/settings/ai/thinking, admin + audit settings.ai.thinking). Sits next to
// the model profiles; "inherit" uses the profile's Thinking value. The request
// key (chat_template_kwargs.enable_thinking) is only sent to models that accept
// it (Qwen3 family or a profile flagged "accepts enable_thinking"); gemma and
// other models never get it. English copy (new admin section, operator brief).
import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { NativeSelect } from '@/components/shadcn/native-select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import type { AIThinkingPayload, AIThinkMode } from '@/lib/types';
import { FlashBox, SectionTitle, humanize } from './shared';
import { inheritSummary, thinkingStatsLine } from './aiThinking';

const CARD = 'mt-[18px] max-w-[880px] rounded-lg border border-(--border) bg-(--bg2) p-4';
const INTRO = 'mb-2.5 text-[length:var(--fs-sm)] text-(--text2)';

export function AiThinkingPanel() {
  const q = useQuery({ queryKey: ['settings', 'thinking-surfaces'], queryFn: () => api.aiThinking(), retry: false });
  const [data, setData] = useState<AIThinkingPayload | null>(null);
  const [modes, setModes] = useState<Record<string, AIThinkMode>>({});
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const apply = (p: AIThinkingPayload) => {
    setData(p);
    setModes(Object.fromEntries(p.surfaces.map(s => [s.id, s.mode])));
  };
  useEffect(() => { if (q.data) apply(q.data); }, [q.data]);
  useEffect(() => { if (q.error) setFlash({ kind: 'err', text: humanize(q.error) }); }, [q.error]);

  const dirty = !!data && data.surfaces.some(s => modes[s.id] !== s.mode);
  const save = async () => {
    if (!data) return;
    const changed = Object.fromEntries(data.surfaces.filter(s => modes[s.id] !== s.mode).map(s => [s.id, modes[s.id]]));
    setBusy(true); setFlash(null);
    try {
      apply(await api.putAIThinking(changed));
      setFlash({ kind: 'ok', text: 'Thinking settings saved.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className={CARD} data-thinking-surfaces-panel="">
      <SectionTitle>Thinking mode per surface</SectionTitle>
      <div className={INTRO}>
        Qwen3 thinking (<code>enable_thinking</code>) per call surface. Default is <b>on</b>, except the
        intent / router classifier (<b>off</b>: it emits one line of JSON before every turn, thinking only adds latency).
        A saved choice always wins over the default.
        <b> inherit</b> uses the model profile&apos;s Thinking setting. The key is sent only to models that accept it
        (Qwen3 family, or a profile marked &quot;accepts enable_thinking&quot;); gemma and other models ignore this setting.
        Thinking output is stripped from answers in every mode.
      </div>
      {data && data.profiles.length > 0 && (
        <div className="field-hint mb-2" data-inherit-summary="">Inherit today — {inheritSummary(data.profiles)}</div>
      )}
      {/* static table: fixed 8-row editable catalogue in server order (T1, not sortable) */}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Surface</TableHead>
            <TableHead>Mode</TableHead>
            <TableHead>Calls on this pod (thinking · count · avg latency)</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(data?.surfaces ?? []).map(s => (
            <TableRow key={s.id}>
              <TableCell className="whitespace-normal">
                <div className="cell-strong">{s.label}</div>
                <div className="field-hint">{s.hint}</div>
              </TableCell>
              <TableCell>
                <NativeSelect size="sm" className="bg-field dark:bg-field" aria-label={`${s.label} thinking mode`} value={modes[s.id] ?? 'off'} disabled={busy}
                  onChange={e => setModes(m => ({ ...m, [s.id]: e.target.value as AIThinkMode }))}>
                  <option value="off">off</option>
                  <option value="on">on</option>
                  <option value="inherit">inherit (profile)</option>
                </NativeSelect>
              </TableCell>
              <TableCell className="field-hint whitespace-normal">{thinkingStatsLine(data?.stats ?? [], s.id) || '—'}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <div className="mt-3">
        <ActionRow confirm={<Button onClick={save} loading={busy} disabled={!dirty}>Save</Button>} />
      </div>
      {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
    </div>
  );
}
