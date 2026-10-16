package langdetect

import "testing"

func TestDetect(t *testing.T) {
	cases := map[string]string{
		"<p>今天天气很好，我们去公园散步吧</p>":                                           "zh",
		"今日はとても良い天気ですね、散歩に行きましょう":                                          "ja",
		"오늘 날씨가 정말 좋네요 산책하러 가요":                                            "ko",
		"Сегодня очень хорошая погода, пойдём гулять":                      "ru",
		"The weather is really nice today, let's go for a walk":            "en",
		"Das Wetter ist heute schön und wir gehen spazieren":               "de",
		"Il fait très beau aujourd'hui, allons nous promener dans le parc": "fr",
		"ok": "",
		`<a href="https://x.y/abc">https://x.y/abc</a> @a@b.c #tag :blob:`: "",
		"@someone@misskey.io 新しいノートを書いた https://misskey.io/notes/abc":      "ja",
	}
	for in, want := range cases {
		if got := Detect(in); got != want {
			t.Errorf("Detect(%q)=%q want %q", in, got, want)
		}
	}
}

func TestBaseAndContentMap(t *testing.T) {
	for in, want := range map[string]string{"zh-CN": "zh", "zh_TW": "zh", "EN": "en", "und-": "und", "": ""} {
		if got := Base(in); got != want {
			t.Errorf("Base(%q)=%q", in, got)
		}
	}
	if got := FromContentMap(map[string]any{"content": "x", "contentMap": map[string]any{"ja": "x"}}); got != "ja" {
		t.Fatal(got)
	}
	if got := ParseAcceptLanguage("zh-TW,zh;q=0.9,en;q=0.8,fr;q=0"); len(got) != 3 || got[0] != "zh" || got[2] != "en" {
		t.Fatal(got)
	}
}
