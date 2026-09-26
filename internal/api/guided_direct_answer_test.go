package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// Netleştirme soruları (hangi servis / hangi takım) LLM'siz: aday listesi,
// soru ve çipler anlatımdan önce belli; metni sunucu kurar. Takım sorusu
// v0.10.559'un iki grubunu (uygulama VE SRE) ve "+N takım" satırını korur.
func TestAskTeamAnswerKeepsBothGroups(t *testing.T) {
	entries := []mcptools.TeamCatalogueEntry{{Team: "UG-1", Services: 2, Owner: 2}, {Team: "SY-A", Services: 9, SRE: 9}, {Team: "SY-B", Services: 8, SRE: 8}}
	txt := askTeamAnswerTR("Hesabına takım atanmamış. ", entries, 2)
	for _, w := range []string{"Hesabına takım atanmamış.", "Hangi takımdasın?", "Uygulama takımları", "UG-1", "SRE takımları", "SY-A", "1 takım daha"} {
		if !strings.Contains(txt, w) {
			t.Errorf("metin %q taşımalı:\n%s", w, txt)
		}
	}
	for _, bad := range []string{"KULLANICIYA", "UYDURMA", "KURAL"} {
		if strings.Contains(txt, bad) {
			t.Errorf("operatöre giden metinde model talimatı kaldı (%q):\n%s", bad, txt)
		}
	}
	if none := askTeamAnswerTR("", nil, 8); !strings.Contains(none, "takım ataması yok") {
		t.Errorf("boş katalog: %q", none)
	}
}

func TestAskServiceAnswer(t *testing.T) {
	if got := askServiceAnswerTR([]string{"checkout", "checkout-worker"}); !strings.Contains(got, "checkout, checkout-worker") || !strings.Contains(got, "Hangi servisi") {
		t.Errorf("adaylı: %q", got)
	}
	if got := askServiceAnswerTR(nil); !strings.Contains(got, "Servis adını yazar mısın") {
		t.Errorf("adaysız: %q", got)
	}
}
