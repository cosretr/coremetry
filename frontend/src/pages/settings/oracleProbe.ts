// oracleProbe.ts — v0.10.768: Oracle bağlantı testinin saf yardımcıları.
//
// Operatör: "full scan / kilitli sorgu atmasın, önce test edelim, sorgu
// görünür olsun; test ederken hangi servis/operasyon/trace geldiğini
// göreyim". Hüküm metinleri burada, kablolama OracleTab'da (pinli).
import type { OracleCoverageOp, OracleCoverageReason, OracleLivePreview, OracleLongCheck, OracleMappingCheck, OracleScanCheck, OracleSubjectCoverage, OracleWindowSummary } from '@/lib/types';

export const ORACLE_TEST_WINDOWS = [5, 15, 60] as const;
export type OracleTestWindow = (typeof ORACLE_TEST_WINDOWS)[number];

// v0.10.929 (K5) — 'b-ok' tondan çıktı: iyi yapılandırma hükmü (indeksli,
// partition, LONG select dışında) sağlıklı bir HÂL, geçiş değil → nötr b-gray.
// Test başarısının yeşili FlashBox'ta (düğmenin doğrudan geri bildirimi) kalır.
export type ScanTone = 'b-warn' | 'b-err' | 'b-gray';

/** Tam-tarama hükmü: indeks ya da partition varsa nötr (sorun yok); ikisi de
 *  yoksa kırmızı; sözlük okunamadıysa / tablo bulunamadıysa gri (hüküm yok). */
export function scanVerdict(s: OracleScanCheck | undefined): { tone: ScanTone; text: string; detail: string } {
  if (!s || !s.checked) {
    return { tone: 'b-gray', text: 'tam tarama kontrolü yapılamadı', detail: s?.error ?? 'sözlük okunmadı' };
  }
  if (!s.found) {
    return { tone: 'b-gray', text: 'tablo sözlükte yok', detail: 'view ya da synonym olabilir — altındaki tabloyu kontrol et' };
  }
  const rows = s.numRows > 0 ? `${s.numRows.toLocaleString('tr-TR')} satır` : 'satır sayısı bilinmiyor';
  const analyzed = s.lastAnalyzed ? ` (istatistik ${s.lastAnalyzed})` : '';
  if (s.indexed) {
    return { tone: 'b-gray', text: `${s.tsColumn} indeksli`, detail: `${s.indexName ?? 'indeks'} · ${rows}${analyzed}` };
  }
  if (s.partitioned) {
    return { tone: 'b-gray', text: `${s.tsColumn} partition anahtarı`, detail: `partition budaması · ${rows}${analyzed}` };
  }
  return {
    tone: 'b-err',
    text: 'TAM TARAMA RİSKİ',
    detail: `${s.tsColumn} ne indeksli ne partition anahtarı${s.partitionKey ? ` (partition: ${s.partitionKey})` : ''} — her poll ${rows} okur${analyzed}`,
  };
}

/** Pencere özeti başlığı: satır sayısı (tavanlıysa "500+"), eşleme oranı. */
export function summaryHeadline(sm: OracleWindowSummary | undefined): string {
  if (!sm) return '';
  if (sm.error) return `özet alınamadı: ${sm.error}`;
  const rows = sm.capped ? `${sm.rows}+` : String(sm.rows);
  const trace = sm.lookupDone
    ? `${sm.tracesFound}/${sm.traceIds} trace Coremetry'de`
    : sm.lookupError ? `trace araması başarısız (${sm.lookupError})` : `${sm.traceIds} ayrık trace id (arama yok)`;
  return `son ${sm.windowMin} dk: ${rows} satır · ${sm.mapped} eşlendi · ${sm.noTimestamp} damgasız · ${sm.badTraceId} bozuk trace id · ${trace}`;
}

/** v0.10.845 — LONG kolon hükmü. null = gösterilecek bir şey yok (sözlük okundu,
 *  LONG kolon yok). Oracle FETCH FIRST + select listesinde LONG = ORA-00997;
 *  SELECT * kipinde tablodaki her LONG kolon listededir. */
