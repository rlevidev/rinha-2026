package fraud

import (
	"encoding/json"
	"os"
	"strconv"
)

const scale = 10000

// Normalizer stores the corpus constants and MCC risk table used by
// vectorization.
type Normalizer struct {
	MaxAmount        float64
	MaxInstallments  float64
	AmountVsAvgRatio float64
	MaxMinutes       float64
	MaxKm            float64
	MaxTxCount24h    float64
	MaxMerchantAvg   float64
	MccRisk          map[[4]byte]int16
}

// LoadNormalizer reads normalization and MCC data from JSON files.
func LoadNormalizer(normPath, mccPath string) (*Normalizer, error) {
	var raw struct {
		MaxAmount        float64 `json:"max_amount"`
		MaxInstallments  float64 `json:"max_installments"`
		AmountVsAvgRatio float64 `json:"amount_vs_avg_ratio"`
		MaxMinutes       float64 `json:"max_minutes"`
		MaxKm            float64 `json:"max_km"`
		MaxTxCount24h    float64 `json:"max_tx_count_24h"`
		MaxMerchantAvg   float64 `json:"max_merchant_avg_amount"`
	}

	normFile, err := os.ReadFile(normPath)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(normFile, &raw); err != nil {
		return nil, err
	}

	mccFile, err := os.ReadFile(mccPath)
	if err != nil {
		return nil, err
	}
	var mccRaw map[string]float64
	if err := json.Unmarshal(mccFile, &mccRaw); err != nil {
		return nil, err
	}
	mccRisk := make(map[[4]byte]int16, len(mccRaw))
	for k, v := range mccRaw {
		var code [4]byte
		for i := 0; i < 4 && i < len(k); i++ {
			code[i] = k[i]
		}
		mccRisk[code] = clamp01I16(v)
	}

	return &Normalizer{
		MaxAmount:        raw.MaxAmount,
		MaxInstallments:  raw.MaxInstallments,
		AmountVsAvgRatio: raw.AmountVsAvgRatio,
		MaxMinutes:       raw.MaxMinutes,
		MaxKm:            raw.MaxKm,
		MaxTxCount24h:    raw.MaxTxCount24h,
		MaxMerchantAvg:   raw.MaxMerchantAvg,
		MccRisk:          mccRisk,
	}, nil
}

// Request contains only the fields needed by scoring.
type Request struct {
	Amount        float64
	Installments  int
	RequestedAt   int64
	CustomerAvg   float64
	TxCount24h    int
	KmFromHome    float64
	IsOnline      bool
	CardPresent   bool
	KnownMerchant bool
	HasLastTx     bool
	LastAt        int64
	KmFromLast    float64
	MerchantAvg   float64
	MCC           [4]byte
}

type parser struct {
	b   []byte
	pos int
	end int

	merchantIDStart int
	merchantIDEnd   int
	knownStart      int
	knownEnd        int
}

