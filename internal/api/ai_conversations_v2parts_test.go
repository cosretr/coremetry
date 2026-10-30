package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// ai_conversations_v2parts_test.go — CoSRE v2 araç seti dilim 1: arşiv kaydı
// kompakt ve güvenli (kapalı kümeler, kök-göreli page_url, tavanlar).

func TestSanitizeChatToolResults(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	in := []aiChatToolResult{
		{ID: "s2", Data: raw(`{"kind":"text","tool":"get_service_health","ok":true,"text":"büyük önizleme","page_url":"/service?name=orders-api"}`)},
		{ID: "s3", Data: raw(`{"kind":"evidence","tool":"query_traces","ok":true,"query":"SELECT 1","page_url":"//evil.example"}`)},
		{ID: "x9", Data: raw(`{"kind":"text"}`)},  // kimlik biçimsiz
		{ID: "s4", Data: raw(`{"kind":"chart"}`)}, // kapalı küme dışı
		{ID: "s5", Data: raw(`[1,2]`)},            // nesne değil
		{ID: "s6", Data: raw(`{"kind":"skipped","ok":false,"reason":"call_cap"}`)},
	}
	got := sanitizeChatToolResults(in)
	ids := []string{}
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "s2,s3,s6" {
		t.Fatalf("kalan kimlikler %v", ids)
	}
	if strings.Contains(string(got[0].Data), "önizleme") || !strings.Contains(string(got[0].Data), `"page_url":"/service?name=orders-api"`) {
		t.Errorf("s2: text düşmeli, page_url kalmalı: %s", got[0].Data)
	}
	if strings.Contains(string(got[1].Data), "page_url") || !strings.Contains(string(got[1].Data), "SELECT 1") {
		t.Errorf("s3: dış page_url düşmeli, sorgu kalmalı: %s", got[1].Data)
	}
}

func TestSanitizeChatToolResultsCaps(t *testing.T) {
	var in []aiChatToolResult
	q := strings.Repeat("q", 500)
	for i := 1; i <= 30; i++ {
		in = append(in, aiChatToolResult{ID: "s" + strconv.Itoa(i), Data: json.RawMessage(`{"kind":"evidence","query":"` + q + `"}`)})
	}
	got := sanitizeChatToolResults(in)
	total := 0
	for _, r := range got {
		total += len(r.Data)
	}
	if len(got) > aiChatToolResultsMax || total > aiChatToolResultsMaxBytes || len(got) == 0 {
		t.Fatalf("tavan: %d kayıt, %d bayt", len(got), total)
	}
}

func TestSanitizeChatStop(t *testing.T) {
	if sanitizeChatStop(&aiChatStop{Reason: "bilinmeyen"}) != nil {
		t.Error("bilinmeyen neden kaldı")
	}
	got := sanitizeChatStop(&aiChatStop{Reason: "call_cap", Calls: -1, Rounds: 3, ElapsedMs: -5})
	if got == nil || got.Calls != 0 || got.Rounds != 3 || got.ElapsedMs != 0 {
		t.Errorf("%+v", got)
	}
}

// Kullanıcı turu araç sonucu / durma taşımaz.
func TestSanitizeChatMessagesV2PartsAssistantOnly(t *testing.T) {
	tr := []aiChatToolResult{{ID: "s1", Data: json.RawMessage(`{"kind":"text","ok":true}`)}}
	st := &aiChatStop{Reason: "stop", Calls: 1, Rounds: 1}
	out := sanitizeChatMessages([]aiChatMessage{
		{Role: "user", Text: "soru", ToolResults: tr, Stop: st},
		{Role: "assistant", Text: "cevap", ToolResults: tr, Stop: st},
	})
	if out[0].ToolResults != nil || out[0].Stop != nil {
		t.Error("kullanıcı turu v2 parçası taşıyor")
	}
	if len(out[1].ToolResults) != 1 || out[1].Stop == nil || out[1].Stop.Reason != "stop" {
		t.Errorf("asistan turu kaybetti: %+v", out[1])
	}
}
