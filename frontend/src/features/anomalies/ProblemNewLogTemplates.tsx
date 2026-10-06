import { Link } from 'react-router-dom';
import { useProblemLogTemplates } from '@/lib/queries';
import { tsLong } from '@/lib/utils';
import type { Problem, ProblemLogTemplateEvidence } from '@/lib/types';
import { Sect } from './detailSections';
import { isProblemLive } from './alertMetricSeries';
import { newTemplateLogsHref, startOffsetLabel, startOffsetTitle } from './problemLogTemplates';

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
  const q = useProblemLogTemplates(problem.id, { startedAt: problem.startedAt, open: isProblemLive(problem) });
  // Veriden çizilir, hata bayrağından DEĞİL: yoklamadaki başarısız bir arka
  // plan tazelemesi eldeki satırları silmesin. Hiç veri yoksa (ilk okuma
  // düştü / boş) bölüm yok.
  const rows = q.data?.templates ?? [];
  if (rows.length === 0) return null;
  const services = q.data?.services ?? [];
  return (
    <Sect title="Başlangıçta doğan log şablonları"
      sub={<span title="Templater defteri (5 dk'da ≤1000 satır örneklenir); bilinen bir şablonun varyantı sayılmaz">
        {services.length > 0 ? `${services.join(', ')} · ` : ''}başlangıç −10 dk … +5 dk
      </span>}>
      <ul className="pd-newtpl">
        {rows.map(r => <NewTemplateRow key={r.templateId} row={r} win={win} />)}
      </ul>
    </Sect>
  );
}

function NewTemplateRow({ row: r, win }: { row: ProblemLogTemplateEvidence; win: { fromNs: number; toNs: number } }) {
  const full = r.sample ? `${r.template}\n\nÖrnek satır:\n${r.sample}` : r.template;
  return (
    <li className="pd-newtpl-row" data-template-id={r.templateId}>
      <div className="pd-newtpl-tpl" title={full}>{r.template}</div>
      <div className="pd-newtpl-meta">
        <span className="pd-newtpl-svc" title="Şablonun bağlandığı servis">{r.service}</span>
        <span className="pd-newtpl-off" title={`${startOffsetTitle(r.offsetSec)} · ${tsLong(r.firstSeen)}`}>
          {startOffsetLabel(r.offsetSec)}
        </span>
        <span title="Son templater örnekleminde görülen satır (5 dk'da ≤1000 satır örneklenir) — gerçek log sayısı değil">
          {r.totalCount.toLocaleString()} örnek
        </span>
        <Link to={newTemplateLogsHref(r, win)}
          title={r.query ? `Ara: ${r.query}` : 'Bu servisin logları, şablonun doğumundan itibaren'}>
          Logları aç
        </Link>
      </div>
    </li>
  );
}
