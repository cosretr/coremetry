import { useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';
import type { WikiSyncStatus, WikiTestHit, WikiTestSearchResult } from '@/lib/types';
import { FlashBox } from './shared';
import { liveOutcomeText } from './wikiKnowledge';

// WikiTestSearch — v0.10.1124 "Aramayı test et" (yönetici). Bir sorgunun
// yerel isabetlerini, canlı Azure DevOps Search denemesini (sınıf, http
// durumu, api-version, sonuç sayısı) ve hangi tabanın neyi düşüreceğini
// gösterir. Oturumluk: sonuç kaydedilmez; sunucu sorgu METNİNİ audit'e yazmaz.
// İçerik yok — isabet başına ≤160 karakter kesit.

// Faz 2 dilim 1 R12 — inline stil → Tailwind utility (D11 tip ölçeği).
const MUTED = 'text-(--text3)';

function HitList({ title, hits }: { title: string; hits: WikiTestHit[] }) {
  return (
    <div className="mt-1.5">
      <div className="font-semibold">{title} ({hits.length})</div>
      {hits.length === 0
        ? <div className={MUTED}>yok</div>
        : (
          <ol className="mt-0.5 mb-0 pl-[18px]">
            {hits.map((h, i) => (
              <li key={`${h.project}/${h.wiki}/${h.path}/${i}`} className="[word-break:break-word]">
                <span className="mono">{h.score.toFixed(2)}</span>{' '}
                {h.title || h.path}
                <span className={MUTED}> · {h.project}/{h.wiki}{h.live ? ' · canlı' : ''}</span>
                {!h.passesWikiTier
                  ? <span className="text-(--warn)"> · iki tabanın da altında</span>
                  : !h.passesRag ? <span className={MUTED}> · yalnız açık wiki sorusunda</span> : null}
                {h.snippet && <div className={MUTED}>{h.snippet}</div>}
              </li>
            ))}
          </ol>
        )}
    </div>
  );
}

/** onStatus — v0.10.1126: test sonrası tazelenmiş paylaşılan durum (kart "henüz denenmedi"de kalmasın). */
export function WikiTestSearch({ onStatus }: { onStatus?: (st: WikiSyncStatus) => void } = {}) {
  const [q, setQ] = useState('');
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<WikiTestSearchResult | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const run = async () => {
    if (!q.trim()) return;
    setBusy(true); setErr(null);
    try {
      const r = await api.testWikiSearch(q.trim());
      setRes(r);
      if (r.status) onStatus?.(r.status);
    } catch (e) {
      setRes(null);
      setErr(e instanceof Error ? e.message : String(e));
    } finally { setBusy(false); }
  };

  return (
    <div data-testid="wiki-test-search" className="mt-2.5 text-[length:var(--fs-sm)]">
      <div className="flex flex-wrap items-center gap-2">
        <Input type="text" value={q} maxLength={300} placeholder="ör. svc-orders nasıl yeniden başlatılır"
               aria-label="Test sorgusu" onChange={e => setQ(e.target.value)}
               onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); void run(); } }}
               className="h-8 w-[320px] max-w-full bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field" />
        <Button variant="outline" size="sm" loading={busy} disabled={!q.trim()} onClick={() => { void run(); }}>
          Aramayı test et
        </Button>
      </div>
      {err && <FlashBox kind="err">{err}</FlashBox>}
      {res && (
        <div className="mt-2 rounded-md border border-(--border) px-2.5 py-2">
          <div className="font-semibold">{res.verdict}</div>
          <div className={cn('mt-0.5', MUTED)}>
            Terimler: <span className="mono">{res.terms.join(' ')}</span>
            {res.liveQueries?.length ? <> · canlı sorgu: <span className="mono">{res.liveQueries.join('  |  ')}</span></> : null}
            {res.stale ? ' · yerel indeks boş/bayat' : ''}
          </div>
          <div className="mt-0.5" data-testid="wiki-test-live">{liveOutcomeText(res)}</div>
          <HitList title="Yerel isabetler" hits={res.local} />
          <HitList title={`Son liste (RAG tabanı ${res.floors.rag}, açık wiki sorusu tabanı ${res.floors.wikiTier})`} hits={res.final} />
        </div>
      )}
    </div>
  );
}
