/**
 * StatePathRebuild — v0.10.965 — "State tablolarının ZK yolu" bloğu
 * (Admin → ClickHouse → Replika tutarlılığı; operatör kararı 2026-09-27:
 * "sihirbaza ekle … düzeltmesi ve kontrolü").
 *
 * Denetim sunucuda (chstore.statePathCheckFor, raporun kendi okumaları);
 * burada liste, kilit rozeti ve kural 3 açıklaması. Düzeltme: seçilenler
 * ON CLUSTER … SYNC ile düşer, her host'ta gittiği yoklanır, sonra hepsi
 * birleşik yolda kurulur ve doğrulanır. Veri TAŞINMAZ (ingest_ledger ve
 * ai_eval_runs için kayıp operatör kararıyla kabul). Plan salt okuma; apply
 * taze ölçer ve plandaki durum/satırı onayla karşılaştırır.
 *
 * Yalnız atomlar (Button, Modal, SectionHead, badge sınıfları) ve tema
 * token'ları; yeni tablo elemanı yok (T1 ratchet tavanı) — satırlar ul/li.
 */
import { useId, useState } from 'react';
import { Button, Modal, SectionHead } from '@/components/ui';
import { api, apiErrorDetail, UnauthorizedError } from '@/lib/api';
import type { CHStatePathCheck, CHStatePathRebuildPlan, CHStatePathRebuildResult } from '@/lib/types';
import {
  ackFromPlan, actionLabel, classLabel, defaultSelection, fmtRows, groupHostsTitle, groupSummary,
  kindLabel, lockAfterSelection, phaseLabel, rowsToDelete, stateLabel,
} from './statePaths';

export interface StatePathBlockProps {
  check: CHStatePathCheck;
  cluster: string;
  /** Apply sonrası (200 ya da cevapsız kalan istek) kartı yeniden ölçer; 400/401/403/409 reddinde çağrılmaz. */
  onDone: () => void;
}

// v0.10.965 — apply sürerken diyalog kapanmaz (Esc / × / arka plan): DDL sunucuda
// 12 dk'ya kadar sürer ve kapatmak onu DURDURMAZ; operatör iptal ettiğini sanıp
// metnin yasakladığı rollout/restart'ı yapmasın.
const noop = () => {};

