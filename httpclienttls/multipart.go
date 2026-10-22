package httpclienttls

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (c *Client) PostMultipartContext(ctx context.Context, target string, fields map[string]string, files []FileField, options *Options) (*Response, error) {
	r, err := c.PostMultipartStream(ctx, target, fields, files, options)
	if err != nil {
		return nil, err
	}
	return c.bufferResponse(r)
}

// PostMultipartStream streams files from disk with bounded buffering. The
// upload is not replayable; 307/308 responses are returned to the caller.
func (c *Client) PostMultipartStream(ctx context.Context, target string, fields map[string]string, files []FileField, options *Options) (*StreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fields = cloneStringMap(fields)
	files = append([]FileField(nil), files...)
	for _, f := range files {
		if strings.ContainsAny(f.ContentType, "\r\n") {
			return nil, fmt.Errorf("invalid multipart content type")
		}
	}
	reader, writer := io.Pipe()
	mw := multipart.NewWriter(writer)
	options = cloneOptions(options)
	setContentType(options, mw.FormDataContentType())
	// Closing the reader also unblocks a producer when setup/transport fails.
	stop := context.AfterFunc(ctx, func() { reader.CloseWithError(ctx.Err()) })
	go func() {
		err := writeMultipart(mw, fields, files)
		if err == nil {
			err = mw.Close()
		}
		writer.CloseWithError(err)
		stop()
	}()
	r, err := c.DoStream(ctx, http.MethodPost, target, reader, options)
	if err != nil {
		reader.CloseWithError(err)
	}
	return r, err
}

func writeMultipart(writer *multipart.Writer, fields map[string]string, files []FileField) error {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writer.WriteField(name, fields[name]); err != nil {
			return err
		}
	}
	for _, file := range files {
		if err := writeMultipartFile(writer, file); err != nil {
			return err
		}
	}
	return nil
}

func writeMultipartFile(writer *multipart.Writer, file FileField) error {
	var source io.Reader = bytes.NewReader(file.Data)
	if file.FilePath != "" {
		f, err := os.Open(file.FilePath)
		if err != nil {
			return fmt.Errorf("open multipart file: %w", err)
		}
		defer f.Close()
		source = f
		if file.FileName == "" {
			file.FileName = filepath.Base(file.FilePath)
		}
	}
	if file.FieldName == "" {
		file.FieldName = "file"
	}
	if file.FileName == "" {
		file.FileName = "file"
	}
	if file.ContentType == "" {
		file.ContentType = "application/octet-stream"
	}
	part, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {mime.FormatMediaType("form-data", map[string]string{"name": file.FieldName, "filename": file.FileName})},
		"Content-Type":        {file.ContentType},
	})
	if err != nil {
		return err
	}
	_, err = io.Copy(part, source)
	return err
}
