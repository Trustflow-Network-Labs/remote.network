package utils

import (
	"os"
	"path/filepath"
	"runtime"
)

type AppPaths struct {
	AppDir    string
	ConfigDir string
	LogDir    string
	DataDir   string
	TempDir   string
}

func GetAppPaths(appName string) *AppPaths {
	if appName == "" {
		appName = "remote-network"
	}

	var homeDir string
	var err error

	// Get home directory
	if homeDir, err = os.UserHomeDir(); err != nil {
		// Fallback to current directory if home directory is not available
		if homeDir, err = os.Getwd(); err != nil {
			homeDir = "."
		}
	}

	paths := &AppPaths{}

	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		paths.AppDir = filepath.Join(appData, appName)
		paths.ConfigDir = paths.AppDir
		paths.LogDir = paths.AppDir
		paths.DataDir = paths.AppDir
		paths.TempDir = os.TempDir()

	case "darwin":
		paths.AppDir = filepath.Join(homeDir, "Library", "Application Support", appName)
		paths.ConfigDir = paths.AppDir
		paths.LogDir = filepath.Join(homeDir, "Library", "Logs", appName)
		paths.DataDir = paths.AppDir
		paths.TempDir = os.TempDir()

	case "linux":
		// Follow XDG Base Directory Specification
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(homeDir, ".config")
		}

		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			dataHome = filepath.Join(homeDir, ".local", "share")
		}

		cacheHome := os.Getenv("XDG_CACHE_HOME")
		if cacheHome == "" {
			cacheHome = filepath.Join(homeDir, ".cache")
		}

		paths.AppDir = filepath.Join(dataHome, appName)
		paths.ConfigDir = filepath.Join(configHome, appName)
		paths.LogDir = filepath.Join(cacheHome, appName, "logs")
		paths.DataDir = filepath.Join(dataHome, appName)
		paths.TempDir = os.TempDir()

	default:
		// Fallback for unknown OS
		paths.AppDir = filepath.Join(homeDir, "."+appName)
		paths.ConfigDir = paths.AppDir
		paths.LogDir = paths.AppDir
		paths.DataDir = paths.AppDir
		paths.TempDir = os.TempDir()
	}

	// Ensure all directories exist
	for _, dir := range []string{paths.AppDir, paths.ConfigDir, paths.LogDir, paths.DataDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			// If we can't create the directory, fallback to current directory
			fallbackDir := "."
			paths.AppDir = fallbackDir
			paths.ConfigDir = fallbackDir
			paths.LogDir = fallbackDir
			paths.DataDir = fallbackDir
			break
		}
	}

	return paths
}

// GetConfigPath returns the path to a config file
func (ap *AppPaths) GetConfigPath(filename string) string {
	return filepath.Join(ap.ConfigDir, filename)
}

// GetLogPath returns the path to a log file
func (ap *AppPaths) GetLogPath(filename string) string {
	return filepath.Join(ap.LogDir, filename)
}

// GetDataPath returns the path to a data file
func (ap *AppPaths) GetDataPath(filename string) string {
	return filepath.Join(ap.DataDir, filename)
}

// GetTempPath returns the path to a temporary file
func (ap *AppPaths) GetTempPath(filename string) string {
	return filepath.Join(ap.TempDir, filename)
}