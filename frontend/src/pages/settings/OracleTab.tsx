// OracleTab — Settings → "Oracle hata tablosu" (v0.10.580, AŞAMA 1;
// audit: docs/audit/oracle-error-log-2026-09-09.md §7).
//
// InfluxTab'ın anatomisi birebir: kaynak kartları listesi + kaynak başına
// "Bağlantıyı dene" (formdaki değerlerle, KAYDETMEDEN) + tek Kaydet.
// Karar mantığı sekmede DEĞİL, oracleForm.ts'te (saf + tablo-testli); bu
// dosya yalnız çizer ve hata haritasını kutuların altına dağıtır.
//
// AŞAMA 1 kapsamı: datasource tanımı, credential ve bağlantı testi. Poller,
// metrik yazımı ve Problem üretimi Aşama 2-3'te — bu yüzden "durum" şeridi
// bilerek mütevazı: uydurma bir "sağlıklı" satırı basmıyoruz, yalnız o pod'da
// koşmuş son bağlantı testinin izini ve şifre referansının çözülüp
// çözülmediğini gösteriyoruz.
//
// ŞİFRE: hiçbir yolda ekrana yazılmaz, hiçbir yolda log'a girmez. GET onu
// maskeliyor (hasPassword rozeti), kutu daima boş açılıyor ve boş kutu
// "dokunmadım" demek — `sourceForSave` `password` anahtarını gövdeye HİÇ
// koymuyor.
//
// TEST UCU SÖZLEŞMESİ: başarısızlık 200 + {ok:false} ile gelir. Bu bir HTTP
// hatası DEĞİL, operatörün sorusuna verilmiş başarılı bir cevaptır — kırmızı
// rozet + gerekçe metni olarak çizilir, "istek başarısız" olarak değil.
import { useEffect, useState, type FormEvent } from 'react';
import { useQuery } from '@tanstack/react-query'; // v0.10.897 — öğrenilmiş harita
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { Field, SelectField, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { TimeRangePicker } from '@/components/coremetry/TimeRangePicker';
import type { Status } from '@/lib/statusTone';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { fmtDateTime } from '@/lib/utils';
import { useSettingsLoad, SettingsLoadError, FlashBox } from './shared';
import { ORACLE_TEST_WINDOWS, coverageText, customTruncatedText, livePreviewText, longVerdict, mappingVerdict, scanVerdict, summaryHeadline, uiTestVerdict, type OracleTestWindow } from './oracleProbe'; // v0.10.768, longVerdict v0.10.845, mappingVerdict v0.10.886, v0.10.1092 uiTestVerdict/customTruncatedText
import {
  emptyOracleSource, sourceFromSnapshot, sourceForSave, validateOracleSource,
  hasOracleErrors, parseTypeFilter, typeFilterToText, numFromForm, numToForm,
  ORACLE_DEFAULT_PORT, ORACLE_DEFAULT_TIMESTAMP_COLUMN, ORACLE_DEFAULT_TYPE_COLUMN,
  ORACLE_DEFAULT_MAX_OPEN_CONNS, ORACLE_DEFAULT_QUERY_TIMEOUT_SEC, ORACLE_DEFAULT_INTERVAL_SEC,
  ORACLE_DEFAULT_TIMEZONE, ORACLE_MAPPING_FIELDS, ORACLE_COLUMN_DISABLED,
  ORACLE_DEFAULT_WINDOW_MIN, ORACLE_CUSTOM_ONLY_FIELDS, isCustomQuery,
  ORACLE_DEFAULT_MAX_PAGES, ORACLE_MIN_MAX_PAGES, ORACLE_MAX_MAX_PAGES,
  ORACLE_DEFAULT_DWELL_MIN, ORACLE_MIN_DWELL_MIN, ORACLE_MAX_DWELL_MIN,
  type OracleFieldErrors,
} from './oracleForm';
import type {
  OraclePollStatus, OracleSource, OracleSourceSnapshot, OracleSourceStatus, OracleStatusPayload, OracleTestResult,
} from '@/lib/types';

/** Bağlantı biçimi form durumda AYRI tutulur: yalnız `dsn` doluluğundan
 *  türetilseydi, operatör dsn kutusunu boşaltır boşaltmaz form host kipine
 *  atlar ve yazdığı şey gözünün önünde kaybolurdu. */
type ConnMode = 'host' | 'dsn';

// Faz 2 dilim 1 R16 — `.oracle-*` ailesi (23 kural, yalnız bu sekme) +
// `is-narrow` → Tailwind utility; ui/ atomları → coremetry Field ailesi +
// shadcn; 4 ham onay kutusu → CheckboxField; Kip `<select>` → SelectField;
// test penceresi → coremetry TimeRangePicker `window` (5m/15m/1h — üçü de
// QUICK_PRESETS'te). Değerler globals.css'teki kuralların aynısı
// (--sp-4 8px, --sp-5 10px, --sp-6 12px, --sp-7 16px). Alan sırası,
// metinler, gövdeler ve test/okuma sözleşmeleri aynı.
const TITLE = 'mb-1.5 text-[length:var(--fs-md)] font-semibold';
const NOTE = 'mb-3 text-[length:var(--fs-sm)] text-(--text2)';
const SRC = 'mb-3 rounded-lg border border-(--border) bg-(--bg2) p-4';
const STATUS = 'mb-2.5 text-[length:var(--fs-sm)] text-(--text2)';
const ROW = 'flex flex-wrap gap-2.5';
const COL = 'flex-[1_1_200px]';
const NARROW = 'flex-[0_1_150px]';
const SUB = 'mt-2.5 mb-2 text-[length:var(--fs-xs)] font-semibold uppercase tracking-[.5px] text-(--text2)';
const Q = 'mt-2.5 rounded-[var(--radius-sm)] border border-dashed border-(--border) bg-(--bg1) px-3 py-2.5';
const ACTIONS = 'mt-2.5 flex flex-wrap items-center gap-2';
const SCAN = 'mt-2 text-[length:var(--fs-xs)]';
const SCROLL = 'mt-2 overflow-x-auto';
const SQL = 'mt-2 mb-0 overflow-x-auto rounded-[var(--radius-sm)] border border-(--border) bg-(--bg1) px-2.5 py-2 font-mono text-[length:var(--fs-xs)] text-(--text2)';
const QUIET = 'text-(--text3)';
const ERR = 'text-(--err)';
const MONO_XS = 'font-mono text-[length:var(--fs-xs)] md:text-[length:var(--fs-xs)]';
/** Saf hüküm tonu (oracleProbe ScanTone, pinli) → StatusBadge durumu (D7). */
const TONE: Record<string, Status> = { 'b-warn': 'warn', 'b-err': 'critical', 'b-gray': 'unknown' };
/** Test penceresi (dk) ↔ QUICK_PRESETS anahtarı. */
const windowPreset = (m: OracleTestWindow) => (m === 60 ? '1h' : `${m}m`);
const presetWindow = (p: string): OracleTestWindow => (p === '1h' ? 60 : p === '5m' ? 5 : 15);

interface EditRow {
  src: OracleSource;
  mode: ConnMode;
  /** Sunucudaki karşılığı — id taşıma ve `hasPassword` rozeti için. */
  snapshot?: OracleSourceSnapshot;
}

function rowFromSnapshot(s: OracleSourceSnapshot): EditRow {
  return { src: sourceFromSnapshot(s), mode: s.dsn ? 'dsn' : 'host', snapshot: s };
}

/** ≤3 örnek satır: test cevabı 5 satıra kadar dönebiliyor ama buradaki iş
 *  "tablo gerçekten okunuyor mu"yu göstermek; Aşama 2'nin alan eşlemesi için
 *  kolon LİSTESİ zaten tam. */
const SAMPLE_ROWS = 3;

export function OracleTab() {
  const [rows, setRows] = useState<EditRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [probe, setProbe] = useState<Record<number, OracleTestResult | { pending: true }>>({});
  const [testWindow, setTestWindow] = useState<OracleTestWindow>(15); // v0.10.768 — test penceresi
  // Durum: sekme açılışında bir kez + elle yenile. Poll YOK — ayar sekmesi.
  const [status, setStatus] = useState<OracleStatusPayload | null>(null);
  const [statusErr, setStatusErr] = useState<string | null>(null);
  // v0.10.603 — alan eşlemesi 14 kutu; varsayılan katlı (çoğu kurulum §5 adlarını kullanır).
  const [showCols, setShowCols] = useState<Record<number, boolean>>({});

  const loadStatus = () => {
    setStatusErr(null);
    api.oracleStatus().then(setStatus)
      .catch(e => setStatusErr(e instanceof Error ? e.message : 'durum alınamadı'));
  };
  useEffect(() => { loadStatus(); }, []);

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.oracleSettings(),
    s => setRows((s.sources ?? []).map(rowFromSnapshot)),
  );

  const patch = (i: number, p: Partial<OracleSource>) =>
    setRows(rs => rs.map((r, j) => (j === i ? { ...r, src: { ...r.src, ...p } } : r)));

  /** Satır silme, deneme sonuçlarını da KAYDIRIR. Probe haritası indeksle
   *  anahtarlı: 0. satır silinince 1'in sonucu 0'ın altına düşer ve operatör
   *  BAŞKA bir kaynağın cevabını bu kaynağınki sanardı. */
  const removeRow = (i: number) => {
    setRows(rs => rs.filter((_, j) => j !== i));
    setProbe(p => {
      const next: typeof p = {};
      for (const [k, v] of Object.entries(p)) {
        const idx = Number(k);
        if (idx === i) continue;
        next[idx > i ? idx - 1 : idx] = v;
      }
      return next;
    });
  };

  /** Kip değişimi karşı kipin alanlarını TEMİZLER: sunucu "ya dsn ya
   *  host/serviceName" diyor, ikisi birden dolu kalırsa kaydetme reddedilirdi
   *  ve operatör göremediği bir kutu yüzünden takılırdı. */
  const setMode = (i: number, mode: ConnMode) =>
    setRows(rs => rs.map((r, j) => {
      if (j !== i) return r;
      const src = mode === 'dsn'
        ? { ...r.src, host: '', serviceName: '', port: undefined }
        : { ...r.src, dsn: '', port: r.src.port ?? ORACLE_DEFAULT_PORT };
      return { ...r, mode, src };
    }));

  const errorsFor = (i: number): OracleFieldErrors => validateOracleSource(rows[i].src, {
    others: rows.filter((_, j) => j !== i).map(r => r.src),
    hasStoredPassword: !!rows[i].snapshot?.hasPassword,
  });

  const allErrors = rows.map((_, i) => errorsFor(i));
  const blocked = allErrors.some(hasOracleErrors);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (blocked) {
      setMsg({ kind: 'err', text: 'Kaydedilmedi — kırmızı alanları düzeltin.' });
      return;
    }
    setBusy(true); setMsg(null);
    try {
      const next = await api.putOracleSettings({
        sources: rows.map(r => sourceForSave(r.src, r.snapshot)),
      });
      setRows((next.sources ?? []).map(rowFromSnapshot));
      const on = (next.sources ?? []).filter(s => s.enabled).length;
      setMsg({ kind: 'ok', text: `Kaydedildi — ${on} kaynak etkin.` });
      loadStatus();
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Kaydetme başarısız' });
    } finally {
      setBusy(false);
    }
  };

  // Formdaki değerlerle dener; KAYDETMEZ. Başarısız bir deneme 200 + ok:false
  // ile gelir ve aşağıda cevap olarak çizilir; yalnız ağ/HTTP hatası catch'e
  // düşer ve o da aynı kabın içinde gösterilir (ok:false ile aynı şekil).
  const runTest = async (i: number) => {
    setProbe(p => ({ ...p, [i]: { pending: true } }));
    try {
      const res = await api.testOracleSource(sourceForSave(rows[i].src, rows[i].snapshot), testWindow);
      setProbe(p => ({ ...p, [i]: res }));
    } catch (err) {
      setProbe(p => ({
        ...p,
        [i]: {
          ok: false, passwordResolved: false, rowCount: 0, columns: [],
          error: err instanceof Error ? err.message : 'Test başarısız',
        },
      }));
    } finally {
      loadStatus();
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  return (
    <div className="max-w-[820px]">
      <h2 className={TITLE}>Oracle hata tablosu</h2>
      <p className={NOTE}>
        Bir Oracle hata tablosunu (ör. <code>&lt;ŞEMA&gt;.ERROR_LOG</code>) dış kaynak
        olarak bağlar. Satırlar <b>periyodik olarak okunur</b> (worker lideri, kalıcı
        watermark), Coremetry'de saklanır ve ilgili trace'in Logs sekmesinde <b>oracle</b>
        rozetiyle görünür; hata satırları Exceptions'ta (kaynak · kod · operasyon) grup olarak
        triyaj edilir. Şema, tablo ve kolon
        adları koda gömülü değildir — hepsi buradan yönetilir. Sorgu daima{' '}
        <b>salt-okunur</b>, zaman aralığıyla sınırlı ve satır tavanlıdır; değerler bind
        edilir. Şifre <b>saklanır ama geri gösterilmez</b>; alternatifi referanstır
        (<code>env:AD</code> ya da <code>file:/yol</code>).
      </p>

      <form onSubmit={save}>
        {rows.length === 0 && (
          <div className={Q}>Henüz kaynak yok — aşağıdan ekleyin.</div>
        )}

        {rows.map((r, i) => {
          const src = r.src;
          const err = allErrors[i];
          const pr = probe[i];
          const st: OracleSourceStatus | undefined = src.id
            ? status?.sources.find(x => x.id === src.id)
            : undefined;
          return (
            <div key={i} className={cn(SRC, !src.enabled && 'opacity-65')}>
              {st && (
                <div className={STATUS}>
                  {/* v0.10.929 (K5) — başarılı/çözüldü/sağlıklı arka plan durumu: nötr (is-quiet); renk yalnız is-err'de. */}
                  {/* v0.10.1092 — bu satır Settings'teki TESTİN izi (bu API pod'u,
                      formdaki değerler); okuma sağlığı aşağıdaki "Son okuma (worker)". */}
                  {st.lastCheckAt
                    ? (() => {
                      const v = uiTestVerdict(st.lastCheckOK, st.lastError);
                      return <>Son UI testi (bu API pod'u, formdaki değerler) <b>{fmtDateTime(st.lastCheckAt)}</b>{' · '}
                        <span className={v.ok ? QUIET : ERR}>{v.text}</span></>;
                    })()
                    : <span className={QUIET}>Bu API pod'unda henüz UI testi koşmadı.</span>}
                  {src.passwordRef && (
                    <> · şifre referansı{' '}
                      {st.passwordResolved
                        ? <span className={QUIET}>çözüldü</span>
                        : <span className={ERR}>{st.passwordError || 'çözülemedi'}</span>}</>
                  )}
                </div>
              )}

              {src.id && src.enabled && (() => {
                // v0.10.603 — işçi durumu (v0.10.601 blobu): worker lideri hangi
                // pod'da olursa olsun görünür. Yok = henüz yayın yok; uydurma
                // "sağlıklı" basılmaz.
                const ps: OraclePollStatus | undefined = status?.poll?.sources.find(x => x.sourceId === src.id);
                return (
                  <div className={STATUS}>
                    {ps ? (
                      <>
                        Son okuma (worker) <b>{fmtDateTime(ps.lastPollAt)}</b>
                        {' · '}{ps.lastRows} satır okundu, {ps.lastMapped} yazıldı
                        {(ps.lastExpanded ?? 0) > 0 && <> ({ps.lastExpanded} trace listesinden)</>}
                        {(ps.lastSkipped ?? 0) > 0 && <>, {ps.lastSkipped} değişmediği için yeniden yazılmadı</>}
                        {ps.expandCapped && <> · <StatusBadge status="warn" title="Bir poll'da en çok 50.000 satır trace listesinden açılır; kalan gruplar trace'siz yazıldı (sayı doğru, trace bağlantısı kısmi).">trace listesi tavanı</StatusBadge></>}
                        {ps.lastNoTimestamp > 0 && <> · <span className={ERR}>{ps.lastNoTimestamp} zamansız satır düştü</span></>}
                        {ps.lastBadTraceId > 0 && <> · {ps.lastBadTraceId} geçersiz trace id</>}
                        {' · '}watermark <b>{fmtDateTime(ps.watermarkNs / 1e6)}</b>
                        {/* v0.10.1092 — özel SQL: pencere poll içinde sayfalarla okunur; tavan
                            dolarsa "devam ediyor" DEĞİL (devam eden bir şey yok), kesik. */}
                        {ps.unordered && <> · <StatusBadge status="warn" title="Eşlenen sıralama kolonları (zaman / operasyon / kod …) sorgu çıktısında bulunamadı (ORA-00904); pencere sırasız okundu. Eşlemeyi sorgunun takma adlarıyla düzeltin.">sırasız okuma</StatusBadge></>}
                        {ps.truncated
                          ? <> · <StatusBadge status="warn" title="Poll başına satır tavanı (sayfa tavanı × 5000) doldu; pencerenin kalanı okunmadı. Sorgunun GROUP BY'ını daraltın ya da sayfa tavanını artırın.">{customTruncatedText(ps.pages)}</StatusBadge></>
                          : ps.capped && !ps.unordered && <> · <StatusBadge status="warn">tavana çarptı — devam ediyor</StatusBadge></>}
                        {ps.lastError
                          ? <> · <span className={ERR}>{ps.lastError}</span></>
                          : <> · <span className={QUIET}>okuma sağlıklı</span></>}
                        {status?.poll?.pod && <> · pod <code>{status.poll.pod}</code></>}
                      </>
                    ) : (
                      <span className={QUIET}>
                        İşçi bu kaynak için henüz durum yayınlamadı — kaynak kaydedildi mi, worker lideri koşuyor mu?
                      </span>
                    )}
                  </div>
                );
              })()}

              <div className="mb-2.5 flex flex-wrap items-end gap-2.5">
                <Field className="flex-1" label="Kaynak adı" value={src.name} required
                  onChange={e => patch(i, { name: e.target.value })}
                  placeholder="core-bank" error={err.name}
                  hint={err.name ? undefined : 'Kaynağı bu adla anacağız; tekil olmalı.'} />
                <CheckboxField className="pb-2.5" label="Etkin" checked={src.enabled}
                  onCheckedChange={v => patch(i, { enabled: v })} />
                {src.id && (
                  <Badge variant="outline" className="mb-2.5 font-mono" title="Sunucu sahipli kimlik — yeniden adlandırma korur">
                    {src.id}
                  </Badge>
                )}
                <Button type="button" variant="ghost" size="sm" className="mb-1.5" onClick={() => removeRow(i)}>
                  Kaldır
                </Button>
              </div>

              <div className={SUB}>Bağlantı</div>
              <div className={ROW}>
                <SelectField label="Bağlantı biçimi" className={NARROW} value={r.mode}
                  onChange={e => setMode(i, e.target.value as ConnMode)}
                  hint="Tek parça dsn ile host/servis üçlüsü aynı anda verilemez.">
                  <option value="host">Host / servis</option>
                  <option value="dsn">Tek parça dsn</option>
                </SelectField>
                {r.mode === 'dsn' ? (
                  <Field className={COL} label="DSN" value={src.dsn ?? ''} error={err.dsn}
                    onChange={e => patch(i, { dsn: e.target.value })}
                    placeholder="oracle://kullanıcı:şifre@host:1521/servis"
                    autoComplete="off"
                    hint={err.dsn ? undefined : 'Credential dizenin içindeyse kullanıcı/şifre kutuları boş bırakılabilir.'} />
                ) : (
                  <>
                    <Field className={COL} label="Host" value={src.host ?? ''} error={err.host}
                      onChange={e => patch(i, { host: e.target.value })}
                      placeholder="oradb.internal" />
                    <Field label="Port" className={NARROW} inputMode="numeric" error={err.port}
                      value={numToForm(src.port)}
                      onChange={e => patch(i, { port: numFromForm(e.target.value) })}
                      placeholder={String(ORACLE_DEFAULT_PORT)}
                      hint={err.port ? undefined : `boş = ${ORACLE_DEFAULT_PORT}`} />
                    <Field className={COL} label="Servis adı" value={src.serviceName ?? ''} error={err.serviceName}
                      onChange={e => patch(i, { serviceName: e.target.value })}
                      placeholder="ORCLPDB1" />
                  </>
                )}
              </div>

              <div className={ROW}>
                <Field className={COL} label="Kullanıcı" value={src.user} error={err.user}
                  onChange={e => patch(i, { user: e.target.value })}
                  placeholder="coremetry_ro" autoComplete="off"
                  hint={err.user ? undefined : 'Salt-okunur bir hesap yeterli.'} />
                <Field className={COL} type="password" autoComplete="new-password" error={err.password}
                  label="Şifre" labelAside={r.snapshot?.hasPassword ? <span className="text-[length:var(--fs-sm)] text-(--text3)">· kayıtlı</span> : undefined}
                  value={src.password ?? ''}
                  onChange={e => patch(i, { password: e.target.value })}
                  placeholder={r.snapshot?.hasPassword ? '(saklı değeri korumak için boş bırakın)' : 'Oracle şifresi…'}
                  hint={err.password ? undefined : 'Saklanır, geri gösterilmez. Rotasyon: yenisini yazıp Kaydet.'} />
                <Field className={COL} label="…ya da şifre referansı" value={src.passwordRef ?? ''}
                  onChange={e => patch(i, { passwordRef: e.target.value })}
                  placeholder="env:COREMETRY_ORACLE_PW · file:/var/run/secrets/oracle/pw"
                  error={err.passwordRef ?? r.snapshot?.passwordError}
                  autoComplete="off"
                  hint={err.passwordRef ? undefined
                    : 'Doluysa saklı şifre yerine bu kullanılır ve her kullanımda çözülür.'} />
                {src.passwordRef && r.snapshot?.passwordRef === src.passwordRef && (
                  <span className="flex items-center self-center">
                    {r.snapshot.passwordResolved
                      ? <Badge variant="secondary">referans çözüldü</Badge>
                      : <StatusBadge status="critical">referans çözülemedi</StatusBadge>}
                  </span>
                )}
              </div>

              <div className={SUB}>Sorgu</div>
              {/* v0.10.902 (operatör) — özel SQL kipi: sorgu operatörün metni,
                  salt-okunur sarmalayıcıda (tek SELECT/WITH, FETCH FIRST tavanı);
                  bind yok, pencere sorgunun kendi SYSDATE aralığı. Şema/tablo/tip
                  süzgeci/ek koşul o kipte anlamsız → gizli. */}
              <div className={ROW}>
                <SelectField label="Sorgu kipi" className={NARROW} value={isCustomQuery(src) ? 'custom' : 'table'}
                  onChange={e => patch(i, { queryMode: e.target.value === 'custom' ? 'custom' : 'table' })}
                  hint="Tablo: şema/tablo/zaman kolonundan üretilen pencereli sorgu. Özel SQL: sizin metniniz, olduğu gibi.">
                  <option value="table">Tablo (üretilen sorgu)</option>
                  <option value="custom">Özel SQL (ön-toplanmış / JOIN'li)</option>
                </SelectField>
                {!isCustomQuery(src) && (
                  <>
                    <Field className={COL} label="Şema" value={src.schema} error={err.schema}
                      onChange={e => patch(i, { schema: e.target.value })}
                      placeholder="APP_SCHEMA" />
                    <Field className={COL} label="Tablo" value={src.table} error={err.table}
                      onChange={e => patch(i, { table: e.target.value })}
                      placeholder="ERROR_LOG" />
                  </>
                )}
                <Field className={COL} label={isCustomQuery(src) ? 'Zaman kolonu (çıktı takma adı)' : 'Zaman kolonu'} value={src.timestampColumn ?? ''} error={err.timestampColumn}
                  onChange={e => patch(i, { timestampColumn: e.target.value })}
                  placeholder={isCustomQuery(src) ? 'TIMESLICE' : ORACLE_DEFAULT_TIMESTAMP_COLUMN}
                  hint={err.timestampColumn ? undefined
                    : isCustomQuery(src) ? 'Zorunlu — epoch saniye (TimeSlice) ya da DD.MM.YYYY HH24:MI (Zaman, kaynağın dilimi)'
                      : `boş = ${ORACLE_DEFAULT_TIMESTAMP_COLUMN}`} />
                <Field className={COL} label={isCustomQuery(src) ? 'Tip kolonu (çıktı takma adı)' : 'Tip kolonu'} value={src.typeColumn ?? ''} error={err.typeColumn}
                  onChange={e => patch(i, { typeColumn: e.target.value })}
                  placeholder={isCustomQuery(src) ? 'SONUC' : ORACLE_DEFAULT_TYPE_COLUMN}
                  hint={err.typeColumn ? undefined : isCustomQuery(src) ? 'Yalnız gösterim (süzgeç sorgunuzda)' : `boş = ${ORACLE_DEFAULT_TYPE_COLUMN}`} />
              </div>
              {isCustomQuery(src) ? (
                <>
                  <div className={Q}>
                    <TextareaField label="Özel SQL (tek SELECT / WITH; olduğu gibi koşar)" rows={14} controlClassName={MONO_XS}
                      value={src.customSql ?? ''} error={err.customSql}
                      onChange={e => patch(i, { customSql: e.target.value })}
                      placeholder={"SELECT ROUND((TRUNC(ts,'MI') - DATE '1970-01-01') * 86400) - 10800 AS TimeSlice, …, COUNT(*) AS Adet, … AS TRACEIDS\nFROM …\nWHERE ts >= TRUNC(SYSDATE,'MI') - INTERVAL '15' MINUTE AND ts < TRUNC(SYSDATE,'MI')\nGROUP BY … HAVING COUNT(*) > 1"}
                      hint={err.customSql ? undefined
                        : 'Bind eklenmez: pencereyi (SYSDATE aralığı) ve süzgeçleri sorgu belirler. Poller pencereyi TEK ifadeyle okur: `SELECT * FROM (…) ORDER BY "<zaman>", "<eşlenen anahtar kolonlar>" FETCH FIRST <sayfa tavanı × 5000 + 1> ROWS ONLY` (adlar tırnaklı — eşlemedeki yazımla, olmazsa büyük harf; yorum ve /*+ ipuçları korunur); sondaki `;` düşer. Alan eşlemesi → göster, kutulara çıktı TAKMA ADLARINI yazın (Oracle büyük harfe çevirir): count (sayaç ağırlığı) ← ADET, trace_id listesi ← TRACEIDS, operation.code ← OPERATIONCODE, error.code ← FUNCTIONCODE (sorguda hata kodu yok; fonksiyon kodu Problem başlığında error.code olarak görünür), channel.code ← KANALKOD, host.name ← HOSTNAME. HAVING COUNT(*) > 1 varsa dakikada tek hata sayaçta 0 görünür. Hesap yalnız SELECT yetkili olmalı: sorgu içinden çağrılan fonksiyonların yan etkisini Coremetry kesemez.'} />
                  </div>
                  {/* v0.10.1092 — satır sayısı ipucu (sayfa tavanının nedeni). */}
                  <div className={cn(NOTE, 'mt-1')}>
                    Dakika × kanal × fonksiyon × operasyon × hata × host × instance çok satır üretir; HOSTNAME/INSTANCEID'yi GROUP BY'dan çıkarmak satır sayısını ~10× düşürür.
                  </div>
                  <div className={ROW}>
                    <Field label="Pencere (dk)" className={NARROW} inputMode="numeric" error={err.windowMin}
                      value={numToForm(src.windowMin)}
                      onChange={e => patch(i, { windowMin: numFromForm(e.target.value) })}
                      placeholder={String(ORACLE_DEFAULT_WINDOW_MIN)}
                      hint={err.windowMin ? undefined : `Sorgunuzun INTERVAL'i ile AYNI olmalı — büyük yazılırsa sorgunun görmediği dakikalara 0 yazılır (sahte "düzeldi"); 1-240, boş = ${ORACLE_DEFAULT_WINDOW_MIN}`} />
                    <Field label="Sayfa tavanı" className={NARROW} inputMode="numeric" error={err.maxPages}
                      value={numToForm(src.maxPages)}
                      onChange={e => patch(i, { maxPages: numFromForm(e.target.value) })}
                      placeholder={String(ORACLE_DEFAULT_MAX_PAGES)}
                      hint={err.maxPages ? undefined : `Poll başına satır tavanı, sayfa cinsinden (1 sayfa = 5000 satır); aşılırsa pencere kesik ilan edilir. ${ORACLE_MIN_MAX_PAGES}-${ORACLE_MAX_MAX_PAGES}, boş = ${ORACLE_DEFAULT_MAX_PAGES}`} />
                  </div>
                </>
              ) : (
                <>
                  <div className={ROW}>
                    <Field className={COL} label="Tip süzgeci" error={err.typeFilter}
                      value={typeFilterToText(src.typeFilter)}
                      onChange={e => patch(i, { typeFilter: parseTypeFilter(e.target.value) })}
                      placeholder="T"
                      hint={err.typeFilter ? undefined
                        : 'Virgülle ayrılmış tip kolonu değerleri (bind edilir); boş = T.'} />
                  </div>
                  <div className={Q}>
                    <TextareaField label="Ek koşul (WHERE'e AND ile eklenir)" rows={2} controlClassName={MONO_XS}
                      value={src.extraWhere ?? ''} error={err.extraWhere}
                      onChange={e => patch(i, { extraWhere: e.target.value })}
                      placeholder="ERR_CODE NOT IN ('ERR_020')"
                      hint={err.extraWhere ? undefined
                        : "Serbest ifade; `;` `--` `/*` yasak — bunlar sorgunun zaman yüklemini ve satır tavanını susturur."} />
                  </div>
                </>
              )}

              <div className={SUB}>Zaman</div>
              <div className={ROW}>
                <Field className={COL} label="Zaman dilimi" value={src.timezone ?? ''} error={err.timezone}
                  onChange={e => patch(i, { timezone: e.target.value })}
                  placeholder={ORACLE_DEFAULT_TIMEZONE}
                  hint={err.timezone ? undefined
                    : `Dilimsiz TIMESTAMP bu dilimde okunur; boş = ${ORACLE_DEFAULT_TIMEZONE}. Yanlış dilim = sabit saat kayması.`} />
                <CheckboxField className="self-center" label="Zaman kolonu dilimli (TIMESTAMP WITH TIME ZONE)"
                  checked={!!src.timestampHasZone}
                  onCheckedChange={v => patch(i, { timestampHasZone: v })} />
              </div>

              <div className={SUB}>
                Alan eşlemesi{' '}
                <Button type="button" variant="ghost" size="sm"
                  onClick={() => setShowCols(sc => ({ ...sc, [i]: !sc[i] }))}>
                  {showCols[i] ? 'gizle' : 'göster'}
                </Button>
              </div>
              {/* v0.10.843 (operatör) — kurum tablosunda SELECT * sürücüde
                  düşüyordu; eşleme zaten kolon listesidir, SELECT onu kullansın. */}
              {!isCustomQuery(src) && (
                <CheckboxField className="pb-2.5" checked={!!src.selectMappedOnly}
                  onCheckedChange={v => patch(i, { selectMappedOnly: v })}
                  label={<>SELECT yalnız eşlenen kolonlar (<code>SELECT *</code> yerine) — eşlenmeyen kolonlar attribute olmaz</>} />
              )}
              {/* v0.10.897 (Aşama 3 dilim D) — Problem üretimi: kip / jenerik kodlar /
                  sayılmayan kodlar + öğrenilmiş op→servis haritası (görüntüle · sıfırla). */}
              <div className={cn(SUB, 'mt-3')}>Problem üretimi</div>
              <div className={ROW}>
                <SelectField className={COL} label="Kip" value={src.problemMode ?? 'shadow'}
                  onChange={e => patch(i, { problemMode: e.target.value as OracleSource['problemMode'] })}>
                  <option value="off">Kapalı — sayaç yazar, Problem yok</option>
                  <option value="shadow">Gölge — Problem açılır, alarm yok</option>
                  <option value="live">Canlı — Problem + bildirim</option>
                </SelectField>
                <Field label="Jenerik kodlar (virgül)" className={NARROW} value={(src.genericCodes ?? []).join(', ')}
                  onChange={e => patch(i, { genericCodes: parseTypeFilter(e.target.value) })}
                  hint="external_code / exception tipiyle ayrılan kodlar (ör. ERR_020, BSA_020)" />
                <Field label="Sayılmayan kodlar (virgül)" className={NARROW} value={(src.ignoreCodes ?? []).join(', ')}
                  onChange={e => patch(i, { ignoreCodes: parseTypeFilter(e.target.value) })}
                  hint="Problem üretmez; satır Trace › Logs'ta yine görünür" />
                {/* v0.10.1083 — sürdürme: tek dakikalık kıpırtı Problem (ve ≥10/dk'da P1) açmasın. */}
                <Field label="Sürdürme (dk)" className={NARROW} inputMode="numeric" error={err.dwellMinutes}
                  value={numToForm(src.dwellMinutes)}
                  onChange={e => patch(i, { dwellMinutes: numFromForm(e.target.value) })}
                  placeholder={String(ORACLE_DEFAULT_DWELL_MIN)}
                  hint={err.dwellMinutes ? undefined : `Hata serisi bu kadar ardışık dakika eşik üstünde kalınca Problem açılır; ${ORACLE_MIN_DWELL_MIN}-${ORACLE_MAX_DWELL_MIN}, boş = ${ORACLE_DEFAULT_DWELL_MIN}`} />
              </div>
              {/* v0.10.1000 (operatör teyidi) — Oracle'daki fonksiyon kodu span'lerdeki
                  FUNCTION_CODE ile aynı değer: trace'i olmayan satır da servise bağlanır. */}
              <CheckboxField className="mt-2.5" checked={!!src.functionCodeMatch}
                onCheckedChange={v => patch(i, { functionCodeMatch: v })}
                label={<>Satırın fonksiyon kodu (<code>FUNCTIONCODE</code> kolonu) span'lerdeki <code>FUNCTION_CODE</code> ile
                  aynı değer — servisi bulunamayan satırı o kodu taşıyan span'lerin servisine bağla ve eşlemeyi zamanla
                  öğren (etkisi: özne kapsamı)</>} />
              {src.id && <OracleLearnedLine id={src.id} />}
              {src.id && <OracleCoverageLine id={src.id} />}
              {src.id && <OracleLivePreviewLine id={src.id} />}
              {showCols[i] && (
                <>
                  <div className={Q}>
                    {isCustomQuery(src)
                      ? <>Kolon = sorgunun <b>çıktı takma adı</b> (Oracle büyük harfe çevirir). Boş kutu = alan
                        eşlenmemiş (count/traceIds için kapalı; diğerleri için varsayılan ERR_* adı aranır). Zaman ve
                        tip kolonları yukarıdaki kutulardan. <b>count</b> sayaç ağırlığıdır (Adet; attribute olarak da
                        kalır), <b>traceIds</b> ayırıcılı liste — trace başına satıra patlatılır. Tüketilmeyen her
                        kolon (ZAMAN, DURATION…) attribute olarak kalır.</>
                      : <>Boş kutu = varsayılan kolon; <code>{ORACLE_COLUMN_DISABLED}</code> = bu alan
                        tabloda yok. Zaman ve tip kolonları yukarıdaki kutulardan. Tüketilmeyen her
                        kolon satırda olduğu gibi attribute olarak kalır.</>}
                  </div>
                  {err.columns && <div className={cn(Q, ERR)}>{err.columns}</div>}
                  <div className={ROW}>
                    {ORACLE_MAPPING_FIELDS.filter(f => isCustomQuery(src) || !ORACLE_CUSTOM_ONLY_FIELDS.has(f.field)).map(f => (
                      <Field key={f.field} label={f.target} className={NARROW}
                        value={src.columns?.[f.field] ?? ''}
                        onChange={e => patch(i, { columns: { ...(src.columns ?? {}), [f.field]: e.target.value } })}
                        placeholder={f.column} />
                    ))}
                  </div>
                </>
              )}

              <div className={SUB}>Sınırlar</div>
              <div className={ROW}>
                <Field label="Bağlantı havuzu" className={NARROW} inputMode="numeric" error={err.maxOpenConns}
                  value={numToForm(src.maxOpenConns)}
                  onChange={e => patch(i, { maxOpenConns: numFromForm(e.target.value) })}
                  placeholder={String(ORACLE_DEFAULT_MAX_OPEN_CONNS)}
                  hint={err.maxOpenConns ? undefined : `1-16; boş = ${ORACLE_DEFAULT_MAX_OPEN_CONNS}`} />
                <Field label="Sorgu zaman aşımı (sn)" className={NARROW} inputMode="numeric" error={err.queryTimeoutSec}
                  value={numToForm(src.queryTimeoutSec)}
                  onChange={e => patch(i, { queryTimeoutSec: numFromForm(e.target.value) })}
                  placeholder={String(ORACLE_DEFAULT_QUERY_TIMEOUT_SEC)}
                  hint={err.queryTimeoutSec ? undefined : `5-120; boş = ${ORACLE_DEFAULT_QUERY_TIMEOUT_SEC}`} />
                <Field label="Okuma aralığı (sn)" className={NARROW} inputMode="numeric" error={err.intervalSec}
                  value={numToForm(src.intervalSec)}
                  onChange={e => patch(i, { intervalSec: numFromForm(e.target.value) })}
                  placeholder={String(ORACLE_DEFAULT_INTERVAL_SEC)}
                  hint={err.intervalSec ? undefined : `10-3600; boş = ${ORACLE_DEFAULT_INTERVAL_SEC} (poll aralığı)`} />
              </div>

              <div className={ACTIONS}>
                <Button type="button" variant="accent" size="sm"
                  disabled={busy || !!(pr && 'pending' in pr)}
                  onClick={() => { void runTest(i); }}>
                  Bağlantıyı dene
                </Button>
                {isCustomQuery(src) ? (
                  <span className={QUIET}>pencere: sorgunun SYSDATE aralığı ({src.windowMin || ORACLE_DEFAULT_WINDOW_MIN} dk)</span>
                ) : (
                  <TimeRangePicker variant="window" label="Test penceresi"
                    presets={ORACLE_TEST_WINDOWS.map(windowPreset)}
                    value={{ preset: windowPreset(testWindow) }}
                    onChange={r => setTestWindow(presetWindow(r.preset))} />
                )}
                {pr && 'pending' in pr && <span className={QUIET}>deneniyor…</span>}
              </div>

              {pr && !('pending' in pr) && (
                <div className="mt-2.5 text-[length:var(--fs-sm)]">
                  <FlashBox kind={pr.ok ? 'ok' : 'err'}>
                    {pr.ok
                      ? `Bağlantı kuruldu — son ${pr.windowMin ?? 15} dakikada ${pr.summary && !pr.summary.error ? `${pr.summary.rows}${pr.summary.capped ? '+' : ''}` : pr.rowCount} satır okundu.`
                      : (pr.error || 'Başarısız')}
                    {' · '}şifre {pr.passwordResolved ? 'çözüldü' : 'çözülemedi'}
                    {pr.latencyMs !== undefined && <> · {pr.latencyMs} ms</>}
                  </FlashBox>
                  {pr.hint && <div className={cn(Q, QUIET)}>{pr.hint}</div>}
                  {/* v0.10.845 — LONG kolon hükmü; test DÜŞSE de görünür (ORA-00997'nin
                      sebebi hatanın yanında dursun). */}
                  {(() => {
                    const lv = longVerdict(pr.long);
                    return lv && (
                      <div className={SCAN}>
                        <StatusBadge status={TONE[lv.tone] ?? 'unknown'} title={lv.detail}>{lv.text}</StatusBadge>
                        <span className={QUIET}> {lv.detail}</span>
                      </div>
                    );
                  })()}
                  {/* v0.10.886 (operatör) — "trace bulunamadı diyor ama var, kolon adlarından mı?"
                      Eşlenen kolon tabloda yoksa burada söylenir; önekli karşılık tek tıkla. */}
                  {(() => {
                    const mv = mappingVerdict(pr.mapping);
                    if (!mv) return null;
                    const suggest = Object.fromEntries((pr.mapping?.missing ?? []).filter(x => x.suggest).map(x => [x.field, x.suggest!]));
                    return (
                      <div className={SCAN}>
                        <StatusBadge status={TONE[mv.tone] ?? 'unknown'} title={mv.detail}>{mv.text}</StatusBadge>
                        <span className={QUIET}> {mv.detail}</span>
                        {Object.keys(suggest).length > 0 && (
                          <Button size="sm" variant="outline" className="ml-2"
                            onClick={() => patch(i, { columns: { ...(src.columns ?? {}), ...suggest } })}>
                            Önerilen eşlemeyi uygula
                          </Button>
                        )}
                      </div>
                    );
                  })()}
                  {(pr.columns?.length ?? 0) > 0 && (
                    <div className={SCROLL}>
                      <div className={SUB}>Kolonlar ({pr.columns!.length})</div>
                      <code>{pr.columns!.join(', ')}</code>
                    </div>
                  )}
                  {(pr.sample?.length ?? 0) > 0 && (
                    <div className={SCROLL}>
                      {/* v0.10.942 — statik tablo: sabit ≤3 örnek satır, sıralanmaz (T1). */}
                      <table className="text-[length:var(--fs-xs)]">
                        <thead>
                          <tr>{(pr.columns ?? []).map(c => <th key={c}>{c}</th>)}</tr>
                        </thead>
                        <tbody>
                          {pr.sample!.slice(0, SAMPLE_ROWS).map((row, ri) => (
                            <tr key={ri}>
                              {(pr.columns ?? []).map(c => (
                                <td key={c} className="font-mono">{row[c] ?? ''}</td>
                              ))}
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                  {/* v0.10.768 — tam tarama hükmü + pencere özeti + poller sorgusu.
                      Operatör: "full scan / kilit olmasın, önce test edelim, sorgu
                      görünür olsun; hangi servis/operasyon/trace geldi". */}
                  {pr.ok && pr.scan && (() => {
                    const sv = scanVerdict(pr.scan);
                    return (
                      <div className={SCAN}>
                        <StatusBadge status={TONE[sv.tone] ?? 'unknown'} title={sv.detail}>{sv.text}</StatusBadge>
                        <span className={QUIET}> {sv.detail}</span>
                        {/* v0.10.885 — pencere bind'i kolon tipine göre; sözlük okunamadıysa söyle. */}
                        {pr.scan?.tsBind && (
                          <span className={QUIET}> · zaman bind'i: {pr.scan.tsType || 'tip okunamadı, kutuya göre'} → {pr.scan.tsBind}(:1)</span>
                        )}
                        {sv.tone === 'b-err' && (
                          <div className={QUIET}>
                            Kaynağı etkinleştirmeden önce zaman kolonuna indeks (ya da partition) ekletin; yoksa her
                            poll tabloyu baştan sona okur. SELECT kilit almaz; FOR UPDATE üretilmez ve reddedilir.
                          </div>
                        )}
                      </div>
                    );
                  })()}
                  {pr.ok && pr.summary && (
                    <div className="mt-2 text-[length:var(--fs-xs)]">
                      <div className={SUB}>{summaryHeadline(pr.summary)}{(pr.summary.expanded ?? 0) > 0 && <> · {pr.summary.expanded} satır trace listesinden</>}</div>
                      {!pr.summary.error && (
                        <div className={ROW}>
                          <div className="flex-[1_1_160px]">
                            <div className={SUB}>Operasyon kodu</div>
                            {pr.summary.operations.length === 0
                              ? <span className={QUIET}>—</span>
                              : pr.summary.operations.map(o => <div key={o.name} className="font-mono">{o.name} · {o.count}</div>)}
                          </div>
                          <div className="flex-[1_1_160px]">
                            <div className={SUB}>Hata kodu</div>
                            {pr.summary.errorCodes.length === 0
                              ? <span className={QUIET}>—</span>
                              : pr.summary.errorCodes.map(o => <div key={o.name} className="font-mono">{o.name} · {o.count}</div>)}
                          </div>
                          <div className="flex-[1_1_160px]">
                            <div className={SUB} title="v0.10.892 — trace'te hata veren en derin span'ın servisi; hata span'ı yoksa kök servis (kanal). Kök tek başına hep giriş noktasını gösteriyordu.">Servis (trace'te hata veren span'ın servisi; yoksa kök)</div>
                            {!pr.summary.lookupDone
                              ? <span className={QUIET}>arama yapılmadı</span>
                              : pr.summary.services.length === 0
                                ? <span className={QUIET}>trace bulunamadı</span>
                                : pr.summary.services.map(o => <div key={o.name} className="font-mono">{o.name} · {o.count} trace</div>)}
                            {/* v0.10.908 — instance kolonundaki pod adından (canlı doğrulanmış) */}
                            {(pr.summary.podsSeen ?? 0) > 0 && (
                              <>
                                <div className={cn(SUB, 'mt-2')} title="Pod adından ReplicaSet/pod eki atılır, '-prod' öneki denenir; yalnız Coremetry'de son 24 saatte canlı servis adı kabul edilir.">Pod adından servis</div>
                                {(pr.summary.podServices ?? []).map(o => <div key={o.name} className="font-mono">{o.name} · {o.count} pod</div>)}
                                {(pr.summary.podsUnmatched ?? 0) > 0 && <div className={QUIET}>{pr.summary.podsUnmatched} pod Coremetry'deki bir servisle eşleşmedi</div>}
                              </>
                            )}
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                  {pr.query && (
                    <>
                      <div className={SUB}>Test sorgusu</div>
                      <pre className={SQL}>{pr.query}</pre>
                    </>
                  )}
                  {pr.pollQuery && (
                    <>
                      <div className={SUB}>Poller'ın koşacağı sorgu ({(pr.pollBinds?.length ?? 0) > 0 ? `bind: ${pr.pollBinds!.join(' · ')}` : 'bind yok — pencere sorgunun kendi SYSDATE aralığı'})</div>
                      <pre className={SQL}>{pr.pollQuery}</pre>
                    </>
                  )}
                </div>
              )}
            </div>
          );
        })}

        <div className={ACTIONS}>
          <Button type="button" variant="outline" size="sm"
            onClick={() => setRows(rs => [...rs, { src: emptyOracleSource(), mode: 'host' }])}>
            + Kaynak ekle
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={loadStatus}>
            Durumu yenile
          </Button>
          {statusErr && <span className={ERR}>{statusErr}</span>}
          <Button type="submit" size="sm" loading={busy} disabled={blocked}>
            Kaydet
          </Button>
          {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
        </div>
      </form>
    </div>
  );
}

// OracleCoverageLine — v0.10.999 (Oracle odak "2"): özne kapsamı — satırların
// servise bağlanma oranı ve bağlanmayanların nedeni (oracleProbe.coverageText,
// saf). İstek yalnız açılınca gider; KAYITLI kaynağı okur.
function OracleCoverageLine({ id }: { id: string }) {
  const [open, setOpen] = useState(false);
  const q = useQuery({ queryKey: ['oracle-subject-coverage', id], queryFn: () => api.oracleSubjectCoverage(id), staleTime: 60_000, enabled: open });
  const t = q.data ? coverageText(q.data) : null;
  return (
    <div className={cn(Q, 'mt-1.5')}>
      Özne kapsamı (satır → servis){' '}
      <Button size="sm" variant="ghost" aria-expanded={open} onClick={() => setOpen(o => !o)}>{open ? 'gizle' : 'göster'}</Button>
      {open && q.isLoading && <span className={QUIET}> okunuyor…</span>}
      {open && q.isError && <div className={cn(Q, ERR)}>özne kapsamı okunamadı — kaynak kayıtlı mı?</div>}
      {open && t && (
        <div className="mt-1.5">
          <div className={t.warn ? ERR : undefined}>{t.headline}</div>
          {t.lines.map((line, i) => <div key={i} className={QUIET}>{line}</div>)}
          {t.ops.length > 0 && (
            <div className={cn(SCROLL, 'mt-1.5')}>
              {t.ops.map((line, i) => <div key={i} className="font-mono">{line}</div>)}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// OracleLivePreviewLine — v0.10.998 (operatör kararı: kaynak kipi gölge →
// canlı): "canlıya alırsam kaç bildirim gelir, kime gider" — gölgede açılan
// Problem sayıları + bu türü alan kanallar. KAYITLI kaynağı okur (taslağı
// değil); istek yalnız açılınca gider (ES-cost disiplini: prefetch yok).
// Metin oracleProbe.livePreviewText'te (saf, tablo testli).
function OracleLivePreviewLine({ id }: { id: string }) {
  const [open, setOpen] = useState(false);
  const q = useQuery({ queryKey: ['oracle-live-preview', id], queryFn: () => api.oracleLivePreview(id), staleTime: 60_000, enabled: open });
  const t = q.data ? livePreviewText(q.data) : null;
  return (
    <div className={cn(Q, 'mt-1.5')}>
      Canlıya geçiş önizlemesi{' '}
      <Button size="sm" variant="ghost" aria-expanded={open} onClick={() => setOpen(o => !o)}>{open ? 'gizle' : 'göster'}</Button>
      {open && q.isLoading && <span className={QUIET}> okunuyor…</span>}
      {open && q.isError && <div className={cn(Q, ERR)}>önizleme okunamadı — kaynak kayıtlı mı?</div>}
      {open && t && (
        <div className="mt-1.5">
          <div className={t.warn ? ERR : undefined}>{t.headline}</div>
          {t.lines.map((line, i) => <div key={i} className={QUIET}>{line}</div>)}
        </div>
      )}
    </div>
  );
}

// OracleLearnedLine — v0.10.897: öğrenilmiş op→servis haritası (kaynak
// kaydedildikten sonra; blob sunucuda). Sıfırlama audit'li.
function OracleLearnedLine({ id }: { id: string }) {
  const q = useQuery({ queryKey: ['oracle-learned', id], queryFn: () => api.oracleLearned(id), staleTime: 60_000 });
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  if (q.isError) return <div className={cn(Q, ERR)}>öğrenilmiş harita okunamadı</div>;
  const n = q.data?.count ?? 0;
  const entries = Object.entries(q.data?.entries ?? {}).sort((a, b) => b[1].hits - a[1].hits);
  return (
    <div className={cn(Q, 'mt-1.5')}>
      Öğrenilmiş eşleme: <b>{n}</b> operasyon → servis
      {n > 0 && <> · <Button size="sm" variant="ghost" onClick={() => setOpen(o => !o)}>{open ? 'gizle' : 'görüntüle'}</Button></>}
      {' '}<Button size="sm" variant="ghost-danger" disabled={busy || n === 0}
        onClick={() => { setBusy(true); void api.oracleLearnedReset(id).finally(() => { setBusy(false); void q.refetch(); }); }}>sıfırla</Button>
      {open && entries.length > 0 && (
        <div className={cn(SCROLL, 'mt-1.5')}>
          {entries.slice(0, 200).map(([op, e]) => (
            <div key={op} className="font-mono">{op} → {e.service} · {e.hits}/{e.total}</div>
          ))}
        </div>
      )}
    </div>
  );
}
