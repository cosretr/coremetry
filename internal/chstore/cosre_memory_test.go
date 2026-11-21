package chstore

import (
	"encoding/json"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/cosrememory"
)

func TestDecodeCosreMemoryRows(t *testing.T) {
	row := func(id string, it cosrememory.Item, page, name string) SavedView {
		b, _ := json.Marshal(it)
		return SavedView{ID: CosreMemoryRowID(id), Page: page, Name: name, QueryString: string(b)}
	}
	ok := cosrememory.Item{ID: "a", Statement: "geçerli bir ekip notu", Source: cosrememory.Source{Kind: "manual", At: 2}}
	newer := ok
	newer.ID, newer.Source.At = "b", 5
	vote := ok
	vote.ID, vote.Source.Kind = "v", "vote"
	got := decodeCosreMemoryRows([]SavedView{
		row("a", ok, CosreMemoryPage, "note"),
		row("b", newer, CosreMemoryPage, "note"),
		row("t", ok, CosreMemoryPage, ""),       // tombstone
		row("x", ok, "traces", "note"),          // başka sayfa
		row("v", vote, CosreMemoryPage, "note"), // kapalı küme dışı kaynak
		{ID: CosreMemoryRowID("z"), Page: CosreMemoryPage, Name: "note", QueryString: "{bozuk"},
	})
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" || got[1].Status != cosrememory.StatusActive {
		t.Fatalf("çözüm: %+v", got)
	}
	if !IsSystemSavedViewPage(CosreMemoryPage) {
		t.Error("genel /api/views notları yazamamalı")
	}
}
