package fraud

import (
	"encoding/json"
	"os"
)

var (
	mccKeys []int
	mccVals []int16
)

// InitMCCRisk loads the MCC risk overrides from the given JSON file path.
func InitMCCRisk(path string) error {
	// Try the absolute path first. If it doesn't exist, fallback to relative path.
	b, err := os.ReadFile(path)
	if err != nil {
		b, err = os.ReadFile("resources/mcc_risk.json")
		if err != nil {
			b, err = os.ReadFile("../resources/mcc_risk.json")
			if err != nil {
				return err
			}
		}
	}
	var raw map[string]float64
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	
	mccKeys = make([]int, 0, len(raw))
	mccVals = make([]int16, 0, len(raw))
	
	for k, v := range raw {
		iv := int(k[0]-'0')*1000 + int(k[1]-'0')*100 + int(k[2]-'0')*10 + int(k[3]-'0')
		mccKeys = append(mccKeys, iv)
		mccVals = append(mccVals, int16(v*scale + 0.5))
	}
	return nil
}

// mccRisk maps a 4-digit MCC to its risk weight in [0, scale].
func mccRisk(mcc *[4]byte) int16 {
	v := int(mcc[0]-'0')*1000 + int(mcc[1]-'0')*100 + int(mcc[2]-'0')*10 + int(mcc[3]-'0')
	for i, k := range mccKeys {
		if k == v {
			return mccVals[i]
		}
	}
	return 5000
}
