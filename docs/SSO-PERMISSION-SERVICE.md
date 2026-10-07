# SSO — merkezi login yetki servisi (v0.10.1110; anahtarsız kip + IP izin listesi v0.10.1111; IdP TLS v0.10.1112; doğrulanmamış e-posta v0.10.1120; e-posta çözüm zinciri v0.10.1121)

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

## TLS — kurum içi CA ile imzalı kimlik sağlayıcı (v0.10.1112)

IdP'nin sertifikası kurum içi bir CA ile imzalıysa Coremetry'nin keşif / JWKS /
token çağrıları sertifika doğrulamasında düşer ("Bağlantıyı test et" →
"kimlik sağlayıcıya ulaşılamadı"). Settings > SSO'da Issuer'ın altında iki seçenek:

- **Özel CA sertifikası (PEM) — önerilen.** Kurum CA'sının (gerekirse ara CA'ların)
  `-----BEGIN CERTIFICATE-----` bloklarını yapıştırın; birden çok sertifika
  eklenebilir, en çok 64 KB. Sertifikalar sistem kök sertifikalarına **eklenir**.
  Özel anahtar yapıştırmayın — kayıt (SSO kapalıyken de) reddedilir. Yalnız
  sertifika blokları saklanır; aradaki yorum satırları kayıtta düşer. CA'yı
  IdP'nin zincirinden almak için:

  ```bash
  openssl s_client -connect idp.example.test:443 -showcerts </dev/null
  ```

- **TLS sertifika doğrulamasını kapat — son çare.** Sertifika hiç doğrulanmaz;
  ağ yolundaki biri IdP'yi taklit edip kendi imzaladığı token'la herhangi bir
  kullanıcı olarak giriş yapabilir. Açıkken Settings kırmızı uyarı gösterir ve
  sunucu her ayar yüklemesinde bir WARN satırı loglar.

Her iki durumda da **https zorunlu** kalır (doğrulamayı kapatmak düz `http`'ye
izin vermez) ve IdP adresleri yine loopback / link-local / bulut metadata
adresine gidemez. "Bağlantıyı test et" formdaki **kaydedilmemiş** değerlerle
çalışır — önce test edip sonra kaydedin. Değişiklik audit'e eski→yeni olarak
girer (`settings.oidc.update`; CA için PEM değil, CN + SHA-256 parmak izi).
Depodaki CA çözülemiyorsa SSO özel CA olmadan uygulanır ve "Son hata"da görünür.

## Doğrulanmamış e-posta — `email_verified=false` (v0.10.1120)

Coremetry, id_token'da `email_verified` claim'i **varsa ve false ise** girişi
reddeder (`[oidc] callback failed: class=email_unverified`) — doğrulanmamış bir
e-posta başka bir kullanıcının hesabına bağlanamasın diye. Claim hiç yoksa ya da
true ise giriş normal sürer. AD/LDAP federasyonlu kurumsal IdP'ler e-postayı
çoğu zaman doğrulamadan `false` gönderir.

**Önerilen düzeltme IdP tarafında (Keycloak "Trust Email"):**

- LDAP/AD federasyonu: *User federation → (LDAP sağlayıcısı) → Advanced settings
  → Trust Email* açın, ardından *Sync all users* ile mevcut kullanıcıları
  yeniden eşitleyin.
- Dış kimlik sağlayıcı (brokering): *Identity providers → (sağlayıcı) → Trust
  Email* açın.

