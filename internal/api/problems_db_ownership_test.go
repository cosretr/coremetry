package api

import (
	"strings"
	"testing"
)

// problems_db_ownership_test.go — v0.9.1345.
//
// db-konulu problemlerin (v0.9.1338) sahipliği artık TÜRETİLİYOR: "bu
// veritabanını en çok çağıran servisin takımı" (operatör kararı
// 2026-08-24, chstore/db_ownership.go).
//
// ── BU DOSYANIN KORUDUĞU ÇELİŞKİ ────────────────────────────────────
//
// Takım süzgeci SQL'e bir SERVİS ADI allowlist'i olarak iniyor
// (servicesForTeam, v0.9.342 perf düzeltmesi). Bir db öznesinin `service`
// alanı bir DBSubjectID (`db:oracle@corebank-scan.prod`) ve o listede
// ASLA olamaz. Yani sahiplik verilip kaçış kapısı VERİLMEZSE ürün kendi
// kendisiyle çelişir: satırın çipi "core-banking" yazarken
// owner=core-banking süzgeci o satırı SESSİZCE gizler.
//
// Kaçış kapısı TEK BAŞINA da yeterli değil ve ters yönde tehlikeli:
// gevşetilmiş SQL BÜTÜN db problemlerini içeri alır, dolayısıyla Go
// tarafında KESİN eşleştirme (matchesTeamFilter, zenginleştirilmiş
// OwnerTeam üzerinden) olmak ZORUNDA. İkisi BİRLİKTE doğru; biri
// eksikken ürün ya satır gizler ya başka takımın satırını gösterir.
//
// Neden kaynak taraması: bu iki handler canlı bir Store olmadan
// çalıştırılamıyor. inbox_team_scope_test.go (v0.9.353) aynı sınırla
// aynı çözümü kullanıyor — bu dosya onun /problems + /problems/buckets
// tarafındaki ikizi. Saf karar zaten ayrı ve tablo-testli
// (chstore.problemServicesConjunct).

// ⚠️ KAYNAK TARAYAN GATE YORUMLARI OKUMAMALI (v0.9.1345, ilk yazımın
// bulgusu). Bu dosyanın sıralama kontrolü ÖNCE yanlış alarm verdi: aranan
// `matchesTeamFilter` adı, o çağrıyı AÇIKLAYAN yorumda çağrıdan ÖNCE
// geçiyordu. Yani gate kodu değil PROZAYI ölçtü.
//
// Aynı kusur ters yönde çok daha tehlikeli: bir yorumda geçen ad, silinmiş
// bir çağrının yerine geçip gate'i SUSTURABİLİR. Bu yüzden tarama
// yorumlardan arındırılmış kaynağa bakıyor.
//
// stripLineComments yalnız `//` satır yorumlarını atar — bu dosyanın
// baktığı bölgede blok yorum yok ve dizgi literali içinde `//` geçmiyor
// (kontrol edildi). Daha fazlası bir Go ayrıştırıcısı ister ve bu gate'in
// kazandığından fazlasını maliyet olarak getirirdi.
func stripLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestProblemsTeamFilterLetsDBSubjectsThrough — /api/problems listesi.
func TestProblemsTeamFilterLetsDBSubjectsThrough(t *testing.T) {
	src := stripLineComments(readSrc(t, "api.go"))

	i := strings.Index(src, "scan.Services = servicesForTeam(")
	if i < 0 {
		t.Fatal("listProblems takım allowlist'ini çözmüyor")
	}
	// Kaçış kapısı allowlist ÇÖZÜMÜYLE aynı dalda olmalı: allowlist
	// kurulmadığında (katalog blip'i) daraltma zaten kapalıdır ve
	// gevşetmenin anlamı yoktur.
	window := srcWindow(src, i, 900)
	if !strings.Contains(window, "scan.ServicesAllowDBSubjects = true") {
		t.Error("listProblems: db özneleri için kaçış kapısı YOK — sahiplikleri " +
			"türetildiği hâlde owner/sre süzgeci onları SQL'de eler; satırın " +
			"çipi bir takım yazarken o takımın süzgeci satırı gizler")
	}

	// ...ve gevşetmenin karşılığı olan KESİN eşleştirme hâlâ orada.
	if !strings.Contains(src, "matchesTeamFilter(ta, p.OwnerTeam, p.SRETeam, ownerTeam, sreTeam)") {
		t.Error("listProblems: kesin takım eşleştirmesi kaybolmuş — gevşetilmiş " +
			"SQL bütün db problemlerini içeri alır, Go kapısı olmadan operatör " +
			"BAŞKA takımların veritabanı alarmlarını görür")
	}
}

// srcWindow — kaynağın [from, from+n) dilimi, sınır güvenli.
func srcWindow(src string, from, n int) string {
	to := from + n
	if to > len(src) {
		to = len(src)
	}
	return src[from:to]
}
