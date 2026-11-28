---
name: dogrula
description: 'Değişikliği çalışan yerel yığında kanıtla: diff''e göre yalnız ilgili adımları koşar (Go → gate go + dev-api; ClickHouse → dev-ch + EXPLAIN; önyüz → gate fe + dev-shot iki tema; Helm → lint + template) ve tek kanıt bloğu yazar. WHEN: commit/sürümden önce, "doğrula", "kanıtla", "çalıştığını göster", dilim bitti denmeden önce. WHEN NOT: kod yazılmadan önce tasarım (/spec, /audit), prod ya da prep doğrulaması (yerel yığın dışı), tek yavaş yüzeyin kök nedeni (/perf-triage).'
---

# /dogrula — diff'e göre kanıt üret, tek blokta yaz

Kural: **koşmadığın adım "geçti" yazılmaz.** Kanıt üretilemeyen adım `doğrulanmadı: <neden>`
diye yazılır. Ajan raporundaki "geçti" cümlesi kanıt değildir; yalnız bu oturumda koşulan
komutun gerçek çıkış kodu ve son satırı sayılır.

## 1. İş türünü seç

```
{ git diff --name-only; git diff --name-only --cached; } | sort -u
```
(commit'lenmiş dilim için `git diff --name-only <taban>..HEAD`.) Eşleşen her tür koşulur:

| Yol | Tür |
|---|---|
| `*.go`, `go.mod` (`internal/chstore/`, `migrations/` hariç) | Go |
| `internal/chstore/**`, `migrations/**`, Go içinde değişen SQL metni | ClickHouse (+ Go) |
| `frontend/src/**`, `frontend/index.html`, `frontend/vite.config.ts` | Önyüz |
| `charts/**` | Helm |
| yalnız `docs/`, `.claude/`, `scripts/` | betik ise `bash -n`; belge ise "doğrulanacak kod yok" |

## 2. Yığın hazır mı (Go / CH / önyüz için)

`scripts/dev-up.sh` → ayaktaysa hiçbir şey oluşturmaz, durum + sürüm yazar. Çalışan
binary bu ağacın kodu değilse (`version` satırı) önce `scripts/dev-deploy.sh` (coremetry
container'ını yeniden oluşturur: operatör yığını kullanıyorsa sor). Veri yoksa
`scripts/dev-seed.sh`. Yığın kalkmıyorsa Go/CH/önyüz canlı adımları `doğrulanmadı`.

## 3. Adımlar (yalnız seçilen türler)

- **Go:** `scripts/gate.sh go`. Sonra değişen her uç için `scripts/dev-api.sh "/api/<yol>?<parametre>"`
  (uç `internal/api/<alan>.go` `register…Routes` içindeki desen; GET dışı uç yalnız
  operatör isterse `--write`). Beklenen: 2xx + gövdede yeni alan. Sürüm öncesi
  `~/go/bin/golangci-lint run ./...` (kapıda yok).
- **ClickHouse:** sorgunun kendisi, yer tutucular yerel değerlerle doldurulmuş hâliyle
  `scripts/dev-ch.sh "<SELECT …>"` ve `scripts/dev-ch.sh "EXPLAIN indexes = 1 <SELECT …>"`.
  Kanıt: satır sayısı, süre, EXPLAIN'de kullanılan MV/indeks. Yeni MV/tablo:
  `scripts/dev-ch.sh "SHOW CREATE TABLE <ad>"`. Yerel süre prod maliyetini **göstermez**.
- **Önyüz:** `scripts/gate.sh fe`, sonra etkilenen her sayfa için
  `scripts/dev-shot.sh "<yol>" <ad>` (redhat + dark). Kabul: çıkış 0, iki temada
  `console_errors=0`, `LOGIN-PAGE` / `THEME-MISMATCH` yok. Görüntüleri **Read ile aç ve bak**:
  sayfa çizilmiş mi, tablo beyaz yüzeyde mi, iki tema gerçekten farklı mı. Önyüz
  binary'ye gömülüdür: görüntüden önce `dev-deploy.sh` yoksa eski sayfayı çekersin.
- **Helm:** `helm lint charts/coremetry` ve
  `helm template t charts/coremetry >/dev/null` (değişen values dosyasıyla bir kez daha `-f`).
- **Betik:** `bash -n <dosya>`; `shellcheck` kuruluysa o da.

Uzun komutun çıktısını özetleme; son satırı olduğu gibi al.

## 4. Kanıt bloğu (tek blok, bu biçimde)

```
KANIT — <dal> @ <kısa sha> — <tarih saat>
tür: Go, Önyüz
| adım | komut | çıkış | süre | son satır / sonuç |
|---|---|---|---|---|
| gate go | scripts/gate.sh go | 0 | 94s | go test: 61 ok |
| api | scripts/dev-api.sh "/api/x?range=1h" | 0 | 0.1s | HTTP 200 41ms 2.1KB GET … |
| shot | scripts/dev-shot.sh /x x | 0 | 6s | e2e/shots/x-redhat.png, x-dark.png; console 0; diff 0.4% |
| helm | — | — | — | doğrulanmadı: değişiklik yok |
gözle: <görüntüde ne gördün, bir cümle>
doğrulanmadı: <liste ya da "yok">
```

Yazıldığı yer: **önyüz dilimi** → `docs/frontend/migration-log.md` satırının Notes
sütununa kısa hâli (`kanıt: gate fe 0 · shot x 2 tema console 0`); **diğer işler** →
commit gövdesi. Tüm adımlar 0 ise ve `scripts/dev/verify-stamp.sh` varsa
`scripts/dev/verify-stamp.sh write`; bir adım kırmızı ya da `doğrulanmadı` ise damga yazılmaz.

## 5. Yerelde doğrulanamayanlar (bloğa ekle, her seferinde)

Yerel demo ~45 servis, `-rps 3`, örneklemesiz ve prod'dan ~7000× küçük veriyle çalışır;
bu yüzden sorgu maliyeti ve süresi, kardinalite, örnekleme yanlılığı (prod'da hatalı
trace'lerin tamamı + başarılıların %5'i) ve dedektör gürültüsü burada doğrulanmaz.
Yerelde geçerli model anahtarı olmadığından CoSRE / AI cevaplarının gerçek model
çıktısı, varsayılan yığında Elasticsearch kapalı olduğundan (`dev-up.sh --es` hariç)
prod'un ES log yolu, OpenShift kimlik doğrulaması / rastgele UID / route ve çok kopyalı
kip de doğrulanmaz. Bunlardan birine dokunan değişiklikte satırı `doğrulanmadı` yaz.

## Yapma

- Kırmızı adımı tekrar tekrar koşup yeşili seçme; ilk kırmızıyı rapora koy.
- Yığını durdurma, `docker compose down`, volume ya da prune komutu çalıştırma.
- `dev-api.sh --write`'ı operatör istemeden kullanma; gizli değeri bloğa yazma.
