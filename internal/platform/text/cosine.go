package text

import "math"

// Cosine 计算两个等长 float32 向量的余弦相似度，返回 [-1, 1]。
//
// 记忆检索精排与语义缓存共用这一份实现（docs/02 §1.2：精排只有一个实现）。
// 任一向量为空、长度不一致或模为 0 时返回 0：维度漂移（换 embed 模型）时
// 精排自动回退到词面相似度，而不是 panic 或除零。
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
