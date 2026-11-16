// EvidenceCard.tsx — v0.10.558 (CoSRE Faz 4c-2; operatör mockup onayı 2026-09-08).
// Guided kök-neden rotasının yapısal kanıt bloğu: verdict, RED şimdi/taban/Δ,
// penceredeki değişiklikler, log desenleri, açık problemler. Balonun altında
// chart bloğuyla aynı yerde; sabit ≤10 satırlı ham tablolar (sıralanmaz).
// Hipotez yoksa üst satır soluk, kart yine çizilir. Tek commit, kolay geri alınır.
import { Link } from 'react-router-dom';
import { serviceHref } from '@/lib/serviceHref';
import { fmtNum } from '@/lib/utils';
import { evidenceDeltaClass, evidenceHasHypothesis, fmtEvidenceTime, fmtRangeTR, redRows } from '@/lib/chatEvidence';
import type { ChatEvidence } from '@/lib/types';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2292 — kart metni arayüz dilinde (sunucu metni aynen)
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget'; // v0.10.1125 — /cosre yeni sekme

export function EvidenceCard({ ev }: { ev: ChatEvidence }) {
  const lang = useLang();
  const rows = redRows(ev.red, lang);
  const hasHyp = evidenceHasHypothesis(ev);
  const suspect = ev.problems.find(p => p.topSuspect)?.topSuspect;
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  return (
    <section className="ev-card" aria-label={`${t('mlRecord.card.evidence', lang)} · ${ev.service}`}>
      <div className="ev-head">{t('mlRecord.card.evidence', lang)} · <span className="mono">{ev.service}</span> · {fmtRangeTR(ev.rangeS, lang)}</div>
      <div className={hasHyp ? 'ev-verdict' : 'ev-verdict ev-verdict--none'}>
        <span aria-hidden>{hasHyp ? '●' : '○'}</span>{' '}
        {ev.verdict}
        {suspect && <> · <Link to={serviceHref(suspect)} className="ev-link" {...linkProps}>{suspect} ↗</Link></>}
      </div>
      {rows.length > 0 && (
        <table className="ev-table" aria-label={t('chatEv.tableAria', lang)}>
          <thead><tr><th>RED</th><th className="num">{t('chatEv.now', lang)}</th><th className="num">{t('chatEv.base', lang)}</th><th>Δ</th></tr></thead>
          <tbody>
            {rows.map(r => (
              <tr key={r.key}>
                <td>{r.label}</td>
                {/* v0.10.973 — tablo standardı T4: sayı arayüz fontunda (`td.num.mono` zaten inherit basıyordu; görünüm aynı). */}
                <td className="num">{r.now}</td>
                <td className="num">{r.base}</td>
                {/* v0.10.929 (K5) — ton satırın yön bayrağından: istek/sn artışı kırmızı değil. */}
                <td className={evidenceDeltaClass(r)}>{r.delta.text}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="ev-sub">{t('chatEv.changes', lang)}</div>
      {ev.changes.length === 0 ? <div className="ev-empty">{t('chatEv.noChanges', lang)}</div> : (
        <ul className="ev-list">
          {ev.changes.map((c, i) => (
            <li key={i}>
              <span className="mono">{fmtEvidenceTime(c.timeUnixNs)}</span>{' '}
              <span className="badge b-gray">{c.source}</span>{' '}
              {c.workload || c.service}{c.version ? ` → ${c.version}` : ''}{c.status ? ` · ${c.status}` : ''}{c.namespace ? ` · ns ${c.namespace}` : ''}
            </li>
          ))}
        </ul>
      )}
      <div className="ev-sub">{t('chatEv.patterns', lang)}</div>
      {ev.logPatterns.length === 0 ? <div className="ev-empty">{t('chatEv.noPatterns', lang)}</div> : (
        <ul className="ev-list">
          {ev.logPatterns.map((p, i) => (
            <li key={i}>
              <span className="mono">{p.pattern}</span> ×{fmtNum(p.currentCount)}{' '}
              <span className={p.kind === 'new' ? 'badge b-warn' : 'badge b-err'}>{p.kind}</span>
              {p.baselineCount > 0 ? tf('chatEv.patBase', lang, { n: fmtNum(p.baselineCount), r: p.ratio.toFixed(1) }) : ''}
            </li>
          ))}
        </ul>
      )}
      {ev.problems.length > 0 && (
        <div className="ev-foot">
          {t('chatEv.open', lang)} {ev.problems.length}
          {ev.problems.slice(0, 3).map(p => (
            <Link key={p.id} to={`/problems?problem=${encodeURIComponent(p.id)}`} className="ev-link" title={p.ruleName} {...linkProps}>
              [{p.ruleName || p.id}]
            </Link>
          ))}
        </div>
      )}
    </section>
  );
}