export function longVerdict(l: OracleLongCheck | undefined): { tone: ScanTone; text: string; detail: string } | null {
  if (!l) return null;
  if (!l.checked) {
    return { tone: 'b-gray', text: 'LONG kolon kontrolü yapılamadı', detail: l.error ?? 'sözlük okunmadı' };
  }
  if (l.columns.length === 0) return null;
  const sel = l.selected.join(', ');
  if (l.selected.length > 0 && !l.mappedOnly) {
    return {
      tone: 'b-err',
      text: `LONG kolon sorguda: ${sel}`,
      detail: 'SELECT * + FETCH FIRST bu kolonla ORA-00997 verir. "SELECT yalnız eşlenen kolonlar" kutusunu aç ve bu kolonu eşlemeye alma.',
    };
  }
  if (l.selected.length > 0) {
    return {
      tone: 'b-err',
      text: `eşlenen kolon LONG: ${sel}`,
      detail: 'LONG kolon FETCH FIRST ile okunamaz (ORA-00997). Alanı kapat (-) ya da başka kolona eşle.',
    };
  }
  return { tone: 'b-gray', text: 'LONG kolonlar select dışında', detail: l.columns.join(', ') };
}

/** v0.10.886 — eşleme hükmü. null = gösterilecek bir şey yok (sözlük okundu, her
 *  eşlenen kolon tabloda). traceId/service/code eksikse kırmızı (özet "trace
 *  bulunamadı" derdi — sebep bu); diğer alanlar eksikse sarı. */
export const ORACLE_MATCH_FIELDS = ['traceId', 'service', 'code'] as const;
export function mappingVerdict(m: OracleMappingCheck | undefined): { tone: ScanTone; text: string; detail: string } | null {
  if (!m) return null;
  if (!m.checked) {
    return { tone: 'b-gray', text: 'eşleme kontrolü yapılamadı', detail: m.error ?? 'sözlük okunmadı' };
  }
  if (m.missing.length === 0) return null;
  // v0.10.902 — özel SQL kipinde zaman ve trace LİSTESİ de kritik: zaman yoksa
  // her satır düşer, liste yoksa hiçbir trace bağlanmaz.
  const critical = m.missing.filter(x => (ORACLE_MATCH_FIELDS as readonly string[]).includes(x.field)
    || (m.source === 'query' && (x.field === 'timestamp' || x.field === 'traceIds')));
  // v0.10.907 — kolon boş = alan hiç eşlenmemiş (özel SQL kipi).
  const list = m.missing.map(x => x.column ? `${x.field}→${x.column}` : `${x.field}: eşlenmedi${x.suggest ? ` (öneri ${x.suggest})` : ''}`).join(', ');
  const suggested = m.missing.filter(x => x.suggest).length;
  // v0.10.902 — özel SQL kipinde karşılaştırma sorgunun ÇIKTI kolonlarına (takma
  // adlar); "tabloda yok" cümlesi orada yanlış olurdu.
  const where = m.source === 'query' ? 'sorgu çıktısında' : 'tabloda';
  const hint = suggested > 0
    ? m.source === 'query'
      ? ` · sorgu çıktısında karşılıkları var (${suggested}/${m.missing.length}) — "Önerilen eşlemeyi uygula", sonra Kaydet`
      : ` · tabloda ${m.prefix} önekli karşılıkları var (${suggested}/${m.missing.length}) — "Önerilen eşlemeyi uygula"`
    : m.source === 'query'
      ? ' · sorgunun SELECT takma adlarını (büyük harf) kolon kutularına yaz'
      : ' · Alan eşlemesi → göster ve kolon adlarını yaz';
  if (critical.length > 0) {
    return {
      tone: 'b-err',
      text: m.missing.some(x => !x.column)
        ? `${m.missing.length} alan eşlenmemiş ya da ${where} yok — trace/operasyon/hata kodu okunmaz`
        : `eşlenen ${m.missing.length} kolon ${where} yok — ${m.source === 'query' ? 'zaman/trace/servis/hata kodu' : 'trace/servis/hata kodu'} okunmaz`,
      detail: list + hint,
    };
  }
  return { tone: 'b-warn', text: `eşlenen ${m.missing.length} kolon ${where} yok`, detail: list + hint };
}

// ── v0.10.998 — canlıya geçiş önizlemesi (kaynak kipi gölge → canlı) ────────
//
// Operatör kararı 2026-10-01. Canlı kip bildirimi YALNIZ yeni açılışta
// gönderir; gölgede aynı Problem'ler bildirimsiz açılıyor. Bu yüzden
// "gölgede kaç Problem açıldı" = "canlıda kaç bildirim giderdi". İkinci soru
// "kime": dış seri Problem'leri bildirimde ANOMALİ türüdür; türü süzen kanal
// / ekip maili almaz — "canlıya aldım, hiçbir şey gelmedi"nin en olası sebebi.

const NOTIFY_KIND_TR: Record<string, string> = { anomaly: 'Anomali', problem: 'Problem', incident: 'Incident', exception: 'Exception' };
const MODE_TR: Record<string, string> = { off: 'kapalı', shadow: 'gölge', live: 'canlı' };

