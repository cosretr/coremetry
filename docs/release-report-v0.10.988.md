# Release raporu — v0.10.988

Etiket `v0.10.988` (tagger 2026-09-29 19:20:29 UTC, hedef commit
`4736967c755e58accd5c6cae32c03dc0860eb952`) için tetiklenen "Release"
workflow koşusunun sonucu. Veriler GitHub Actions API'sinden ve GHCR
manifest uçlarından okunmuştur.

## Koşu

| Alan | Değer |
|---|---|
| Workflow | Release |
| Koşu id | 36618552000 |
| URL | https://github.com/cosretr/coremetry/actions/runs/36618552000 |
| Tetikleyici | `push` (tag `v0.10.988`) |
| Deneme | 1 |
| Başlangıç | 2026-09-29 19:20:34 UTC |
| Bitiş | 2026-09-29 19:29:22 UTC (~8 dk 48 sn) |
| Sonuç | ✅ `success` |

## Job'lar

| Job | Sonuç | Süre | Bağlantı |
|---|---|---|---|
| Gate (build + test before publish) | ✅ success | 19:20:37 → 19:24:47 (4 dk 10 sn) | [job 109577742557](https://github.com/cosretr/coremetry/actions/runs/36618552000/job/109577742557) |
| Build + push Docker image | ✅ success | 19:24:50 → 19:29:21 (4 dk 31 sn) | [job 109579457145](https://github.com/cosretr/coremetry/actions/runs/36618552000/job/109579457145) |
| Package + publish Helm chart (OCI) | ✅ success | 19:24:50 → 19:25:04 (14 sn) | [job 109579456916](https://github.com/cosretr/coremetry/actions/runs/36618552000/job/109579456916) |
| GitHub Release | ✅ success | 19:20:37 → 19:20:53 (16 sn) | [job 109577743499](https://github.com/cosretr/coremetry/actions/runs/36618552000/job/109577743499) |

Adım ayrıntısı:

- **Gate:** Frontend typecheck + build ✅, Go vet + build + test ✅,
  `make audit` ✅.
- **Docker:** Login to GHCR ✅, Image metadata ✅, Build + push ✅.
- **Helm:** Sync Chart appVersion to git tag ✅, Package chart ✅,
  Login to GHCR for Helm OCI ✅, Push chart to OCI ✅.
- **GitHub Release:** Chart version (strip leading v) ✅, Create release ✅
  → https://github.com/cosretr/coremetry/releases/tag/v0.10.988

## İmaj etiketleri / digest

Docker job'ının ham logu bu ortamdan indirilemedi (log deposu
`productionresultssa*.blob.core.windows.net` ve
`results-receiver.actions.githubusercontent.com` ağ politikasınca
engelli, CONNECT 403). Etiketler bu yüzden doğrudan GHCR registry
API'sinden (`/v2/cosretr/coremetry/manifests/<tag>`) doğrulandı.

Index digest (OCI image index, `application/vnd.oci.image.index.v1+json`):

```
sha256:2de5d48e3a5eb81ad0d63eef21404886ba62e710497e5ab750e0b9fbdfa17d09
```

| Etiket | Digest | Kaynak |
|---|---|---|
| `ghcr.io/cosretr/coremetry:v0.10.988` | `sha256:2de5d48e…17d09` | `type=ref,event=tag` |
| `ghcr.io/cosretr/coremetry:0.10.988` | `sha256:2de5d48e…17d09` | `type=semver,pattern={{version}}` |
| `ghcr.io/cosretr/coremetry:0.10` | `sha256:2de5d48e…17d09` | `type=semver,pattern={{major}}.{{minor}}` |
| `ghcr.io/cosretr/coremetry:0` | `sha256:2de5d48e…17d09` | `type=semver,pattern={{major}}` |
| `ghcr.io/cosretr/coremetry:latest` | `sha256:2de5d48e…17d09` | `type=raw,value=latest,enable={{is_default_branch}}` |

Beş etiketin tamamı aynı index digest'ine işaret ediyor; `latest` de
bu sürüme çekilmiş (etiketlenen commit `main` ucu olduğu için
`is_default_branch` koşulu sağlanmış).

Index içeriği:

| Platform | Manifest digest | Tür |
|---|---|---|
| linux/amd64 | `sha256:3fdb6480c6d62014c69fe9c079948b54f4b0ae089d451359d581c3318cb56025` | image manifest |
| unknown/unknown | `sha256:454697472c21b7b6460caefaf60bf55dc647806f7ce0e1b676ff779e024d3980` | attestation-manifest (provenance) |

Yalnız `linux/amd64` basılmış (`vars.BUILD_PLATFORMS` tanımsız →
workflow varsayılanı). İmaj config blob'u (`org.opencontainers.image.*`
etiketleri) `pkg-containers.githubusercontent.com` engelli olduğundan
okunamadı; `VERSION`/`VITE_APP_VERSION` build-arg'larının `v0.10.988`
olarak damgalandığı workflow tanımından (release.yml "Build + push"
adımı) çıkarılmıştır, imaj içinden doğrulanmamıştır.

### Helm chart

- Referans: `oci://ghcr.io/cosretr/charts/coremetry` sürüm `0.10.988`
  (`helm install coremetry oci://ghcr.io/cosretr/charts/coremetry --version 0.10.988`).
- "Push chart to OCI" adımı ✅. Chart manifest'ine anonim GHCR
  token'ıyla erişim 403 döndü (paket görünürlüğü); chart digest'i bu
  raporda yok.

## Hatalar

Yok. Kırmızı job ya da adım bulunmuyor; `continue-on-error` işaretli
Docker ve Helm job'ları da gerçek `success` ile bitti.

## Notlar

- GitHub Release notu gövdesi: Docker imajı
  `ghcr.io/cosretr/coremetry:v0.10.988`, Helm komutu yukarıdaki gibi;
  changelog `v0.10.979...v0.10.988`.
- Bu rapordaki tek boşluk ham job logudur; log dosyası indirilebilen
  bir ortamda "Build + push" adımının çıktısındaki
  `ghcr.io/cosretr/coremetry:` satırları buradaki tabloyla
  karşılaştırılabilir.
