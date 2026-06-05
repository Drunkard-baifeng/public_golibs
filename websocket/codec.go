package websocket

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// BytesFromHex 将常见 hex 文本格式转为 bytes。
func BytesFromHex(value any) ([]byte, error) {
	switch v := value.(type) {
	case []byte:
		return append([]byte(nil), v...), nil
	case string:
		return decodeHexString(v)
	default:
		return nil, fmt.Errorf("hex value must be string or []byte")
	}
}

// BytesToHex 将 bytes 或文本转成 hex 字符串。
func BytesToHex(value any, separator string, uppercase bool) (string, error) {
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return "", fmt.Errorf("value must be string or []byte")
	}

	result := hex.EncodeToString(data)
	if separator != "" && len(result) > 2 {
		parts := make([]string, 0, len(result)/2)
		for i := 0; i < len(result); i += 2 {
			parts = append(parts, result[i:i+2])
		}
		result = strings.Join(parts, separator)
	}
	if uppercase {
		result = strings.ToUpper(result)
	}
	return result, nil
}

func decodeHexString(value string) ([]byte, error) {
	normalized := strings.TrimSpace(value)
	for _, old := range []string{"0x", "0X", " ", "\n", "\r", "\t", "-", ":"} {
		normalized = strings.ReplaceAll(normalized, old, "")
	}
	if normalized == "" {
		return []byte{}, nil
	}
	if len(normalized)%2 != 0 {
		return nil, fmt.Errorf("hex value must contain an even number of digits")
	}
	data, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("hex value contains non-hex characters: %w", err)
	}
	return data, nil
}
