package services

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	composetypes "github.com/compose-spec/compose-go/types"
	"github.com/docker/cli/cli/config"
	dockerTypes "github.com/docker/cli/cli/config/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/joho/godotenv"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"gopkg.in/yaml.v3"
	"github.com/compose-spec/compose-go/loader"
)

// pullImage pulls a Docker image from a registry
func (ds *DockerService) pullImage(cli *client.Client, imageName, username, password string) error {
	ctx := context.Background()

	// Check if image already exists locally
	exists, err := ds.imageExistsLocally(cli, imageName)
	if err != nil {
		return err
	}
	if exists {
		ds.logger.Info(fmt.Sprintf("Image %s already exists locally, skipping pull", imageName), "docker")
		return nil
	}

	ds.logger.Info(fmt.Sprintf("Pulling image: %s", imageName), "docker")

	// Get authentication config
	authConfig, err := ds.getAuthConfig(imageName, username, password)
	if err != nil {
		ds.logger.Warn(fmt.Sprintf("Failed to get auth config, proceeding without auth: %v", err), "docker")
		authConfig = dockerTypes.AuthConfig{}
	}

	encodedAuth, err := ds.encodeAuthToBase64(authConfig)
	if err != nil {
		return fmt.Errorf("failed to encode auth: %w", err)
	}

	// Get platform
	platform := ds.getPlatform(cli)
	var platformStr string
	if platform != nil {
		platformStr = fmt.Sprintf("%s/%s", platform.OS, platform.Architecture)
	}

	// Pull image
	reader, err := cli.ImagePull(ctx, imageName, image.PullOptions{
		Platform:     platformStr,
		RegistryAuth: encodedAuth,
	})
	if err != nil {
		ds.logger.Error(fmt.Sprintf("Failed to pull image: %v", err), "docker")
		return err
	}
	defer reader.Close()

	// Process output
	if err := ds.processDockerPullOutput(reader, imageName); err != nil {
		return err
	}

	ds.logger.Info(fmt.Sprintf("Image pulled successfully: %s", imageName), "docker")
	return nil
}

// buildImage builds a Docker image from a context directory
func (ds *DockerService) buildImage(cli *client.Client, contextDir, imageName, dockerfile string) error {
	ctx := context.Background()

	ds.logger.Info(fmt.Sprintf("Building image %s from context: %s", imageName, contextDir), "docker")

	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	// Create tar archive of build context
	tarBuf, err := ds.tarDirectory(contextDir)
	if err != nil {
		return fmt.Errorf("failed to create tar archive: %w", err)
	}

	// Get platform
	platform := ds.getPlatform(cli)
	var platformStr string
	if platform != nil {
		platformStr = fmt.Sprintf("%s/%s", platform.OS, platform.Architecture)
	}

	// Build image
	resp, err := cli.ImageBuild(ctx, bytes.NewReader(tarBuf.Bytes()), types.ImageBuildOptions{
		Tags:       []string{imageName},
		Dockerfile: dockerfile,
		Remove:     true,
		Platform:   platformStr,
	})
	if err != nil {
		ds.logger.Error(fmt.Sprintf("Failed to build image: %v", err), "docker")
		return err
	}
	defer resp.Body.Close()

	// Process build output
	if err := ds.processDockerBuildOutput(resp.Body, imageName); err != nil {
		return err
	}

	ds.logger.Info(fmt.Sprintf("Image built successfully: %s", imageName), "docker")
	return nil
}

