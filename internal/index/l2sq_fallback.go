//go:build !goexperiment.simd

package index

// l2sqF32 is the squared L2 distance over 16 lanes (14 real + 2 zero pad).
func l2sqF32(a, b *[16]float32) float32 {
	var sum float32
	for i := 0; i < 14; i++ {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}
