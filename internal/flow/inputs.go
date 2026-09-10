package flow

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
)

// InputError is one invalid input at trigger time (REQ-FLOW-008).
type InputError struct {
	Field   string
	Message string
}

// CoerceInput checks v against the input type and returns the typed value.
func CoerceInput(in Input, v any) (any, error) {
	switch in.Type {
	case "string":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("must be a string")
		}
		return s, nil
	case "int":
		switch x := v.(type) {
		case int:
			return int64(x), nil
		case int64:
			return x, nil
		case uint64:
			return int64(x), nil
		case float64:
			if x != math.Trunc(x) || math.IsInf(x, 0) {
				return nil, fmt.Errorf("must be an integer")
			}
			return int64(x), nil
		case json.Number:
			n, err := strconv.ParseInt(string(x), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("must be an integer")
			}
			return n, nil
		}
		return nil, fmt.Errorf("must be an integer")
	case "number":
		switch x := v.(type) {
		case int:
			return float64(x), nil
		case int64:
			return float64(x), nil
		case float64:
			return x, nil
		case json.Number:
			f, err := x.Float64()
			if err != nil {
				return nil, fmt.Errorf("must be a number")
			}
			return f, nil
		}
		return nil, fmt.Errorf("must be a number")
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("must be true or false")
		}
		return b, nil
	case "select":
		for _, allowed := range in.Values {
			if sameValue(allowed, v) {
				return v, nil
			}
		}
		return nil, fmt.Errorf("must be one of %v", in.Values)
	case "json":
		return v, nil
	}
	return nil, fmt.Errorf("unknown input type %q", in.Type)
}

// sameValue compares YAML and JSON values (ints from YAML, float64 from JSON).
func sameValue(a, b any) bool {
	na, aNum := toFloat(a)
	nb, bNum := toFloat(b)
	if aNum && bNum {
		return na == nb
	}
	return reflect.DeepEqual(a, b)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// ResolveInputs validates given inputs, applies defaults and checks required inputs.
func ResolveInputs(defs []Input, given map[string]any) (map[string]any, []InputError) {
	out := map[string]any{}
	var errs []InputError
	known := map[string]Input{}
	for _, d := range defs {
		known[d.ID] = d
	}
	keys := make([]string, 0, len(given))
	for k := range given {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d, ok := known[k]
		if !ok {
			errs = append(errs, InputError{Field: "inputs." + k, Message: "unknown input"})
			continue
		}
		v, err := CoerceInput(d, given[k])
		if err != nil {
			errs = append(errs, InputError{Field: "inputs." + k, Message: err.Error()})
			continue
		}
		out[k] = v
	}
	for _, d := range defs {
		if _, ok := out[d.ID]; ok {
			continue
		}
		if _, bad := given[d.ID]; bad {
			continue
		}
		if d.Default != nil {
			v, err := CoerceInput(d, d.Default)
			if err == nil {
				out[d.ID] = v
				continue
			}
		}
		if d.Required {
			errs = append(errs, InputError{Field: "inputs." + d.ID, Message: "required input is missing"})
		}
	}
	return out, errs
}
