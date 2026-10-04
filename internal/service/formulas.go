package service

import (
	"encoding/json"
	"math/big"
	"strings"
)

// ApplyFormulas validates the formula graph and writes calculated number values.
// submitted contains only client-supplied keys and is used to protect calculated fields.
func ApplyFormulas(fields []DynamicField, values, submitted map[string]any) FieldErrors {
	errorsByField := FieldErrors{}
	definitions := map[string]DynamicField{}
	formulas := map[string]map[string]any{}
	for _, field := range fields {
		definitions[field.Key] = field
		if field.Type == "number" && field.Enabled {
			if formula, ok := field.Config["formula"].(map[string]any); ok {
				formulas[field.Key] = formula
				if _, supplied := submitted[field.Key]; supplied {
					errorsByField[field.Key] = "calculated field is read-only"
				}
			}
		}
	}
	state := map[string]int{}
	var evaluate func(string, int) (*big.Rat, bool)
	evaluate = func(key string, depth int) (*big.Rat, bool) {
		if depth > 20 {
			errorsByField[key] = "formula dependency limit exceeded"
			return nil, false
		}
		if state[key] == 1 {
			errorsByField[key] = "formula cycle"
			return nil, false
		}
		if state[key] == 2 {
			return ratValue(values[key])
		}
		formula, calculated := formulas[key]
		if !calculated {
			field, known := definitions[key]
			if !known || field.Type != "number" {
				errorsByField[key] = "formula operand must be a number field"
				return nil, false
			}
			value, present := values[key]
			if !present || value == nil {
				if field.Required {
					errorsByField[key] = "required formula operand is missing"
					return nil, false
				}
				return new(big.Rat), true
			}
			result, ok := ratValue(value)
			if !ok {
				errorsByField[key] = "invalid numeric formula operand"
			}
			return result, ok
		}
		state[key] = 1
		a, aOK := formula["a"].(string)
		b, bOK := formula["b"].(string)
		op, opOK := formula["op"].(string)
		if !aOK || !bOK || !opOK || a == key || b == key {
			errorsByField[key] = "invalid formula"
			return nil, false
		}
		left, leftOK := evaluate(a, depth+1)
		right, rightOK := evaluate(b, depth+1)
		if !leftOK || !rightOK {
			errorsByField[key] = "invalid formula dependency"
			return nil, false
		}
		result := new(big.Rat)
		switch op {
		case "+":
			result.Add(left, right)
		case "-":
			result.Sub(left, right)
		case "*":
			result.Mul(left, right)
		case "/":
			if right.Sign() == 0 {
				errorsByField[key] = "division by zero"
				return nil, false
			}
			result.Quo(left, right)
		default:
			errorsByField[key] = "invalid formula operator"
			return nil, false
		}
		limit, _ := new(big.Rat).SetString("9999999999.99")
		absolute := new(big.Rat).Abs(new(big.Rat).Set(result))
		if absolute.Cmp(limit) > 0 {
			errorsByField[key] = "calculated value is out of range"
			return nil, false
		}
		state[key] = 2
		values[key] = json.Number(roundRat(result))
		return ratValue(values[key])
	}
	for key := range formulas {
		_, _ = evaluate(key, 1)
	}
	return errorsByField
}

func ratValue(value any) (*big.Rat, bool) {
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case float64:
		text = strings.TrimRight(strings.TrimRight(new(big.Float).SetFloat64(typed).Text('f', 12), "0"), ".")
	case int:
		return new(big.Rat).SetInt64(int64(typed)), true
	default:
		return nil, false
	}
	result, ok := new(big.Rat).SetString(text)
	return result, ok
}

func roundRat(value *big.Rat) string {
	scale := big.NewInt(1_000_000)
	scaledNumerator := new(big.Int).Mul(value.Num(), scale)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(scaledNumerator, value.Denom(), remainder)
	twiceRemainder := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
	if twiceRemainder.Cmp(value.Denom()) >= 0 {
		if value.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	negative := quotient.Sign() < 0
	abs := new(big.Int).Abs(quotient)
	integer, fraction := new(big.Int), new(big.Int)
	integer.QuoRem(abs, scale, fraction)
	text := integer.String()
	if fraction.Sign() != 0 {
		fractionText := fraction.String()
		fractionText = strings.Repeat("0", 6-len(fractionText)) + fractionText
		text += "." + strings.TrimRight(fractionText, "0")
	}
	if negative && text != "0" {
		text = "-" + text
	}
	return text
}
