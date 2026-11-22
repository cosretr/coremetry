package mcptools

// schema_validate_catalog_test.go — v0.10.1456 (C1, karar 6): yerli şema
// doğrulayıcısı (mcp/schema_validate.go) 80 aracın HER BİRİNİN gerçek
// şemasıyla tablo testli. Her araç için:
//
//  1. `{}` → bulgu listesi TAM OLARAK şemanın required alanlarıdır (sıra
//     dahil); required'sız araçta bulgu yok. Panik yok.
//  2. Şemadan türetilen asgari örnek (yalnız required) → bulgu yok.
//  3. Şemadan türetilen tam örnek (her özellik) → bulgu yok.
//  4. Sayı/boolean alanı metin olarak gönderilince ("30") → o alanda `type`
//     bulgusu (küçük modelin en sık hatası; doğrulayıcının varlık sebebi).
//
// Şemalar bu sürümde DEĞİŞMEDİ (toolset_catalog_golden_test.go altını yeşil);
// doğrulayıcının desteklemediği bir anahtar şemaya girerse 5. adım düşer ki
// yeni anahtar bilinçli olarak desteklensin ya da yok sayılsın.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// schemaKnownKeys — doğrulayıcının denetlediği + bilinçli yok saydığı anahtarlar.
var schemaKnownKeys = map[string]bool{
	"type": true, "properties": true, "required": true, "enum": true, "minimum": true, "maximum": true,
	"items": true, "maxItems": true, "minItems": true, "minLength": true, "maxLength": true, "pattern": true,
	"additionalProperties": true,
	"description":          true, // yok sayılır (modele metin)
}

func schemaExample(sch map[string]any, full bool) any {
	types := schemaTypeList(sch["type"])
	if enum := enumList(sch["enum"]); len(enum) > 0 {
		return enum[0]
	}
	t := ""
	if len(types) > 0 {
		t = types[0]
	}
	switch t {
	case "string":
		n := 1
		if mn, ok := numOf(sch["minLength"]); ok && int(mn) > n {
			n = int(mn)
		}
		s := ""
		for i := 0; i < n; i++ {
			s += "x"
		}
		return s
	case "integer", "number":
		v := 1.0
		if mx, ok := numOf(sch["maximum"]); ok && mx < v {
			v = mx
		}
		if mn, ok := numOf(sch["minimum"]); ok && mn > v {
			v = mn
		}
		return v
	case "boolean":
		return true
	case "array":
		items, _ := sch["items"].(map[string]any)
		if items == nil {
			return []any{}
		}
		return []any{schemaExample(items, full)}
	case "object", "":
		props, _ := sch["properties"].(map[string]any)
		out := map[string]any{}
		req := map[string]bool{}
		for _, r := range strList(sch["required"]) {
			req[r] = true
		}
		for name, p := range props {
			ps, _ := p.(map[string]any)
			if ps == nil || (!full && !req[name]) {
				continue
			}
			out[name] = schemaExample(ps, full)
		}
		return out
	}
	return nil
}

func schemaTypeList(v any) []string {
	if s, ok := v.(string); ok {
		return []string{s}
	}
	return strList(v)
}

func strList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func enumList(v any) []any {
	switch x := v.(type) {
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case []any:
		return x
	}
	return nil
}

func numOf(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func walkSchemaKeys(t *testing.T, tool, path string, v any) {
	m, ok := v.(map[string]any)
	if !ok {
		t.Errorf("%s %s: şema düğümü map değil (%T)", tool, path, v)
		return
	}
	for k, sub := range m {
		if !schemaKnownKeys[k] {
			t.Errorf("%s %s: doğrulayıcının bilmediği şema anahtarı %q — schema_validate.go'ya ekle ya da bilinçli yok say", tool, path, k)
		}
		switch k {
		case "properties":
			pm, _ := sub.(map[string]any)
			for n, p := range pm {
				walkSchemaKeys(t, tool, path+"."+n, p)
			}
		case "items":
			walkSchemaKeys(t, tool, path+"[]", sub)
		case "additionalProperties":
			if am, ok := sub.(map[string]any); ok {
				walkSchemaKeys(t, tool, path+"{}", am)
			}
		}
	}
}

func TestAllCatalogSchemasValidateTheirOwnExamples(t *testing.T) {
	tools := ToolList(Deps{})
	if len(tools) != 80 {
		t.Fatalf("katalog %d araç — 80 pini (discovery_test.go) ile aynı küme beklenir", len(tools))
	}
	typeProbes := 0
	for _, tl := range tools {
		tl := tl
		t.Run(tl.Name, func(t *testing.T) {
			sch := tl.InputSchema
			if sch == nil {
				t.Fatal("şema nil")
			}
			walkSchemaKeys(t, tl.Name, "", sch)

			// 1. {} → tam olarak required listesi.
			got := mcp.ValidateArgs(sch, json.RawMessage(`{}`))
			var gotReq []string
			for _, is := range got {
				if is.Rule != "required" {
					t.Errorf("{} → beklenmeyen bulgu %+v", is)
				}
				gotReq = append(gotReq, is.Path)
			}
			wantReq := strList(sch["required"])
			if len(wantReq) > 8 { // argIssuesMax
				wantReq = wantReq[:8]
			}
			if len(wantReq) == 0 {
				wantReq = nil
			}
			if !reflect.DeepEqual(gotReq, wantReq) {
				t.Errorf("{} → required %v, şema %v", gotReq, wantReq)
			}

			// 2-3. asgari + tam örnek geçerli.
			for _, full := range []bool{false, true} {
				ex, _ := json.Marshal(schemaExample(sch, full))
				if is := mcp.ValidateArgs(sch, ex); len(is) != 0 {
					t.Errorf("örnek (full=%v) %s → %+v", full, ex, is)
				}
			}

			// 4. sayı/boolean alanı metin olarak.
			props, _ := sch["properties"].(map[string]any)
			names := make([]string, 0, len(props))
			for n := range props {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				ps, _ := props[n].(map[string]any)
				ty := schemaTypeList(ps["type"])
				if len(ty) != 1 || (ty[0] != "integer" && ty[0] != "number" && ty[0] != "boolean") {
					continue
				}
				args := schemaExample(sch, false).(map[string]any)
				args[n] = fmt.Sprint(schemaExample(ps, false))
				raw, _ := json.Marshal(args)
				is := mcp.ValidateArgs(sch, raw)
				if len(is) != 1 || is[0].Path != n || is[0].Rule != "type" {
					t.Errorf("%s metin olarak → %+v, tek type bulgusu beklenir", n, is)
				}
				typeProbes++
			}
		})
	}
	if typeProbes < 50 { // HEAD: ~110 sayı/boolean alanı; sıfıra düşerse üreteç bozuldu
		t.Errorf("tip sondası yalnız %d alan", typeProbes)
	}
}
