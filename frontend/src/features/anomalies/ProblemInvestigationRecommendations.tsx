// ProblemInvestigationRecommendations — v0.10.1293 — AI SRE Faz 4: yalnız öneri
// (runbook eşleme + geri alma metni). InvestigationResult çağırır.
//   - her kart "öneri — uygulanmadı" rozeti taşır: ajan HİÇBİR şeyi uygulamaz;
//   - metin + gerekçe; geri alma önerisinde rollout satırı (şimdiki → önceki
//     sürüm) ve okunduysa Argo CD durumu — yalnız bilgi, düğme değil;
//   - "Runbook'u aç": mevcut runbook sayfasına problemId önceden dolu gider
//     (/runbook?id=…&problemId=…); çalıştırmayı İNSAN başlatır (mevcut
//     runbook.execute audit'i). Dış bağlantı yeni sekmede açılır;
//   - editor: "Uygulandı" / "Ele" — POST …/recommendations/{rid}; yalnız
//     etiket (audit sre.recommendation.ack). Viewer kartları GÖRÜR, düğmeyi
//     görmez.
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Badge, Button } from '@/components/ui';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { tsLong } from '@/lib/utils';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2288 — metinler arayüz dilinde (TR aynı)
import type { SRERecommendation, SREInvestigation } from '@/lib/types';
import { OrigLang, MsgText } from './OrigLang';
import { canAckRecommendation, recHumanLabel, runbookTarget, shownRecommendations } from './recommendationView';

const muted = { color: 'var(--text3)' } as const;

export function ProblemInvestigationRecommendations({ inv, canEdit }: { inv: SREInvestigation; canEdit: boolean }) {
  const lang = useLang();
  const recs = shownRecommendations(inv);
  if (recs.length === 0) return null;
  return (
    <div>
      <div style={{ ...muted, fontSize: 12, marginBottom: 6 }}>
        {t('rec.head', lang)}
      </div>
      <div style={{ display: 'grid', gap: 8 }}>
        {recs.map(r => <RecommendationCard key={r.id} inv={inv} r={r} canEdit={canEdit} />)}
      </div>
    </div>
  );
}

function RecommendationCard({ inv, r, canEdit }: { inv: SREInvestigation; r: SRERecommendation; canEdit: boolean }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const lang = useLang();
  const [busy, setBusy] = useState<'' | 'ack' | 'dismissed'>('');
  const human = recHumanLabel(r, lang);
  const target = runbookTarget(r);
  const actionable = canEdit && canAckRecommendation(inv, r);

  const mark = async (state: 'ack' | 'dismissed') => {
    setBusy(state);
    try {
      await api.ackProblemRecommendation(inv.problemId, r.id, { state });
      toast.success(t(state === 'ack' ? 'rec.toast.ack' : 'rec.toast.dismissed', lang));
      await qc.invalidateQueries({ queryKey: keys.problems.investigation(inv.problemId) });
    } catch (e) {
      toast.error(tf('hyp.toast.markErr', lang, { e: e instanceof Error ? e.message : String(e) }));
    } finally {
      setBusy('');
    }
  };

  return (
    <div data-recommendation={r.id} style={{
      border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '8px 10px',
      opacity: r.state === 'dismissed' ? 0.7 : undefined,
    }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
        <Badge tone="neutral" title={t('rec.pending.title', lang)}>{t('rec.pending', lang)}</Badge>
        {human && <Badge tone={human.tone} title={r.ackAt ? tsLong(r.ackAt) : undefined}>{human.text}</Badge>}
      </div>
      {/* v0.10.2301 (S4) — EN metin anahtardan (eski kayıt: özgün + işaret); gerekçe özgün. */}
      <div style={{ marginTop: 4 }}><MsgText msg={r.textMsg} text={r.text} /></div>
      {r.rationale && <div style={{ ...muted, fontSize: 12, marginTop: 2 }}>{tf('rec.rationale', lang, { r: '' })}<OrigLang>{r.rationale}</OrigLang></div>}
      {r.rollout && (
        <div style={{ ...muted, fontSize: 12, marginTop: 2 }}>
          Rollout: <span className="mono">{r.rollout.workload}</span> {r.rollout.from} → {r.rollout.to}
          {r.argo ? <> · Argo CD <span className="mono">{r.argo.app}</span>: {r.argo.status}</> : null}
        </div>
      )}
      {!r.rollout && r.argo && (
        <div style={{ ...muted, fontSize: 12, marginTop: 2 }}>Argo CD <span className="mono">{r.argo.app}</span>: {r.argo.status}</div>
      )}
      {(target || actionable) && (
        <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap', alignItems: 'center' }}>
          {target?.kind === 'internal' && (
            <Button variant="secondary" size="sm" onClick={() => navigate(target.href)}
              title={t('rec.runbook.title', lang)}>
              {t('rec.runbook', lang)}
            </Button>
          )}
          {target?.kind === 'external' && (
            <a href={target.href} target="_blank" rel="noopener noreferrer" title={t('rec.runbook.ext.title', lang)}>
              {t('rec.runbook', lang)} ↗
            </a>
          )}
          {actionable && (
            <>
              <Button variant="ghost" size="sm" loading={busy === 'ack'} disabled={!!busy} onClick={() => { void mark('ack'); }}
                title={t('rec.ack.title', lang)}>
                {t('rec.ack', lang)}
              </Button>
              <Button variant="ghost" size="sm" loading={busy === 'dismissed'} disabled={!!busy} onClick={() => { void mark('dismissed'); }}>
                {t('hyp.dismiss', lang)}
              </Button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
