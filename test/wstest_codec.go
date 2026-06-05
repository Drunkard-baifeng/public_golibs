package main

import (
	"bytes"
	"compress/gzip"
	"crypto/des"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// NeaxWSCodec 复现 Python ws_decrypt.py 里的 NEAX 报文编解码。
type NeaxWSCodec struct {
	WSPrivateKey string
	key          []byte
}

type neaxPayload struct {
	Code string         `json:"code"`
	Data map[string]any `json:"data"`
}

type neaxURLTokenPayload struct {
	WSPrivateKey string `json:"wsPrivateKey"`
}

func NewNeaxWSCodec(wsPrivateKey string) (*NeaxWSCodec, error) {
	keyBytes := []byte(wsPrivateKey)
	if len(keyBytes) < des.BlockSize {
		return nil, fmt.Errorf("wsPrivateKey 长度需 >= %d 字节", des.BlockSize)
	}
	return &NeaxWSCodec{
		WSPrivateKey: wsPrivateKey,
		key:          append([]byte(nil), keyBytes[:des.BlockSize]...),
	}, nil
}

func (c *NeaxWSCodec) Encrypt(obj any) ([]byte, error) {
	encoded, err := c.Encode(obj)
	if err != nil {
		return nil, err
	}
	return gzipWire([]byte(encoded))
}

func (c *NeaxWSCodec) Decrypt(wire []byte) (map[string]any, error) {
	jsonText, err := c.DecryptText(wire)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err = json.Unmarshal([]byte(jsonText), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *NeaxWSCodec) DecryptText(wire []byte) (string, error) {
	reader, err := gzip.NewReader(bytes.NewReader(wire))
	if err != nil {
		return "", err
	}
	defer reader.Close()

	b64Data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}

	jsonText, err := c.Decode(string(b64Data))
	if err != nil {
		return "", err
	}
	return jsonText, nil
}

func (c *NeaxWSCodec) Encode(obj any) (string, error) {
	raw, err := compactJSON(obj)
	if err != nil {
		return "", err
	}
	encrypted, err := c.desEncrypt(raw)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func (c *NeaxWSCodec) Decode(b64Text string) (string, error) {
	cipherText, err := decodeBase64Loose(b64Text)
	if err != nil {
		return "", err
	}
	plainText, err := c.desDecrypt(cipherText)
	if err != nil {
		return "", err
	}
	return string(plainText), nil
}

func (c *NeaxWSCodec) BuildURLToken() (string, error) {
	encoded, err := c.Encode(neaxURLTokenPayload{WSPrivateKey: c.WSPrivateKey})
	if err != nil {
		return "", err
	}
	return url.QueryEscape(url.QueryEscape(encoded)), nil
}

func (c *NeaxWSCodec) BuildSocketURL(address, wsid string, secure bool, t string) string {
	addr := regexp.MustCompile(`^wss?://`).ReplaceAllString(address, "")
	addr = strings.TrimRight(addr, "/")
	scheme := "ws"
	if secure {
		scheme = "wss"
	}
	token, err := c.BuildURLToken()
	if err != nil {
		token = ""
	}
	return fmt.Sprintf("%s://%s/%s/%s?t=%s", scheme, addr, wsid, token, t)
}

func (c *NeaxWSCodec) desEncrypt(plainText []byte) ([]byte, error) {
	block, err := des.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	data := pkcs7Pad(plainText, block.BlockSize())
	out := make([]byte, len(data))
	for start := 0; start < len(data); start += block.BlockSize() {
		block.Encrypt(out[start:start+block.BlockSize()], data[start:start+block.BlockSize()])
	}
	return out, nil
}

func (c *NeaxWSCodec) desDecrypt(cipherText []byte) ([]byte, error) {
	block, err := des.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	if len(cipherText)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("DES 密文长度不是 %d 的倍数", block.BlockSize())
	}
	out := make([]byte, len(cipherText))
	for start := 0; start < len(cipherText); start += block.BlockSize() {
		block.Decrypt(out[start:start+block.BlockSize()], cipherText[start:start+block.BlockSize()])
	}
	return pkcs7Unpad(out, block.BlockSize())
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, 0, len(data)+pad)
	out = append(out, data...)
	out = append(out, bytes.Repeat([]byte{byte(pad)}, pad)...)
	return out
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	pad := int(data[len(data)-1])
	if pad < 1 || pad > blockSize || pad > len(data) {
		return data, nil
	}
	for _, value := range data[len(data)-pad:] {
		if int(value) != pad {
			return data, nil
		}
	}
	return data[:len(data)-pad], nil
}

func gzipWire(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	writer.ModTime = time.Unix(0, 0)
	writer.OS = 3
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) >= 10 {
		// Header 对齐 pako/Python；deflate 压缩块由 Go 标准库生成，不保证逐字节一致。
		out[8] = 0x00
		out[9] = 0x03
	}
	return out, nil
}

func compactJSON(obj any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(obj); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func decodeBase64Loose(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if missing := len(value) % 4; missing != 0 {
		value += strings.Repeat("=", 4-missing)
	}
	return base64.StdEncoding.DecodeString(value)
}
