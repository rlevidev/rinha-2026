package fraud

import (
	"strconv"
	"unsafe"
)

type Request struct {
	Amount      float64
	CustomerAvg float64
	MerchantAvg float64
	KmHome      float64
	KmLast      float64
	TS          int64
	LastTS      int64

	Installments int32
	TxCount24h   int32

	MCC [4]byte

	IsOnline      bool
	CardPresent   bool
	HasLastTx     bool
	KnownMerchant bool
}

type psr struct {
	b   []byte
	p   int
	end int

	kmStart, kmEnd   int
	midStart, midLen int
}

func (s *psr) ws() {
	for s.p < s.end {
		c := s.b[s.p]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return
		}
		s.p++
	}
}

func (s *psr) skipString() (cs, ce int, ok bool) {
	if s.p >= s.end || s.b[s.p] != '"' {
		return 0, 0, false
	}
	s.p++
	cs = s.p
	for s.p < s.end {
		c := s.b[s.p]
		if c == '\\' {
			s.p += 2
			continue
		}
		if c == '"' {
			ce = s.p
			s.p++
			return cs, ce, true
		}
		s.p++
	}
	return 0, 0, false
}

func (s *psr) skipValue() bool {
	s.ws()
	if s.p >= s.end {
		return false
	}
	switch s.b[s.p] {
	case '"':
		_, _, ok := s.skipString()
		return ok
	case '{', '[':
		open := s.b[s.p]
		close := byte('}')
		if open == '[' {
			close = ']'
		}
		depth := 0
		for s.p < s.end {
			c := s.b[s.p]
			switch c {
			case '"':
				if _, _, ok := s.skipString(); !ok {
					return false
				}
				continue
			case open:
				depth++
			case close:
				depth--
				if depth == 0 {
					s.p++
					return true
				}
			}
			s.p++
		}
		return false
	default:
		for s.p < s.end {
			c := s.b[s.p]
			if c == ',' || c == '}' || c == ']' {
				break
			}
			s.p++
		}
		return true
	}
}

var pow10 = [...]float64{
	1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18,
}

func (s *psr) number() (float64, bool) {
	start := s.p
	neg := false
	if s.p < s.end && (s.b[s.p] == '-' || s.b[s.p] == '+') {
		neg = s.b[s.p] == '-'
		s.p++
	}
	var mant uint64
	digits, fracDigits := 0, 0
	for s.p < s.end && s.b[s.p] >= '0' && s.b[s.p] <= '9' {
		mant = mant*10 + uint64(s.b[s.p]-'0')
		s.p++
		digits++
	}
	if s.p < s.end && s.b[s.p] == '.' {
		s.p++
		for s.p < s.end && s.b[s.p] >= '0' && s.b[s.p] <= '9' {
			mant = mant*10 + uint64(s.b[s.p]-'0')
			s.p++
			digits++
			fracDigits++
		}
	}
	if (s.p < s.end && (s.b[s.p] == 'e' || s.b[s.p] == 'E')) || digits > 18 {
		for s.p < s.end {
			c := s.b[s.p]
			if (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.' || c == 'e' || c == 'E' {
				s.p++
				continue
			}
			break
		}
		f, err := strconv.ParseFloat(unsafe.String(&s.b[start], s.p-start), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	if digits == 0 {
		return 0, false
	}
	val := float64(mant)
	if fracDigits > 0 {
		val /= pow10[fracDigits]
	}
	if neg {
		val = -val
	}
	return val, true
}

func (s *psr) int32v() (int32, bool) {
	f, ok := s.number()
	return int32(f), ok
}

func (s *psr) afterValue() {
	s.ws()
	if s.p < s.end && s.b[s.p] == ',' {
		s.p++
	}
}

func ParseRequest(body []byte, r *Request) bool {
	*r = Request{}
	s := psr{b: body, end: len(body)}
	s.ws()
	if s.p >= s.end || s.b[s.p] != '{' {
		return false
	}
	s.p++

	for {
		s.ws()
		if s.p >= s.end || s.b[s.p] == '}' {
			break
		}
		if s.b[s.p] == ',' {
			s.p++
			continue
		}
		cs, ce, ok := s.skipString()
		if !ok {
			return false
		}
		key := body[cs:ce]
		s.ws()
		if s.p >= s.end || s.b[s.p] != ':' {
			return false
		}
		s.p++
		s.ws()

		switch string(key) {
		case "transaction":
			if !s.parseTransaction(r) {
				return false
			}
		case "customer":
			if !s.parseCustomer(r) {
				return false
			}
		case "merchant":
			if !s.parseMerchant(r) {
				return false
			}
		case "terminal":
			if !s.parseTerminal(r) {
				return false
			}
		case "last_transaction":
			if !s.parseLastTx(r) {
				return false
			}
		default:
			if !s.skipValue() {
				return false
			}
		}
		s.afterValue()
	}
	s.resolveKnownMerchant(r)
	return true
}

func (s *psr) resolveKnownMerchant(r *Request) {
	if s.kmEnd <= s.kmStart || s.midLen <= 0 || s.midLen >= 256 {
		return
	}
	arr := s.b[s.kmStart:s.kmEnd]
	id := s.b[s.midStart : s.midStart+s.midLen]
	last := len(arr) - (s.midLen + 2)
	for i := 0; i <= last; i++ {
		if arr[i] != '"' || arr[i+s.midLen+1] != '"' {
			continue
		}
		match := true
		for k := 0; k < s.midLen; k++ {
			if arr[i+1+k] != id[k] {
				match = false
				break
			}
		}
		if match {
			r.KnownMerchant = true
			return
		}
	}
}

func (s *psr) nextKey() (key []byte, more bool, ok bool) {
	s.ws()
	if s.p >= s.end {
		return nil, false, false
	}
	if s.b[s.p] == '}' {
		s.p++
		return nil, false, true
	}
	if s.b[s.p] == ',' {
		s.p++
	}
	s.ws()
	cs, ce, ok2 := s.skipString()
	if !ok2 {
		return nil, false, false
	}
	s.ws()
	if s.p >= s.end || s.b[s.p] != ':' {
		return nil, false, false
	}
	s.p++
	s.ws()
	return s.b[cs:ce], true, true
}
