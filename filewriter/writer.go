package filewriter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	// DefaultFilename 默认文件名（不含扩展名）
	DefaultFilename = "export"
	// DefaultExt 默认扩展名
	DefaultExt = ".txt"
)

var (
	// ErrEmptyContent 写入内容为空
	ErrEmptyContent = errors.New("empty content")
	// ErrWriterClosed writer 已关闭
	ErrWriterClosed = errors.New("file writer is closed")
)

// FileWriter 并发安全的文件写入器（追加写，不覆盖）
type FileWriter struct {
	dir string

	mu          sync.Mutex
	fileLocks   map[string]*sync.Mutex
	fileHandles map[string]*os.File
	closed      bool
}

var (
	defaultMu     sync.Mutex
	defaultDir    string
	defaultWriter *FileWriter
)

// New 创建一个新的 FileWriter。
// dir 为空时默认使用当前工作目录。
func New(dir string) (*FileWriter, error) {
	resolvedDir, err := resolveDir(dir)
	if err != nil {
		return nil, err
	}

	return &FileWriter{
		dir:         resolvedDir,
		fileLocks:   make(map[string]*sync.Mutex),
		fileHandles: make(map[string]*os.File),
	}, nil
}

// Default 获取默认单例写入器（目录为当前工作目录）。
func Default() (*FileWriter, error) {
	defaultMu.Lock()
	if defaultWriter != nil {
		w := defaultWriter
		defaultMu.Unlock()
		return w, nil
	}
	dir := defaultDir
	defaultMu.Unlock()

	newWriter, err := New(dir)
	if err != nil {
		return nil, err
	}

	defaultMu.Lock()
	if defaultWriter == nil {
		defaultWriter = newWriter
		defaultMu.Unlock()
		return newWriter, nil
	}
	w := defaultWriter
	defaultMu.Unlock()

	_ = newWriter.Close()
	return w, nil
}

// SetDefaultDir 设置默认单例目录。
// 如果默认单例已初始化，会原地切换到新目录。
func SetDefaultDir(dir string) error {
	resolvedDir, err := resolveDir(dir)
	if err != nil {
		return err
	}

	defaultMu.Lock()
	defaultDir = resolvedDir
	w := defaultWriter
	defaultMu.Unlock()

	if w != nil {
		return w.SetDir(resolvedDir)
	}
	return nil
}

// Dir 返回绑定目录。
func (w *FileWriter) Dir() string {
	return w.dir
}

// SaveData 保存一行数据，格式与 Python 示例一致：
// one----two----three----four
// filename 为空时默认写入“export.txt”。
func (w *FileWriter) SaveData(one, two, three, four string, filename ...string) error {
	content := buildContent(one, two, three, four)
	if content == "" {
		return ErrEmptyContent
	}

	name := DefaultFilename
	if len(filename) > 0 && strings.TrimSpace(filename[0]) != "" {
		name = filename[0]
	}
	return w.AppendLine(name, content)
}

// AppendLine 追加写入一行文本，不会覆盖原内容。
func (w *FileWriter) AppendLine(filename, text string) error {
	line := strings.TrimRight(text, "\r\n")
	if line == "" {
		return ErrEmptyContent
	}

	lockKey := normalizeFilename(filename)
	fileLock := w.getOrCreateFileLock(lockKey)

	fileLock.Lock()
	defer fileLock.Unlock()

	fh, err := w.getOrCreateFileHandle(lockKey)
	if err != nil {
		return err
	}

	if _, err := fh.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("write file failed: %w", err)
	}
	return nil
}

// SaveLine 使用默认单例写入器追加一行。
func SaveLine(text string, filename ...string) error {
	w, err := Default()
	if err != nil {
		return err
	}
	name := DefaultFilename
	if len(filename) > 0 && strings.TrimSpace(filename[0]) != "" {
		name = filename[0]
	}
	return w.AppendLine(name, text)
}

// SaveData 使用默认单例写入器保存格式化数据。
func SaveData(one, two, three, four string, filename ...string) error {
	w, err := Default()
	if err != nil {
		return err
	}
	return w.SaveData(one, two, three, four, filename...)
}