**Coremetry tarafı (önerilmez, açık-seçim):** Settings > SSO'da TLS kutusunun
altındaki "Doğrulanmamış e-postaya güven (önerilmez)" (`trustUnverifiedEmail`,
varsayılan kapalı). Yalnız **izinli alan adları** doluyken seçilebilir ve
etkilidir: `email_verified=false` olan giriş kabul edilir ama e-postanın alan
adı listede olmalıdır (alan adı kontrolü aynen sürer). SSO açıkken liste boşsa
kayıt 400 döner ("İzinli alan adları boşken doğrulanmamış e-postaya
güvenilemez"); SSO'yu kapatan kayıt her zaman geçer. Depodaki blob elle böyle
yazılmışsa anahtar kapalı sayılır ve "Son hata"da görünür.

**Ön koşul ve risk.** Anahtarı yalnız IdP kullanıcının e-postasını **kendisinin
belirleyemediği ve değiştiremediği** bir kurulumda açın: self-registration yok,
hesap konsolunda/profilde e-posta düzenleme yok, sosyal ya da brokered
(dış) IdP yok. Aksi hâlde biri e-postasını bir başkasınınkiyle aynı yapıp o
hesaba girebilir (**e-posta çarpışmasıyla hesap ele geçirme**). Bu riski
sınırlamak için anahtar sayesinde gelen giriş **admin hesabına ve yerel/LDAP
hesaplara bağlanmaz** — giriş sayfası "Bu hesap doğrulanmamış e-posta ile
SSO'dan açılamaz; parola/LDAP ile girin veya IdP'de Trust Email açılsın" der,
loga yalnız kullanıcı id'si düşer (`class=email_unverified_privileged`). Yeni
kullanıcı varsayılan rolle açılır; mevcut oidc viewer/editor normal girer.
Bu anahtar sayesinde gelen girişte ASCII dışı karakter içeren e-posta
`email_invalid` ile reddedilir (Unicode katlamasıyla alan adı/hesap çarpışması
olmasın); doğrulanmış ya da claim'siz girişte Türkçe karakterli e-posta eskisi
gibi kabul edilir.
Yalnız bu anahtar sayesinde kabul edilen girişler loga alan adı başına saatte
bir `[auth] WARNING: OIDC email_verified=false accepted (trustUnverifiedEmail)
domain=example.test` satırı bırakır (tam e-posta loglanmaz); anahtar açıkken her
ayar yüklemesinde bir WARN daha düşer. Değişiklik audit'e eski→yeni girer
(`settings.oidc.update`). IdP tarafı düzeltildiğinde anahtarı kapatın.

## E-posta yok — `email_missing`: çözüm zinciri (v0.10.1121)

Bazı kurumsal IdP'ler (LDAP federasyonlu Keycloak) id_token'a `email` koymaz;
`preferred_username` ise AD sAMAccountName'idir (sicil, ör. `n0000001`).
Coremetry bu durumda girişi `[oidc] callback failed: class=email_missing` ile
reddediyordu.

**Önerilen düzeltme IdP tarafında (Keycloak):**

- *User federation → (LDAP sağlayıcısı) → Mappers*: `mail` LDAP özniteliğini
  `email` kullanıcı özniteliğine eşleyen bir *user-attribute-ldap-mapper*
  olmalı (varsayılan "email" mapper'ı; yoksa ekleyin), ardından *Sync all users*.
- *Clients → (coremetry) → Client scopes*: `email` scope'u **Default** olarak
  bağlı olsun; *Client scopes → email → Mappers → email* içinde **Add to ID
  token** açık olsun. Kapsamlarda (`scopes`) `email` bulunmalı.
- Gerekirse doğrulama: "Doğrulanmamış e-posta" bölümündeki *Trust Email*.

**Coremetry tarafı — çözüm zinciri.** id_token'da e-posta YOKSA şu sıra
denenir, ilk isabette durulur:

1. id_token `email` (e-posta varsa davranış birebir eskisi gibi).
2. **UserInfo** `email` — her zaman denenir (keşif belgesinde
   `userinfo_endpoint` varsa); IdP'ye aynı sınırlı istemci ve TLS ayarıyla
   (özel CA / skip-verify) gidilir, ≤5 sn. UserInfo'nun `sub`'ı id_token'ınkiyle
   aynı değilse giriş **reddedilir** (`userinfo_sub_mismatch`). UserInfo'daki
   `email_verified` id_token'daki gibi değerlendirilir (güven anahtarı + izinli
   alan adları); UserInfo `email_verified` göndermiyorsa id_token'daki değer
   kullanılır.
3. **"E-posta yoksa kullanıcı adıyla eşleştir (AD/LDAP)"** (`usernameFallback`,
   varsayılan **kapalı**) açıksa: *Kullanıcı adı claim'i* (`usernameClaim`,
   varsayılan `preferred_username`; yalnız bu claim okunur) doğrulanmış
   id_token'dan (yoksa sub'ı eşleşmiş UserInfo'dan) alınır:
   - **LDAP yapılandırılmışsa** servis hesabıyla dizinde TEK öznitelikte tam
     eşleşme: `sAMAccountName` (devre dışı AD hesapları hariç) ya da ayarlı
     benzersiz kullanıcı özniteliği (ör. OpenLDAP `uid`); `mail`,
     `userPrincipalName`, `cn`, `displayName` gibi öznitelikler anahtar olamaz.
     Kaçışlı filtre, en çok 2 kayıt, ≤5 sn. Birden çok kayıt ⇒ red
     (`username_ambiguous`), dizin hatası ⇒ red (`directory_lookup_failed`;
     loga kategori başına dakikada bir `[oidc] directory lookup failed:
     category=bind|timeout|search|ambiguous|other`). Dizinde kayıt **yoksa**
     ⇒ `email_missing` (Coremetry'deki eski bir `ldap_username` satırına
     düşülmez).
     Kaydın e-postası (LDAP girişinin kullandığı e-posta özniteliği, boşsa
     `mail`) **doğrulanmış** sayılır — dizin yetkili kaynaktır; LDAP girişiyle
     aynı kullanıcı satırına düşer. İzinli alan adları uygulanır.
   - **LDAP yoksa ya da dizin kaydında e-posta yoksa** kullanıcı, Coremetry'deki
     **mevcut, devre dışı olmayan** kullanıcıya LDAP kullanıcı adıyla
     (`ldap_username`, büyük-küçük harf duyarsız) eşlenir; iki kullanıcı
     eşleşirse red (`username_ambiguous`); kayıtlı e-postasına izinli alan
     adları uygulanır.
     E-postasız **yeni kullanıcı açılmaz** — eşleşme yoksa `email_missing`.

