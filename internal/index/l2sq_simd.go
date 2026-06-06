//go:build goexperiment.simd

package index

import "simd/archsimd"

// l2sqF32 is the squared L2 distance over 16 lanes (14 real + 2 zero pad).
// Compiles to VSUBPS + VMULPS + VADDPS on 256-bit registers.
func l2sqF32(a, b *[16]float32) float32 {
	a0 := archsimd.LoadFloat32x8Slice(a[0:8])
	a1 := archsimd.LoadFloat32x8Slice(a[8:16])
	b0 := archsimd.LoadFloat32x8Slice(b[0:8])
	b1 := archsimd.LoadFloat32x8Slice(b[8:16])
	d0 := a0.Sub(b0)
	d1 := a1.Sub(b1)
	sq := d1.Mul(d1).Add(d0.Mul(d0))
	var out [8]float32
	sq.StoreSlice(out[:])
	return out[0] + out[1] + out[2] + out[3] + out[4] + out[5] + out[6] + out[7]
}
