package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// RotationInterval defines rotation time intervals
type RotationInterval string

const (
	RotationHourly  RotationInterval = "hourly"
	RotationDaily   RotationInterval = "daily"
	RotationWeekly  RotationInterval = "weekly"
	RotationMonthly RotationInterval = "monthly"
)

// LogRotationConfig holds rotation configuration
type LogRotationConfig struct {
	MaxSizeMB      int64
	MaxAge         int
	MaxBackups     int
	TimeInterval   RotationInterval
	EnableRotation bool
}

type LogsManager struct {
	cm              *ConfigManager
	dir             string
	logFileName     string
	logger          *log.Logger
	File            *os.File
	mutex           sync.RWMutex
	rotationConfig  LogRotationConfig
	lastRotateCheck time.Time
	fileSize        int64
}

func NewLogsManager(cm *ConfigManager) *LogsManager {
	paths := GetAppPaths("")
	logFileName := cm.GetConfigWithDefault("logfile", "remote-network")

	rotationConfig := LogRotationConfig{
		MaxSizeMB:      parseConfigInt64(cm.GetConfigWithDefault("log_max_size_mb", "100")),
		MaxAge:         parseConfigInt(cm.GetConfigWithDefault("log_max_age_days", "30")),
		MaxBackups:     parseConfigInt(cm.GetConfigWithDefault("log_max_backups", "10")),
		TimeInterval:   RotationInterval(cm.GetConfigWithDefault("log_rotation_interval", "daily")),
		EnableRotation: parseConfigBool(cm.GetConfigWithDefault("log_enable_rotation", "true")),
	}

	lm := &LogsManager{
		cm:              cm,
		dir:             paths.LogDir,
		logFileName:     logFileName,
		logger:          log.New(),
		rotationConfig:  rotationConfig,
		lastRotateCheck: time.Now(),
	}

	if err := lm.initLogger(); err != nil {
		panic(err)
	}

	return lm
}

func (lm *LogsManager) initLogger() error {
	switch runtime.GOOS {
	case "linux", "darwin":
		lm.logFileName = filepath.ToSlash(lm.logFileName)
	case "windows":
		lm.logFileName = filepath.FromSlash(lm.logFileName)
	default:
		return fmt.Errorf("unsupported OS type `%s`", runtime.GOOS)
	}

	path := filepath.Join(lm.dir, lm.logFileName)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return err
	}

	lm.File = file

	if stat, err := file.Stat(); err == nil {
		lm.fileSize = stat.Size()
	}

	logLevel := lm.cm.GetConfigWithDefault("log_level", "info")
	level, err := log.ParseLevel(logLevel)
	if err != nil {
		fmt.Printf("Invalid log level '%s', defaulting to 'info'\n", logLevel)
		level = log.InfoLevel
	}

	lm.logger.SetLevel(level)
	lm.logger.SetOutput(file)
	lm.logger.SetFormatter(&log.JSONFormatter{})

	return nil
}

// ✅ AUTOMATIC CALLER DETECTION (file:line + function)
func (lm *LogsManager) callerInfo() (string, string) {
	for i := 2; i < 15; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			continue
		}

		// Skip logger internals
		if strings.Contains(file, "logs.go") {
			continue
		}

		// Short file name only
		if idx := strings.LastIndex(file, "/"); idx != -1 {
			file = file[idx+1:]
		}

		// Extract function name
		fn := "unknown"
		if f := runtime.FuncForPC(pc); f != nil {
			name := f.Name()
			if lastDot := strings.LastIndex(name, "."); lastDot != -1 {
				fn = name[lastDot+1:]
			} else {
				fn = name
			}
		}

		return fmt.Sprintf("%s:%d", file, line), fn
	}

	return "unknown:0", "unknown"
}

func (lm *LogsManager) Log(level string, message string, category string) {
	if lm.rotationConfig.EnableRotation {
		lm.checkAndRotate()
	}

	lm.mutex.RLock()
	defer lm.mutex.RUnlock()

	if lm.File == nil {
		return
	}

	fileLine, function := lm.callerInfo()

	entry := lm.logger.WithFields(log.Fields{
		"category": category,
		"file":     fileLine,
		"func":     function,
	})

	switch level {
	case "trace":
		entry.Trace(message)
	case "debug":
		entry.Debug(message)
	case "info":
		entry.Info(message)
	case "warn":
		entry.Warn(message)
	case "error":
		entry.Error(message)
	case "fatal":
		entry.Fatal(message)
	case "panic":
		entry.Panic(message)
	default:
		entry.Info(message)
	}

	lm.fileSize += int64(len(message) + 100)
}

func (lm *LogsManager) Debug(message string, category string) {
	lm.Log("debug", message, category)
}

func (lm *LogsManager) Info(message string, category string) {
	lm.Log("info", message, category)
}

func (lm *LogsManager) Warn(message string, category string) {
	lm.Log("warn", message, category)
}

