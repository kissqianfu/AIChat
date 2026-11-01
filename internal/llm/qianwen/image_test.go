package qianwen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 真实链路：POST 原生端点 → 拿到图片 URL → 当场下载。三处都断言到，
// 因为千问这两步都可能悄悄错（路径不对 = 404，只存 URL = 24 小时后失效）。
func TestImageGenRoundTrip(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/img.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNGDATA"))
			return
		}
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("请求体不是 JSON：%v", err)
		}
		// 内容数组刻意先给 text 再给 image：官方顺序不保证，实现不能取 content[0]。
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[
			{"text":"好的"},{"image":"` + srv.URL + `/img.png"}]}}]},
			"usage":{"image_count":1},"request_id":"req-1"}`))
	}))
	defer srv.Close()

	gen := NewImageGen(ImageConfig{
		APIKey:  func(context.Context) (string, error) { return "sk-test", nil },
		BaseURL: srv.URL, // 裸域名：应被补成原生路径
		Model:   "",
		Size:    "1024x1024", // OpenAI 写法，应被换成 1024*1024
		HTTP:    srv.Client(),
	})
	data, mime, err := gen(context.Background(), "一只橘猫")
	if err != nil {
		t.Fatalf("GenerateImage() error = %v", err)
	}
	if string(data) != "PNGDATA" || mime != "image/png" {
		t.Fatalf("结果 = %q/%q，期望 PNGDATA/image/png", data, mime)
	}
	if gotPath != imageGenPath {
		t.Fatalf("请求路径 = %q，期望 %q", gotPath, imageGenPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotBody["model"] != DefaultImageModel {
		t.Fatalf("model = %v，期望默认 %q", gotBody["model"], DefaultImageModel)
	}
	params, _ := gotBody["parameters"].(map[string]any)
	if params["size"] != "1024*1024" {
		t.Fatalf("size = %v，期望 1024*1024（百炼用星号）", params["size"])
	}
	if params["watermark"] != false {
		t.Fatalf("watermark = %v，期望 false", params["watermark"])
	}
	msgs, _ := gotBody["input"].(map[string]any)["messages"].([]any)
	content, _ := msgs[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["text"] != "一只橘猫" {
		t.Fatalf("提示词没进 input.messages[0].content[0].text：%v", content)
	}
}

// 接口报错要能指出是"文生图"这一步、带 code 与 request_id——
// 否则线上只看到一句 400，没法判断是模型名错、鉴权错还是参数错。
func TestImageGenAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"request_id":"req-9","code":"InvalidParameter","message":"model not found"}`))
	}))
	defer srv.Close()

	gen := NewImageGen(ImageConfig{
		APIKey:  func(context.Context) (string, error) { return "k", nil },
		BaseURL: srv.URL,
		Model:   "Qwen-Image-3.0", // 线上写错的那个名字
		HTTP:    srv.Client(),
	})
	_, _, err := gen(context.Background(), "猫")
	if err == nil {
		t.Fatal("400 应返回错误")
	}
	for _, want := range []string{"InvalidParameter", "model not found", "req-9"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误信息缺 %q：%v", want, err)
		}
	}
}

// 响应里没有 image（只有 text）时不能返回空图，否则上游发出去的是 0 字节文件。
func TestImageGenNoImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[{"text":"拒绝"}]}}]}}`))
	}))
	defer srv.Close()

	gen := NewImageGen(ImageConfig{
		APIKey:  func(context.Context) (string, error) { return "k", nil },
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
	})
	if _, _, err := gen(context.Background(), "猫"); err == nil {
		t.Fatal("没有图片时应报错")
	}
}

// 配置里 base_url 的三种常见写法都要落到同一个原生端点。
func TestImageEndpoint(t *testing.T) {
	const native = "/api/v1/services/aigc/multimodal-generation/generation"
	cases := []struct{ base, want string }{
		{"", "https://maas.qianwenaiapi.com" + native},
		{"https://dashscope.aliyuncs.com", "https://dashscope.aliyuncs.com" + native},
		{"https://dashscope.aliyuncs.com/", "https://dashscope.aliyuncs.com" + native},
		// 照抄对话槽位那一行（兼容模式）——最常见，必须能接住
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", "https://dashscope.aliyuncs.com" + native},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/", "https://dashscope.aliyuncs.com" + native},
		{"https://dashscope.aliyuncs.com/api/v1", "https://dashscope.aliyuncs.com" + native},
		// 业务空间专属域名
		{"https://ws1.cn-beijing.maas.aliyuncs.com", "https://ws1.cn-beijing.maas.aliyuncs.com" + native},
		// 已写全的端点原样使用
		{"https://proxy.example.com" + native, "https://proxy.example.com" + native},
	}
	for _, c := range cases {
		if got := ImageEndpoint(c.base); got != c.want {
			t.Fatalf("ImageEndpoint(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

// size 写法容错：1024x1024 / 1024*1024 / 1024X1024 都要能认，认不出才退默认。
func TestNormalizeImageSize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "1024*1024"},
		{"1024x1024", "1024*1024"},
		{"1024*1024", "1024*1024"},
		{"1024X768", "1024*768"},
		{"1024×1024", "1024*1024"},
		{"768", "1024*1024"},
		{"axb", "1024*1024"},
		{"0x100", "1024*1024"},
	}
	for _, c := range cases {
		if got := NormalizeImageSize(c.in); got != c.want {
			t.Fatalf("NormalizeImageSize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
