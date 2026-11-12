// LogsNLQuery — Log Search Faz 6 dilim 6.6 (audit §7 "Doğal dilden sorgu"; §8
// kabul: "Sorgu gösterilir; kullanıcı Ara'ya basmadan çalışmaz").
// Sorgu çubuğunun altında, açılıp kapanan TEK satır (yüzen / yapışkan değil):
// cümle → POST /api/copilot/logs-nl-query → üretilen KQL + seviye + aralık
// önerisi GÖSTERİLİR. "Sorgu kutusuna koy" yalnız TASLAĞI değiştirir; arama
// kullanıcı Search / Enter'a basınca gider. Uç sorgu çalıştırmaz (ES isteği
// yok); alan adları sayfanın zaten yüklediği listeden.
import { useEffect, useRef, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { Toggle } from '@/components/shadcn/toggle';
import { Spinner } from '@/components/Spinner';
import { IconSparkles } from '@/components/icons';
import { useCopilotEnabled } from '@/components/ai/useCopilotEnabled';
import { api } from '@/lib/api';
import { nlSeverityLabel } from '@/lib/logsNLQuery';
import type { LogsNLQueryResponse } from '@/lib/types';

export function LogsNLToggle({ open, onOpenChange }: { open: boolean; onOpenChange(v: boolean): void }) {
  const enabled = useCopilotEnabled();
  if (enabled !== true) return null;
  return (
    <Toggle variant="outline" size="sm" pressed={open} onPressedChange={onOpenChange} className="px-3"
      data-testid="logs-nl-toggle" title="Ne aradığını cümleyle yaz; CoSRE KQL sorgusu önerir. Sorgu sen Search’e basmadan çalışmaz.">
      <IconSparkles /> Doğal dille
    </Toggle>
  );
}

export interface LogsNLQueryRowProps {
  fields: readonly string[];
  /** Önerilen sorguyu (ve seviye tabanını) TASLAĞA koyar; çalıştırmaz. */
  onUse(query: string, severityMin: number): void;
  onClose(): void;
}

type St = { status: 'idle' } | { status: 'loading' } | { status: 'error'; message: string } | { status: 'done'; res: LogsNLQueryResponse };

export function LogsNLQueryRow({ fields, onUse, onClose }: LogsNLQueryRowProps) {
  const [text, setText] = useState('');
  const [st, setSt] = useState<St>({ status: 'idle' });
  const [used, setUsed] = useState(false);
  const ctl = useRef<AbortController | null>(null);
  useEffect(() => () => ctl.current?.abort(), []);
  const run = () => {
    const prompt = text.trim();
    if (!prompt) return;
    ctl.current?.abort();
    const ac = new AbortController();
    ctl.current = ac;
    setSt({ status: 'loading' });
    setUsed(false);
    api.logsNLQuery(prompt, fields.slice(0, 80), ac.signal)
      .then(res => { if (!ac.signal.aborted) setSt({ status: 'done', res }); })
      .catch((e: unknown) => {
        if (!ac.signal.aborted) setSt({ status: 'error', message: e instanceof Error ? e.message : 'CoSRE cevap veremedi.' });
      });
  };
  const res = st.status === 'done' ? st.res : null;
  const sev = res ? nlSeverityLabel(res.severityMin) : '';
  return (
    <section aria-label="Doğal dille sorgu" className="grid gap-2 rounded-md border bg-(--bg2) px-3 py-2.5" data-testid="logs-nl-row">
      <div className="flex flex-wrap items-center gap-2">
        <Input className="h-8 min-w-[18rem] flex-1" value={text} onChange={e => setText(e.target.value)} maxLength={1024}
          aria-label="Ne arıyorsun?" placeholder="Ne arıyorsun? ör. son 1 saatte orders-api zaman aşımı hataları"
          onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); run(); } }} />
        <Button variant="secondary" size="sm" onClick={run} disabled={!text.trim() || st.status === 'loading'}>Sorgu öner</Button>
        <Button variant="ghost" size="sm" onClick={onClose}>Kapat</Button>
      </div>
      {st.status === 'loading' && <Spinner label="CoSRE sorguyu yazıyor…" />}
      {st.status === 'error' && <p className="m-0 text-[length:var(--fs-xs)] text-status-critical" role="alert">{st.message}</p>}
      {res && (
        <div className="grid gap-1.5" data-testid="logs-nl-result">
          {res.query && <code className="block rounded-sm border bg-(--bg1) px-2 py-1 font-mono text-[length:var(--fs-xs)] break-all">{res.query}</code>}
          <p className="m-0 text-[length:var(--fs-xs)] text-(--text2)">
            {[res.explain, sev && `seviye ≥ ${sev}`, res.range && `önerilen aralık: son ${res.range} (zaman seçiciden değiştir)`].filter(Boolean).join(' · ')}
          </p>
          {!res.valid && <p className="m-0 text-[length:var(--fs-xs)] text-status-warn" role="status">{res.error || 'Sorgu kullanılamaz.'}</p>}
          {res.valid && (
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => { onUse(res.query, res.severityMin); setUsed(true); }}>Sorgu kutusuna koy</Button>
              <span className="text-[length:var(--fs-xs)] text-(--text3)" role="status">
                {used ? 'Kutuya kondu — çalıştırmak için Search’e bas.' : 'Sorgu çalıştırılmadı.'}
              </span>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
