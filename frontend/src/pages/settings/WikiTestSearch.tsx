import { useState } from 'react';
import { Button } from '@/components/ui';
import { api } from '@/lib/api';
import type { WikiSyncStatus, WikiTestHit, WikiTestSearchResult } from '@/lib/types';
import { FlashBox } from './shared';
import { liveOutcomeText } from './wikiKnowledge';

// WikiTestSearch — v0.10.1124 "Aramayı test et" (yönetici). Bir sorgunun
// yerel isabetlerini, canlı Azure DevOps Search denemesini (sınıf, http
// durumu, api-version, sonuç sayısı) ve hangi tabanın neyi düşüreceğini
// gösterir. Oturumluk: sonuç kaydedilmez; sunucu sorgu METNİNİ audit'e yazmaz.
// İçerik yok — isabet başına ≤160 karakter kesit.

function HitList({ title, hits }: { title: string; hits: WikiTestHit[] }) {
  return (
    <div style={{ marginTop: 6 }}>
      <div style={{ fontWeight: 600 }}>{title} ({hits.length})</div>
      {hits.length === 0
        ? <div style={{ color: 'var(--text3)' }}>yok</div>
        : (
          <ol style={{ margin: '2px 0 0', paddingLeft: 18 }}>
            {hits.map((h, i) => (
              <li key={`${h.project}/${h.wiki}/${h.path}/${i}`} style={{ wordBreak: 'break-word' }}>
                <span className="mono">{h.score.toFixed(2)}</span>{' '}
                {h.title || h.path}
                <span style={{ color: 'var(--text3)' }}> · {h.project}/{h.wiki}{h.live ? ' · canlı' : ''}</span>
                {!h.passesWikiTier
                  ? <span style={{ color: 'var(--warn)' }}> · iki tabanın da altında</span>
                  : !h.passesRag ? <span style={{ color: 'var(--text3)' }}> · yalnız açık wiki sorusunda</span> : null}
                {h.snippet && <div style={{ color: 'var(--text3)' }}>{h.snippet}</div>}
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
    <div data-testid="wiki-test-search" style={{ marginTop: 10, fontSize: 12 }}>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <input type="text" value={q} maxLength={300} placeholder="ör. svc-orders nasıl yeniden başlatılır"
               aria-label="Test sorgusu" onChange={e => setQ(e.target.value)}
               onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); void run(); } }}
               style={{ width: 320 }} />
        <Button variant="secondary" size="sm" type="button" loading={busy} disabled={!q.trim()} onClick={() => { void run(); }}>
          Aramayı test et
        </Button>
      </div>
      {err && <FlashBox kind="err">{err}</FlashBox>}
      {res && (
        <div style={{ marginTop: 8, padding: '8px 10px', border: '1px solid var(--border)', borderRadius: 6 }}>
          <div style={{ fontWeight: 600 }}>{res.verdict}</div>
          <div style={{ color: 'var(--text3)', marginTop: 2 }}>
            Terimler: <span className="mono">{res.terms.join(' ')}</span>
            {res.liveQueries?.length ? <> · canlı sorgu: <span className="mono">{res.liveQueries.join('  |  ')}</span></> : null}
            {res.stale ? ' · yerel indeks boş/bayat' : ''}
          </div>
          <div style={{ marginTop: 2 }} data-testid="wiki-test-live">{liveOutcomeText(res)}</div>
          <HitList title="Yerel isabetler" hits={res.local} />
          <HitList title={`Son liste (RAG tabanı ${res.floors.rag}, açık wiki sorusu tabanı ${res.floors.wikiTier})`} hits={res.final} />
        </div>
      )}
    </div>
  );
}
