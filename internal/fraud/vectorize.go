package fraud

const scale = 10000

func clamp01I16(x float64) int16 {
	if x < 0 {
		x = 0
	} else if x > 1 {
		x = 1
	}
	return int16(x*scale + 0.5)
}

func Vectorize(r *Request) [16]int16 {
	var v [16]int16
	v[0] = clamp01I16(r.Amount / 10000.0)
	v[1] = clamp01I16(float64(r.Installments) / 12.0)
	if r.CustomerAvg > 0 {
		v[2] = clamp01I16((r.Amount / r.CustomerAvg) / 10.0)
	}
	ts := r.TS
	daysSince := ts / 86400
	wd := (daysSince + 3) % 7
	if wd < 0 {
		wd += 7
	}
	hour := (ts / 3600) % 24
	if hour < 0 {
		hour += 24
	}
	v[3] = clamp01I16(float64(hour) / 23.0)
	v[4] = clamp01I16(float64(wd) / 6.0)
	if r.HasLastTx {
		minutes := float64(ts-r.LastTS) / 60.0
		v[5] = clamp01I16(minutes / 1440.0)
		v[6] = clamp01I16(r.KmLast / 1000.0)
	} else {
		v[5] = -scale
		v[6] = -scale
	}
	v[7] = clamp01I16(r.KmHome / 1000.0)
	v[8] = clamp01I16(float64(r.TxCount24h) / 20.0)
	if r.IsOnline {
		v[9] = scale
	}
	if r.CardPresent {
		v[10] = scale
	}
	if !r.KnownMerchant {
		v[11] = scale
	}
	v[12] = mccRisk(&r.MCC)
	v[13] = clamp01I16(r.MerchantAvg / 10000.0)
	return v
}

func Tag(v *[16]int16) int {
	tag := 0
	if v[11] > 0 {
		tag |= 2
	}
	if v[5] >= 0 {
		tag |= 1
	}
	return tag
}