// parseCompose parses a docker-compose.yml file
func (ds *DockerService) parseCompose(composePath, envFile string) (*composetypes.Project, error) {
	// Load environment file if specified
	if envFile != "" {
		_ = godotenv.Load(envFile)
	}

	// Read compose file
	content, err := os.ReadFile(composePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read compose file: %w", err)
	}

	// Parse YAML
	var raw map[string]any
	if err := yaml.Unmarshal(content, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Load with compose-go
	project, err := loader.Load(composetypes.ConfigDetails{
		ConfigFiles: []composetypes.ConfigFile{{
			Filename: composePath,
			Config:   raw,
		}},
		WorkingDir:  filepath.Dir(composePath),
		Environment: ds.getEnvMap(),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to load compose project: %w", err)
	}

	return project, nil
}

// detectInterfacesFromImage auto-detects interfaces from Docker image metadata
func (ds *DockerService) detectInterfacesFromImage(cli *client.Client, imageName string) ([]SuggestedInterface, error) {
	ctx := context.Background()
	var suggestions []SuggestedInterface

	// Inspect image
	inspect, _, err := cli.ImageInspectWithRaw(ctx, imageName)
	if err != nil {
		return nil, err
	}

	// Detect STDIN if image has an entrypoint or cmd
	if len(inspect.Config.Entrypoint) > 0 || len(inspect.Config.Cmd) > 0 {
		suggestions = append(suggestions, SuggestedInterface{
			InterfaceType: "STDIN",
			Path:          "",
			Description:   "Container accepts input via STDIN",
		})
	}

	// Detect STDOUT (most containers output to stdout)
	suggestions = append(suggestions, SuggestedInterface{
		InterfaceType: "STDOUT",
		Path:          "",
		Description:   "Container outputs to STDOUT",
	})

	// Detect MOUNT interfaces from exposed volumes
	if inspect.Config.Volumes != nil {
		for volumePath := range inspect.Config.Volumes {
			suggestions = append(suggestions, SuggestedInterface{
				InterfaceType: "MOUNT",
				Path:          volumePath,
				Description:   fmt.Sprintf("Volume mount point: %s", volumePath),
			})
		}
	}

	// Detect additional MOUNT interfaces from working dir
	if inspect.Config.WorkingDir != "" {
		suggestions = append(suggestions, SuggestedInterface{
			InterfaceType: "MOUNT",
			Path:          inspect.Config.WorkingDir,
			Description:   fmt.Sprintf("Working directory: %s", inspect.Config.WorkingDir),
		})
	}

	return suggestions, nil
}

// detectInterfacesFromCompose auto-detects interfaces from docker-compose project
func (ds *DockerService) detectInterfacesFromCompose(project *composetypes.Project) []SuggestedInterface {
	var suggestions []SuggestedInterface
	seenPaths := make(map[string]bool)

	for _, svc := range project.Services {
		// STDIN/STDOUT
		if svc.StdinOpen || svc.Tty {
			if !seenPaths["STDIN"] {
				suggestions = append(suggestions, SuggestedInterface{
					InterfaceType: "STDIN",
					Path:          "",
					Description:   "Service accepts input via STDIN",
				})
				seenPaths["STDIN"] = true
			}
		}

		// STDOUT (assume all services output)
		if !seenPaths["STDOUT"] {
			suggestions = append(suggestions, SuggestedInterface{
				InterfaceType: "STDOUT",
				Path:          "",
				Description:   "Service outputs to STDOUT",
			})
			seenPaths["STDOUT"] = true
		}

		// MOUNT interfaces from volumes
		for _, volume := range svc.Volumes {
			if volume.Target != "" && !seenPaths[volume.Target] {
				suggestions = append(suggestions, SuggestedInterface{
					InterfaceType: "MOUNT",
					Path:          volume.Target,
					Description:   fmt.Sprintf("Volume mount: %s", volume.Target),
				})
				seenPaths[volume.Target] = true
			}
		}
	}

	return suggestions
}

// Helper methods for Docker operations

func (ds *DockerService) imageExistsLocally(cli *client.Client, imageName string) (bool, error) {
	_, _, err := cli.ImageInspectWithRaw(context.Background(), imageName)
	if err == nil {
		return true, nil
	}
	if client.IsErrNotFound(err) {
		return false, nil
	}
	return false, err
}

func (ds *DockerService) getAuthConfig(imageName, username, password string) (dockerTypes.AuthConfig, error) {
	// If explicit credentials provided, use them
	if username != "" || password != "" {
		return dockerTypes.AuthConfig{
			Username: username,
			Password: password,
		}, nil
	}

	// Parse registry from image name (default to Docker Hub)
	registry := "https://index.docker.io/v1/"
	if strings.Contains(imageName, "/") {
		parts := strings.Split(imageName, "/")
		if strings.Contains(parts[0], ".") {
			registry = parts[0]
		}
	}

	// Load from Docker config file
	configFile, err := config.Load(config.Dir())
	if err != nil {
		return dockerTypes.AuthConfig{}, nil // Return empty auth, not an error
	}

	authConfig, err := configFile.GetAuthConfig(registry)
	if err != nil {
		return dockerTypes.AuthConfig{}, nil // Return empty auth, not an error
	}

	return authConfig, nil
}

func (ds *DockerService) encodeAuthToBase64(authConfig dockerTypes.AuthConfig) (string, error) {
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(encodedJSON), nil
}

func (ds *DockerService) getPlatform(cli *client.Client) *specs.Platform {
	// Check environment variable
	if env := os.Getenv("DOCKER_DEFAULT_PLATFORM"); env != "" {
		parts := strings.Split(env, "/")
		if len(parts) == 2 {
			return &specs.Platform{
				OS:           parts[0],
				Architecture: parts[1],
			}
		}
	}

	// Detect from Docker daemon info
	info, err := cli.Info(context.Background())
	if err != nil {
		ds.logger.Warn(fmt.Sprintf("Could not detect platform: %v", err), "docker")
		return nil
	}

	return &specs.Platform{
		OS:           info.OSType,
		Architecture: info.Architecture,
	}
}

func (ds *DockerService) tarDirectory(dir string) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)
	defer tw.Close()

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath) // POSIX-style paths

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Write file content
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})

	return buf, err
}

func (ds *DockerService) getEnvMap() map[string]string {
	env := map[string]string{}
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env
}

func (ds *DockerService) processDockerPullOutput(reader io.Reader, imageName string) error {
	// Create logs directory
	logDir := filepath.Join(ds.appPaths.DataDir, "docker_logs", "pulls")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	sanitizedName := strings.ReplaceAll(imageName, "/", "_")
	sanitizedName = strings.ReplaceAll(sanitizedName, ":", "_")
	logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", sanitizedName))

	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Bytes()

		// Write to log file
		logFile.Write(append(line, '\n'))

		// Parse JSON for logging
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if status, ok := msg["status"].(string); ok {
			ds.logger.Debug(fmt.Sprintf("Pull: %s", status), "docker")
		}
	}

	return scanner.Err()
}

func (ds *DockerService) processDockerBuildOutput(reader io.Reader, imageName string) error {
	// Create logs directory
	logDir := filepath.Join(ds.appPaths.DataDir, "docker_logs", "builds")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	sanitizedName := strings.ReplaceAll(imageName, "/", "_")
	sanitizedName = strings.ReplaceAll(sanitizedName, ":", "_")
	logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", sanitizedName))

	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Bytes()

		// Write to log file
		logFile.Write(append(line, '\n'))

		// Parse JSON for logging
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if stream, ok := msg["stream"].(string); ok {
			ds.logger.Debug(strings.TrimSpace(stream), "docker")
		}
		if errMsg, ok := msg["error"].(string); ok {
			ds.logger.Error(fmt.Sprintf("Build error: %s", errMsg), "docker")
			return fmt.Errorf("build failed: %s", errMsg)
		}
	}

	return scanner.Err()
}
