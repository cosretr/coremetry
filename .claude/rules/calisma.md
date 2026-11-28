# Çalışma kuralları (her oturum ve her ajan brifi için geçerli)

Bu kurallar ajan briflerine tek tek yazılmaz; brif "`.claude/rules/` geçerli" der.

- **Commit:** dosyalar ADIYLA stage edilir; `git add -A` ve çıplak `git stash` yok. Dilim başına tek commit;
  son satır oturumun bildirdiği imza satırı. Tag, push ve deploy yalnız operatör isteyince.
- **Rapor:** kapı ve komut sonuçları yalnız GERÇEK çıktının son satırlarıyla yazılır. Koşmayan ya da bitmeyen
  adım "koşmadı" / "doğrulanmadı" diye yazılır. Yük altında zaman aşımına düşen test tek başına yeniden koşulur
  ve o sonuç raporlanır. Tarayıcıda görülen ile yalnız testle doğrulanan ayrı yazılır.
- **Ad ve veri:** fikstür, test, belge, mockup ve commit'te gerçek şirket, cluster, servis ya da host adı yok.
  Kimlik bilgisi dosyaları okunmaz; anahtar, parola, belirteç hiçbir çıktıya yazılmaz.
- **İşlev kaybı yok:** bir sayfa ya da bileşen yeniden kurulurken klasik halin hiçbir işlevi (sekme, sütun,
  sparkline, satır bağlantısı, süzgeç) sessizce düşmez. Önce işlev dökümü, sonra eşleme; çıkarılan her işlev
  gerekçesiyle listelenir.
- **Karar operatörün:** veri silme, prod davranışı, güvenlik, canlı bildirim metni ve mevcut bir işlevin
  kaldırılması öneri cümlesine gömülmez; soru olarak sorulur.
- **Ajan:** alt ajan başka alt ajan açmaz. Worktree'de çalışan ajan önce taban etikete geçer
  (`git fetch --tags origin && git reset --hard <etiket>`).
