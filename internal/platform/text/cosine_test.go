package text

import (
	"math"
	"testing"
)

func TestCosine(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"同向", []float32{1, 2, 3}, []float32{1, 2, 3}, 1},
		{"反向", []float32{1, 2}, []float32{-1, -2}, -1},
		{"正交", []float32{1, 0}, []float32{0, 1}, 0},
		{"不同长度", []float32{1, 2}, []float32{1}, 0},
		{"空向量", nil, []float32{1}, 0},
		{"零向量", []float32{0, 0}, []float32{1, 2}, 0},
	}
	for _, tt := range tests {
		if got := Cosine(tt.a, tt.b); math.Abs(got-tt.want) > 1e-9 {
			t.Fatalf("Cosine(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCosineScales(t *testing.T) {
	// 等比缩放不改变方向 → 余弦仍为 1。
	got := Cosine([]float32{1, 1, 1}, []float32{10, 10, 10})
	if math.Abs(got-1) > 1e-9 {
		t.Fatalf("Cosine(scaled) = %v, want 1", got)
	}
}