Kullanıcı adı yoluyla gelen giriş güven anahtarı (`trustUnverifiedEmail`)
sayılmaz; kimlik, IdP'nin imzaladığı id_token'daki kullanıcı adıdır — yerel /
LDAP / oidc viewer-editor hesaplar bu yoldan açılır. **Admin hesabı açılmaz**
(giriş sayfası "Yönetici hesabı SSO'da kullanıcı adı eşleştirmesiyle
açılamaz…", `class=username_admin_refused`); yalnız ayrı açık-seçim "Kullanıcı
adı eşleştirmesiyle admin hesaplarını da aç (önerilmez)"
(`usernameFallbackAllowAdmin`, varsayılan kapalı, yalnız eşleştirme açıkken)
bunu değiştirir. **Ön koşul:** kullanıcı bu claim'i IdP'de değiştirememeli
(LDAP federasyonu salt-okunur, "Edit username" kapalı). Brokered (dış/sosyal)
IdP, self-registration ya da düzenlenebilir kullanıcı adı varsa bu seçenek
**güvensizdir** — kullanıcı adını seçebilen, o hesabı açar. Kullanıcı adı `@`
içeremez. Claim adı yalnız harf/rakam ve `_ - . :` (≤64); profil/e-posta/protokol
claim'leri (`email`, `name`, `nickname`, `aud` …) seçilemez (400). Kullanıcı
adı değeri yalnız yazdırılabilir ASCII kabul edilir.

**Log.** Başarı: `[oidc] email resolved via userinfo|ldap|ldap_username user
id=<id>` (e-posta yok). Başarısızlık: `[oidc] email resolution failed:
class=email_missing tried=id_token,userinfo,… claims=<claim adları>` — yalnız
claim ADLARI, değerler asla. Anahtarlar ve claim adındaki değişiklik audit'e
eski→yeni girer (`settings.oidc.update`).

## Gözlem

- Sayaç: `auth_permission_requests_total{result=ok|default_role|disabled_user|unauthorized|ip_denied|service_off|bad_request|error}`
  (self-observability açıksa).
- Log: çağrı başına tek satır, `slog` DEBUG düzeyinde; anahtar hiçbir zaman
  loglanmaz.
