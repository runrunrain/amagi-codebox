package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Level 日志级别
type Level string

const (
	LevelDebug Level = "DEBUG"
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
)

// Entry 单条日志
type Entry struct {
	Time    string `json:"time"`
	Level   Level  `json:"level"`
	Source  string `json:"source"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// Service 日志服务
type Service struct {
	mu       sync.Mutex
	entries  []Entry
	logDir   string
	logFile  *os.File
	logDate  string // 当前 logFile 对应的日期（2006-01-02），用于跨天滚动
	maxMem   int    // 内存中保留的最大条数
	minLevel Level
	closed   bool // Close 后置位，阻止 log() 重新打开文件
}

var levelOrder = map[Level]int{
	LevelDebug: 0,
	LevelInfo:  1,
	LevelWarn:  2,
	LevelError: 3,
}

// reportWriteError surfaces a log-file write failure, so a full disk or an
// unwritable file no longer causes log lines to disappear silently (the file
// sink already shares every line with the stderr mirror, but the write error
// itself was previously dropped). Default prints to stderr. Overridable in
// tests to assert the error is reported (see service_test.go).
var reportWriteError = func(err error) {
	fmt.Fprintf(os.Stderr, "[logging] write log failed: %v\n", err)
}

func NewService(configDir string) *Service {
	dir := filepath.Join(configDir, "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		// 目录创建失败时日志退化为内存+stderr 镜像（与下方 OpenFile 失败同策略），
		// 但不静默：与 reportWriteError 一致地向 stderr 报告。
		fmt.Fprintf(os.Stderr, "[logging] create log dir failed: %v\n", err)
	}

	today := time.Now().Format("2006-01-02")
	s := &Service{
		logDir:   dir,
		logDate:  today,
		entries:  make([]Entry, 0, 512),
		maxMem:   2000,
		minLevel: LevelDebug,
	}

	// 打开当天日志文件
	name := fmt.Sprintf("amagi-codebox-%s.log", today)
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		s.logFile = f
	}

	s.Info("logging", "日志系统启动", "logDir="+dir)
	return s
}

func (s *Service) log(level Level, source, message, detail string) {
	if levelOrder[level] < levelOrder[s.minLevel] {
		return
	}

	now := time.Now()
	e := Entry{
		Time:    now.Format("2006-01-02 15:04:05.000"),
		Level:   level,
		Source:  source,
		Message: message,
		Detail:  detail,
	}

	s.mu.Lock()
	s.entries = append(s.entries, e)
	if len(s.entries) > s.maxMem {
		s.entries = s.entries[len(s.entries)-s.maxMem:]
	}
	// 跨天滚动与写盘都在 s.mu 内完成：日期变更时关闭旧文件并打开当天新文件，
	// 避免跨午夜瞬间多 goroutine 写入已关闭的旧 fd；写错误通过 reportWriteError
	// 上报，不再静默丢弃。Shutdown（closed=true）后不再重开文件，但内存条目与
	// stderr 镜像仍保留。
	if s.closed {
		// 已关闭：不再写文件。
	} else if rerr := s.ensureLogFileLocked(now.Format("2006-01-02")); rerr != nil {
		reportWriteError(rerr)
	} else if s.logFile != nil {
		line := fmt.Sprintf("[%s] %s [%s] %s", e.Time, e.Level, e.Source, e.Message)
		if e.Detail != "" {
			line += " | " + e.Detail
		}
		if _, werr := s.logFile.WriteString(line + "\n"); werr != nil {
			reportWriteError(werr)
		}
	}
	s.mu.Unlock()

	// 同时输出到 stderr 便于开发调试
	fmt.Fprintf(os.Stderr, "[%s] %s [%s] %s\n", e.Time, e.Level, e.Source, e.Message)
}

// ensureLogFileLocked 确保当前打开的日志文件对应 today。跨天运行时关闭旧文件
// 并打开当天新文件，实现日志文件按天滚动（启动后不再跨天则直接复用）。调用
// 方必须持有 s.mu。Close 后由 log() 跳过调用，避免重开文件。
func (s *Service) ensureLogFileLocked(today string) error {
	if s.logFile != nil && s.logDate == today {
		return nil
	}
	// 日期变化（或首次打开失败后补开）：滚动到当天文件。
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
	name := fmt.Sprintf("amagi-codebox-%s.log", today)
	f, err := os.OpenFile(filepath.Join(s.logDir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	s.logFile = f
	s.logDate = today
	return nil
}

func (s *Service) Debug(source, message string, detail ...string) {
	s.log(LevelDebug, source, message, join(detail))
}

func (s *Service) Info(source, message string, detail ...string) {
	s.log(LevelInfo, source, message, join(detail))
}

func (s *Service) Warn(source, message string, detail ...string) {
	s.log(LevelWarn, source, message, join(detail))
}

func (s *Service) Error(source, message string, detail ...string) {
	s.log(LevelError, source, message, join(detail))
}

// GetEntries 返回内存中的日志条目，支持过滤
func (s *Service) GetEntries(level string, source string, keyword string, limit int) []Entry {
	s.mu.Lock()
	all := make([]Entry, len(s.entries))
	copy(all, s.entries)
	s.mu.Unlock()

	filterLevel := Level(strings.ToUpper(level))
	minLvl, hasLevel := levelOrder[filterLevel]

	result := make([]Entry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		if hasLevel && levelOrder[e.Level] < minLvl {
			continue
		}
		if source != "" && e.Source != source {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(e.Message+e.Detail), strings.ToLower(keyword)) {
			continue
		}
		result = append(result, e)
		if limit > 0 && len(result) >= limit {
			break
		}
	}

	return result
}

// GetSources 返回所有出现过的日志来源
func (s *Service) GetSources() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := map[string]bool{}
	for _, e := range s.entries {
		seen[e.Source] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// GetLogFiles 返回日志文件列表
func (s *Service) GetLogFiles() []string {
	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			result = append(result, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(result)))
	return result
}

// GetLogFileContent 读取指定日志文件内容
func (s *Service) GetLogFileContent(filename string) (string, error) {
	// 防止目录遍历
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return "", fmt.Errorf("invalid filename")
	}
	data, err := os.ReadFile(filepath.Join(s.logDir, filename))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ClearEntries 清空内存日志
func (s *Service) ClearEntries() {
	s.mu.Lock()
	s.entries = s.entries[:0]
	s.mu.Unlock()
}

// ExportJSON 导出所有内存日志为 JSON
func (s *Service) ExportJSON() (string, error) {
	s.mu.Lock()
	all := make([]Entry, len(s.entries))
	copy(all, s.entries)
	s.mu.Unlock()

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Close 关闭日志文件并标记关闭，阻止后续 log() 重新打开文件。
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.logFile != nil {
		s.logFile.Close()
		s.logFile = nil
	}
}

func join(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}
