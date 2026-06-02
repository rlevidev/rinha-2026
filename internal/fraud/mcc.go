package fraud

var mccKeys = [9]int{5411, 5812, 5912, 5944, 7801, 7802, 7995, 4511, 5311}
var mccVals = [9]int16{1500, 3000, 2000, 4500, 8000, 7500, 8500, 3500, 2500}

func mccRisk(mcc *[4]byte) int16 {
	v := int(mcc[0]-'0')*1000 + int(mcc[1]-'0')*100 + int(mcc[2]-'0')*10 + int(mcc[3]-'0')
	for i, k := range mccKeys {
		if k == v {
			return mccVals[i]
		}
	}
	return 5000
}