/** Önizlemenin metni: özet satırı + ayrıntı satırları. `warn` = canlıya almak
 *  bugünkü ayarlarla beklenen sonucu vermez (bildirim alıcısı yok ya da tarama
 *  koşmuyor); renk yalnız o sapmada. */
export function livePreviewText(p: OracleLivePreview): { headline: string; lines: string[]; warn: boolean } {
  const kind = NOTIFY_KIND_TR[p.notifyKind] ?? p.notifyKind;
  const mode = MODE_TR[p.mode] ?? p.mode;
  const n = (v: number) => v.toLocaleString('tr-TR');
  const lines: string[] = [];
  let warn = false;

  let headline: string;
  if (p.mode === 'off') {
    headline = `Kayıtlı kip ${mode}: tarama koşmuyor, Problem açılmıyor — önce gölgeye alıp birkaç gün izleyin.`;
    warn = true;
  } else if (p.opened7d === 0) {
    headline = `Kayıtlı kip ${mode}: son 7 günde bu kaynaktan hiç Problem açılmadı (şu an açık ${n(p.openNow)}).`;
  } else {
    const extra = [`${n(p.critical7d)} kritik`];
    if (p.clusters7d > 0) extra.push(`en az ${n(p.clusters7d)} küme`);
    headline = `Kayıtlı kip ${mode}: son 24 saatte ${n(p.opened24h)}, son 7 günde ${n(p.opened7d)} Problem açıldı (${extra.join(', ')}) · şu an açık ${n(p.openNow)}.`;
  }

  if (p.channelsAccepting === 0 && !p.teamMail) {
    warn = true;
    lines.push(`Canlıda bildirim GİTMEZ: bu Problem'lerin türü "${kind}" ve etkin ${n(p.channelsEnabled)} kanalın hiçbiri ile ekip maili bu türü almıyor. Bildirim kanalında (ya da Team routing'de) olay türlerinden "${kind}"yi açın.`);
  } else {
    const more = p.channelsAccepting > p.channelNames.length ? ` +${n(p.channelsAccepting - p.channelNames.length)}` : '';
    const names = p.channelNames.length ? ` (${p.channelNames.join(', ')}${more})` : '';
    lines.push(`Canlıda her yeni açılış bir bildirimdir (tür: ${kind}): etkin ${n(p.channelsEnabled)} kanaldan ${n(p.channelsAccepting)} tanesi alıyor${names}; ekip maili ${p.teamMail ? 'alıyor' : 'almıyor'}. Kanalın servis / ekip / öncelik süzgeçleri ayrıca uygulanır.`);
  }
  if (p.mode !== 'live') {
    lines.push(`Kipi canlıya alınca zaten açık olan ${n(p.openNow)} Problem için bildirim gönderilmez; yalnız yeni açılışlar bildirilir.`);
  }
  if (p.openCapPerTick > 0) {
    lines.push(`Okuma başına en çok ${n(p.openCapPerTick)} açılış; fazlası tek özet Problem'de toplanır (Anomali ayarları).`);
  }
  if (p.top.length > 0) {
    lines.push(`En çok açan özneler (7 gün): ${p.top.map(t => `${t.subject} (${n(t.opened)})`).join(', ')}.`);
  }
  return { headline, lines, warn };
}

// ── v0.10.999 — özne kapsamı (Oracle odak "2") ─────────────────────────────
//
// "Hata satırlarının ne kadarı bir servise bağlanıyor, bağlanmayan neden
// bağlanmıyor?" Boşluğu kapatacak yöntem (pod adı / host adı / elle eşleme)
// baskın NEDENE göre seçilir; bu metin o nedeni operatörün önüne koyar.

export const COVERAGE_REASON_TR: Record<OracleCoverageReason, string> = {
  no_trace_id: 'satırlarda trace kimliği yok',
  trace_not_found: "trace Coremetry'de yok",
  multi_service: 'çok servisli operasyon',
  unconfirmed: 'eşleme henüz onaysız',
  dead_service: 'öğrenilmiş servis canlı değil',
  no_operation: 'operasyon kodu boş',
};
/** Nedenlerin sabit gösterim sırası (eşit satırda kararlı çıktı). */
const COVERAGE_REASON_ORDER: OracleCoverageReason[] = ['no_trace_id', 'trace_not_found', 'multi_service', 'unconfirmed', 'dead_service', 'no_operation'];

