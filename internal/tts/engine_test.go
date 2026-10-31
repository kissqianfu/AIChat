package tts

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeSynth 返回固定 PCM。
type fakeSynth struct {
	pcm []byte
	err error
}

func (f *fakeSynth) Speech(context.Context, string, string) ([]byte, error) {
	return f.pcm, f.err
}

// fakeEncoder 返回固定 silk。
type fakeEncoder struct {
	silk []byte
	err  error
}

func (f *fakeEncoder) Encode(context.Context, []byte) ([]byte, error) {
	return f.silk, f.err
}

func TestEngineSynthesize(t *testing.T) {
	engine, err := NewEngine(&fakeSynth{pcm: []byte("PCM")}, &fakeEncoder{silk: []byte("SILK")}, "voice-1", nil)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	silk, err := engine.Synthesize(context.Background(), " 你好 ")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if string(silk) != "SILK" {
		t.Fatalf("silk = %q", silk)
	}
}

func TestEngineValidation(t *testing.T) {
	if _, err := NewEngine(nil, &fakeEncoder{}, "v", nil); err == nil {
		t.Fatal("nil synth 未报错")
	}
	if _, err := NewEngine(&fakeSynth{}, nil, "v", nil); err == nil {
		t.Fatal("nil encoder 未报错")
	}
	if _, err := NewEngine(&fakeSynth{}, &fakeEncoder{}, "  ", nil); err == nil {
		t.Fatal("空 voice_id 未报错")
	}
	engine, _ := NewEngine(&fakeSynth{pcm: []byte("P")}, &fakeEncoder{silk: []byte("S")}, "v", nil)
	if _, err := engine.Synthesize(context.Background(), ""); err == nil {
		t.Fatal("空文本未报错")
	}
	long := strings.Repeat("字", maxSynthRunes+1)
	if _, err := engine.Synthesize(context.Background(), long); err == nil {
		t.Fatal("超长文本未报错")
	}
}

func TestEngineErrors(t *testing.T) {
	synthErr, _ := NewEngine(&fakeSynth{err: errors.New("boom")}, &fakeEncoder{silk: []byte("S")}, "v", nil)
	if _, err := synthErr.Synthesize(context.Background(), "hi"); err == nil {
		t.Fatal("合成失败未透传")
	}
	encErr, _ := NewEngine(&fakeSynth{pcm: []byte("P")}, &fakeEncoder{err: errors.New("silk down")}, "v", nil)
	if _, err := encErr.Synthesize(context.Background(), "hi"); err == nil {
		t.Fatal("编码失败未透传")
	}
	emptyPCM, _ := NewEngine(&fakeSynth{pcm: nil}, &fakeEncoder{silk: []byte("S")}, "v", nil)
	if _, err := emptyPCM.Synthesize(context.Background(), "hi"); err == nil {
		t.Fatal("空 PCM 未报错")
	}
	emptySilk, _ := NewEngine(&fakeSynth{pcm: []byte("P")}, &fakeEncoder{silk: nil}, "v", nil)
	if _, err := emptySilk.Synthesize(context.Background(), "hi"); err == nil {
		t.Fatal("空 silk 未报错")
	}
}

func TestExternalEncoderValidation(t *testing.T) {
	if _, err := NewExternalEncoder("", "{in} {out}"); err == nil {
		t.Fatal("空路径未报错")
	}
	if _, err := NewExternalEncoder("encoder", "{in}"); err == nil {
		t.Fatal("缺 {out} 未报错")
	}
	// 编码器不存在：执行失败并带 stderr 信息。
	enc, err := NewExternalEncoder("aichat-definitely-missing-encoder", "{in} {out}")
	if err != nil {
		t.Fatalf("NewExternalEncoder() error = %v", err)
	}
	if _, err := enc.Encode(context.Background(), []byte("pcm")); err == nil {
		t.Fatal("缺失编码器未报错")
	}
}

func TestDirectEngine(t *testing.T) {
	engine, err := NewDirectEngine(&fakeSynth{pcm: []byte("MP3BYTES")}, "voc_x", "audio/mpeg", nil)
	if err != nil {
		t.Fatalf("NewDirectEngine() error = %v", err)
	}
	if engine.MIME() != "audio/mpeg" {
		t.Fatalf("MIME() = %q", engine.MIME())
	}
	// 直传模式：没有编码器，产物原样返回。
	audio, err := engine.Synthesize(context.Background(), "你好")
	if err != nil || string(audio) != "MP3BYTES" {
		t.Fatalf("Synthesize() = %q/%v", audio, err)
	}
}

func TestDirectEngineValidation(t *testing.T) {
	if _, err := NewDirectEngine(nil, "v", "audio/mpeg", nil); err == nil {
		t.Fatal("nil synth 未报错")
	}
	if _, err := NewDirectEngine(&fakeSynth{}, "v", "text/plain", nil); err == nil {
		t.Fatal("非 audio/ mime 未报错")
	}
	if _, err := NewDirectEngine(&fakeSynth{}, "", "audio/mpeg", nil); err == nil {
		t.Fatal("空 voice 未报错")
	}
}
