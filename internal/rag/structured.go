package rag

// structured.go — v0.10.1174 — JSON / YAML içerik RAG'e "anahtar yolu: değer"
// KAYITLARI olarak girer (operatör: "içeride JSON / yaml olunca anlayamıyor").
//
// Ham YAML/JSON parçalara bölününce bir değerin hangi anahtara ait olduğu
// (ör. hangi cluster'ın names listesi) parça sınırında kaybolur ve model
// yapıyı yanlış okur. Her "kayıt" (yalnız skaler / skaler-dizi alanları olan
// eşleme) yolu ile kendi paragrafı olur:
//
//	clusters[1] — zone: test · domain: example.com · names: kubeint, kubeuat
//
// İç içe eşlemeler kendi yollarıyla ayrı paragraf; sıra kaynaktaki sıra.
// YAML JSON'un üst kümesi: ikisi de yaml.v3 Node ağacıyla okunur (çok
// belgeli YAML dahil).
//   - .json / .yaml / .yml dosyası: StructuredToText (ham metin + kayıtlar).
//   - md / txt içindeki ```yaml / ```json blokları: AppendFencedStructure
//     belgenin sonuna o blokların kayıtlarını ekler (ham metin aynen kalır).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	structMaxRecords  = 50000
	structValMaxRunes = 500
	structRawMaxRunes = 200000 // ham metin bundan uzunsa yalnız kayıtlar
)

// IsStructuredFile — SAF: uzantı JSON / YAML mı.
func IsStructuredFile(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(low, ".json") || strings.HasSuffix(low, ".yaml") || strings.HasSuffix(low, ".yml")
}

// StructuredToText — JSON / YAML dosyası: kayıtlar (+ kısa ise ham metin).
func StructuredToText(name string, b []byte) (string, error) {
	recs, err := structuredRecords(b)
	if err != nil {
		return "", fmt.Errorf("%s okunamadı: %w", name, err)
	}
	if len(recs) == 0 {
		return "", fmt.Errorf("%s içinde değer yok", name)
	}
	out := strings.Join(recs, "\n\n")
	if raw := string(b); len([]rune(raw)) <= structRawMaxRunes {
		out += "\n\n```\n" + strings.TrimSpace(raw) + "\n```"
	}
	return out, nil
}

var fencedStructRe = regexp.MustCompile("(?ms)^\\s*```[ \\t]*(yaml|yml|json)[^\\n]*\\n(.*?)^\\s*```")

// AppendFencedStructure — SAF: md / txt metnindeki ```yaml / ```json
// bloklarının kayıtlarını sona ekler; okunamayan blok atlanır.
func AppendFencedStructure(text string) string {
	var extra []string
	for _, m := range fencedStructRe.FindAllStringSubmatch(text, -1) {
		recs, err := structuredRecords([]byte(m[2]))
		if err != nil || len(recs) == 0 {
			continue
		}
		extra = append(extra, recs...)
	}
	if len(extra) == 0 {
		return text
	}
	return text + "\n\n## Yapılandırma kayıtları (YAML / JSON bloklarından)\n\n" + strings.Join(extra, "\n\n")
}

func structuredRecords(b []byte) ([]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var out []string
	for doc := 0; ; doc++ {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		root := ""
		if doc > 0 {
			root = "belge" + strconv.Itoa(doc+1)
		}
		walkStructured(&n, root, &out)
		if len(out) >= structMaxRecords {
			break
		}
	}
	return out, nil
}

// walkStructured — kayıt üretimi (bkz. dosya başı).
func walkStructured(n *yaml.Node, path string, out *[]string) {
	if n == nil || len(*out) >= structMaxRecords {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			walkStructured(c, path, out)
		}
	case yaml.AliasNode:
		walkStructured(n.Alias, path, out)
	case yaml.ScalarNode:
		if v := scalarText(n); v != "" {
			*out = append(*out, label(path, "değer")+": "+v)
		}
	case yaml.SequenceNode:
		if vals, ok := scalarList(n); ok {
			if len(vals) > 0 {
				*out = append(*out, label(path, "liste")+": "+strings.Join(vals, ", "))
			}
			return
		}
		for i, c := range n.Content {
			walkStructured(c, path+"["+strconv.Itoa(i)+"]", out)
		}
	case yaml.MappingNode:
		var fields []string
		type child struct {
			key  string
			node *yaml.Node
		}
		var nested []child
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i].Value, n.Content[i+1]
			if v.Kind == yaml.AliasNode {
				v = v.Alias
			}
			switch {
			case v == nil:
			case v.Kind == yaml.ScalarNode:
				if s := scalarText(v); s != "" {
					fields = append(fields, k+": "+s)
				}
			case v.Kind == yaml.SequenceNode:
				if vals, ok := scalarList(v); ok {
					if len(vals) > 0 {
						fields = append(fields, k+": "+strings.Join(vals, ", "))
					}
					continue
				}
				nested = append(nested, child{k, v})
			default:
				nested = append(nested, child{k, v})
			}
		}
		if len(fields) > 0 {
			*out = append(*out, label(path, "kök")+" — "+strings.Join(fields, " · "))
		}
		for _, c := range nested {
			p := c.key
			if path != "" {
				p = path + "." + c.key
			}
			walkStructured(c.node, p, out)
		}
	}
}

func label(path, fallback string) string {
	if path == "" {
		return fallback
	}
	return path
}

func scalarText(n *yaml.Node) string {
	return clampRunes(strings.Join(strings.Fields(n.Value), " "), structValMaxRunes)
}

func scalarList(n *yaml.Node) ([]string, bool) {
	vals := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		if c.Kind != yaml.ScalarNode {
			return nil, false
		}
		if s := scalarText(c); s != "" {
			vals = append(vals, s)
		}
	}
	return vals, true
}
