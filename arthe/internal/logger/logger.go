package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

// LoggingConfig mirrors the config.yaml structure to avoid import cycles
type LoggingConfig struct {
	Level   string        `yaml:"level"`
	Console bool          `yaml:"console"`
	File    FileLogConfig `yaml:"file"`
	Discord DiscordConfig `yaml:"discord"`
}

// FileLogConfig contains file logging settings
type FileLogConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Path       string `yaml:"path"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
}

// DiscordConfig contains Discord webhook settings
type DiscordConfig struct {
	Enabled    bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"`
	Level      string `yaml:"level"`
}

// Field is a functional option for adding structured context
type Field func(*zap.Field)

// String creates a string field
func String(key, value string) Field {
	return func(f *zap.Field) {
		*f = zap.String(key, value)
	}
}

// Int creates an int field
func Int(key string, value int) Field {
	return func(f *zap.Field) {
		*f = zap.Int(key, value)
	}
}

// Err creates an error field
func Err(err error) Field {
	return func(f *zap.Field) {
		*f = zap.Error(err)
	}
}

// Any creates a field for any value
func Any(key string, value any) Field {
	return func(f *zap.Field) {
		*f = zap.Any(key, value)
	}
}

// Logger is the logging interface
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)
	Sync()
}

type zapLogger struct {
	log        *zap.Logger
	discordCfg  DiscordConfig
	discordCh   chan discordPayload
	discordWg   sync.WaitGroup
	discordDone chan struct{}
}

type discordPayload struct {
	level string
	msg   string
}

var levelMap = map[string]zapcore.Level{
	"debug": zapcore.DebugLevel,
	"info":  zapcore.InfoLevel,
	"warn":  zapcore.WarnLevel,
	"error": zapcore.ErrorLevel,
	"fatal": zapcore.FatalLevel,
}

func parseLevel(levelStr string) zapcore.Level {
	if level, ok := levelMap[strings.ToLower(levelStr)]; ok {
		return level
	}
	return zapcore.InfoLevel
}

func discordLevelToInt(lvl string) int {
	switch strings.ToLower(lvl) {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn", "warning":
		return 2
	case "error":
		return 3
	case "fatal":
		return 4
	default:
		return 3
	}
}

// New creates a new logger based on the configuration
func New(cfg LoggingConfig) (Logger, error) {
	var cores []zapcore.Core

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	level := parseLevel(cfg.Level)

	// Console output
	if cfg.Console {
		enc := zapcore.NewConsoleEncoder(encoderConfig)
		cores = append(cores, zapcore.NewCore(enc, zapcore.AddSync(os.Stdout), level))
	}

	// File output with rotation
	if cfg.File.Enabled {
		if err := os.MkdirAll(filepath.Dir(cfg.File.Path), 0755); err != nil {
			return nil, fmt.Errorf("create log dir: %w", err)
		}

		writer := &rotateWriter{
			path:       cfg.File.Path,
			maxSizeMB:  cfg.File.MaxSizeMB,
			maxBackups: cfg.File.MaxBackups,
		}

		enc := zapcore.NewJSONEncoder(encoderConfig)
		cores = append(cores, zapcore.NewCore(enc, zapcore.AddSync(writer), level))
	}

	if len(cores) == 0 {
		// Fallback to dev console
		logger, _ := zap.NewDevelopment()
		return &zapLogger{log: logger}, nil
	}

	combined := zapcore.NewTee(cores...)
	logger := zap.New(combined, zap.AddCaller(), zap.AddCallerSkip(1))

	l := &zapLogger{
		log:        logger,
		discordCfg: cfg.Discord,
	}

	// Start Discord webhook worker if enabled
	if cfg.Discord.Enabled && cfg.Discord.WebhookURL != "" {
		l.discordCh = make(chan discordPayload, 100)
		l.discordDone = make(chan struct{})
		l.discordWg.Add(1)
		go l.discordWorker()
	}

	return l, nil
}

