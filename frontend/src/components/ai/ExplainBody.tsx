// ExplainBody — v0.10.165: CoSRE cevap kartının SABİT gövde anatomisi (tasarım
// etüdü «cevap paneli» seçenek A «Yapılandırılmış kart», iki yargıçta birinci;
// dilim 1, sıfır backend). Sıra her türde aynı:
//   Karar (Kök Neden / Olası neden bölümünün ilk cümlesi; bölüm tek cümleyse
//   ÇİZİLMEZ — kopya olurdu; çizilince aynı cümle gövdeden DÜŞER; kod kartı
//   açıkken ilk kart Karar çizmez — iki rakip Karar olmasın, `verdict`) →
//   Kanıt (span/trace kimlik sayısı; v0.10.1033'ten beri span'ler waterfall'da
//   kutulu, trace'ler AIDrawer'ın altındaki listede — yalnız sayfada gerçekten
//   gösterilenler sayılır, ipucu `evidenceHint`) → kod alıntıları (oluklu
//   çitler gövdeden buraya hoist; Markdown CodeBlock anatomy: satır numarası
//   oluğu, dosya başlığı, mapper etiketi) → bölümler (Stacktrace Detayı'nın
//   stack çiti yerinde kalır, katlanır) → dipnot/geri bildirim (çağıran çizer).
// Akış SÜRERKEN ham metin aynen akar (yarım çit titremesin); anatomi yalnız
// bitmiş metne uygulanır. Güven puanı çizilmez (böyle bir alan yok — brief §5).
// v0.10.1065 — v0.10.948 incelemesinin gövde ekleri (`id`siz kanıt linkleri,
// `sources` kaynak durumu dipnotu, metindeki "Kaynak durumu" bloğunun
// ayrılması, beş başlıklı / güven satırlı cevap şekli) erişilemeyen inceleme
// hattıyla silindi; v0.10.1036'dan beri sunucu hiçbirini üretmiyordu.
import { useMemo } from 'react';
import { RenderedMarkdown, CodeBlock } from '@/components/Markdown';
import type { IdLink } from '@/components/ai/inlineIdLinks';
import { verdictLine, hoistCodeQuotes, dropVerdictSentence } from './explainAnatomy';

// oracle — v0.10.921 (Kademe A): Explain'e giren Oracle hata satırı sayısı.
export interface ExplainEvidence { spans: number; traces: number; oracle?: number }

// evidenceHint — Kanıt satırının ipucu; yalnız DOĞRU olanı söyler.
// v0.10.1033 (operatör: "Kanıt span'lere gerek yok") — span kimlikleri artık
// çekmecede listelenmiyor; görünür oldukları tek yer waterfall'daki kutu
// (v0.9.408). Trace kimlikleri exception çekmecesinin altındaki "Kanıt
// trace'leri" listesinde kalıyor. Oracle satırı hiçbir yerde listelenmez →
// tek başına ipucu yok (eski "kimlikler çekmecenin altında" orada da yanlıştı).
function evidenceHint(ev: ExplainEvidence): string {
  const spans = ev.spans > 0;
  const traces = ev.traces > 0;
  if (spans && traces) return "span'ler waterfall'da kutulu, trace'ler çekmecenin altında";
  if (spans) return "waterfall'da kutulu";
  if (traces) return 'kimlikler çekmecenin altında, satır satır';
  return '';
}

export function ExplainBody({ text, busy, links, evidence, verdict: wantVerdict = true }: {
  text: string;
  busy: boolean;
  links?: IdLink[];
  /** ilk cevabın kanıt kimlik sayıları (exception/trace); yoksa satır çizilmez */
  evidence?: ExplainEvidence;
  /** false → Karar satırı çizilmez (kod kartı açıkken ilk kart) */
  verdict?: boolean;
}) {
  const verdict = useMemo(() => (busy || !wantVerdict ? null : verdictLine(text)), [busy, text, wantVerdict]);
  const hoisted = useMemo(() => {
    if (busy) return { quotes: [], rest: text };
    const h = hoistCodeQuotes(text);
    return verdict ? { quotes: h.quotes, rest: dropVerdictSentence(h.rest, verdict) } : h;
  }, [busy, text, verdict]);
  const ev = evidence && (evidence.spans > 0 || evidence.traces > 0 || (evidence.oracle ?? 0) > 0) ? evidence : null;
  const hint = ev ? evidenceHint(ev) : '';
  return (
    <>
      {verdict && (
        <div className="cx-verdict"><span className="cx-verdict-k">Karar</span>{verdict}</div>
      )}
      {!busy && ev && (
        <div className="cx-evidence">
          Kanıt: {[
            ev.spans > 0 ? `${ev.spans} span` : '',
            ev.traces > 0 ? `${ev.traces} trace` : '',
            (ev.oracle ?? 0) > 0 ? `${ev.oracle} Oracle satırı` : '',
          ].filter(Boolean).join(' · ')}
          {hint && <span className="field-hint"> · {hint}</span>}
        </div>
      )}
      {hoisted.quotes.map((q, i) => <CodeBlock key={`q${i}`} lang={q.lang} lines={q.lines} anatomy />)}
      <RenderedMarkdown text={hoisted.rest} idLinks={links} anatomy />
    </>
  );
}
