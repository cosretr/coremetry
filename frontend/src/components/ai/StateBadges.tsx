import { sourceStateTone, windowPrefix, type SourceStateView } from './toolSteps';

// StateBadges — araç sonucunun kaynak durumu rozetleri (boş · erişilemedi ·
// yetki yok · zaman aşımı · kısmi · gecikmeli · limitli). v0.10.948'da
// ChatBubble'dan taşındı: sohbet çipi, adım tablosu ve "CoSRE'ye sor"
// ilerleme listesi (ExplainSteps) aynı rozeti çizer — tek yazım.
// v0.10.944 — kaynak öneki yalnız BİRDEN ÇOK kaynak varken: tek kaynağın
// ikincil bayrakları (kısmi + limitli) "logs · " tekrarı taşımaz.
// v0.10.944 — pencere öneki (compare_periods: "sorun · " / "referans · ")
// detail'den; aynı kaynaklı iki rozet başlığa inmeden ayırt edilir.
export function StateBadges({ states }: { states: SourceStateView[] }) {
  if (states.length === 0) return null;
  const multi = new Set(states.map(st => st.source)).size > 1;
  return (<>
    {states.map((st, k) => (
      <span key={k} className={`badge b-${sourceStateTone(st.state)}`}
        title={`${st.source || 'kaynak'}: ${st.label}${st.detail ? ` — ${st.detail}` : ''}`}>
        {multi && st.source ? `${st.source} · ` : ''}{windowPrefix(st.detail)}{st.label}
      </span>
    ))}
  </>);
}
