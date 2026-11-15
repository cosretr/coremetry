package rag

// structured_test.go — v0.10.1174: JSON / YAML anahtar yollu kayıtlar.

import (
	"strings"
	"testing"
)

func TestStructuredYAMLRecordsKeepKeyPaths(t *testing.T) {
	y := "clusters:\n  - { zone: dmz, domain: example.com, names: [kubedmza, kubedmzb] }\n  - zone: test\n    names: [kubeint, kubeuat]\n    thanos: { url: https://t, timeout: 30s }\nversion: 3\n"
	got, err := StructuredToText("config.yaml", []byte(y))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"clusters[0] — zone: dmz · domain: example.com · names: kubedmza, kubedmzb",
		"clusters[1] — zone: test · names: kubeint, kubeuat",
		"clusters[1].thanos — url: https://t · timeout: 30s",
		"kök — version: 3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("eksik %q\n%s", want, got)
		}
	}
	if !strings.Contains(got, "```") {
		t.Error("kısa dosyada ham metin de kalmalı")
	}
}

func TestStructuredJSONAndFenced(t *testing.T) {
	got, err := StructuredToText("inv.json", []byte(`{"servers":[{"host":"srv-1","db":"ORCL19"}],"tags":["a","b"]}`))
	if err != nil || !strings.Contains(got, "servers[0] — host: srv-1 · db: ORCL19") || !strings.Contains(got, "kök — tags: a, b") {
		t.Fatalf("json: %v\n%s", err, got)
	}
	if _, err := StructuredToText("bozuk.json", []byte(`{"a":`)); err == nil {
		t.Fatal("bozuk JSON hata vermeli")
	}
	md := "# Kurulum\n\nÖrnek:\n\n```yaml\nclusters:\n  - { zone: dmz, names: [kubedmza] }\n```\n\nSon.\n"
	out := AppendFencedStructure(md)
	if !strings.HasPrefix(out, md) || !strings.Contains(out, "clusters[0] — zone: dmz · names: kubedmza") {
		t.Fatalf("fenced:\n%s", out)
	}
	if AppendFencedStructure("düz metin") != "düz metin" {
		t.Fatal("blok yoksa metin aynen")
	}
	if !IsStructuredFile("A.YML") || IsStructuredFile("a.md") {
		t.Fatal("IsStructuredFile")
	}
}
