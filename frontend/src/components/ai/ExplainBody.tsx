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
// v0.10.948 (CoSRE Faz B) — gövdenin ALTINA iki deterministik satır: sunucunun
// `id`siz kanıt linkleri (ExplainEvidenceLinks) ve `sources` kaynak durumu
// dipnotu (ExplainSourceFooter). Yapısal `sources` varken sunucunun metne
// eklediği "Kaynak durumu" bloğu gövdeden ayrılır (splitSourceFooter) — aynı
// bilgi iki kez basılmaz; `sources` yoksa metin aynen kalır.
// v0.10.948 — beş başlıklı inceleme cevabında (**Bulgu** … **Sonraki kontrol**)
// Karar ÇİZİLMEZ: «Olası neden» hipotezdir; verdictLine metnin şeklinden
// null döner (`sources`suz önbellek isabetinde de), bölüm bütün kalır.
// v0.10.972 — o bölüm «Kök neden» oldu (ilk satırı güven): "Güven: kesin" →
// Karar = kök neden cümlesi (gövdeden düşer, güven satırı kalır); "olası" ya
// da güvensiz → bugünkü gibi Karar yok. Koşullu «Stacktrace detayı» bölümü
// gövdede yerinde (stack çiti varsa katlanır). Kod değişmedi: karar metnin
// şeklinde, explainAnatomy.ts'te.
import { useMemo } from 'react';
import { RenderedMarkdown, CodeBlock } from '@/components/Markdown';
import type { IdLink } from '@/components/ai/inlineIdLinks';
import type { ExplainSourceStatus } from '@/lib/types';
import { verdictLine, hoistCodeQuotes, dropVerdictSentence, splitSourceFooter } from './explainAnatomy';
import { ExplainEvidenceLinks, ExplainSourceFooter } from './ExplainEvidence';

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

export function ExplainBody({ text: raw, busy, links, evidence, verdict: wantVerdict = true, sources }: {
  text: string;
  busy: boolean;
  links?: IdLink[];
  /** ilk cevabın kanıt kimlik sayıları (exception/trace); yoksa satır çizilmez */
  evidence?: ExplainEvidence;
  /** false → Karar satırı çizilmez (kod kartı açıkken ilk kart) */
  verdict?: boolean;
  /** v0.10.948 — cevap çerçevesinin kaynak durumları (explain-trace); yoksa dipnot yok */
  sources?: ExplainSourceStatus[];
}) {
  const hasSources = !!sources?.length;
  // v0.10.948 — sunucunun metin dipnotu yalnız yapısal kopyası VARKEN ayrılır.
  const text = useMemo(() => (busy || !hasSources ? raw : splitSourceFooter(raw).body), [busy, hasSources, raw]);
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
      {!busy && <ExplainEvidenceLinks links={links} />}
      {!busy && <ExplainSourceFooter sources={sources} />}
    </>
  );
}
