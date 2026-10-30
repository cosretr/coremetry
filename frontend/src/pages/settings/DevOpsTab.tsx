import { useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { Field, SelectField, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { useConfirm } from '@/components/coremetry/Confirm';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { useSettingsLoad, SettingsLoadError, ConfigStatusBanner } from './shared';
import { fmtDateTime } from '@/lib/utils';
import { FunctionCodeShapePanel } from './FunctionCodeShapePanel';
import type { DevOpsFlavor, DevOpsResolveDryRun, DevOpsTestResult, SchemaCatalogSummary } from '@/lib/types';

// DevOpsTab — Azure DevOps Server / TFS bağlantısı (v0.9.829),
// v0.9.830'da adlandırma konvansiyonu eklendi.
//
// TempoTab şablonu: PAT hiç geri dönmez (hasPat = "kayıtlı"
// göstergesi), boş bırakılan PAT alanı saklı değeri korur, TLS
// atlama kutusu aynı uyarı metniyle.
//
// Konvansiyon alanları VİRGÜLLE yazılır, sunucuya dizi gider.
// Alan boş bırakılırsa sunucu nil kaydeder ve varsayılan uygulanır;
// snapshot çözülmüş değeri geri döndüğü için kutu asla boş kalmaz —
// operatör çözücünün gerçekte neyi soyduğunu ekranda görür.
// splitList — "svc-, svc-" → ['svc-','svc-']. Boş girdi [] döner ve
// sunucu onu nil'e çevirip varsayılana düşer (cleanConventionList).
// Faz 2 dilim 1 R13 — inline stil → Tailwind utility; elle yazılmış
// <label><div/><input/></label> → coremetry Field ailesi; ui/ atomları →
// shadcn/coremetry. Alan sırası, metinler, gövde ve PAT sözleşmesi aynı
// (DevOpsTab.secret.test). Kart = D3 (bg2 + border + radius 8 + p-4).
const CARD = 'mt-[18px] rounded-lg border border-(--border) bg-(--bg2) p-4';
const H2 = 'mb-1.5 text-[length:var(--fs-lg)] font-semibold';
const H3 = 'mb-1.5 text-[length:var(--fs-md)] font-semibold';
const ASIDE = 'text-[length:var(--fs-sm)] text-(--text3)';
const NUM = 'w-[120px] tabular-nums';

function splitList(s: string): string[] {
  return s.split(',').map(x => x.trim()).filter(Boolean);
}

// v0.10.1038 — kod bütçesi sınırları; tek kaynak sunucu
// (devops.ClampCodeBudgetRunes), burada yalnız kutunun min/max'ı ve metni.
const CODE_BUDGET_DEFAULT = 10000;
const CODE_BUDGET_MIN = 2000;
const CODE_BUDGET_MAX = 20000;

// budgetBoxValue — v0.10.1038: kutunun tohumu. Kayıtlı değer aralık
// dışındaysa (elle düzenlenmiş blob, ör. 500) kutu YÜRÜRLÜKTEKİ (sunucunun
// sıkıştırdığı) değerle dolar; yoksa tarayıcının min/max doğrulaması tüm
// formun Kaydet'ini sessizce kilitlerdi. Ayar yoksa boş (= varsayılan).
function budgetBoxValue(stored?: number, effective?: number): string {
  if (!stored) return '';
  if (stored >= CODE_BUDGET_MIN && stored <= CODE_BUDGET_MAX) return String(stored);
  return String(effective || Math.min(Math.max(stored, CODE_BUDGET_MIN), CODE_BUDGET_MAX));
}

export function DevOpsTab() {
  const confirm = useConfirm();
  const [baseUrl, setBaseUrl] = useState('');
  const [collection, setCollection] = useState('');
  const [project, setProject] = useState('');
  const [username, setUsername] = useState('');
  const [pat, setPat] = useState('');
  const [hasPat, setHasPat] = useState(false);
  const [flavor, setFlavor] = useState<DevOpsFlavor>('auto');
  const [insecure, setInsecure] = useState(false);
  // v0.10.75 — organizasyon kod araması. Varsayılan KAPALI:
  // ayrı bir uzantı (Code Search) ve PAT kapsamı istiyor.
  const [codeSearch, setCodeSearch] = useState(false);
  const [repoPrefixes, setRepoPrefixes] = useState('');
  const [branchOrder, setBranchOrder] = useState('');
  const [versionRef, setVersionRef] = useState(''); // v0.10.590
  // v0.10.112 — uygulama paket önekleri + deneme tavanı (operatör-raporlu
  // "tavan çerçeve sınıflarına gidiyor"). Tavan metin olarak tutulur:
  // boş kutu = varsayılan, sunucu 0'ı "ayar yok" okur.
  const [appPrefixes, setAppPrefixes] = useState('');
  const [lookupLimit, setLookupLimit] = useState('');
  const [searchLimit, setSearchLimit] = useState(''); // v0.10.353
  const [effectiveLimit, setEffectiveLimit] = useState(6);
  // v0.10.1038 (operatör: "Kod bütçesi daha fazla karakter olabilir bence,
  // default 10k gibi") — tavanın kalıbı: metin tutulur, boş = varsayılan
  // (sunucu 0'ı "ayar yok" okur); yürürlükteki değer ayrıca gösterilir.
  const [codeBudget, setCodeBudget] = useState('');
  const [effectiveBudget, setEffectiveBudget] = useState(CODE_BUDGET_DEFAULT);
  const [detected, setDetected] = useState<{ flavor?: DevOpsFlavor; apiVersion?: string }>({});
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [test, setTest] = useState<DevOpsTestResult | null>(null);
  // v0.9.1242 — çözüm provası. AYRI busy bayrağı: kaydet/test ile aynı
  // `busy`i paylaşsalardı bir prova, form butonlarını da kilitlerdi.
  const [dryService, setDryService] = useState('');
  const [dryBusy, setDryBusy] = useState(false);
  const [dry, setDry] = useState<DevOpsResolveDryRun | null>(null);
  const [dryErr, setDryErr] = useState('');
  // v0.10.115 — şema kataloğu (SQLCODE'lu Explain'lerde kolon tanımı).
  // Kolon içeriği ekrana DÖNMEZ; yalnız sayı/tarih. CSV metin olarak
  // yüklenir (dosya seçici de okuyup metne çevirir).
  const [schema, setSchema] = useState<SchemaCatalogSummary | null>(null);
  const [schemaFlavor, setSchemaFlavor] = useState('db2');
  const [schemaCsv, setSchemaCsv] = useState('');
  const [schemaBusy, setSchemaBusy] = useState(false);
  const [schemaMsg, setSchemaMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const loadSchema = () => api.getSchemaCatalog().then(setSchema).catch(() => setSchema(null));
  const importSchema = async (e: FormEvent) => {
    e.preventDefault();
    if (!schemaCsv.trim()) return;
    setSchemaBusy(true); setSchemaMsg(null);
    try {
      const out = await api.putSchemaCatalog({ csv: schemaCsv, flavor: schemaFlavor, source: 'settings-upload' });
      setSchema(out); setSchemaCsv('');
      setSchemaMsg({ kind: 'ok', text: `${out.tables} tablo · ${out.columns} kolon yüklendi.` });
    } catch (err) {
      setSchemaMsg({ kind: 'err', text: String((err as Error).message || err) });
    } finally { setSchemaBusy(false); }
  };
  const clearSchema = async () => {
    // v0.10.133 — yıkıcı: katalog sunucudan silinir, geri dönüşü CSV'yi
    // yeniden yüklemek (destructiveConfirm kapısı; v0.10.115'te onaysızdı).
    if (!await confirm({
      title: 'Şema kataloğu silinsin mi?',
      body: <>Kayıtlı DB2/Oracle kolon kataloğu sunucudan kaldırılacak; SQL hatası açıklamaları
        yeni bir CSV yüklenene kadar <b>şema bağlamı olmadan</b> üretilecek.</>,
      confirmLabel: 'Kataloğu sil',
      danger: true,
    })) return;
    setSchemaBusy(true); setSchemaMsg(null);
    try { setSchema(await api.deleteSchemaCatalog()); setSchemaMsg({ kind: 'ok', text: 'Katalog temizlendi.' }); }
    catch (err) { setSchemaMsg({ kind: 'err', text: String((err as Error).message || err) }); }
    finally { setSchemaBusy(false); }
  };
  const readCsvFile = (f: File | undefined) => {
    if (!f) return;
    const rd = new FileReader();
    rd.onload = () => setSchemaCsv(String(rd.result || ''));
    rd.readAsText(f);
  };

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getDevOpsSettings(),
    s => {
      void loadSchema();
      setBaseUrl(s.baseUrl || '');
      setCollection(s.collection || '');
      setProject(s.project || '');
      setUsername(s.username || '');
      setHasPat(s.hasPat);
      setFlavor((s.flavor || 'auto') as DevOpsFlavor);
      setInsecure(!!s.insecureSkipVerify);
      setCodeSearch(!!s.codeSearch);
      setRepoPrefixes((s.repoPrefixes || []).join(', '));
      setBranchOrder((s.branchOrder || []).join(', '));
      setVersionRef(s.versionRef || '');
      setAppPrefixes((s.appPrefixes || []).join(', '));
      setLookupLimit(s.codeLookupLimit ? String(s.codeLookupLimit) : '');
      setSearchLimit(s.codeSearchLimit ? String(s.codeSearchLimit) : '');
      setEffectiveLimit(s.effectiveLookupLimit || 6);
      setCodeBudget(budgetBoxValue(s.codeBudgetRunes, s.effectiveCodeBudgetRunes));
      setEffectiveBudget(s.effectiveCodeBudgetRunes || CODE_BUDGET_DEFAULT);
      setDetected({ flavor: s.detectedFlavor, apiVersion: s.detectedApiVersion });
    },
  );

  // PAT boşsa gövdeden tamamen çıkar — sunucu sözleşmesi "boş =
  // saklıyı koru", ama alanı hiç göndermemek niyeti daha net
  // ifade ediyor ve aynı yola düşüyor.
  const buildInput = () => ({
    baseUrl, collection, project, username, flavor,
    insecureSkipVerify: insecure,
    codeSearch,
    repoPrefixes: splitList(repoPrefixes),
    branchOrder: splitList(branchOrder),
    versionRef: versionRef.trim(),
    appPrefixes: splitList(appPrefixes),
    codeLookupLimit: Math.max(0, parseInt(lookupLimit, 10) || 0),
    codeSearchLimit: Math.max(0, parseInt(searchLimit, 10) || 0),
    codeBudgetRunes: Math.max(0, parseInt(codeBudget, 10) || 0), // v0.10.1038 — boş = 0 = varsayılan
    ...(pat ? { pat } : {}),
  });

  const runTest = async () => {
    setBusy(true); setMsg(null); setTest(null);
    try {
      const r = await api.testDevOpsSettings(buildInput());
      setTest(r);
      if (r.ok && r.detectedFlavor) {
        setDetected({ flavor: r.detectedFlavor, apiVersion: r.apiVersion });
      }
    } catch (err) {
      setTest({ ok: false, projectCount: 0,
        error: err instanceof Error ? err.message : 'Test başarısız' });
    } finally {
      setBusy(false);
    }
  };

  // runDryRun — servis → depo/branş/ağaç provası (v0.9.1242).
  //
  // Sunucu başarısızlığı 200 + {ok:false, steps} olarak döner (test
  // ucunun duruşu): "depo bulunamadı" operatörün sorusuna verilmiş
  // BAŞARILI bir cevaptır. catch yalnız gerçek taşıma/yetki hataları
  // için — o hâlde adım listesi yoktur, tek satır hata gösterilir.
  const runDryRun = async (e: FormEvent) => {
    e.preventDefault();
    const svc = dryService.trim();
    if (!svc) return;
    setDryBusy(true); setDry(null); setDryErr('');
    try {
      setDry(await api.resolveDevOpsDryRun(svc));
    } catch (err) {
      setDryErr(err instanceof Error ? err.message : 'Çözüm denemesi başarısız');
    } finally {
      setDryBusy(false);
    }
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      const next = await api.putDevOpsSettings(buildInput());
      setHasPat(next.hasPat);
      setPat('');
      // Sunucu konvansiyonu ÇÖZÜLMÜŞ döner: alanı boş bırakan operatör
      // kaydettiği anda varsayılanı kutuda görür.
      setRepoPrefixes((next.repoPrefixes || []).join(', '));
      setBranchOrder((next.branchOrder || []).join(', '));
      setVersionRef(next.versionRef || '');
      // v0.10.1038 — sunucu sıkıştırdıysa (ör. 500 → 2000) kutu kaydedileni gösterir.
      setCodeBudget(budgetBoxValue(next.codeBudgetRunes, next.effectiveCodeBudgetRunes));
      setEffectiveBudget(next.effectiveCodeBudgetRunes || CODE_BUDGET_DEFAULT);
      setDetected({ flavor: next.detectedFlavor, apiVersion: next.detectedApiVersion });
      setMsg({ kind: 'ok', text: next.baseUrl
        ? 'Kaydedildi — bağlantı bilgileri saklandı (tüm pod’lar <30s içinde eşitlenir).'
        : 'Kaydedildi — bağlantı temizlendi.' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Kayıt başarısız' });
    } finally {
      setBusy(false);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  const configured = baseUrl.trim().length > 0;
  const flavorLabel = (f?: DevOpsFlavor) =>
    f === 'tfs' ? 'TFS' : f === 'azure-devops-server' ? 'Azure DevOps Server' : 'Auto';

  return (
    <div className="max-w-[640px]">
      <h2 className={H2}>Kod entegrasyonu (Azure DevOps / TFS)</h2>
      <p className="mb-4 text-[length:var(--fs-md)] text-(--text2)">
        Şirket içi Azure DevOps Server / TFS koleksiyonuna okuma bağlantısı.
        Yapılandırıldığında CoSRE, exception ve trace açıklamalarında
        stack trace’teki uygulama satırlarının <strong className="text-(--text)">
        kaynak kodunu</strong> da okuyabilir (AI çekmecesindeki
        “Kodu da incele” kutusu). Kod yalnız modele gider; AI çağrı
        kaydına kod gövdesi değil, yalnız <code>dosya:aralık</code> özeti yazılır.
      </p>

      {/* v0.10.929 (K5) — kurulu/kurulmamış bir ayar durumu: nötr bant (eskiden yeşil/amber). */}
      <ConfigStatusBanner label={configured ? 'YAPILANDIRILDI' : 'YAPILANDIRILMADI'}>
        {configured
          ? `${baseUrl}${collection ? ` / ${collection}` : ''}`
          : 'Sunucu adresi girilmedi.'}
      </ConfigStatusBanner>

      <form onSubmit={save} className={cn(CARD, 'flex flex-col gap-3')}>
        <Field label="Sunucu URL" value={baseUrl}
          onChange={e => setBaseUrl(e.target.value)}
          placeholder="https://devops.sirket.local/tfs"
          hint={<>Sondaki eğik çizgi serbest. Çağrı:{' '}
            <code>{'{url}/{collection}/_apis/projects'}</code></>} />

        <Field label="Collection" value={collection}
          onChange={e => setCollection(e.target.value)}
          placeholder="DefaultCollection"
          hint="Koleksiyonu sunucu URL’sine dahil ettiyseniz boş bırakın." />

        {/* v0.9.1183 — boş Project'in ARTIK iki anlamı var ve ikisi de
            burada yazılı olmalı: bağlantı testi hâlâ yalnız koleksiyonu
            doğrular, ama kod çekimi artık servis önekinden proje türetiyor.
            Yazılmazsa operatör türetmenin varlığını hiç öğrenemez —
            tam da bu alan boş kaldığı için kod bağlamı sessizce
            çalışmıyordu. */}
        <Field label="Project" labelAside={<span className={ASIDE}>(opsiyonel)</span>}
          value={project}
          onChange={e => setProject(e.target.value)}
          placeholder="boş bırakılırsa servis önekinden türetilir (svc- → SVC)"
          hint={<>Bağlantı testi boşken yalnız koleksiyonu doğrular. <b>Kod çekiminde</b> boş
            bırakılırsa proje, servis adının eşleşen önekinden türetilir
            (<code>svc-…</code> → <code>SVC</code>). Buraya yazılan değer türetmeyi ezer.</>} />

        <Field label="Kullanıcı adı" labelAside={<span className={ASIDE}>(opsiyonel)</span>}
          value={username}
          onChange={e => setUsername(e.target.value)}
          placeholder="PAT kullanıyorsanız boş bırakın"
          hint="PAT’ta kullanıcı adı boş olağandır; eski TFS/NTLM kurulumları hesap adı isteyebilir." />

        <Field label="Personal Access Token (PAT)"
          labelAside={hasPat ? <span className="text-[length:var(--fs-sm)] text-(--text3)">· kayıtlı</span> : undefined}
          type="password" value={pat}
          onChange={e => setPat(e.target.value)}
          placeholder={hasPat ? '(boş bırak = saklı değer korunur)' : 'PAT yapıştır…'}
          hint={<>Yeterli yetki: <strong>Code (Read)</strong>. Token hiçbir zaman geri
            gösterilmez; değiştirmek için yenisini yapıştırın.</>} />

        <SelectField label="Sürüm (flavor)" value={flavor}
          onChange={e => setFlavor(e.target.value as DevOpsFlavor)}
          hint={<>Auto her açılışta yeniden tespit eder — sonuç ayara yazılmaz, böylece
            sunucu yükseltildiğinde eski tahmin yalan söylemez.
            {detected.flavor && (
              <> Son tespit: <strong>{flavorLabel(detected.flavor)}</strong>
                {detected.apiVersion ? ` · api-version ${detected.apiVersion}` : ''}.</>
            )}</>}>
          <option value="auto">Auto (URL şeklinden tahmin + ping ile doğrula)</option>
          <option value="azure-devops-server">Azure DevOps Server (api-version 6.0)</option>
          <option value="tfs">TFS (api-version 4.1)</option>
        </SelectField>

        <Field label="Servis adı önekleri" labelAside={<span className={ASIDE}>(virgülle)</span>}
          value={repoPrefixes}
          onChange={e => setRepoPrefixes(e.target.value)}
          placeholder="svc-"
          hint={<>Servis adından depo adı türetilirken soyulur; ortam eki
            (<code>-prod/-int/-uat/-prep</code>) her hâlükârda soyulur.
            Örnek: <code>shop-payment-service-prod</code> → <code>payment-service</code>.
            Katalogdaki <strong>Repository</strong> alanı doluysa O kazanır —
            elle pin konvansiyonu ezer.</>} />

        {/* v0.10.112 — UYGULAMA PAKET ÖNEKLERİ + DENEME TAVANI.
            Kurum-içi çerçeve sınıfları (RestFilter, BasicDispatcher…) JDK/
            Spring listesinde olmadığı için uygulama sayılıyor ve stack'te
            iş sınıfından önce geldikleri için tavanı önce onlar yiyordu.
            Önek listesi iş sınıflarını başa alır; tavan ayarlanabilir. */}
        <Field label="Uygulama paket önekleri" labelAside={<span className={ASIDE}>(virgülle)</span>}
          value={appPrefixes}
          onChange={e => setAppPrefixes(e.target.value)}
          placeholder="com.shop.payment., com.shop.cards."
          hint={<>Bu öneklerle başlayan stack frame'leri <strong>önce</strong> denenir;
            kurum-içi çerçeve ve kütüphane sınıfları (ör. <code>com.banka.core.rest.*</code>)
            arkaya düşer, atılmaz. Boş = stack sırası (yalnız JDK/Spring/JBoss elenir).</>} />

        <Field label="Kod çekme deneme tavanı" labelAside={<span className={ASIDE}>(1–30, boş = varsayılan)</span>}
          value={lookupLimit} inputMode="numeric"
          onChange={e => setLookupLimit(e.target.value.replace(/[^0-9]/g, ''))}
          placeholder={String(effectiveLimit)}
          controlClassName={NUM}
          hint={<>Bir açıklama için en fazla kaç dosya çekilir. Ağaçta bulunamayan frame
            ve aynı dosyanın başka satırı tavandan düşmez. Yürürlükte: <strong>{effectiveLimit}</strong>.</>} />

        {/* v0.10.353 (operatör: "arama tavanını yükseltelim") — organizasyon
            geneli kod araması kaç frame / hata-kodu için denenir; eski sabit 2. */}
        <Field label="Kod araması tavanı" labelAside={<span className={ASIDE}>(1–20, boş = varsayılan 6)</span>}
          value={searchLimit} inputMode="numeric"
          onChange={e => setSearchLimit(e.target.value.replace(/[^0-9]/g, ''))}
          placeholder="6"
          controlClassName={NUM}
          hint={<>Depo ağacında bulunamayan frame'ler ve hata-kodu token'ları için organizasyon
            genelinde en fazla kaç arama yapılır (her biri ayrı bir DevOps çağrısı). Yalnız
            "Organizasyon geneli kod araması" açıkken etkili; tavana takılan frame'ler
            açıklamada "eşleşmeyen" olarak listelenir.</>} />

        {/* v0.10.1038 — kod bütçesi (operatör: "default 10k gibi"). Eskiden sabit
            4000'di ve "1 pencere düştü" notu rutindi. Çekilen dosya sayısını
            değiştirmez; yalnız modele giden kodu kırpar. */}
        <Field label="Kod bütçesi (karakter)" type="number" inputMode="numeric"
          min={CODE_BUDGET_MIN} max={CODE_BUDGET_MAX}
          value={codeBudget}
          onChange={e => setCodeBudget(e.target.value.replace(/[^0-9]/g, ''))}
          placeholder={String(CODE_BUDGET_DEFAULT)}
          controlClassName={NUM}
          hint={<>Modele gönderilen kaynak kod miktarı. Küçük bağlamlı modellerde cevap
            kesiliyorsa düşürün. {CODE_BUDGET_MIN}–{CODE_BUDGET_MAX}, boş = varsayılan;
            yürürlükte: <strong>{effectiveBudget}</strong>.</>} />

        <Field label="Branş sırası" labelAside={<span className={ASIDE}>(virgülle)</span>}
          value={branchOrder}
          onChange={e => setBranchOrder(e.target.value)}
          placeholder="release, master"
          hint={<>İlk <strong>var olan</strong> branş kullanılır. Hiçbiri yoksa deponun
            kendi varsayılan branşına düşülür.</>} />

        {/* v0.10.590 — olay anındaki sürümü VCS ref'ine bağlayan desen. */}
        <Field label="Sürüm → ref deseni" labelAside={<span className={ASIDE}>(versionRef)</span>}
          value={versionRef}
          onChange={e => setVersionRef(e.target.value)}
          placeholder="tags/{version}"
          hint={<>Stack frame linkleri olay anındaki sürüme (image tag / service.version) göre bu
            ref'e bağlanır: ref bulunursa link ve dosya yolu o commit'ten gider, bulunmazsa branş
            ucu + uyarı. <code>{'{version}'}</code> zorunlu; <code>tags/</code> ya da
            <code>heads/</code> ile başlar. Boş = <code>tags/{'{version}'}</code>.
            {/* v0.10.1047 — mono-repo: {service} = önek ve ortam eki soyulmuş servis adı. */}
            {' '}Aynı depoda birden çok servis varsa <code>{'{service}'}</code> kullanın,
            ör. <code>tags/{'{service}'}-{'{version}'}</code>.</>} />

        <CheckboxField checked={insecure} onCheckedChange={setInsecure}
          label={<>TLS doğrulamasını atla{' '}
            <span className="text-[length:var(--fs-xs)] italic text-(--text3)">
              (kendinden imzalı / iç CA sertifikaları — POC)
            </span></>} />

        {/* v0.10.75 — ORGANİZASYON KOD ARAMASI.
            Depo servis adından konvansiyonla çözülüyor; hatanın atıldığı
            sınıf başka bir depoda olduğunda konvansiyon onu bulamıyor.
            Arama o boşluğu kapatıyor ama AYRI bir uzantı ve PAT kapsamı
            istiyor — o yüzden varsayılan kapalı ve ne yaptığı burada
            yazılı. */}
        <CheckboxField checked={codeSearch} onCheckedChange={setCodeSearch}
          label="Organizasyon geneli kod araması"
          hint={<>
            <span className="block italic">
              Depo ağacında bulunamayan stack frame'leri için tüm organizasyonda
              {' '}<code>Sınıf.metot</code>{' '}
              aranır ve bulunan deponun kendisinden çekilir. Azure DevOps
              <b> Code Search</b> uzantısı ve PAT'te <b>Code (Read)</b> kapsamı
              gerekir; yoksa açıklama yine üretilir, künyeye “kod araması
              başarısız” yazılır.
            </span>
            {/* v0.10.1362 — v0.10.1349'dan beri stacktrace'siz trace'in kod
                incelemesi de bu aramaya dayanıyor; açıklama bunu söylemiyordu. */}
            <span className="mt-0.5 block italic">
              Stacktrace'i olmayan trace'lerin kod incelemesi de bu aramayı kullanır:
              trace'in ana operasyonunun adı depoda aranır ve o operasyonun
              kodu okunur. Kapalıysa bu trace'ler kodsuz incelenir.
            </span>
          </>} />

        {test && (
          <div role="status" className={cn(
            'rounded-md border border-(--border) bg-(--bg0) px-2.5 py-2 text-[length:var(--fs-sm)]',
            test.ok ? 'text-(--ok)' : 'text-(--err)')}>
            {test.ok ? (
              <>
                ✓ {test.projectCount} proje
                {' · tespit: '}{flavorLabel(test.detectedFlavor)}
                {test.apiVersion ? ` ${test.apiVersion}` : ''}
                {test.projectChecked && ` · "${project}" projesi doğrulandı`}
              </>
            ) : (
              <>✗ {test.error || 'Bağlantı kurulamadı'}</>
            )}
          </div>
        )}

        {msg && (
          <div className={cn('text-[length:var(--fs-sm)]', msg.kind === 'ok' ? 'text-(--ok)' : 'text-(--err)')}>
            {msg.text}
          </div>
        )}

        <div>
          <ActionRow
            secondary={
              <Button type="button" variant="outline" disabled={!configured}
                onClick={() => { void runTest(); }} loading={busy}>
                Bağlantıyı test et
              </Button>
            }
            confirm={<Button type="submit" loading={busy}>Kaydet</Button>} />
          <div className="mt-2 text-[length:var(--fs-xs)] text-(--text3)">
            Test kaydetmez — formdaki değerlerle dener. PAT alanı boşsa saklı
            token kullanılır.
          </div>
        </div>
      </form>

      {/* v0.9.1242 — çözüm provası. Ayrı bir <form>: ayar formunun
          İÇİNDE olsaydı servis adı kutusunda Enter'a basmak ayarları
          kaydederdi. Zincir KAYITLI ayarlarla koşar, o yüzden de
          burada — kaydetmeden denemek başka bir sorunun cevabı. */}
      <form onSubmit={runDryRun} className={CARD}>
        <h3 className={H3}>Çözümü dene</h3>
        <p className="mb-3 text-[length:var(--fs-sm)] text-(--text2)">
          Bir servis adı yazın; <strong className="text-(--text)">aynı çözüm
          zinciri</strong> (katalog pini → depo adı → proje → branş → depo ağacı)
          AI’a hiç uğramadan koşar. Konvansiyonu denemek için artık gerçek bir
          exception ve tam bir LLM turu gerekmiyor. Salt okuma: hiçbir şey
          kaydedilmez, kod-çekme isabet sayaçları etkilenmez.
        </p>
        <Field label="Servis adı" value={dryService}
          onChange={e => setDryService(e.target.value)}
          placeholder="shop-payment-service-prod"
          hint={configured
            ? 'Kayıtlı ayarlarla denenir — kaydetmediğiniz değişiklikler hesaba katılmaz.'
            : 'Önce sunucu adresini girip kaydedin.'} />
        <div className="mt-2">
          <ActionRow confirm={
            <Button type="submit" variant="outline"
              disabled={!configured || !dryService.trim()} loading={dryBusy}>
              Çözümü dene
            </Button>
          } />
        </div>

        {dryBusy && <div className="mt-3"><Spinner /></div>}

        {!dryBusy && dryErr && (
          <div role="alert" className="mt-3 text-[length:var(--fs-sm)] text-(--err)">✗ {dryErr}</div>
        )}

        {!dryBusy && !dryErr && dry && (
          <div className="mt-3">
            {/* Özet satırı — adımlardan türer, çözülemeyen alan hiç yazılmaz. */}
            <div className="mb-2 flex flex-wrap items-center gap-2 text-[length:var(--fs-sm)] text-(--text2)">
              <StatusBadge status={dry.ok ? 'ok' : 'critical'}>
                {dry.ok ? 'ÇÖZÜLDÜ' : 'ÇÖZÜLEMEDİ'}
              </StatusBadge>
              <span>
                {dry.repo
                  ? <>
                      <strong className="text-(--text)">{dry.repo}</strong>
                      {dry.branch ? ` @ ${dry.branch}` : ''}
                      {dry.project ? ` · proje ${dry.project}` : ''}
                      {dry.fileCount ? ` · ${dry.fileCount} dosya` : ''}
                    </>
                  : `“${dry.service}” için depo adı üretilemedi.`}
              </span>
            </div>
            <ol className="m-0 flex list-none flex-col gap-1.5 p-0">
              {(dry.steps || []).map((st, i) => (
                <li key={`${st.key}-${i}`}
                  className="flex items-baseline gap-2 text-[length:var(--fs-sm)]">
                  {/* v0.10.58 — ÜÇ DURUM. "Depo adı" ve "Proje" adımları
                      DevOps'a sorulmadan, saf türetmeyle üretiliyor; onlara
                      yeşil tik vermek operatöre "doğrulandı" der ve tam da
                      bu ekranın cevaplaması gereken soruyu ("depo doğru mu")
                      cevaplamış gibi yapar. Operatör bildirdi: "Kodu incele
                      dediğimde doğru repoyu bulmuyor" — ekranda iki yeşil
                      tik dururken. */}
                  <StatusBadge status={st.derived ? 'info' : st.ok ? 'ok' : 'critical'}>
                    {st.derived ? '~' : st.ok ? '✓' : '✗'}
                  </StatusBadge>
                  <span className="min-w-[110px] text-(--text2)">{st.label}</span>
                  {/* Uzun gerekçe (çıkmaz cümleleri üç kaynağı birden
                      anlatıyor) kırpılmaz: kesilen bir teşhis, teşhis
                      değildir. */}
                  <span className={cn('break-words',
                    st.derived ? 'text-(--text2)' : st.ok ? 'text-(--text)' : 'text-(--err)')}>{st.detail}</span>
                </li>
              ))}
            </ol>
            {/* Zincir ilk kırmızıda durur; koşmamış adımlar listede YOK. */}
            {!dry.ok && (
              <div className="mt-2 text-[length:var(--fs-xs)] text-(--text3)">
                Zincir ilk hatada durdu — sonraki adımlar hiç çalıştırılmadı.
              </div>
            )}
          </div>
        )}
      </form>

      {/* v0.10.115 — ŞEMA KATALOĞU. SQLCODE/SQLSTATE'li hatalarda model hedef
          kolonun tipini/uzunluğunu bilmediği için tahmin yürütüyordu
          ("muhtemelen telefon numarası"). Katalog SALT-OKUNUR bir anlık
          görüntü: operatör aşağıdaki SELECT'i kendi tarafında koşturur,
          CSV'yi yükler. Canlı DB bağlantısı yok — sürücü, kimlik, ağ yok. */}
      <form onSubmit={importSchema} className={CARD}>
        <h3 className={H3}>Şema kataloğu (SQL hataları için)</h3>
        <p className="mb-2.5 text-[length:var(--fs-sm)] text-(--text2)">
          <code>SQLCODE</code>/<code>SQLSTATE</code>/<code>ORA-</code> taşıyan hatalarda AI,
          hedef tablonun <strong className="text-(--text)">kolon tanımlarını</strong>
          (tip, uzunluk, NULL) kanıt olarak alır. Aşağıdaki salt-okunur sorguyu DB
          tarafında koşturup CSV çıktısını yükleyin; katalog Coremetry'de saklanır,
          DB'ye bağlantı kurulmaz.
        </p>
        <div className="mb-2 flex flex-wrap items-end gap-2">
          <SelectField small label="Veritabanı" value={schemaFlavor} onChange={e => setSchemaFlavor(e.target.value)}>
            {['db2', 'oracle', 'postgres', 'mysql', 'mssql'].map(f => <option key={f} value={f}>{f}</option>)}
          </SelectField>
          <div className="pb-1.5">
            {schema && schema.importedAt > 0 ? (
              <Badge variant="secondary" className="tabular-nums">{schema.tables} tablo · {schema.columns} kolon · {fmtDateTime(new Date(schema.importedAt))}{schema.flavor ? ` · ${schema.flavor}` : ''}</Badge>
            ) : (
              <Badge variant="secondary">katalog yüklü değil</Badge>
            )}
          </div>
        </div>
        <details className="mb-2">
          <summary className="cursor-pointer text-[length:var(--fs-sm)] text-(--text2)">Katalog sorgusu ({schemaFlavor}) — DB tarafında koşturun, çıktıyı CSV alın</summary>
          <pre className="mt-1.5 overflow-x-auto whitespace-pre-wrap rounded-md border border-(--border) bg-(--bg) p-2 text-[length:var(--fs-xs)]">
            {schema?.snapshotSql?.[schemaFlavor] || '—'}
          </pre>
        </details>
        <TextareaField label="CSV (başlık satırlı; virgül / noktalı virgül / sekme) — yapıştırın ya da dosya seçin"
          value={schemaCsv} onChange={e => setSchemaCsv(e.target.value)} rows={5}
          placeholder={'TABSCHEMA,TABNAME,COLNAME,TYPENAME,LENGTH,SCALE,NULLS\nSHOP,ORDER_PHONE,PHONE,VARCHAR,10,0,N'}
          controlClassName="font-mono" />
        {/* Yerel dosya girdisi bilinçli olarak native (plan Q3). */}
        <input type="file" accept=".csv,.txt,text/csv" aria-label="CSV dosyası seç"
          className="mt-1.5 mb-2 block text-[length:var(--fs-sm)]"
          onChange={e => readCsvFile(e.target.files?.[0])} />
        <ActionRow
          destructive={
            <Button type="button" variant="destructive" disabled={!schema || schema.importedAt === 0 || schemaBusy}
              onClick={() => { void clearSchema(); }}>Kataloğu sil</Button>
          }
          confirm={<Button type="submit" disabled={!schemaCsv.trim()} loading={schemaBusy}>Kataloğu yükle</Button>} />
        {schemaMsg && (
          <div role={schemaMsg.kind === 'err' ? 'alert' : 'status'}
            className={cn('mt-2 text-[length:var(--fs-sm)]', schemaMsg.kind === 'ok' ? 'text-(--ok)' : 'text-(--err)')}>
            {schemaMsg.text}
          </div>
        )}
      </form>
      <FunctionCodeShapePanel />
    </div>
  );
}
