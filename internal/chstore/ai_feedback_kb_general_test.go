package chstore

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
)

// v0.10.194 kaynak-pini — genel bilgi cevabı (surface chat-general) KB adayı
// listesine giremez; süzgeç SQL'de, gerekçe ListKBCandidates başlığında.
// v0.10.1153 — dışlama listesi elle yazılmış literal değil, yüzey kaydından
// (aisurface.NoKBLabels) bağlanır; chat-general o listede kalmalı.
func TestKBCandidatesExcludeGeneralAnswers(t *testing.T) {
	b, err := os.ReadFile("ai_feedback.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Store) ListKBCandidates(")
	if i < 0 {
		t.Fatal("ListKBCandidates bulunamadı")
	}
	body := src[i:]
	if !strings.Contains(body, "AND c.surface NOT IN (?)") || !strings.Contains(body, "aisurface.NoKBLabels()") {
		t.Fatal("ListKBCandidates kayıttaki KB-dışı yüzeyleri süzmüyor — kanıtsız cevap rag_chunks'a girebilir")
	}
	if !slices.Contains(aisurface.NoKBLabels(), aisurface.ChatGeneral) {
		t.Fatal("chat-general KB-dışı değil")
	}
}
