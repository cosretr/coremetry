// ProblemInvestigationPanel — v0.10.1282 (AI SRE Faz 0a). Problem detayında
// AI SRE ajanının araştırma kaydı: kuyruk sırası, "Araştırılıyor…", sonuç
// (servis taraması hükmü + bulgular + yaş) ve editor için "Araştır / Yeniden
// araştır". Ajan YALNIZ öneri üretir; burada hiçbir eylem bir şeyi değiştirmez.
// v0.10.1288 (Faz 2) — sonuç gövdesinde hipotez kartları + zaman çizelgesi.
//
// Veri: GET /api/problems/{id}/investigation (10 s cache); yoklama yalnız iş
// kuyrukta / koşarken (10 s, gizli sekmede durur); değişim SSE
// 'sre.investigation' ile. Kuyruk durumu (GET /api/sre/status) yalnız kayıt
// kuyruktayken okunur: rol yoksa banner "SRE rolü yok — kuyrukta N iş
// bekliyor". Viewer durumu GÖRÜR, düğmeyi görmez. Bulgular ≤8 satır, sabit
// sıra (taramanın sıralaması) → liste, tablo değil (tablo standardı).
import { useState } from 'react';
import { Link } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { Badge, Button } from '@/components/ui';
import type { Tone } from '@/components/ui';
import { Spinner } from '@/components/Spinner';
import { keys, useProblemInvestigation, useSREStatus } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { tsLong } from '@/lib/utils';
import { agoLang, t, tf, useLang, type Lang } from '@/lib/i18n'; // v0.10.2285 — metinler arayüz dilinde (TR aynı)
import type { ProblemInvestigationResponse, SREInvestigation, SREStatus } from '@/lib/types';
import { Sect } from './detailSections';
// v0.10.1287 — Faz 1b sonuç gövdesi (adım tablosu, bulgular + kanıt, AI özeti, Sohbette sor).
import { InvestigationResult } from './InvestigationResult';
import { queueReasonText } from './hypothesisView';
import { SRE_SETTINGS_PATH, sreRoleMissing } from './sreInvestigationView';

// v0.10.2285 — etiketler katalogda (inv.state.<durum>); ton burada.
const STATE_TONE: Record<string, Tone> = {
  none: 'neutral', queued: 'info', running: 'accent', done: 'success',
  partial: 'warning', shared: 'neutral', error: 'danger', skipped: 'neutral',
};
const stateView = (s: string, lang: Lang) => (s in STATE_TONE
  ? { label: t(`inv.state.${s}`, lang), tone: STATE_TONE[s] }
  : { label: t('inv.state.none', lang), tone: STATE_TONE.none });

export function ProblemInvestigationPanel({ problemId }: { problemId: string }) {
  const q = useProblemInvestigation(problemId);
  const data = q.data;
  const status = useSREStatus(data?.state === 'queued');
  const { user } = useAuth();
  const canEdit = user?.role === 'admin' || user?.role === 'editor';
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const lang = useLang();

  if (q.isLoading) {
    return <Sect title={t('inv.title', lang)}><Spinner label={t('inv.loading', lang)} /></Sect>;
  }
  if (!data) {
    // İlk okuma düştü: boş kart yerine dürüst tek satır.
    return (
      <Sect title={t('inv.title', lang)}>
        <div style={{ fontSize: 13, color: 'var(--text3)' }}>{t('inv.readErr', lang)}</div>
      </Sect>
    );
  }
  const st = stateView(data.state, lang);
  const inv = data.investigation;

  const enqueue = async () => {
    setBusy(true);
    try {
      const r = await api.enqueueProblemInvestigation(problemId);
      if (r.created) toast.success(t('inv.toast.queued', lang));
      else toast.info(r.reason ? tf('inv.toast.already', lang, { r: queueReasonText(r.state, r.reason, lang) }) : t('inv.toast.alreadyQueued', lang));
      await qc.invalidateQueries({ queryKey: keys.problems.investigation(problemId) });
      await qc.invalidateQueries({ queryKey: keys.sre.all });
    } catch (e) {
      toast.error(tf('inv.toast.err', lang, { e: e instanceof Error ? e.message : String(e) }));
    } finally {
      setBusy(false);
    }
  };
  const finished = data.state === 'done' || data.state === 'partial' || data.state === 'error' || data.state === 'shared' || data.state === 'skipped';
  const showButton = canEdit && data.enabled && (data.state === 'none' || finished);

  return (
    <Sect title={t('inv.title', lang)}
      sub={<span><Badge tone={st.tone}>{st.label}</Badge>{inv?.finishedAt ? <span title={tsLong(inv.finishedAt)}> · {agoLang(inv.finishedAt, lang)}</span> : null}</span>}>
      {!data.enabled && (
        <div className="cm-callout" role="status">
          {t('inv.off', lang)}{' '}
          {user?.role === 'admin'
            ? <Link to={SRE_SETTINGS_PATH}>{t('inv.off.link', lang)}</Link>
            : t('inv.off.ask', lang)}
        </div>
      )}
      <StateBody data={data} status={status.data} canEdit={canEdit} />
      {showButton && (
        <div style={{ marginTop: 10 }}>
          <Button variant="secondary" size="sm" onClick={enqueue} loading={busy}>
            {t(data.state === 'none' ? 'inv.btn.run' : 'inv.btn.rerun', lang)}
          </Button>
        </div>
      )}
    </Sect>
  );
}

function StateBody({ data, status, canEdit }: { data: ProblemInvestigationResponse; status?: SREStatus; canEdit: boolean }) {
  const inv = data.investigation;
  const lang = useLang();
  switch (data.state) {
    case 'none':
      return <div style={{ fontSize: 13, color: 'var(--text3)' }}>{t('inv.none', lang)}</div>;
    case 'queued':
      return (
        <>
          <div style={{ fontSize: 13 }}>
            {t('inv.queued', lang)}{data.position ? tf('inv.queued.pos', lang, { n: data.position }) : ''}
            {inv?.enqueuedAt ? <span style={{ color: 'var(--text3)' }} title={tsLong(inv.enqueuedAt)}> · {tf('inv.queued.at', lang, { ago: agoLang(inv.enqueuedAt, lang) })}</span> : null}
          </div>
          {sreRoleMissing(status) && (
            <div className="cm-callout is-warning" role="alert" style={{ marginTop: 8 }}>
              {tf('inv.noRole', lang, { n: status!.queueDepth })}
            </div>
          )}
        </>
      );
    case 'running':
      return <Spinner label={t('inv.running', lang)} hint={inv?.claimedBy ? `pod ${inv.claimedBy}` : undefined} />;
    case 'shared':
      return (
        <div style={{ fontSize: 13 }}>
          {tf('inv.shared', lang, { svc: inv?.service ?? '' })}{' '}
          {inv?.sharedFrom && <Link to={`/problems?problem=${encodeURIComponent(inv.sharedFrom)}`}>{t('inv.shared.open', lang)}</Link>}
        </div>
      );
    default:
      return inv ? <Result inv={inv} canEdit={canEdit} /> : null;
  }
}

function Result({ inv, canEdit }: { inv: SREInvestigation; canEdit: boolean }) {
  // v0.10.1287 — gövde InvestigationResult'ta (0a tek-adım kaydı da orada çizilir).
  // v0.10.1288 — editor hipotez kartlarında Doğrula / Ele / Onayla görür.
  return <InvestigationResult inv={inv} canEdit={canEdit} />;
}
