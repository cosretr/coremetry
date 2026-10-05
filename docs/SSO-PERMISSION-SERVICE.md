# SSO — merkezi login yetki servisi (v0.10.1110; anahtarsız kip + IP izin listesi v0.10.1111)

Merkezi OIDC login'i, kullanıcı oturumu başına **bir kez** (token yenilemede
değil) Coremetry'ye "bu kullanıcının bu uygulamada hangi yetkileri var" diye
sorar ve dönen listeyi access token'a `permissions` claim'i olarak koyar.
Roller **Coremetry'de** yönetilir (Kullanıcılar sayfası: `/users`, menüde "Kullanıcıları yönet"); servis yalnız
kullanıcının rolünü bildirir.

## Kurulum (Settings > SSO > "Yetki servisi (merkezi login)")

| Alan | Anlam |
|---|---|
| Yetki servisini aç | Kapalıyken uç `404` döner. SSO girişinin açık olması gerekmez. |
| Anahtarsız kabul et | **Varsayılan kapalı.** Açıkken `X-Coremetry-Auth-Key` başlığı **hiç denetlenmez** (kayıtlı bir anahtar olsa da). Merkezi login özel başlık gönderemiyorsa kullanılır; sınır IP izin listesidir. Aşağıya bakın. |
| Paylaşılan anahtar | Merkezi login'in `X-Coremetry-Auth-Key` başlığında göndereceği değer. En az 16 karakter, boşluksuz, yer tutucu olmayan (`openssl rand -hex 32` önerilir). Saklanır, hiçbir ekranda/cevapta geri gösterilmez; boş bırakılan kutu kayıtlı değeri korur. "Anahtarsız kabul et" açıkken isteğe bağlı ve kullanılmaz. |
| IP izin listesi (CIDR) | Satır başına (ya da virgülle) bir IPv4/IPv6 CIDR ya da tek IP; en çok 32. Boş = IP kısıtı yok. Doluysa listede olmayan çağıran `403` alır. Kayıtta kanonikleşir (`10.1.2.3/8` → `10.0.0.0/8`). |
| Güvenilen vekiller (ingress) | `X-Forwarded-For`'un okunacağı doğrudan eşler (ingress / yük dengeleyici pod ya da düğüm CIDR'ı); en çok 32. Boşsa `X-Forwarded-For` hiç okunmaz. Aşağıya bakın. |
| Önbellek süresi (ttlSeconds) | Cevapta dönen, merkezi login'in cevabı önbellekte tutacağı süre. 60–3600 sn, varsayılan 300. |
| Claim adı | Token'da yetkilerin bulunduğu claim. Varsayılan `permissions`. |
| Rolü token'daki claim'den al | **Varsayılan kapalı.** Aşağıya bakın. |

Kayıt `system_settings` `auth_oidc` blobuna yazılır, restart gerekmez; her
kayıt audit'lenir (`settings.oidc.update`, anahtarın kendisi değil yalnız
"değişti mi" bayrağı).

## Uç sözleşmesi

```
POST /api/auth/permissions
Content-Type: application/json
X-Coremetry-Auth-Key: <paylaşılan anahtar>

{"userId":"12345","username":"12345","email":"user12345@example.test","registrationNumber":"12345"}
```

| Cevap | Ne zaman |
|---|---|
| `200` `{"subject":"12345","permissions":["COREMETRY_EDITOR"],"ttlSeconds":300}` | Kullanıcı bulundu. `subject` = istekteki `registrationNumber`, **aynen**. |
| `200` `{"subject":"12345","permissions":["COREMETRY_VIEWER"],"ttlSeconds":300}` | Kullanıcı Coremetry'de **tanımsız**: Settings > SSO'daki varsayılan rol (varsayılan viewer) bildirilir — herkes viewer olarak girebilir, ilk kez girecek kullanıcı dahil. |
| `204` (gövdesiz) | **Yalnız** devre dışı bırakılmış hesap — bu uygulamada yetkisi yok (Coremetry girişi de bu hesabı reddeder). |
| `400` | Gövde JSON değil, 4 KB'tan büyük, `registrationNumber` yok ya da bir alan 256 karakterden uzun. |
| `401` (gövdesiz) | Başlık yok ya da anahtar yanlış. Anahtarsız kipte hiç dönmez. |
| `403` (gövdesiz) | IP izin listesi dolu ve çağıranın IP'si listede değil. |
| `404` (gövdesiz) | Servis kapalı, ya da anahtar tanımlı değil ve anahtarsız kip kapalı. |

