package pop3client

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"

	"golang.org/x/net/html/charset"
)

// EmailInfo 邮件内容信息。
type EmailInfo struct {
	From     string
	To       string
	Subject  string
	Date     string
	Body     string
	HtmlBody string
}

// ParseEmail 解析原始邮件内容。
func ParseEmail(raw []byte) (*EmailInfo, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}

	info := &EmailInfo{
		From:    decodeMIMEHeader(msg.Header.Get("From")),
		To:      decodeMIMEHeader(msg.Header.Get("To")),
		Subject: decodeMIMEHeader(msg.Header.Get("Subject")),
		Date:    msg.Header.Get("Date"),
	}

	if err := parseMIMEEntity(textproto.MIMEHeader(msg.Header), msg.Body, info); err != nil {
		return nil, err
	}
	return info, nil
}

func parseMIMEEntity(header textproto.MIMEHeader, body io.Reader, info *EmailInfo) error {
	contentDisposition := strings.ToLower(header.Get("Content-Disposition"))
	if strings.Contains(contentDisposition, "attachment") {
		_, _ = io.Copy(io.Discard, body)
		return nil
	}

	contentType := header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
		params = map[string]string{}
	}
	mediaType = strings.ToLower(mediaType)

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			_, _ = io.Copy(io.Discard, body)
			return nil
		}

		reader := multipart.NewReader(body, boundary)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if err := parseMIMEEntity(part.Header, part, info); err != nil {
				_ = part.Close()
				return err
			}
			_ = part.Close()
		}
	}

	if mediaType != "text/plain" && mediaType != "text/html" {
		_, _ = io.Copy(io.Discard, body)
		return nil
	}

	reader := transferDecodedReader(header, body)
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}

	text := decodeBody(data, params["charset"])
	if mediaType == "text/html" {
		info.HtmlBody = appendText(info.HtmlBody, text)
		return nil
	}
	info.Body = appendText(info.Body, text)
	return nil
}

func transferDecodedReader(header textproto.MIMEHeader, body io.Reader) io.Reader {
	encoding := strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding")))
	switch encoding {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		return quotedprintable.NewReader(body)
	default:
		return body
	}
}

func decodeMIMEHeader(value string) string {
	if value == "" {
		return ""
	}

	decoder := mime.WordDecoder{
		CharsetReader: func(label string, input io.Reader) (io.Reader, error) {
			return charset.NewReaderLabel(label, input)
		},
	}
	decoded, err := decoder.DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func decodeBody(data []byte, charsetName string) string {
	if strings.TrimSpace(charsetName) == "" {
		return string(data)
	}

	reader, err := charset.NewReaderLabel(charsetName, bytes.NewReader(data))
	if err != nil {
		return string(data)
	}

	decoded, err := io.ReadAll(reader)
	if err != nil {
		return string(data)
	}
	return string(decoded)
}

func appendText(existing, next string) string {
	if strings.TrimSpace(next) == "" {
		return existing
	}
	if existing == "" {
		return next
	}
	return existing + "\n" + next
}