const listStyle = { listStyle: 'none', padding: 0, margin: '6px 0' } as const;
const rowStyle = { padding: '4px 0', borderTop: '1px solid var(--border)' } as const;
const lineStyle = { display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' } as const;
const checkStyle = { display: 'inline-flex', alignItems: 'center', gap: 6 } as const;

export function StatePathBlock({ check, cluster, onDone }: StatePathBlockProps) {
  // Seçim DIŞLANANLAR olarak tutulur: kart yeniden ölçülünce yeni listede
  // varsayılan (izinli her tablo) kendiliğinden gelir.
  const [excluded, setExcluded] = useState<ReadonlySet<string>>(() => new Set());
  const [partialAck, setPartialAck] = useState(false);
  const [planBusy, setPlanBusy] = useState(false);
  const [plan, setPlan] = useState<CHStatePathRebuildPlan | null>(null);
  const [ack, setAck] = useState(false);
  // v0.10.965 — kısmi onay TAZE plandan: kartın ölçümü bayat olabilir (bu
  // arada açılan bir pod kural 3 ile yeni bir tabloyu eski yola kurmuş, ya da
  // o an erişilemeyen host'taki eski tablo görünmemiş olabilir). Plan kilidin
  // kapalı kalacağını söylüyorsa onay diyalogda istenir; kartta verilen onay
  // yalnız tohumdur (normal kısmi akış iki kez sormaz).
  const [planPartialAck, setPlanPartialAck] = useState(false);
  const [applying, setApplying] = useState(false);
  const [result, setResult] = useState<{ res: CHStatePathRebuildResult; hosts: number } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const totalId = useId();

  const rebuildable = defaultSelection(check);
  const selected = rebuildable.filter(t => !excluded.has(t));
  const lock = lockAfterSelection(check, selected);
  const needsPartialAck = !lock.opens;
  const legacyCount = check.legacy.filter(t => t.kind !== 'absent').length;
  const absentCount = check.legacy.filter(t => t.kind === 'absent').length;
  const busy = planBusy || applying;

  const toggle = (table: string, on: boolean) => {
    setExcluded(prev => {
      const next = new Set(prev);
      if (on) next.delete(table); else next.add(table);
      return next;
    });
  };
  const closePlan = () => { setPlan(null); setAck(false); setPlanPartialAck(false); };
  const openPlan = async () => {
    setPlanBusy(true); setError(null); setResult(null); setAck(false);
    try {
      const p = await api.chStatePathRebuildPlan(selected);
      setPlanPartialAck(needsPartialAck && partialAck);
      setPlan(p);
    }
    catch (e: unknown) { setError(apiErrorDetail(e).message); }
    finally { setPlanBusy(false); }
  };
  const apply = async () => {
    if (!plan) return;
    setApplying(true); setError(null);
    try {
      const res = await api.chStatePathRebuildApply({
        cluster,
        tables: plan.tables.map(t => t.table),
        ack: ackFromPlan(plan),
        partialOK: !plan.lockOpensAfter && planPartialAck,
      });
      setResult({ res, hosts: plan.hosts });
      closePlan();
      onDone();
    } catch (e: unknown) {
      const raw = e instanceof Error ? e.message : String(e);
      // v0.10.965 — Yalnız sunucunun KENDİ reddi (400 girdi, 401 oturum, 403 rol, 409 kapı) "hiçbir ifade koşmadı"
      // demektir. Geçit/proxy zaman aşımı (502/503/504), istemci zaman aşımı ya da kopan bağlantıda apply sunucuda
      // SÜRÜYOR olabilir (context.WithoutCancel, 12 dk): operatöre söylenir ve kart yeniden ölçülür.
      if (e instanceof UnauthorizedError || /^HTTP (400|403|409):/.test(raw)) {
        setError(apiErrorDetail(e).message);
      } else {
        const why = raw.startsWith('HTTP ') ? raw.slice(0, raw.indexOf(':'))
          : raw.startsWith('Request timed out') ? 'istemci zaman aşımı' : 'bağlantı koptu';
        setError(`Sunucudan cevap alınamadı (${why}) — çalıştırma sunucuda sürüyor olabilir (en çok 12 dk). Bu sırada rollout/restart yapma; birkaç dakika sonra kartı yeniden ölç, yarım kalan iş yeniden planla → çalıştır ile tamamlanır.`);
        onDone();
      }
      closePlan();
    } finally { setApplying(false); }
  };

  const planRows = plan ? rowsToDelete(plan) : 0;
  const planBlocked = plan?.blocked ?? [];
  // v0.10.965 — düşürme olmayan plan (yarım çalıştırmanın devamı, yalnız eksik
  // tablolar): "0 tabloyu SİLMEYİ onaylıyorum" / "Düşür" denmez; başlık ATLA
  // satırlarını saymaz; tabloya özgü veri uyarıları yalnız o tablo düşecekse.
  const createOnly = !!plan && plan.drops.length === 0;
  const workCount = plan ? plan.tables.filter(t => t.action !== 'skip').length : 0;
  const dropsTable = (name: string) => !!plan?.tables.some(t => t.table === name && t.action === 'rebuild');
  const needsPlanPartial = !!plan && !plan.lockOpensAfter;
  return (
    <div role="group" aria-label="State tablolarının ZK yolu" style={{ margin: '8px 0 12px' }}>
      <SectionHead title="State tablolarının ZK yolu" badges={<>
        {check.lockOpen
          ? <span className="badge b-ok" title={check.lockReason}>kilit açık · yeni state tabloları birleşik yola kurulur</span>
          : <span className="badge b-err" title={check.lockReason}>kilit KAPALI · {legacyCount} state tablosu eski yolda</span>}
        {absentCount > 0 && <span className="badge b-warn">{absentCount} tablo birleşik yolda eksik</span>}
      </>} meta={<span className="cell-hint">{check.unified} birleşik</span>} />
      {!check.lockOpen && (
        <p className="cell-hint">
          Bu tablolar eski (shard&apos;lı) ZK yolunda: her shard ayrı bir replikasyon grubu, uygulama bağlandığı host&apos;un yarısını görür.
          Biri bile eski yolda durdukça boot, HİÇ var olmayan yeni state tablolarını da eski yola kurar (kural 3) — sonradan eklenen her
          tablo bölünmüş doğar. Hepsi birleşik yola geçince kilit bir sonraki açılışta kendiliğinden açılır.
        </p>
      )}
      {!check.complete && (
        <div role="alert" className="cell-hint" style={{ color: 'var(--err)' }}>
          Uyarı: küme tanımındaki {check.unreachable} host cevap vermedi — liste eksik olabilir; yeniden kurulum tüm host&apos;lar erişilebilir olmadan çalışmaz.
        </div>
      )}
      {check.legacy.length > 0 && (
        <ul style={listStyle} aria-label="Birleşik yolda olmayan state tabloları">
          {check.legacy.map(t => (
            <li key={t.table} style={rowStyle}>
              <div style={lineStyle}>
                {t.rebuildable ? (
                  <label className="mono" style={checkStyle}>
                    <input type="checkbox" checked={!excluded.has(t.table)} disabled={busy}
                      onChange={e => toggle(t.table, e.target.checked)} />
                    {t.table}
                  </label>
                ) : <span className="mono">{t.table}</span>}
                <span className={`badge ${t.kind === 'absent' ? 'b-warn' : 'b-err'}`}>{kindLabel(t.kind)}</span>
                {t.class && <span className="badge b-gray">{classLabel(t.class)}</span>}
                <span className="cell-hint" title={groupHostsTitle(t)}>{fmtRows(t.rows)} satır · {groupSummary(t)}</span>
              </div>
              {t.rebuildable
                ? t.classNote && <div className="cell-hint">{t.classNote}</div>
                : <div className="cell-hint" style={{ color: 'var(--warn)' }}>bu sihirbazın izin listesinde değil — kilidi kapalı tutar (0009/0010 runbook&apos;u ya da kalıntı temizliği)</div>}
            </li>
          ))}
        </ul>
      )}
      {rebuildable.length > 0 && needsPartialAck && (
        <div role="status" className="cell-hint" style={{ color: 'var(--warn)' }}>
          Seçim dışı kalan eski tablolar: {lock.stillLegacy.join(', ')} — kilit KAPALI kalır; boot yeni state tablolarını yine eski yola kurar.
          Düşürülüp kurulurken açılan bir pod bu tabloları da eski yola kurabilir.
          <label style={{ ...checkStyle, marginLeft: 8, color: 'var(--text3)' }}>
            <input type="checkbox" checked={partialAck} disabled={busy} onChange={e => setPartialAck(e.target.checked)} />
            Kilidin kapalı kalacağını anlıyorum
          </label>
        </div>
      )}
      {rebuildable.length > 0 && (
        <div style={{ ...lineStyle, marginTop: 6 }}>
          <Button variant="accent" size="sm" loading={planBusy}
            disabled={selected.length === 0 || (needsPartialAck && !partialAck) || busy}
            title="Plan salt okuma: katı ölçüm (skip_unavailable_shards yok), kapılar ve koşacak ifadeler; çalıştırma ayrı onay ister"
            onClick={() => void openPlan()}>Yeniden kurulumu planla ({selected.length} tablo)</Button>
        </div>
      )}
      {error && <div role="alert" className="cell-hint" style={{ color: 'var(--err)' }}>{error}</div>}
      {result && (result.res.ok ? (
        <div role="status" className="cell-hint">
          Tamam: {result.res.tables.filter(t => t.verified).length} tablo birleşik yolda doğrulandı ({result.hosts}/{result.hosts} host).
          Kilit: {result.res.lockOpen ? 'açık' : `KAPALI — ${result.res.stillLegacy.join(', ')}`}. {result.res.note}
        </div>
      ) : (
        <>
          <div role="alert" className="cell-hint" style={{ color: 'var(--err)' }}
            title={result.res.statements.map(s => `${s.ok ? '✓' : '✗'} ${s.head}${s.err ? ` — ${s.err}` : ''}`).join('\n')}>
            Yarıda kaldı ({phaseLabel(result.res.phase)}): {result.res.resume}
          </div>
          {/* v0.10.965 — hata metni yalnız title'da kalmaz: klavye/dokunmatik/ekran okuyucu için görünür liste (0015 StmtResultList emsali). */}
          {result.res.statements.length > 0 && (
            <details open={result.res.statements.some(s => !s.ok)} style={{ marginTop: 4 }}>
              <summary style={{ cursor: 'pointer', fontSize: 11 }}>Koşan ifadeler ({result.res.statements.length})</summary>
              <ul aria-label="Koşan ifadeler" style={{ margin: 0, paddingLeft: 18, fontSize: 'var(--fs-xs)' }}>
                {result.res.statements.map((s, i) => (
                  <li key={i} className="mono" style={{ color: s.ok ? 'var(--text2)' : 'var(--err)' }}>
                    {s.ok ? '✓' : '✗'} {s.head}{s.err ? ` — ${s.err}` : ''}
                  </li>
                ))}
              </ul>
            </details>
          )}
        </>
      ))}
      {plan && (
        <Modal open size="lg" title={`State tablolarını birleşik yola yeniden kur — ${workCount} tablo`} onClose={applying ? noop : closePlan} footer={
          <>
            <Button variant="secondary" size="sm" disabled={applying} onClick={closePlan}>Vazgeç</Button>
            <Button variant="danger" size="sm" disabled={!ack || planBlocked.length > 0 || applying || (needsPlanPartial && !planPartialAck)} loading={applying}
              onClick={() => void apply()}>{createOnly ? 'Birleşik yolda kur' : 'Düşür ve yeniden kur'}</Button>
          </>
        }>
          {applying && (
            <div role="status" className="cell-hint" style={{ color: 'var(--warn)' }}>
              Çalışıyor: seçilen tablolar düşürülüp birleşik yolda kuruluyor (en çok 12 dk). Bitene kadar rollout/pod restart yapma.
            </div>
          )}
          {createOnly ? (
            <p style={{ fontSize: 12 }}>
              Düşürülecek tablo yok: seçilenler birleşik yolda ({plan.zkPrefix}/state/{'<ad>'}, replika {'{shard}-{replica}'}) kurulur ve her host&apos;ta doğrulanır.
            </p>
          ) : (
            <p style={{ fontSize: 12 }}>
              Sıra: seçilen tabloların HEPSİ ON CLUSTER … SYNC ile düşürülür, her host&apos;ta gittiği doğrulanır; sonra HEPSİ birleşik yolda
              ({plan.zkPrefix}/state/{'<ad>'}, replika {'{shard}-{replica}'}) yeniden kurulur ve her host&apos;ta doğrulanır. Veri TAŞINMAZ.
            </p>
          )}
          <ul style={listStyle} aria-label="Plan tabloları">
            {plan.tables.map(t => (
              <li key={t.table} style={rowStyle}>
                <span className="mono">{t.table}</span> · {stateLabel(t.state)} · {actionLabel(t.action)} · silinecek {fmtRows(t.action === 'rebuild' ? t.rows : 0)} satır · {classLabel(t.class)}
                {t.detail && <div className="cell-hint" style={{ color: t.action === 'blocked' ? 'var(--err)' : 'var(--text3)' }}>{t.detail}</div>}
                {t.classNote && <div className="cell-hint">{t.classNote}</div>}
              </li>
            ))}
          </ul>
          <p id={totalId} style={{ fontSize: 12 }}>Toplam silinecek: {fmtRows(planRows)} satır (aktif parçalar; birleşmeden önceki fiziksel sayım).</p>
          <div className="cell-hint" style={{ color: 'var(--warn)' }}>Çalışırken Coremetry rollout&apos;u ya da pod yeniden başlatması yapma: bu sırada açılan bir pod eksik tabloyu eski yola kurabilir.</div>
          {dropsTable('ingest_ledger') && <div className="cell-hint" style={{ color: 'var(--warn)' }}>ingest_ledger boşluğunda ingest pod&apos;larının dakikalık defter yazımları düşer (ingest etkilenmez); Filo mutabakatı yeni satırlarla yeniden başlar.</div>}
          {dropsTable('ai_eval_runs') && <div className="cell-hint" style={{ color: 'var(--warn)' }}>ai_eval_runs: çalışan bir evalset koşusu varsa reddedilir; silinen skorlar geri gelmez.</div>}
          {plan.warnings.map(w => <div key={w} className="cell-hint" style={{ color: 'var(--warn)' }}>Uyarı: {w}</div>)}
          {planBlocked.map(b => <div key={b} role="alert" className="cell-hint" style={{ color: 'var(--err)' }}>Engel: {b}</div>)}
          {plan.checks.map(c => <div key={c} className="cell-hint">✓ {c}</div>)}
          <details style={{ marginTop: 6 }}>
            <summary style={{ cursor: 'pointer', fontSize: 11 }}>Koşacak ifadeler ({plan.drops.length + plan.creates.length})</summary>
            <pre className="mono" style={{ fontSize: 11, whiteSpace: 'pre-wrap', margin: '4px 0 0' }}>{[...plan.drops, ...plan.creates].join(';\n\n')}</pre>
          </details>
          {needsPlanPartial && (
            <div className="cell-hint" style={{ color: 'var(--warn)' }}>
              Kilit bu çalıştırmadan sonra da KAPALI: {plan.stillLegacyAfter.join(', ')}
              {/* v0.10.965 — plan taze ve katı; kartın tahmini bayat olabilir, partialOK planın lockOpensAfter'ından sürülür. */}
              <label style={{ ...checkStyle, marginLeft: 8, color: 'var(--text3)' }}>
                <input type="checkbox" checked={planPartialAck} disabled={applying} onChange={e => setPlanPartialAck(e.target.checked)} />
                Kilidin kapalı kalacağını anlıyorum
              </label>
            </div>
          )}
          <label style={{ ...checkStyle, fontSize: 12, marginTop: 8 }}>
            <input type="checkbox" checked={ack} disabled={planBlocked.length > 0 || applying} aria-describedby={totalId}
              onChange={e => setAck(e.target.checked)} />
            {createOnly
              ? `${plan.creates.length} tabloyu birleşik yolda kurmayı onaylıyorum (silinecek veri yok).`
              : `${plan.drops.length} tabloyu ve ${fmtRows(planRows)} satırı SİLMEYİ onaylıyorum; veri geri gelmez.`}
          </label>
        </Modal>
      )}
    </div>
  );
}
