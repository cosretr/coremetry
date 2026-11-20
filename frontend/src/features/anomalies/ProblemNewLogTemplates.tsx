import { Link } from 'react-router';
import { useProblemLogTemplates } from '@/lib/queries';
import { tsLong } from '@/lib/utils';
import type { Problem, ProblemLogTemplateEvidence } from '@/lib/types';
import { Sect } from './detailSections';
import { isProblemLive } from './alertMetricSeries';
import { newTemplateLogsHref, startOffsetLabel, startOffsetTitle } from './problemLogTemplates';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2286 — metinler arayüz dilinde (TR aynı)

// ProblemNewLogTemplates — v0.10.1113 (operatör onaylı kuyruk maddesi "Log
// şablonu kök neden bağlantısı"). "Başlangıçta doğan log şablonları": problem
// başlangıcından 10 dk önce → 5 dk sonra arasında, özne servisinde ya da RCA
// şüphelilerinde DOĞAN ve gerçekten yeni bir biçim ailesine ait Drain
// şablonları — "bu başladığı anda yeni bir hata mesajı belirdi" ipucu.
// `log_template_new` dedektörü kapalı (v0.10.1061); defter burada alarm değil
// kanıt olarak okunur, seçim sunucuda dedektörün aile süzgeciyle.
//
// Boşken / yüklenirken / ilk okuma hatasında HİÇ çizilmez (boş kart yok):
// bölüm tamamlayıcı bir ipucu — "doğan şablon yok" bir bulgu değil, defter
// örneklemeli. Sonraki bir tazeleme düşerse eldeki satırlar kalır.
// Yalnız detay açıkken tek istek; yoklama yalnız açık Problem'de pencere
// dolarken (useProblemLogTemplates). ≤5 satır, sabit sıra (başlangıca
// yakınlık) — tablo değil, liste (tablo standardı: sıralanmayan küçük liste).
export function ProblemNewLogTemplates({ problem, window: win }: {
  problem: Problem;
  /** Problem penceresi (probWindow) — pivotun bitişi. */
  window: { fromNs: number; toNs: number };
}) {
  const lang = useLang();
  const q = useProblemLogTemplates(problem.id, { startedAt: problem.startedAt, open: isProblemLive(problem) });
  // Veriden çizilir, hata bayrağından DEĞİL: yoklamadaki başarısız bir arka
  // plan tazelemesi eldeki satırları silmesin. Hiç veri yoksa (ilk okuma
  // düştü / boş) bölüm yok.
  const rows = q.data?.templates ?? [];
  if (rows.length === 0) return null;
  const services = q.data?.services ?? [];
  return (
    <Sect title={t('pnt.title', lang)}
      sub={<span title={t('pnt.sub.title', lang)}>
        {services.length > 0 ? `${services.join(', ')} · ` : ''}{t('pnt.sub', lang)}
      </span>}>
      <ul className="pd-newtpl">
        {rows.map(r => <NewTemplateRow key={r.templateId} row={r} win={win} />)}
      </ul>
    </Sect>
  );
}

function NewTemplateRow({ row: r, win }: { row: ProblemLogTemplateEvidence; win: { fromNs: number; toNs: number } }) {
  const lang = useLang();
  const full = r.sample ? `${r.template}\n\n${t('pnt.sample', lang)}\n${r.sample}` : r.template;
  return (
    <li className="pd-newtpl-row" data-template-id={r.templateId}>
      <div className="pd-newtpl-tpl" title={full}>{r.template}</div>
      <div className="pd-newtpl-meta">
        <span className="pd-newtpl-svc" title={t('pnt.svc.title', lang)}>{r.service}</span>
        <span className="pd-newtpl-off" title={`${startOffsetTitle(r.offsetSec, lang)} · ${tsLong(r.firstSeen)}`}>
          {startOffsetLabel(r.offsetSec, lang)}
        </span>
        <span title={t('pnt.count.title', lang)}>
          {tf('pnt.count', lang, { n: r.totalCount.toLocaleString() })}
        </span>
        <Link to={newTemplateLogsHref(r, win)}
          title={r.query ? tf('pnt.open.q', lang, { q: r.query }) : t('pnt.open.title', lang)}>
          {t('pnt.open', lang)}
        </Link>
      </div>
    </li>
  );
}
