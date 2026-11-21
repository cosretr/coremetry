// InvestigationResult — v0.10.1287 (AI SRE Faz 1b). Problem detayındaki AI SRE
// araştırmasının SONUÇ gövdesi (ProblemInvestigationPanel çağırır):
//   - adım tablosu: tarama · deploy · bağımlılık · trace pivotu · log artışı ·
//     AI özeti — durum, süre, özet;
//   - AI özeti (ExplainBody) ya da kapı kapalıyken dürüst sabit cümle;
//     (v0.10.1393: özetin altındaki oy rayı kaldırıldı — oy zayıf sinyal);
//   - v0.10.1288: hipotez kartları + zaman çizelgesi + doğrula/ele
//     (ProblemInvestigationCards; sentezleyici kopyası orada AYRI motor kutusu);
//   - bulgular + her bulgunun kanıt çipleri (EvidenceChips — sorgu künyesi +
//     "Explore'da aç");
//   - v0.10.1293: öneri kartları (ProblemInvestigationRecommendations — her
//     kart "öneri — uygulanmadı"; "Runbook'u aç" runbook sayfasına problemId
//     ile gider, çalıştırmayı insan başlatır; Uygulandı / Ele yalnız etiket);
//   - "Sohbette sor": CoSRE'yi problem kapsamıyla açar (coremetry:ai-ask).
// 0a kaydı (tek `sweep` adımı) bugünkü tek satır özetle çizilir.
//
// Ajan YALNIZ öneri üretir; burada hiçbir düğme sistemde bir şeyi
// değiştirmez (Doğrula yeniden okur, Ele/Onayla yalnız kartı etiketler).
import { ExplainBody } from '@/components/ai/ExplainBody';
import { EvidenceChips } from '@/components/ai/EvidenceChips';
import { Badge, Button } from '@/components/ui';
import { tsLong } from '@/lib/utils';
import { agoLang, t, tf, useLang } from '@/lib/i18n'; // v0.10.2285 — metinler arayüz dilinde (TR aynı)
import type { SREInvestigation } from '@/lib/types';
import { OrigLang, MsgText } from './OrigLang'; // v0.10.2301 — MsgText: anahtarlı sunucu metni (S4)
import { InvestigationGateBanner, InvestigationQuality } from './InvestigationQuality';
import { ProblemInvestigationCards } from './ProblemInvestigationCards';
import { ProblemInvestigationRecommendations } from './ProblemInvestigationRecommendations';
import {
  stepLabel, askInChatQuestion, findingEvidence, findingKindLabel, fmtStepDuration, orderedSteps, stepStateView,
} from './investigationResultView';
import { AiGeneratedMark } from '@/components/ai/AiGeneratedMark'; // wave 3 — AI içeriği etiketi

export function InvestigationResult({ inv, canEdit = false }: { inv: SREInvestigation; canEdit?: boolean }) {
  const lang = useLang();
  const sweep = inv.steps.find(s => s.name === 'sweep');
  const multi = inv.steps.length > 1;
  const steps = orderedSteps(inv.steps);
  const finished = inv.state === 'done' || inv.state === 'partial';
  const askInChat = () =>
    window.dispatchEvent(new CustomEvent('coremetry:ai-ask', { detail: { question: askInChatQuestion(inv) } }));

  return (
    <div style={{ display: 'grid', gap: 10, fontSize: 13 }}>
      {inv.error && <div className="cm-callout is-caution" role="alert">{inv.error}</div>}
      {!multi && sweep?.summary && (
        <div><b>{tf('inv.res.sweep', lang, { svc: inv.service })}</b> <MsgText msg={sweep.summaryMsg} text={sweep.summary} /></div>
      )}
      {!multi && sweep && sweep.state !== 'ok' && !inv.error && (
        <div style={{ color: 'var(--text3)' }}>{t(sweep.state === 'partial' ? 'inv.res.sweepPartial' : 'inv.res.sweepFailed', lang)}</div>
      )}
      {multi && (
        // statik tablo (T1): ≤8 sabit satır (v0.10.1288: + olaylar, node), sıra
        // boru hattının (tarama → … → AI özeti), sıralanmaz; yalnız sapan durum rozet alır.
        <table className="dt">
          <colgroup><col style={{ width: 130 }} /><col style={{ width: 92 }} /><col style={{ width: 64 }} /><col /></colgroup>
          <thead>
            <tr>
              <th style={{ textAlign: 'left' }}>{t('inv.res.step', lang)}</th>
              <th style={{ textAlign: 'left' }}>{t('inv.res.status', lang)}</th>
              <th className="num">{t('inv.res.duration', lang)}</th>
              <th style={{ textAlign: 'left' }}>{t('inv.res.summary', lang)}</th>
            </tr>
          </thead>
          <tbody>
            {steps.map(s => {
              const v = stepStateView(s.state, lang);
              return (
                <tr key={s.name}>
                  <td>{stepLabel(s.name, lang)}</td>
                  <td>{v.tone ? <Badge tone={v.tone}>{v.label}</Badge> : v.label}</td>
                  <td className="num">{fmtStepDuration(s.durationMs, lang)}</td>
                  <td className="cell-wrap">{s.summary ? <MsgText msg={s.summaryMsg} text={s.summary} /> : '—'}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      <InvestigationGateBanner inv={inv} />
      {inv.summary && (inv.summary.gated
        ? (
          <div style={{ color: 'var(--text3)' }}>
            <MsgText msg={inv.summary.gateMsg} text={`${inv.summary.text}${inv.summary.reason ? ` — ${inv.summary.reason}` : ''}`} />
          </div>
        )
        : (
          <div>
            <div style={{ color: 'var(--text3)', fontSize: 12, marginBottom: 4 }}>
              <AiGeneratedMark style={{ marginRight: 6 }} />{t('inv.res.aiSummary', lang)}{inv.summary.model ? ` · ${inv.summary.model}` : ''}
              {inv.summary.at ? <span title={tsLong(inv.summary.at)}> · {agoLang(inv.summary.at, lang)}</span> : null}
            </div>
            <OrigLang block><ExplainBody text={inv.summary.text} busy={false} verdict={false} /></OrigLang>
          </div>
        ))}
      <InvestigationQuality inv={inv} canEdit={canEdit} />
      <ProblemInvestigationCards inv={inv} canEdit={canEdit} />
      <ProblemInvestigationRecommendations inv={inv} canEdit={canEdit} />
      {inv.findings.length > 0 && (
        <ul style={{ margin: 0, paddingLeft: 18, display: 'grid', gap: 6 }}>
          {inv.findings.map(f => (
            <li key={`${f.step ?? 'sweep'}:${f.kind}:${f.name}`}>
              <span className="mono">{f.name}</span>{' '}
              <span style={{ color: 'var(--text3)' }}>({findingKindLabel(f.kind, lang)} · {f.level})</span>
              {f.reasons && f.reasons.length > 0 && <> — <OrigLang>{f.reasons.join('; ')}</OrigLang></>}
              <EvidenceChips items={findingEvidence(inv, f)} />
            </li>
          ))}
        </ul>
      )}
      <div style={{ color: 'var(--text3)', fontSize: 12 }}>
        {t('inv.res.advisory', lang)}
        {inv.kind === 'manual' && inv.requestBy ? tf('inv.res.requestBy', lang, { who: inv.requestBy }) : ''}
      </div>
      {finished && (
        <div>
          <Button variant="secondary" size="sm" onClick={askInChat}>{t('inv.res.ask', lang)}</Button>
        </div>
      )}
    </div>
  );
}
