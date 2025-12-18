package database

import (
	"database/sql"
	"time"
)

// DockerServiceDetails represents details about a Docker service
type DockerServiceDetails struct {
	ID               int64     `json:"id"`
	ServiceID        int64     `json:"service_id"`
	ImageName        string    `json:"image_name"`
	ImageTag         string    `json:"image_tag"`
	DockerfilePath   string    `json:"dockerfile_path"`
	ComposePath      string    `json:"compose_path"`       // Path to docker-compose.yml if applicable
	Source           string    `json:"source"`             // "registry", "git", "local"
	GitRepoURL       string    `json:"git_repo_url"`       // For git sources
	GitCommitHash    string    `json:"git_commit_hash"`    // For reproducibility
	LocalContextPath string    `json:"local_context_path"` // For local builds
	Entrypoint       string    `json:"entrypoint"`         // JSON array of entrypoint strings
	Cmd              string    `json:"cmd"`                // JSON array of cmd strings
	CreatedAt        time.Time `json:"created_at"`
}

// AddDockerServiceDetails adds Docker service details to the database
func (sm *SQLiteManager) AddDockerServiceDetails(details *DockerServiceDetails) error {
	query := `
		INSERT INTO docker_service_details (
			service_id, image_name, image_tag, dockerfile_path, compose_path,
			source, git_repo_url, git_commit_hash, local_context_path,
			entrypoint, cmd
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := sm.db.Exec(
		query,
		details.ServiceID,
		details.ImageName,
		details.ImageTag,
		details.DockerfilePath,
		details.ComposePath,
		details.Source,
		details.GitRepoURL,
		details.GitCommitHash,
		details.LocalContextPath,
		details.Entrypoint,
		details.Cmd,
	)

	if err != nil {
		sm.logger.Error("Failed to add docker service details", "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	details.ID = id
	details.CreatedAt = time.Now()

	sm.logger.Info("Docker service details added successfully", "database")
	return nil
}

// GetDockerServiceDetails retrieves Docker service details by service ID
func (sm *SQLiteManager) GetDockerServiceDetails(serviceID int64) (*DockerServiceDetails, error) {
	query := `
		SELECT id, service_id, image_name, image_tag, dockerfile_path, compose_path,
		       source, git_repo_url, git_commit_hash, local_context_path,
		       entrypoint, cmd, created_at
		FROM docker_service_details
		WHERE service_id = ?
	`

	return QueryRowSingle(sm.db, query,
		func(row *sql.Row) (*DockerServiceDetails, error) {
			var details DockerServiceDetails
			err := row.Scan(
				&details.ID,
				&details.ServiceID,
				&details.ImageName,
				&details.ImageTag,
				&details.DockerfilePath,
				&details.ComposePath,
				&details.Source,
				&details.GitRepoURL,
				&details.GitCommitHash,
				&details.LocalContextPath,
				&details.Entrypoint,
				&details.Cmd,
				&details.CreatedAt,
			)
			return &details, err
		},
		sm.logger, "database", serviceID)
}

// GetAllDockerServiceDetails retrieves all Docker service details
func (sm *SQLiteManager) GetAllDockerServiceDetails() ([]*DockerServiceDetails, error) {
	query := `
		SELECT id, service_id, image_name, image_tag, dockerfile_path, compose_path,
		       source, git_repo_url, git_commit_hash, local_context_path,
		       entrypoint, cmd, created_at
		FROM docker_service_details
		ORDER BY created_at DESC
	`

	return QueryRows(sm.db, query,
		func(rows *sql.Rows) (*DockerServiceDetails, error) {
			var details DockerServiceDetails
			err := rows.Scan(
				&details.ID,
				&details.ServiceID,
				&details.ImageName,
				&details.ImageTag,
				&details.DockerfilePath,
				&details.ComposePath,
				&details.Source,
				&details.GitRepoURL,
				&details.GitCommitHash,
				&details.LocalContextPath,
				&details.Entrypoint,
				&details.Cmd,
				&details.CreatedAt,
			)
			return &details, err
		},
		sm.logger, "database")
}

// GetDockerServiceDetailsBySource retrieves Docker service details filtered by source type
func (sm *SQLiteManager) GetDockerServiceDetailsBySource(source string) ([]*DockerServiceDetails, error) {
	query := `
		SELECT id, service_id, image_name, image_tag, dockerfile_path, compose_path,
		       source, git_repo_url, git_commit_hash, local_context_path,
		       entrypoint, cmd, created_at
		FROM docker_service_details
		WHERE source = ?
		ORDER BY created_at DESC
	`

	return QueryRows(sm.db, query,
		func(rows *sql.Rows) (*DockerServiceDetails, error) {
			var details DockerServiceDetails
			err := rows.Scan(
				&details.ID,
				&details.ServiceID,
				&details.ImageName,
				&details.ImageTag,
				&details.DockerfilePath,
				&details.ComposePath,
				&details.Source,
				&details.GitRepoURL,
				&details.GitCommitHash,
				&details.LocalContextPath,
				&details.Entrypoint,
				&details.Cmd,
				&details.CreatedAt,
			)
			return &details, err
		},
		sm.logger, "database", source)
}

// UpdateDockerServiceDetails updates Docker service details
func (sm *SQLiteManager) UpdateDockerServiceDetails(details *DockerServiceDetails) error {
	query := `
		UPDATE docker_service_details
		SET image_name = ?, image_tag = ?, dockerfile_path = ?, compose_path = ?,
		    source = ?, git_repo_url = ?, git_commit_hash = ?, local_context_path = ?,
		    entrypoint = ?, cmd = ?
		WHERE id = ?
	`

	_, err := ExecWithAffectedRowsCheck(
		sm.db,
		query,
		sm.logger,
		"database",
		details.ImageName,
		details.ImageTag,
		details.DockerfilePath,
		details.ComposePath,
		details.Source,
		details.GitRepoURL,
		details.GitCommitHash,
		details.LocalContextPath,
		details.Entrypoint,
		details.Cmd,
		details.ID,
	)

	if err != nil {
		return err
	}

	sm.logger.Info("Docker service details updated successfully", "database")
	return nil
}

// DeleteDockerServiceDetails deletes Docker service details by service ID
func (sm *SQLiteManager) DeleteDockerServiceDetails(serviceID int64) error {
	query := `DELETE FROM docker_service_details WHERE service_id = ?`

	_, err := ExecWithAffectedRowsCheck(
		sm.db,
		query,
		sm.logger,
		"database",
		serviceID,
	)

	if err != nil {
		return err
	}

	sm.logger.Info("Docker service details deleted successfully", "database")
	return nil
}
