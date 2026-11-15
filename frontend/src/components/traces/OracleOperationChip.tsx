import { useMemo } from 'react';
import { IdentityChip } from '@/components/ui/IdentityChip'; // v0.10.1514
import { useOracleFunctionCodes } from '@/lib/queries';
import type { LogRow, SpanRow, TimeRange } from '@/lib/types';
import { functionCodeTracesHref } from '@/lib/pivotHref';
import {
  traceFunctionCodes, exactTraceOperation, pickTraceOperation, oracleOperationTitle,
  exactOperationTitle, dictionaryOperationHint, traceEntryCodes,
} from '@/lib/oracleFunctionOps';

// OracleOperationChip — v0.10.1003 (kuyruk: "operasyon adını trace sayfasında
// göster"). Trace özet şeridinde bu trace'in Oracle operasyon adı. Tıklayınca
// aynı fonksiyon kodunun diğer trace'leri.
//
// v0.10.1329 (operatör-bildirimli) — KESİN kaynak önce (pickTraceOperation):
// (a) bu trace'in kendi Oracle hata satırları, (b) span'in operasyon
// attribute'u, (c) ancak ikisi de yoksa fonksiyon kodu sözlüğü — ve o zaman
// etiket bunun bir tahmin olduğunu söyler ("fonksiyon kodundan, N aday").
// Eskiden yalnız (c) vardı ve bankanın genelinde en çok satırlı aday "bu
// trace'in operasyonu" gibi görünüyordu (TRX trace'inde INQ adı, "+15").
//
// `oracleRows` undefined = trace'in Oracle satırları HÂLÂ yükleniyor: çip
// bekler — önce sözlük tahmini çizip sonra kesin ada atlamak, yanlış adı bir
// an göstermek demek. İstek disiplini: sözlük (kod → ad) TEK paylaşılan istek,
// 5 dk taze, YALNIZ trace bir fonksiyon kodu taşıyor VE kesin kaynak yoksa.
export function OracleOperationChip({ spans, pageRange, oracleRows }: {
  spans: SpanRow[];
  pageRange: TimeRange;
  oracleRows: LogRow[] | undefined;
}) {
  const codes = useMemo(() => traceFunctionCodes(spans), [spans]);
  const exact = useMemo(
    () => (oracleRows === undefined ? undefined : exactTraceOperation(oracleRows, spans)),
    [oracleRows, spans]);
  const q = useOracleFunctionCodes(codes.length > 0 && exact === null);
  const pick = useMemo(
    () => (oracleRows === undefined ? null : pickTraceOperation(oracleRows, spans, q.data)),
    [oracleRows, spans, q.data]);
  if (!pick) return null;

  // v0.10.1514 (trace başlığı A) — her aday/kaynak TEK kimlik çipi (ui/IdentityChip):
  // OPERASYON etiketi · ad (bağlantı) · ipucu · kopyala. Uzun "Operasyon (fonksiyon
  // kodundan, N aday):" etiketi amber ipucu hücresine indi ("tahmin · N aday");
  // kaynak metni (kesin/tahmin, adaylar) kökün ve bağlantının ipucunda aynen kalır.
  if (pick.source === 'dictionary') {
    return (
      <>
        {pick.candidates.map(o => {
          const title = oracleOperationTitle(o);
          return (
            <IdentityChip key={o.code} label="OPERASYON" value={o.operation} title={title} data-op-source="dictionary"
              to={functionCodeTracesHref({ window: pageRange, attrKey: o.key, codes: [o.code] })}
              valueLabel={`${o.operation} — ${title}`}
              hint={dictionaryOperationHint(o)} hintTone="warn" hintTitle={title}
              copyTitle="Operasyon adını kopyala" />
          );
        })}
      </>
    );
  }

  // Kesin kaynak: tek çip. Link hedefi değişmedi — trace'in (ilk) fonksiyon
  // kodunun diğer trace'leri; trace kod taşımıyorsa ad düz metin. "+N" = bu
  // trace'teki diğer operasyonlar (nötr ipucu hücresi; adlar kök ipucunda).
  // v0.10.1418 (operatör: "Operasyon adı kopyalanabilir olsun") — kopyala hücresi.
  const title = exactOperationTitle(pick);
  const fc = codes[0];
  return (
    <IdentityChip label="OPERASYON" value={pick.main} data-op-source={pick.source}
      title={fc ? `${title} Tıkla: bu fonksiyon kodunun diğer trace'leri.` : title}
      to={fc ? functionCodeTracesHref({ window: pageRange, attrKey: fc.key, codes: [fc.code] }) : undefined}
      valueLabel={fc ? `${pick.main} — ${title}` : undefined}
      srNote={fc ? undefined : title}
      hint={pick.others.length > 0 ? `+${pick.others.length}` : undefined}
      hintTitle={pick.others.length > 0 ? `Bu trace'teki diğer operasyonlar: ${pick.others.join(', ')}` : undefined}
      copyTitle="Operasyon adını kopyala" />
  );
}

// TraceEntryCodes — v0.10.1497 (operatör: "function code / channel code
// bilgilerini yukarıda operasyonun yanında yazabiliriz"). Kök → giriş →
// herhangi span'deki İLK fonksiyon ve kanal kodu. Fonksiyon kodu tıklanınca
// aynı kodun diğer trace'leri (operasyon çipiyle aynı hedef); ikisi de
// kopyalanabilir. v0.10.1514 — ikisi de kimlik çipi (CHANNEL artık düz metin
// değil); etiket FUNCTION / CHANNEL, span'deki anahtar yazımı (uydurma ad yok)
// çipin ipucunda: "FUNCTION_CODE: SXQ0112".
export function TraceEntryCodes({ spans, pageRange }: { spans: SpanRow[]; pageRange: TimeRange }) {
  const { functionCode: fc, channelCode: cc } = useMemo(() => traceEntryCodes(spans), [spans]);
  if (!fc && !cc) return null;
  return (
    <>
      {fc && (
        <IdentityChip label="FUNCTION" value={fc.code} data-trace-codes="function" title={`${fc.key}: ${fc.code}`}
          to={functionCodeTracesHref({ window: pageRange, attrKey: fc.key, codes: [fc.code] })}
          valueTitle="Bu fonksiyon kodunun diğer trace'leri"
          copyTitle="Fonksiyon kodunu kopyala" />
      )}
      {cc && (
        <IdentityChip label="CHANNEL" value={cc.code} data-trace-codes="channel" title={`${cc.key}: ${cc.code}`}
          copyTitle="Kanal kodunu kopyala" />
      )}
    </>
  );
}
