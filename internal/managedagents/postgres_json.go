package managedagents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func postgresSafeJSON(payload json.RawMessage) (json.RawMessage, error) {
	if len(payload) == 0 {
		return payload, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	value, changed := replaceJSONNullCharacters(value)
	if !changed {
		return payload, nil
	}
	return json.Marshal(value)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func replaceJSONNullCharacters(value any) (any, bool) {
	changed := false
	switch typed := value.(type) {
	case string:
		if strings.ContainsRune(typed, 0) {
			return strings.ReplaceAll(typed, "\x00", "\uFFFD"), true
		}
	case map[string]any:
		for key, item := range typed {
			if strings.ContainsRune(key, 0) {
				delete(typed, key)
				key = strings.ReplaceAll(key, "\x00", "\uFFFD")
				typed[key] = item
				changed = true
			}
			updated, itemChanged := replaceJSONNullCharacters(item)
			if itemChanged {
				typed[key] = updated
				changed = true
			}
		}
	case []any:
		for index, item := range typed {
			updated, itemChanged := replaceJSONNullCharacters(item)
			if itemChanged {
				typed[index] = updated
				changed = true
			}
		}
	}
	return value, changed
}
