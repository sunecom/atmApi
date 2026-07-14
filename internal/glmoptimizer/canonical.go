package glmoptimizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CanonicalizeJSON returns a deterministic JSON representation. Object keys
// are sorted by encoding/json, arrays retain their order, and equivalent JSON
// decimal spellings are normalized to the same number.
func CanonicalizeJSON(body []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode canonical JSON: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode canonical JSON: multiple values")
		}
		return nil, fmt.Errorf("decode canonical JSON trailer: %w", err)
	}
	normalized, err := normalizeJSONNumbers(value)
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return result, nil
}

func normalizeJSONNumbers(value interface{}) (interface{}, error) {
	switch typed := value.(type) {
	case json.Number:
		number, err := canonicalDecimal(typed.String())
		if err != nil {
			return nil, err
		}
		return json.Number(number), nil
	case map[string]interface{}:
		for key, item := range typed {
			normalized, err := normalizeJSONNumbers(item)
			if err != nil {
				return nil, err
			}
			typed[key] = normalized
		}
	case []interface{}:
		for index, item := range typed {
			normalized, err := normalizeJSONNumbers(item)
			if err != nil {
				return nil, err
			}
			typed[index] = normalized
		}
	}
	return value, nil
}

func canonicalDecimal(input string) (string, error) {
	negative := strings.HasPrefix(input, "-")
	unsigned := strings.TrimPrefix(input, "-")
	mantissa := unsigned
	exponent := 0
	if index := strings.IndexAny(unsigned, "eE"); index >= 0 {
		mantissa = unsigned[:index]
		parsed, err := strconv.Atoi(unsigned[index+1:])
		if err != nil {
			return "", fmt.Errorf("normalize JSON number %q: %w", input, err)
		}
		exponent = parsed
	}
	integer := mantissa
	fraction := ""
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		integer, fraction = mantissa[:index], mantissa[index+1:]
	}
	digits := integer + fraction
	first := strings.IndexFunc(digits, func(r rune) bool { return r != '0' })
	if first < 0 {
		return "0", nil
	}
	decimalPosition := len(integer) + exponent
	significant := strings.TrimRight(digits[first:], "0")
	scientificExponent := decimalPosition - first - 1
	if scientificExponent >= -6 && scientificExponent <= 20 {
		var plain string
		plainDecimalPosition := decimalPosition - first
		switch {
		case plainDecimalPosition <= 0:
			plain = "0." + strings.Repeat("0", -plainDecimalPosition) + significant
		case plainDecimalPosition >= len(significant):
			plain = significant + strings.Repeat("0", plainDecimalPosition-len(significant))
		default:
			plain = significant[:plainDecimalPosition] + "." + significant[plainDecimalPosition:]
		}
		if negative {
			plain = "-" + plain
		}
		return plain, nil
	}
	result := significant[:1]
	if len(significant) > 1 {
		result += "." + significant[1:]
	}
	if scientificExponent != 0 {
		result += "e" + strconv.Itoa(scientificExponent)
	}
	if negative {
		result = "-" + result
	}
	return result, nil
}