func (p *parser) ws() {
	for p.pos < p.end {
		switch p.b[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) skipString() (start, end int, ok bool) {
	if p.pos >= p.end || p.b[p.pos] != '"' {
		return 0, 0, false
	}
	p.pos++
	start = p.pos
	for p.pos < p.end {
		switch p.b[p.pos] {
		case '\\':
			p.pos += 2
			continue
		case '"':
			end = p.pos
			p.pos++
			return start, end, true
		default:
			p.pos++
		}
	}
	return 0, 0, false
}

func (p *parser) skipValue() bool {
	p.ws()
	if p.pos >= p.end {
		return false
	}
	switch p.b[p.pos] {
	case '"':
		_, _, ok := p.skipString()
		return ok
	case '{', '[':
		open := p.b[p.pos]
		close := byte('}')
		if open == '[' {
			close = ']'
		}
		depth := 0
		for p.pos < p.end {
			switch p.b[p.pos] {
			case '"':
				if _, _, ok := p.skipString(); !ok {
					return false
				}
				continue
			case open:
				depth++
			case close:
				depth--
				if depth == 0 {
					p.pos++
					return true
				}
			}
			p.pos++
		}
		return false
	default:
		for p.pos < p.end {
			c := p.b[p.pos]
			if c == ',' || c == '}' || c == ']' {
				break
			}
			p.pos++
		}
		return true
	}
}

func (p *parser) nextKey() ([]byte, bool, bool) {
	p.ws()
	if p.pos >= p.end {
		return nil, false, false
	}
	if p.b[p.pos] == '}' {
		p.pos++
		return nil, false, true
	}
	if p.b[p.pos] == ',' {
		p.pos++
		p.ws()
	}
	start, end, ok := p.skipString()
	if !ok {
		return nil, false, false
	}
	p.ws()
	if p.pos >= p.end || p.b[p.pos] != ':' {
		return nil, false, false
	}
	p.pos++
	p.ws()
	return p.b[start:end], true, true
}

func parseNumber(buf []byte, pos int) (float64, int, bool) {
	start := pos
	n := len(buf)
	neg := false
	if pos < n && (buf[pos] == '-' || buf[pos] == '+') {
		neg = buf[pos] == '-'
		pos++
	}
	var mant uint64
	digits := 0
	fracDigits := 0
	for pos < n && buf[pos] >= '0' && buf[pos] <= '9' {
		mant = mant*10 + uint64(buf[pos]-'0')
		pos++
		digits++
	}
	if pos < n && buf[pos] == '.' {
		pos++
		for pos < n && buf[pos] >= '0' && buf[pos] <= '9' {
			mant = mant*10 + uint64(buf[pos]-'0')
			pos++
			digits++
			fracDigits++
		}
	}
	if pos < n && (buf[pos] == 'e' || buf[pos] == 'E') {
		for pos < n {
			c := buf[pos]
			if (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.' || c == 'e' || c == 'E' {
				pos++
				continue
			}
			break
		}
		f, err := strconv.ParseFloat(string(buf[start:pos]), 64)
		if err != nil {
			return 0, pos, false
		}
		return f, pos, true
	}
	if digits == 0 {
		return 0, start, false
	}
	val := float64(mant)
	if fracDigits > 0 {
		pow := 1.0
		for i := 0; i < fracDigits; i++ {
			pow *= 10
		}
		val /= pow
	}
	if neg {
		val = -val
	}
	return val, pos, true
}

func (p *parser) number() (float64, bool) {
	v, np, ok := parseNumber(p.b, p.pos)
	if !ok {
		return 0, false
	}
	p.pos = np
	return v, true
}

func (p *parser) intValue() (int, bool) {
	v, ok := p.number()
	return int(v), ok
}

func parseISO8601(s []byte) int64 {
	if len(s) < 19 {
		return 0
	}
	if s[4] != '-' || s[7] != '-' || (s[10] != 'T' && s[10] != ' ') || s[13] != ':' || s[16] != ':' {
		return 0
	}
	d2 := func(i int) int { return int(s[i]-'0')*10 + int(s[i+1]-'0') }
	year := int(s[0]-'0')*1000 + int(s[1]-'0')*100 + int(s[2]-'0')*10 + int(s[3]-'0')
	month := d2(5)
	day := d2(8)
	hour := d2(11)
	minute := d2(14)
	second := d2(17)

	y := year
	if month <= 2 {
		y--
	}
	var era int
	if y >= 0 {
		era = y / 400
	} else {
		era = (y - 399) / 400
	}
	yoe := y - era*400
	var m int
	if month > 2 {
		m = month - 3
	} else {
		m = month + 9
	}
	doy := (153*m+2)/5 + day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	days := int64(era)*146097 + int64(doe) - 719468
	epoch := days*86400 + int64(hour)*3600 + int64(minute)*60 + int64(second)

	i := 19
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i < len(s) {
		switch s[i] {
		case 'Z':
			return epoch
		case '+', '-':
			if len(s)-i >= 6 && s[i+3] == ':' {
				offset := int64(d2(i+1))*3600 + int64(d2(i+4))*60
				if s[i] == '+' {
					epoch -= offset
				} else {
					epoch += offset
				}
			}
		}
	}
	return epoch
}

func (p *parser) parseTransaction(r *Request) bool {
	if !p.expectObject() {
		return false
	}
	for {
		key, more, ok := p.nextKey()
		if !ok {
			return false
		}
		if !more {
			return true
		}
		switch {
		case keyEq(key, "amount"):
			v, ok := p.number()
			if !ok {
				return false
			}
			r.Amount = v
		case keyEq(key, "installments"):
			v, ok := p.intValue()
			if !ok {
				return false
			}
			r.Installments = v
		case keyEq(key, "requested_at"):
			start, end, ok := p.skipString()
			if !ok {
				return false
			}
			r.RequestedAt = parseISO8601(p.b[start:end])
		default:
			if !p.skipValue() {
				return false
			}
		}
		p.ws()
		if p.pos < p.end && p.b[p.pos] == ',' {
			p.pos++
		}
	}
}

func (p *parser) parseCustomer(r *Request) bool {
	if !p.expectObject() {
		return false
	}
	for {
		key, more, ok := p.nextKey()
		if !ok {
			return false
		}
		if !more {
			return true
		}
		switch {
		case keyEq(key, "avg_amount"):
			v, ok := p.number()
			if !ok {
				return false
			}
			r.CustomerAvg = v
		case keyEq(key, "tx_count_24h"):
			v, ok := p.intValue()
			if !ok {
				return false
			}
			r.TxCount24h = v
		case keyEq(key, "known_merchants"):
			p.ws()
			if p.pos >= p.end || p.b[p.pos] != '[' {
				return false
			}
			p.knownStart = p.pos
			if !p.skipValue() {
				return false
			}
			p.knownEnd = p.pos
			if p.merchantIDStart >= 0 {
				r.KnownMerchant = arrayContains(p.b[p.knownStart:p.knownEnd], p.b[p.merchantIDStart:p.merchantIDEnd])
			}
		default:
			if !p.skipValue() {
				return false
			}
		}
		p.ws()
		if p.pos < p.end && p.b[p.pos] == ',' {
			p.pos++
		}
	}
}

func (p *parser) parseMerchant(r *Request) bool {
	if !p.expectObject() {
		return false
	}
	for {
		key, more, ok := p.nextKey()
		if !ok {
			return false
		}
		if !more {
			return true
		}
		switch {
		case keyEq(key, "id"):
			start, end, ok := p.skipString()
			if !ok {
				return false
			}
			p.merchantIDStart, p.merchantIDEnd = start, end
		case keyEq(key, "mcc"):
			start, end, ok := p.skipString()
			if !ok {
				return false
			}
			for i := 0; i < 4; i++ {
				if start+i < end {
					r.MCC[i] = p.b[start+i]
				} else {
					r.MCC[i] = '0'
				}
			}
		case keyEq(key, "avg_amount"):
			v, ok := p.number()
			if !ok {
				return false
			}
			r.MerchantAvg = v
		default:
			if !p.skipValue() {
				return false
			}
		}
		p.ws()
		if p.pos < p.end && p.b[p.pos] == ',' {
			p.pos++
		}
	}
}

func (p *parser) parseTerminal(r *Request) bool {
	if !p.expectObject() {
		return false
	}
	for {
		key, more, ok := p.nextKey()
		if !ok {
			return false
		}
		if !more {
			return true
		}
		switch {
		case keyEq(key, "is_online"):
			p.ws()
			r.IsOnline = p.pos < p.end && p.b[p.pos] == 't'
			if !p.skipValue() {
				return false
			}
		case keyEq(key, "card_present"):
			p.ws()
			r.CardPresent = p.pos < p.end && p.b[p.pos] == 't'
			if !p.skipValue() {
				return false
			}
		case keyEq(key, "km_from_home"):
			v, ok := p.number()
			if !ok {
				return false
			}
			r.KmFromHome = v
		default:
			if !p.skipValue() {
				return false
			}
		}
		p.ws()
		if p.pos < p.end && p.b[p.pos] == ',' {
			p.pos++
		}
	}
}

func (p *parser) parseLastTx(r *Request) bool {
	p.ws()
	if p.pos+4 <= p.end && p.b[p.pos] == 'n' && p.b[p.pos+1] == 'u' && p.b[p.pos+2] == 'l' && p.b[p.pos+3] == 'l' {
		r.HasLastTx = false
		p.pos += 4
		return true
	}
	if !p.expectObject() {
		return false
	}
	r.HasLastTx = true
	for {
		key, more, ok := p.nextKey()
		if !ok {
			return false
		}
		if !more {
			return true
		}
		switch {
		case keyEq(key, "timestamp"):
			start, end, ok := p.skipString()
			if !ok {
				return false
			}
			r.LastAt = parseISO8601(p.b[start:end])
		case keyEq(key, "km_from_current"):
			v, ok := p.number()
			if !ok {
				return false
			}
			r.KmFromLast = v
		default:
			if !p.skipValue() {
				return false
			}
		}
		p.ws()
		if p.pos < p.end && p.b[p.pos] == ',' {
			p.pos++
		}
	}
}

func (p *parser) expectObject() bool {
	p.ws()
	if p.pos >= p.end || p.b[p.pos] != '{' {
		return false
	}
	p.pos++
	return true
}

func keyEq(key []byte, s string) bool {
	if len(key) != len(s) {
		return false
	}
	for i := range key {
		if key[i] != s[i] {
			return false
		}
	}
	return true
}

func arrayContains(hay, needle []byte) bool {
	if len(hay) == 0 || len(needle) == 0 {
		return false
	}
	i := 0
	for i < len(hay) {
		for i < len(hay) && (hay[i] == ' ' || hay[i] == '\t' || hay[i] == '\n' || hay[i] == '\r' || hay[i] == ',' || hay[i] == '[' || hay[i] == ']') {
			i++
		}
		if i >= len(hay) {
			break
		}
		if hay[i] != '"' {
			if !skipRawToken(hay, &i) {
				return false
			}
			continue
		}
		i++
		start := i
		for i < len(hay) {
			if hay[i] == '\\' {
				i += 2
				continue
			}
			if hay[i] == '"' {
				if equalBytes(hay[start:i], needle) {
					return true
				}
				i++
				break
			}
			i++
		}
	}
	return false
}

func skipRawToken(buf []byte, pos *int) bool {
	for *pos < len(buf) {
		c := buf[*pos]
		if c == ',' || c == ']' || c == '}' || c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			return true
		}
		(*pos)++
	}
	return true
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (p *parser) resolveKnownMerchant(r *Request) {
	if r.KnownMerchant {
		return
	}
	if p.merchantIDStart < 0 || p.knownStart < 0 {
		return
	}
	r.KnownMerchant = arrayContains(p.b[p.knownStart:p.knownEnd], p.b[p.merchantIDStart:p.merchantIDEnd])
}

// ParseRequest extracts the fields required for scoring.
func ParseRequest(body []byte, r *Request) bool {
	*r = Request{}
	p := parser{
		b:               body,
		end:             len(body),
		merchantIDStart: -1,
		merchantIDEnd:   -1,
		knownStart:      -1,
		knownEnd:        -1,
	}

	if !p.expectObject() {
		return false
	}
	for {
		key, more, ok := p.nextKey()
		if !ok {
			return false
		}
		if !more {
			p.resolveKnownMerchant(r)
			return true
		}
		switch {
		case keyEq(key, "transaction"):
			if !p.parseTransaction(r) {
				return false
			}
		case keyEq(key, "customer"):
			if !p.parseCustomer(r) {
				return false
			}
		case keyEq(key, "merchant"):
			if !p.parseMerchant(r) {
				return false
			}
		case keyEq(key, "terminal"):
			if !p.parseTerminal(r) {
				return false
			}
		case keyEq(key, "last_transaction"):
			if !p.parseLastTx(r) {
				return false
			}
		case keyEq(key, "id"):
			if !p.skipValue() {
				return false
			}
		default:
			if !p.skipValue() {
				return false
			}
		}
		p.ws()
		if p.pos < p.end && p.b[p.pos] == ',' {
			p.pos++
		}
	}
}

func clamp01I16(x float64) int16 {
	if x < 0 {
		x = 0
	} else if x > 1 {
		x = 1
	}
	return int16(x*scale + 0.5)
}

// Vectorize transforms the parsed request into the 16-lane query used by the
// partitioned index. Lanes 14 and 15 stay zero.
func Vectorize(r *Request, n *Normalizer) [16]int16 {
	var v [16]int16

	v[0] = clamp01I16(r.Amount / n.MaxAmount)
	v[1] = clamp01I16(float64(r.Installments) / n.MaxInstallments)
	if r.CustomerAvg > 0 {
		v[2] = clamp01I16((r.Amount / r.CustomerAvg) / n.AmountVsAvgRatio)
	}

	sec := r.RequestedAt % 86400
	if sec < 0 {
		sec += 86400
	}
	hour := sec / 3600
	wd := ((r.RequestedAt/86400+3)%7 + 7) % 7
	v[3] = clamp01I16(float64(hour) / 23.0)
	v[4] = clamp01I16(float64(wd) / 6.0)

	if r.HasLastTx {
		minutes := float64(r.RequestedAt-r.LastAt) / 60.0
		v[5] = clamp01I16(minutes / n.MaxMinutes)
		v[6] = clamp01I16(r.KmFromLast / n.MaxKm)
	} else {
		v[5] = -scale
		v[6] = -scale
	}

	v[7] = clamp01I16(r.KmFromHome / n.MaxKm)
	v[8] = clamp01I16(float64(r.TxCount24h) / n.MaxTxCount24h)
	if r.IsOnline {
		v[9] = scale
	}
	if r.CardPresent {
		v[10] = scale
	}
	if !r.KnownMerchant {
		v[11] = scale
	}
	if risk, ok := n.MccRisk[r.MCC]; ok {
		v[12] = risk
	} else {
		v[12] = clamp01I16(0.5)
	}
	v[13] = clamp01I16(r.MerchantAvg / n.MaxMerchantAvg)
	return v
}

// PartitionTag selects the index partition for a parsed request.
func PartitionTag(r *Request) int {
	tag := 0
	if r.HasLastTx {
		tag |= 1
	}
	if !r.KnownMerchant {
		tag |= 2
	}
	if r.IsOnline {
		tag |= 4
	}
	if r.CardPresent {
		tag |= 8
	}
	return tag
}