func (lm *LogsManager) Error(message string, category string) {
	lm.Log("error", message, category)
}

func (lm *LogsManager) Close() error {
	lm.mutex.Lock()
	defer lm.mutex.Unlock()

	if lm.File != nil {
		err := lm.File.Close()
		lm.File = nil
		return err
	}
	return nil
}

func (lm *LogsManager) checkAndRotate() {
	now := time.Now()

	if lm.rotationConfig.MaxSizeMB > 0 && lm.fileSize > lm.rotationConfig.MaxSizeMB*1024*1024 {
		lm.rotateWithBackup("size")
		return
	}

	if now.Sub(lm.lastRotateCheck) > time.Minute {
		lm.lastRotateCheck = now
		if lm.shouldRotateByTime(now) {
			lm.rotateWithBackup("time")
		}
	}
}

func (lm *LogsManager) shouldRotateByTime(now time.Time) bool {
	if lm.File == nil {
		return false
	}

	stat, err := lm.File.Stat()
	if err != nil {
		return false
	}

	modTime := stat.ModTime()

	switch lm.rotationConfig.TimeInterval {
	case RotationHourly:
		return now.Hour() != modTime.Hour() || now.Day() != modTime.Day()
	case RotationDaily:
		return now.Day() != modTime.Day() || now.Month() != modTime.Month()
	case RotationWeekly:
		_, nowWeek := now.ISOWeek()
		_, modWeek := modTime.ISOWeek()
		return nowWeek != modWeek || now.Year() != modTime.Year()
	case RotationMonthly:
		return now.Month() != modTime.Month() || now.Year() != modTime.Year()
	}

	return false
}

func (lm *LogsManager) rotateWithBackup(reason string) {
	lm.mutex.Lock()
	defer lm.mutex.Unlock()

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	backupFileName := fmt.Sprintf("%s.%s.bak", lm.logFileName, timestamp)
	backupPath := filepath.Join(lm.dir, backupFileName)
	currentPath := filepath.Join(lm.dir, lm.logFileName)

	if lm.File != nil {
		lm.File.Close()
		lm.File = nil
	}

	if _, err := os.Stat(currentPath); err == nil {
		if err := os.Rename(currentPath, backupPath); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create backup %s: %v\n", backupPath, err)
		}
	}

	if err := lm.initLogger(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to reinitialize logger after rotation: %v\n", err)
		return
	}

	lm.cleanupOldBackups()

	lm.logger.WithFields(log.Fields{
		"category": "logrotate",
		"reason":   reason,
		"backup":   backupFileName,
	}).Info("Log rotated")
}

func (lm *LogsManager) cleanupOldBackups() {
	if lm.rotationConfig.MaxAge <= 0 && lm.rotationConfig.MaxBackups <= 0 {
		return
	}

	files, err := filepath.Glob(filepath.Join(lm.dir, lm.logFileName+"*.bak"))
	if err != nil {
		return
	}

	type fileInfo struct {
		path    string
		modTime time.Time
	}

	var backups []fileInfo
	now := time.Now()

	for _, file := range files {
		if stat, err := os.Stat(file); err == nil {
			if lm.rotationConfig.MaxAge > 0 {
				age := now.Sub(stat.ModTime()).Hours() / 24
				if age > float64(lm.rotationConfig.MaxAge) {
					os.Remove(file)
					continue
				}
			}

			backups = append(backups, fileInfo{
				path:    file,
				modTime: stat.ModTime(),
			})
		}
	}

	if lm.rotationConfig.MaxBackups > 0 && len(backups) > lm.rotationConfig.MaxBackups {
		for i := 0; i < len(backups)-1; i++ {
			for j := i + 1; j < len(backups); j++ {
				if backups[i].modTime.After(backups[j].modTime) {
					backups[i], backups[j] = backups[j], backups[i]
				}
			}
		}

		excess := len(backups) - lm.rotationConfig.MaxBackups
		for i := 0; i < excess; i++ {
			os.Remove(backups[i].path)
		}
	}
}

func (lm *LogsManager) SetLogLevel(levelStr string) error {
	level, err := log.ParseLevel(levelStr)
	if err != nil {
		return fmt.Errorf("invalid log level '%s': %v", levelStr, err)
	}

	lm.mutex.Lock()
	defer lm.mutex.Unlock()
	lm.logger.SetLevel(level)

	return nil
}

func (lm *LogsManager) GetLogLevel() string {
	lm.mutex.RLock()
	defer lm.mutex.RUnlock()
	return lm.logger.GetLevel().String()
}

func parseConfigInt64(value string) int64 {
	if result, err := strconv.ParseInt(value, 10, 64); err == nil {
		return result
	}
	return 100
}

func parseConfigInt(value string) int {
	if result, err := strconv.Atoi(value); err == nil {
		return result
	}
	return 30
}

func parseConfigBool(value string) bool {
	if result, err := strconv.ParseBool(value); err == nil {
		return result
	}
	return true
}