// Close 关闭默认单例写入器并释放文件句柄。
// 关闭后再次调用 SaveLine/SaveData 会按当前默认目录自动重建单例。
func Close() error {
	defaultMu.Lock()
	w := defaultWriter
	defaultWriter = nil
	defaultMu.Unlock()

	if w == nil {
		return nil
	}
	return w.Close()
}

// Close 关闭全部已打开文件句柄。
func (w *FileWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true

	locks := w.snapshotLocksLocked()
	w.mu.Unlock()

	lockAll(locks)
	defer unlockAll(locks)

	w.mu.Lock()
	handles := w.detachHandlesLocked()
	w.mu.Unlock()

	return closeHandles(handles)
}

// SetDir 切换写入目录。切换后新写入会落到新目录。
func (w *FileWriter) SetDir(dir string) error {
	resolvedDir, err := resolveDir(dir)
	if err != nil {
		return err
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ErrWriterClosed
	}
	locks := w.snapshotLocksLocked()
	w.mu.Unlock()

	lockAll(locks)
	defer unlockAll(locks)

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ErrWriterClosed
	}
	handles := w.detachHandlesLocked()
	w.dir = resolvedDir
	w.mu.Unlock()

	return closeHandles(handles)
}

func (w *FileWriter) getOrCreateFileLock(fileKey string) *sync.Mutex {
	w.mu.Lock()
	defer w.mu.Unlock()

	lock, ok := w.fileLocks[fileKey]
	if ok {
		return lock
	}

	lock = &sync.Mutex{}
	w.fileLocks[fileKey] = lock
	return lock
}

func (w *FileWriter) getOrCreateFileHandle(fileKey string) (*os.File, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, ErrWriterClosed
	}

	if fh, ok := w.fileHandles[fileKey]; ok {
		return fh, nil
	}

	filePath := filepath.Join(w.dir, fileKey)
	fh, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open file failed: %w", err)
	}

	w.fileHandles[fileKey] = fh
	return fh, nil
}

func buildContent(one, two, three, four string) string {
	switch {
	case four != "":
		return one + "----" + two + "----" + three + "----" + four
	case three != "":
		return one + "----" + two + "----" + three
	case two != "":
		return one + "----" + two
	case one != "":
		return one
	default:
		return ""
	}
}

func normalizeFilename(filename string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		name = DefaultFilename
	}

	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")

	if filepath.Ext(name) == "" {
		name += DefaultExt
	}
	return name
}

func resolveDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get cwd failed: %w", err)
		}
		dir = cwd
	}

	resolved := filepath.Clean(dir)
	if err := os.MkdirAll(resolved, 0o755); err != nil {
		return "", fmt.Errorf("create data dir failed: %w", err)
	}
	return resolved, nil
}

func (w *FileWriter) snapshotLocksLocked() []*sync.Mutex {
	lockKeys := make([]string, 0, len(w.fileLocks))
	for name := range w.fileLocks {
		lockKeys = append(lockKeys, name)
	}
	sort.Strings(lockKeys)

	locks := make([]*sync.Mutex, 0, len(lockKeys))
	for _, name := range lockKeys {
		locks = append(locks, w.fileLocks[name])
	}
	return locks
}

func (w *FileWriter) detachHandlesLocked() map[string]*os.File {
	handles := make(map[string]*os.File, len(w.fileHandles))
	for name, fh := range w.fileHandles {
		handles[name] = fh
	}
	w.fileHandles = make(map[string]*os.File)
	return handles
}

func lockAll(locks []*sync.Mutex) {
	for _, l := range locks {
		l.Lock()
	}
}

func unlockAll(locks []*sync.Mutex) {
	for i := len(locks) - 1; i >= 0; i-- {
		locks[i].Unlock()
	}
}

func closeHandles(handles map[string]*os.File) error {
	var errList []error
	for _, fh := range handles {
		if fh == nil {
			continue
		}
		if err := fh.Sync(); err != nil {
			errList = append(errList, err)
		}
		if err := fh.Close(); err != nil {
			errList = append(errList, err)
		}
	}
	return errors.Join(errList...)
}
