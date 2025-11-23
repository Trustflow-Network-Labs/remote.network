package services

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
)

// DockerFileCheckResult holds the results of scanning for docker-related files
type DockerFileCheckResult struct {
	HasDockerfile bool
	HasCompose    bool
	Dockerfiles   []string
	Composes      []string
}

// GitService handles Git repository operations
type GitService struct {
	logger    *utils.LogsManager
	configMgr *utils.ConfigManager
	appPaths  *utils.AppPaths
}

// NewGitService creates a new GitService instance
func NewGitService(
	logger *utils.LogsManager,
	configMgr *utils.ConfigManager,
	appPaths *utils.AppPaths,
) *GitService {
	return &GitService{
		logger:    logger,
		configMgr: configMgr,
		appPaths:  appPaths,
	}
}

// getAuth returns appropriate authentication method based on provided parameters
func (gs *GitService) getAuth(repoURL, username, password string) (transport.AuthMethod, error) {
	// No auth for public repo
	if username == "" && password == "" && strings.HasPrefix(repoURL, "http") {
		return nil, nil
	}

	// HTTPS basic auth (token or user/pass)
	if strings.HasPrefix(repoURL, "http") {
		return &githttp.BasicAuth{
			Username: username, // Can be anything for token-based auth
			Password: password,
		}, nil
	}

	// SSH auth (assumes key in ~/.ssh/id_rsa)
	if strings.HasPrefix(repoURL, "git@") || strings.HasPrefix(repoURL, "ssh://") {
		usr, err := user.Current()
		if err != nil {
			gs.logger.Error(fmt.Sprintf("Failed to get current user: %v", err), "git")
			return nil, err
		}
		sshPath := filepath.Join(usr.HomeDir, ".ssh", "id_rsa")

		publicKeys, err := gitssh.NewPublicKeysFromFile("git", sshPath, "")
		if err != nil {
			gs.logger.Error(fmt.Sprintf("Failed to load SSH keys from %s: %v", sshPath, err), "git")
			return nil, err
		}
		publicKeys.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		return publicKeys, nil
	}

	return nil, errors.New("unsupported repo auth method")
}

// ValidateRepo checks if the Git repo is reachable by attempting a shallow clone without checkout
func (gs *GitService) ValidateRepo(repoURL, username, password string) error {
	gs.logger.Info(fmt.Sprintf("Validating Git repository: %s", repoURL), "git")

	tmpRoot := filepath.Join(gs.appPaths.DataDir, "tmp")

	// Make sure tmpRoot exists
	if err := os.MkdirAll(tmpRoot, 0755); err != nil {
		gs.logger.Error(fmt.Sprintf("Failed to create tmp directory: %v", err), "git")
		return err
	}

	tmpDir, err := os.MkdirTemp(tmpRoot, "gitcheck-*")
	if err != nil {
		gs.logger.Error(fmt.Sprintf("Failed to create temp directory: %v", err), "git")
		return err
	}
	defer os.RemoveAll(tmpDir)

	auth, err := gs.getAuth(repoURL, username, password)
	if err != nil {
		return err
	}

	_, err = git.PlainClone(tmpDir, false, &git.CloneOptions{
		URL:        repoURL,
		Depth:      1,
		NoCheckout: true,
		Auth:       auth,
	})

	if err != nil {
		gs.logger.Error(fmt.Sprintf("Repository validation failed: %v", err), "git")
		return err
	}

	gs.logger.Info(fmt.Sprintf("Repository validated successfully: %s", repoURL), "git")
	return nil
}

// CheckDockerFiles scans a directory for Dockerfiles and docker-compose files
func (gs *GitService) CheckDockerFiles(path string) (DockerFileCheckResult, error) {
	gs.logger.Info(fmt.Sprintf("Scanning for Docker files in: %s", path), "git")

	result := DockerFileCheckResult{}
	skipDirs := make(map[string]bool)

	// Get skip directories from config
	dockerScanSkip := gs.configMgr.GetConfigWithDefault("docker_scan_skip", ".git,packages,node_modules,.idea,.vscode,vendor,dist,build,target")
	skipList := strings.Split(dockerScanSkip, ",")
	for _, skip := range skipList {
		skip = strings.TrimSpace(skip)
		if skip != "" {
			skipDirs[skip] = true
		}
	}

	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip unwanted directories
		if info.IsDir() && skipDirs[info.Name()] {
			return filepath.SkipDir
		}

		switch strings.ToLower(info.Name()) {
		case "dockerfile":
			result.HasDockerfile = true
			result.Dockerfiles = append(result.Dockerfiles, filePath)
			gs.logger.Debug(fmt.Sprintf("Found Dockerfile: %s", filePath), "git")
		case "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml":
			result.HasCompose = true
			result.Composes = append(result.Composes, filePath)
			gs.logger.Debug(fmt.Sprintf("Found compose file: %s", filePath), "git")
		}
		return nil
	})

	if err != nil {
		gs.logger.Error(fmt.Sprintf("Error scanning for Docker files: %v", err), "git")
		return result, err
	}

	gs.logger.Info(fmt.Sprintf("Docker file scan complete - Dockerfiles: %d, Compose files: %d",
		len(result.Dockerfiles), len(result.Composes)), "git")

	return result, nil
}