func (l *zapLogger) logLevel(level zapcore.Level, msg string, fields ...Field) {
	zapFields := make([]zap.Field, len(fields))
	for i, f := range fields {
		f(&zapFields[i])
	}

	switch level {
	case zapcore.DebugLevel:
		l.log.Debug(msg, zapFields...)
	case zapcore.InfoLevel:
		l.log.Info(msg, zapFields...)
	case zapcore.WarnLevel:
		l.log.Warn(msg, zapFields...)
	case zapcore.ErrorLevel:
		l.log.Error(msg, zapFields...)
	case zapcore.FatalLevel:
		l.log.Fatal(msg, zapFields...)
	}

	// Send to Discord if enabled and level matches
	if l.discordCh != nil {
		discordLvl := strings.ToLower(l.discordCfg.Level)
		if discordLevelToInt(level.String()) >= discordLevelToInt(discordLvl) {
			select {
			case l.discordCh <- discordPayload{level: level.String(), msg: msg}:
			default:
				// Channel full, skip
			}
		}
	}
}

func (l *zapLogger) Debug(msg string, fields ...Field) { l.logLevel(zapcore.DebugLevel, msg, fields...) }
func (l *zapLogger) Info(msg string, fields ...Field)  { l.logLevel(zapcore.InfoLevel, msg, fields...) }
func (l *zapLogger) Warn(msg string, fields ...Field)  { l.logLevel(zapcore.WarnLevel, msg, fields...) }
func (l *zapLogger) Error(msg string, fields ...Field) { l.logLevel(zapcore.ErrorLevel, msg, fields...) }
func (l *zapLogger) Fatal(msg string, fields ...Field) { l.logLevel(zapcore.FatalLevel, msg, fields...) }

func (l *zapLogger) Sync() {
	_ = l.log.Sync()
	if l.discordCh != nil {
		close(l.discordCh)
		<-l.discordDone
	}
}

func (l *zapLogger) discordWorker() {
	defer l.discordWg.Done()
	for p := range l.discordCh {
		l.sendDiscord(p)
	}
	close(l.discordDone)
}

func (l *zapLogger) sendDiscord(p discordPayload) {
	if discordLevelToInt(p.level) < discordLevelToInt(l.discordCfg.Level) {
		return
	}

	payload := map[string]string{"content": fmt.Sprintf("[%s] %s", strings.ToUpper(p.level), p.msg)}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", l.discordCfg.WebhookURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		_, _ = client.Do(req)
	}()
}

// rotateWriter handles log file rotation by size
type rotateWriter struct {
	path       string
	maxSizeMB  int
	maxBackups int
	mu         sync.Mutex
	file       *os.File
}

func (w *rotateWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Open file if not open
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}

	// Check size
	info, err := w.file.Stat()
	if err == nil && info.Size() >= int64(w.maxSizeMB)*1024*1024 {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	return w.file.Write(p)
}

func (w *rotateWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

func (w *rotateWriter) rotate() error {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}

	// Remove oldest backup if over limit
	oldest := fmt.Sprintf("%s.%d", w.path, w.maxBackups)
	os.Remove(oldest)

	// Shift existing backups
	for i := w.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", w.path, i)
		dst := fmt.Sprintf("%s.%d", w.path, i+1)
		os.Rename(src, dst)
	}

	// Rename current to .1
	os.Rename(w.path, w.path+".1")

	// Reopen
	return w.open()
}

// Ensure rotateWriter implements io.Writer
var _ io.Writer = (*rotateWriter)(nil)

// LoadConfig loads LoggingConfig from a YAML file and resolves environment variables
func LoadConfig(path string) (*LoggingConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg LoggingConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Resolve environment variables in webhook URL
	cfg.Discord.WebhookURL = resolveEnvVars(cfg.Discord.WebhookURL)

	return &cfg, nil
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func resolveEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := match[2 : len(match)-1]
		if val := os.Getenv(varName); val != "" {
			return val
		}
		return match
	})
}