**Denetim sırası:** `404` (servis kapalı) → `403` (IP izin listesi) → `401`
(anahtar; anahtarsız kipte atlanır) → `400` (gövde) → kullanıcı eşleme. IP ve
anahtar reddi gövdeye ve depoya ulaşmaz.

Merkezi login 200/204 dışındaki her durumu "cevap yok" sayar ve token'ı
claim'siz basar. Başarı yalnız HTTP durum kodundan okunur; gövdede sonuç kodu
yoktur. Uç oturum istemez (sunucudan-sunucuya), yalnız `POST` muaftır.

**Kullanıcı eşleme sırası** (her adım tek sınırlı okuma):

1. `email` (küçük harfe çevrilerek) → Coremetry kullanıcısının e-postası;
2. bulunamazsa `username`, sonra `registrationNumber` (küçük harf) →
   kullanıcının LDAP kullanıcı adı (`ldap_username`; LDAP ile en az bir kez
   giriş yapmış kullanıcılarda dolu);
3. bulunamazsa tanımsız kullanıcı → varsayılan rol (`COREMETRY_VIEWER`).

Eşleşen hesap devre dışıysa `204`.

**Yetki değerleri:** `COREMETRY_ADMIN`, `COREMETRY_EDITOR`,
`COREMETRY_VIEWER`. Kullanıcıya bir özel rol atanmışsa (viewer tabanlı,
Settings > Custom roles) ve rol katalogda varsa `COREMETRY_ROLE_<AD>` (büyük harf,
harf/rakam dışı karakterler `_`); katalogda yoksa taban rol.

Deneme (anahtar ve adres örnektir):

```bash
curl -sS -X POST https://coremetry.example.test/api/auth/permissions \
  -H 'Content-Type: application/json' \
  -H "X-Coremetry-Auth-Key: $COREMETRY_PERMISSION_KEY" \
  -d '{"userId":"12345","username":"12345","email":"user12345@example.test","registrationNumber":"12345"}' \
  -w '\nHTTP %{http_code}\n'
```

## Anahtarsız kip ve IP izin listesi (v0.10.1111)

Merkezi login özel başlık gönderemiyorsa "Anahtarsız kabul et" açılır. Bu
durumda uç, ona **ağdan erişebilen herkese** cevap verir: herhangi biri bir
e-posta / kullanıcı adı / sicil gönderip o kişinin Coremetry rolünü
öğrenebilir (uç yalnız rol **bildirir**, hiçbir şey değiştirmez, oturum
açmaz). Bunu daraltmak için **IP izin listesine** merkezi login'in çıkış
adres(ler)ini yazın. Anahtarsız kip açık ve liste boşken Settings bir uyarı
satırı gösterir (kayıt engellenmez).

```bash
# Anahtarsız kip — başlık yok; çağıranın IP'si izin listesinde olmalı.
curl -sS -X POST https://coremetry.example.test/api/auth/permissions \
  -H 'Content-Type: application/json' \
  -d '{"userId":"12345","username":"12345","email":"user12345@example.test","registrationNumber":"12345"}' \
  -w '\nHTTP %{http_code}\n'
```

**Çağıranın IP'si nasıl belirlenir (vekil uyarısı):**

- Doğrudan TCP eşi (`RemoteAddr`) **Güvenilen vekiller** listesinde değilse
  çağıran odur; `X-Forwarded-For` **hiç okunmaz** (çağıran bu başlığı
  istediği gibi yazabilir).
- Doğrudan eş güvenilen bir vekilse `X-Forwarded-For` **sağdan sola**
  yürünür; güvenilen vekil olmayan ilk adres çağırandır. Böylece başlığı
  sonuna ekleyen bir ingress'te (ör. OpenShift router varsayılanı) çağıranın
  başa yazdığı sahte bir adres işe yaramaz. Ayrıştırılamayan bir giriş →
  `403`.
