# Rollouts v2 — §11 sorgu paketini çalıştırmak (operatör kılavuzu)

**Sürüm:** v0.10.979. **Kaynak:** `docs/rollouts/v2-audit.md` §11 (sorgu kataloğu),
`docs/rollouts/v2-probe-runbook.md` (İngilizce runbook, curl ayrıntıları).
**Tür:** yalnız dokümantasyon. Bütün adlar örnektir (`cluster-a`, `hub-1`, `coremetry.example.invalid`).

## 0. Kısaca

Rollouts v2'nin Faz 2 (KSM dedektörü) ve Faz 3 (Argo metrikleri) adımları, audit'in §11 sorgu
paketinin iki hedef küme ve iki hub üzerinde koşup **jetonlanmış** sonuçlarının bu sohbete
yapıştırılmasına bağlıydı. ~40 PromQL sorgusunu Grafana'da elle koşup değerleri jetonlamak yerine
Coremetry bunu bir **admin probe** olarak yapar:

- `POST /api/admin/rollouts-v2/probe` — salt-okunur koşuyu başlatır (hemen 202 döner).
- `GET /api/admin/rollouts-v2/probe` — koşu sürerken ilerleme (202), bitince rapor (200);
  `?format=md` markdown dosyasını indirir.

Thanos token'ı senin eline geçmez: Coremetry, Remote Cluster kayıtlarındaki kimlikle (tokenRef)
süreç içinde sorar. Rapordaki **her değer** jetonludur; ham → jeton eşlemesi bellekte kurulur,
hiçbir yere yazılmaz ve koşu bitince kaybolur. Etiket adları, sayımlar, enum değerleri, sürümler,
portlar ve uyarı metni harfi harinedir — raporun amacı tam bu: sohbete yapıştırılabilir olmak.

Arayüz bloğu yok (API-only); bir admin `cmk_` servis token'ı ile curl yeter.

## 1. Ön koşullar

1. Admin › API tokens'tan bir **admin** servis token'ı (`cmk_…`).
2. Settings › Remote Clusters: hedef kümeler ve iki Argo hub'ı etkin; her birinin `tokenRef`'i
   çözülüyor (rozet yeşil). Çözülmeyen referans koşudan ÖNCE reddedilir, istek gitmez.
3. Settings › Argo CD: `hubs[]` dolu; N paketi için `envList` (ör. `dev, test, prod`) ve Remote
   Cluster kayıtlarında `argoSuffix` / `pairGroup`.

## 2. Çalıştırma

```bash
CMK='cmk_…'
BASE='https://coremetry.example.invalid'

curl -sS -X POST -H "Authorization: Bearer $CMK" -H 'Content-Type: application/json' \
     -d '{}' "$BASE/api/admin/rollouts-v2/probe"
# 202 {"runId":"…","status":"running","pod":"coremetry-api-0","budgetS":300,"planned":331,…}

# 10 saniyede bir yokla; status "running" olmaktan çıkınca bitti
curl -sS -H "Authorization: Bearer $CMK" "$BASE/api/admin/rollouts-v2/probe" | head -c 300

# raporu indir
curl -sS -H "Authorization: Bearer $CMK" "$BASE/api/admin/rollouts-v2/probe?format=md" -o rollouts-v2-probe.md
```

Varsayılanlar: hedefler = hub olmayan bütün etkin kümeler, hub'lar = Argo ayarındaki `hubs[]`,
paketler = K D R H N T, bütçe 300 s. Gövdeyle daraltılabilir:

```json
{ "packs": ["K","D","R"], "targets": ["c-1a2b3c4d"], "options": { "budgetS": 600 } }
```

- Rapor `budget_exhausted` derse paketleri böl: önce `["K","D","R"]`, sonra `["H","N","T"]`.
- `envList` / `suffixList` boşsa N paketi atlanır ve rapor gövdede verilmesini söyler.
- Hub'ın Thanos etiketi varsa H0–H1 önce **matcher'sız**, sonra matcher'lı koşar (§11.0); rapor
  ikisini yan yana `Δ` sütunuyla basar. `options.hubInject: {"<hub-id>": false}` "enjeksiyon zaten
  yanlış" demektir: bütün H çağrıları matcher'sız koşar, ayrı geçiş atlanır.
- Aynı pod'da aynı anda tek koşu (`409 busy`). Çok replikalı api'de rapor onu koşturan pod'un
  belleğindedir; başka pod `404 {"status":"none","pod":"…"}` döner — bir pod'a sabitlen
  (port-forward ya da Route sticky cookie) ya da 202'deki `pod`'a düşene kadar yinele.

## 3. Ne yapıştırılır

`rollouts-v2-probe.md` dosyasının **tamamı**. İçinde:

- §11.9 yapıştırma tablosu (`ID | cluster-a | cluster-b | hub-1 | hub-2`),
- her sorgu için şablon ifade, durum (`ok / empty / unauthorized / timeout / truncated …`), satırlar
  ya da skaler, uyarılar,
- V1–V14 varsayım yargıları (confirmed / refuted / unknown) ve kanıtları,
- "bilgilendirilen kararlar" tablosu (dec 2, 3, 5, 7, 8, 9, 11, 12, 15, 16, 26, 27, 28, 29, 30),
- atlananlar (A ve V `skipped (metrics-only)`, erken duran birimler, bütçe).

Yapıştırmadan önce iki kontrol:

1. JSON cevapta `tokens > 0` (jetonlayıcı çalıştı).
2. Dosyada gerçek küme adı / API host'u / takım adı geçmediğini grep'le doğrula
   (`grep -i -E '<gerçek-ad>|<gerçek-host>' rollouts-v2-probe.md` boş dönmeli).

Örnek satır (sentetik):

```
| K0.2 KSM jobs / K0.3 version / K0.4 dup ratio / K0.5 scrape | K0.2: kube-state-metrics=1, openshift-state-metrics=1 · K0.3: v2.13.0=1 · K0.4: 1 (dedup off 2) · K0.5: 10 | … |
| H2 `dest_server` count, `""`, in-cluster, ports, … | — | — | H2.2: 5 · H2.3: 5 · H2.4: 100 · H2.5: 6443=4 · … |
```

## 4. Ne YAPIŞTIRILMAZ

- Settings › Remote Clusters / Argo CD sayfalarının JSON'u ya da ekran görüntüsü (gerçek URL, ad, tokenRef yolu taşır).
- Grafana'dan elle koşulmuş sorgu sonuçları (jetonsuz).
- `GET …/probe` JSON gövdesinin `results` dışındaki başka bir admin ucunun çıktısı.
- Raporun altbilgisinde "default-denied label names" listesi varsa değerleri **zaten gizlidir**;
  yalnız etiket ADINI bildir, biz allowlist'i genişletiriz.

Rapor zaten bütün değerleri jetonladığı için ek elle düzenleme gerekmez; bir şey sızdığından
şüphelenirsen yapıştırma, önce jeton eşlemesinin hangi etiketi kaçırdığını söyle.

## 5. Sonuçta ne olur

Yapıştırılan rapor §11.9 şablonunu doldurur; V1–V14 yargıları Faz 2'nin (P2.2 canlı KSM bağlantısı)
açılıp açılmayacağını, H/N sonuçları Faz 3 (Argo metrik işçisi, eşleyici) kararlarını belirler.
Denetim kaydında (Admin › Audit) koşu başına tek `rollouts_v2.probe` satırı vardır: kimlikler,
paketler, durum, çağrı sayısı, süreler — hiçbir değer değil.
