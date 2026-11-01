package text

import "testing"

// faceOnly 是线上抓到的表情正文原文：不同表情在这层面完全一样。
const faceOnly = `<faceType=6,faceId="0",ext="eyJ0ZXh0IjoiIn0=">`

func TestNormalizeInbound(t *testing.T) {
	cases := map[string]string{
		faceOnly:         PlaceholderFace,
		faceOnly + "看这个": PlaceholderFace + "看这个",
		" <faceType=1,faceId=\"178\",ext=\"\"/>" + " 笑死": PlaceholderFace + "笑死",
		"  普通文本 ": "普通文本",
		"":        "",
	}
	for in, want := range cases {
		if got := NormalizeInbound(in); got != want {
			t.Fatalf("NormalizeInbound(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasSubstance(t *testing.T) {
	cases := map[string]bool{
		"这语义缓存真不好用":              true,
		"在吗":                     true,
		"ok":                     true,
		"11":                     true,
		PlaceholderFace:          false,
		PlaceholderNoText:        false,
		PlaceholderFace + " ？。！": false,
		"":                       false,
	}
	for in, want := range cases {
		if got := HasSubstance(in); got != want {
			t.Fatalf("HasSubstance(%q) = %v, want %v", in, got, want)
		}
	}
}
