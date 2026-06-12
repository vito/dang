package dang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

func encodeTOML(val Value) (string, error) {
	jsonBytes, err := json.Marshal(val)
	if err != nil {
		return "", err
	}

	decoder := json.NewDecoder(bytes.NewReader(jsonBytes))
	decoder.UseNumber()

	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return "", err
	}

	encodable, err := tomlEncodableValue(raw, "$")
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(encodable); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func tomlEncodableValue(raw any, path string) (any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, fmt.Errorf("%s: TOML does not support null", path)
	case string, bool:
		return v, nil
	case json.Number:
		return tomlEncodableNumber(v)
	case []any:
		items := make([]any, len(v))
		for i, item := range v {
			converted, err := tomlEncodableValue(item, joinIndexPath(path, i))
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return items, nil
	case map[string]any:
		obj := make(map[string]any, len(v))
		for key, item := range v {
			converted, err := tomlEncodableValue(item, joinFieldPath(path, key))
			if err != nil {
				return nil, err
			}
			obj[key] = converted
		}
		return obj, nil
	default:
		return nil, fmt.Errorf("%s: cannot encode %T as TOML", path, raw)
	}
}

func tomlEncodableNumber(num json.Number) (any, error) {
	str := num.String()
	if strings.ContainsAny(str, ".eE") {
		val, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", str)
		}
		return val, nil
	}

	val, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid integer %q", str)
	}
	return val, nil
}

func decodeTOML(data string) (any, error) {
	var raw any
	if err := toml.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}
	return tomlDecodedRaw(raw)
}

func tomlDecodedRaw(raw any) (any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string, bool:
		return v, nil
	case int:
		return json.Number(strconv.FormatInt(int64(v), 10)), nil
	case int8:
		return json.Number(strconv.FormatInt(int64(v), 10)), nil
	case int16:
		return json.Number(strconv.FormatInt(int64(v), 10)), nil
	case int32:
		return json.Number(strconv.FormatInt(int64(v), 10)), nil
	case int64:
		return json.Number(strconv.FormatInt(v, 10)), nil
	case uint:
		return json.Number(strconv.FormatUint(uint64(v), 10)), nil
	case uint8:
		return json.Number(strconv.FormatUint(uint64(v), 10)), nil
	case uint16:
		return json.Number(strconv.FormatUint(uint64(v), 10)), nil
	case uint32:
		return json.Number(strconv.FormatUint(uint64(v), 10)), nil
	case uint64:
		return json.Number(strconv.FormatUint(v, 10)), nil
	case float32:
		return json.Number(strconv.FormatFloat(float64(v), 'g', -1, 32)), nil
	case float64:
		return json.Number(strconv.FormatFloat(v, 'g', -1, 64)), nil
	case time.Time:
		return v.Format(time.RFC3339Nano), nil
	case toml.LocalDate:
		return v.String(), nil
	case toml.LocalTime:
		return v.String(), nil
	case toml.LocalDateTime:
		return v.String(), nil
	case []any:
		items := make([]any, len(v))
		for i, item := range v {
			converted, err := tomlDecodedRaw(item)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			items[i] = converted
		}
		return items, nil
	case map[string]any:
		obj := make(map[string]any, len(v))
		for key, item := range v {
			converted, err := tomlDecodedRaw(item)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			obj[key] = converted
		}
		return obj, nil
	default:
		return nil, fmt.Errorf("unsupported TOML value %T", raw)
	}
}