// getRepoCachePath generates a unique folder name based on repo URL and branch
func (gs *GitService) getRepoCachePath(basePath, repoURL, branch string) string {
	key := repoURL
	if branch != "" {
		key = key + ":" + branch
	}
	hash := sha1.Sum([]byte(key))
	hashStr := hex.EncodeToString(hash[:])[:12] // 12 chars is short but quite unique

	// Extract repo name from URL for readability
	name := strings.TrimSuffix(filepath.Base(repoURL), ".git")
	if name == "" {
		name = "repo"
	}

	return filepath.Join(basePath, name+"-"+hashStr)
}

// CloneOrPull clones a Git repository or pulls latest changes if it already exists
// Returns the path to the cloned repository
func (gs *GitService) CloneOrPull(repoURL, branch, username, password string) (string, error) {
	gs.logger.Info(fmt.Sprintf("Cloning or pulling repository: %s (branch: %s)", repoURL, branch), "git")

	auth, err := gs.getAuth(repoURL, username, password)
	if err != nil {
		return "", err
	}

	cloneOptions := &git.CloneOptions{
		URL:  repoURL,
		Auth: auth,
	}
	if branch != "" {
		cloneOptions.ReferenceName = plumbing.NewBranchReferenceName(branch)
		cloneOptions.SingleBranch = true
	}

	// Generate unique and readable folder name for git contents
	basePath := filepath.Join(gs.appPaths.DataDir, "git_cache")
	if err := os.MkdirAll(basePath, 0755); err != nil {
		gs.logger.Error(fmt.Sprintf("Failed to create git cache directory: %v", err), "git")
		return "", err
	}

	path := gs.getRepoCachePath(basePath, repoURL, branch)

	// Make sure path exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		gs.logger.Error(fmt.Sprintf("Failed to create repository directory: %v", err), "git")
		return "", err
	}

	var repo *git.Repository
	_, err = git.PlainClone(path, false, cloneOptions)
	if err != nil {
		if err == git.ErrRepositoryAlreadyExists {
			gs.logger.Info(fmt.Sprintf("Repository already exists, pulling latest changes: %s", path), "git")
			repo, err = git.PlainOpen(path)
			if err != nil {
				gs.logger.Error(fmt.Sprintf("Failed to open existing repository: %v", err), "git")
				return "", err
			}

			worktree, err := repo.Worktree()
			if err != nil {
				gs.logger.Error(fmt.Sprintf("Failed to get worktree: %v", err), "git")
				return "", err
			}

			err = worktree.Pull(&git.PullOptions{
				RemoteName: "origin",
				Auth:       auth,
			})
			if err != nil && err != git.NoErrAlreadyUpToDate {
				gs.logger.Error(fmt.Sprintf("Failed to pull latest changes: %v", err), "git")
				return "", err
			}

			if err == git.NoErrAlreadyUpToDate {
				gs.logger.Info("Repository already up to date", "git")
			} else {
				gs.logger.Info("Repository updated successfully", "git")
			}
		} else {
			gs.logger.Error(fmt.Sprintf("Failed to clone repository: %v", err), "git")
			return "", err
		}
	} else {
		gs.logger.Info(fmt.Sprintf("Repository cloned successfully to: %s", path), "git")
	}

	return path, nil
}

// GetCurrentCommitHash returns the current commit hash of a repository
func (gs *GitService) GetCurrentCommitHash(repoPath string) (string, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		gs.logger.Error(fmt.Sprintf("Failed to open repository: %v", err), "git")
		return "", err
	}

	ref, err := repo.Head()
	if err != nil {
		gs.logger.Error(fmt.Sprintf("Failed to get HEAD reference: %v", err), "git")
		return "", err
	}

	return ref.Hash().String(), nil
}

// ListCommits retrieves a list of commits from a repository
func (gs *GitService) ListCommits(repoPath string, limit int) ([]*object.Commit, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		gs.logger.Error(fmt.Sprintf("Failed to open repository: %v", err), "git")
		return nil, err
	}

	ref, err := repo.Head()
	if err != nil {
		gs.logger.Error(fmt.Sprintf("Failed to get HEAD reference: %v", err), "git")
		return nil, err
	}

	log, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		gs.logger.Error(fmt.Sprintf("Failed to get commit log: %v", err), "git")
		return nil, err
	}

	var commits []*object.Commit
	count := 0
	err = log.ForEach(func(c *object.Commit) error {
		if limit > 0 && count >= limit {
			return errors.New("limit reached")
		}
		commits = append(commits, c)
		count++
		return nil
	})

	if err != nil && err.Error() != "limit reached" {
		gs.logger.Error(fmt.Sprintf("Failed to iterate commits: %v", err), "git")
		return nil, err
	}

	gs.logger.Info(fmt.Sprintf("Retrieved %d commits from repository", len(commits)), "git")
	return commits, nil
}