- **Coremetry bir ingress / yük dengeleyici arkasındaysa** Güvenilen
  vekiller'e ingress'in pod ya da düğüm CIDR'ını yazın; yazmazsanız her
  çağıran ingress'in adresiyle görünür ve izin listesi ya herkesi (ingress
  CIDR'ı listedeyse) ya da kimseyi geçirmez. Settings bu durumda da uyarır.
- Ingress, merkezi login'in **gerçek** adresini `X-Forwarded-For`'a yazmalı
  (TLS'i sonlandırmayan bir L4 yük dengeleyici SNAT yapıyorsa ingress'in
  gördüğü adres yük dengeleyicininkidir — o zaman ya PROXY protokolü ya da
  yük dengeleyici adresi izin listesine).
- Bu çözüm audit satırlarındaki IP'den (`X-Forwarded-For`'un ilk girişi,
  yalnız iz amaçlı) **bilerek farklıdır**; yetki kararı için yalnız bu çözüm
  kullanılır.
- Teşhis: reddedilen çağrı `auth_permission_requests_total{result="ip_denied"}`
  sayacını artırır; slog DEBUG satırı çözülen IP'yi taşır.

## "Rolü token'daki claim'den al"

- **Kapalı (varsayılan):** SSO ile giriş bugünkü gibi — ilk kez giren
  kullanıcı Settings > SSO'daki varsayılan rolle (viewer) açılır; rolü yalnız
  Coremetry'nin Kullanıcılar sayfasından (`/users`) değişir. Token'daki claim yok sayılır.
- **Açık:** SSO girişinde claim access token'dan okunur (imzası id_token ile
  aynı JWKS'le doğrulanır; access token opaksa ya da doğrulanamıyorsa
  id_token'daki aynı adlı claim kullanılır). Claim bir **dizi** olmalıdır
  (`["COREMETRY_VIEWER"]`); tek bir metin değeri yok sayılır. `COREMETRY_ADMIN`
  > `COREMETRY_EDITOR` > `COREMETRY_VIEWER` sırasıyla ilk eşleşen değer
  **kayıtlı** kullanıcıya uygulanır ve **claim'den rol yalnız düşürür;
  yükseltme Kullanıcılar sayfasından** yapılır (önbellekteki eski bir claim,
  düşürülmüş bir yöneticiyi geri yükseltemesin). Düşürme audit'lenir
  (`user.set_role_from_claim`, önceki → yeni rol, istek IP'si). Claim yoksa,
  boşsa ya da yalnız tanınmayan değer taşıyorsa rol değişmez. İlk kez giren
  kullanıcı yine varsayılan rolle açılır. Tek admin claim'le düşürülmez.
- Claim adı standart kimlik/profil claim'lerinden biri olamaz (`sub`, `name`,
  `given_name`, `family_name`, `preferred_username`, `email`, `profile`, …,
  `iss`, `aud`, `exp`, `iat`, `nonce`, `at_hash`, `azp`); kayıt reddedilir.
- Devre dışı bırakılmış bir kullanıcı SSO ile giremez ("hesap devre dışı");
  giriş onu yeni bir kullanıcı olarak yeniden açmaz.

> Not: merkezi login cevabı `ttlSeconds` boyunca önbellekte tutar. Bu açıkken
> bir yönetici Coremetry'de bir kullanıcıyı yükseltirse, kullanıcının o süre
> içindeki girişi önbellekteki eski (daha düşük) claim'i taşıyabilir ve rolü
> geri düşürebilir. Rol değişikliğinin hemen kalıcı olması gerekiyorsa TTL'i
> düşük tutun.

Depodaki ayar (ör. içe aktarılmış bir yedek) geçersiz yetki servisi alanları
taşıyorsa SSO girişi çalışmaya devam eder; yalnız yetki servisi kapanır ve
Settings > SSO'da "Son hata" olarak görünür.

## Gözlem

- Sayaç: `auth_permission_requests_total{result=ok|default_role|disabled_user|unauthorized|ip_denied|service_off|bad_request|error}`
  (self-observability açıksa).
- Log: çağrı başına tek satır, `slog` DEBUG düzeyinde; anahtar hiçbir zaman
  loglanmaz.