/** Çözülmeyen bir operasyonun satırı: kod · satır · neden · ipucu. */
export function coverageOpLine(o: OracleCoverageOp): string {
  const n = (v: number) => v.toLocaleString('tr-TR');
  const parts = [o.operation || '(boş)', `${n(o.rows)} satır`, o.reason ? COVERAGE_REASON_TR[o.reason] : 'çözülmedi'];
  if (o.service && (o.reason === 'multi_service' || o.reason === 'unconfirmed' || o.reason === 'dead_service')) {
    parts.push(`aday ${o.service}${o.votes ? ` (${o.votes})` : ''}`);
  }
  if (o.fnRows && o.fnService) {
    parts.push(`fonksiyon kodundan ${o.fnService} (${n(o.fnRows)} satır)`);
  }
  if (o.instance) {
    parts.push(o.podLike ? `pod ${o.instance} (canlı bir servise çözülmedi)` : `instance ${o.instance} (pod adı değil)`);
  } else if (o.host) {
    parts.push(`host ${o.host}`);
  }
  return parts.join(' · ');
}

/** Raporun metni. `warn` = satırların yarısından azı servise bağlanıyor. */
export function coverageText(c: OracleSubjectCoverage): { headline: string; lines: string[]; ops: string[]; warn: boolean } {
  const n = (v: number) => v.toLocaleString('tr-TR');
  const lines: string[] = [];
  if (c.rowsTotal === 0) {
    return { headline: `Son ${c.hours} saatte bu kaynaktan satır yok.`, lines, ops: [], warn: false };
  }
  const pct = c.rowsListed > 0 ? Math.round((c.rowsResolved / c.rowsListed) * 100) : 0;
  const fnOn = c.fnChecked && c.fnEnabled && !!c.fnSource;
  const fnPart = fnOn ? `, fonksiyon kodundan ${n(c.rowsFunctionCode)}` : '';
  const headline = `Son ${c.hours} saatte ${n(c.rowsListed)} satırın %${pct} kadarı bir servise bağlanıyor (${n(c.rowsResolved)} satır: öğrenilmiş eşleme ${n(c.rowsLearned)}, pod adından ${n(c.rowsPod)}${fnPart}) · ${n(c.opsResolved)}/${n(c.opsListed)} operasyon.`;
  if (c.rowsListed < c.rowsTotal) {
    lines.push(`Yalnız en çok satırlı ${n(c.opsListed)} operasyon sınıflandı (${n(c.opsTotal)} operasyon, ${n(c.rowsTotal)} satırın ${n(c.rowsListed)} tanesi).`);
  }
  if (!c.aliveKnown) {
    lines.push('Canlı servis listesi okunamadı: pod adından eşleme doğrulanamadı — oran alt sınırdır.');
  }
  const unresolved = c.rowsListed - c.rowsResolved;
  // v0.10.1000 — fonksiyon kodu basamağı: ölçülemediyse nedenini, kapalıysa
  // açılınca ne kazandıracağını söyler (açıkken pay zaten başlıkta).
  if (c.fnError) {
    lines.push(`Fonksiyon kodu eşlemesi ölçülemedi: ${c.fnError}`);
  } else if (c.fnChecked && !c.fnSource) {
    if (c.fnEnabled || unresolved > 0) {
      lines.push("Fonksiyon kodu eşlemesi okunamıyor: geniş rollup tablosu da FUNCTION_CODE terfi kolonu da yok.");
    }
  } else if (c.fnChecked && c.fnCodes === 0) {
    if (c.fnEnabled || unresolved > 0) {
      lines.push('Satırlarda fonksiyon kodu yok: sorgu çıktısında FUNCTIONCODE kolonu olmalı (eşlenmemiş kalabilir).');
    }
  } else if (c.fnChecked && !c.fnEnabled && unresolved > 0) {
    if (c.rowsFunctionCode > 0) {
      const withFn = Math.round(((c.rowsResolved + c.rowsFunctionCode) / c.rowsListed) * 100);
      lines.push(`Fonksiyon kodu eşlemesi açılırsa ${n(c.rowsFunctionCode)} satır daha servise bağlanır (oran %${pct} → %${withFn}).`);
    } else {
      lines.push('Fonksiyon kodu eşlemesi açılsa da ek satır bağlanmıyor: kodlar span\'lerde yok ya da tek bir serviste toplanmıyor.');
    }
  }
  if (unresolved > 0) {
    const reasons = COVERAGE_REASON_ORDER
      .filter(r => (c.byReason[r] ?? 0) > 0)
      .sort((a, b) => (c.byReason[b] ?? 0) - (c.byReason[a] ?? 0))
      .map(r => `${COVERAGE_REASON_TR[r]} ${n(c.byReason[r] ?? 0)}`);
    lines.push(`Bağlanmayan ${n(unresolved)} satır — neden: ${reasons.join(' · ')}.`);
  }
  return { headline, lines, ops: c.unresolved.map(coverageOpLine), warn: pct < 50 };
}
