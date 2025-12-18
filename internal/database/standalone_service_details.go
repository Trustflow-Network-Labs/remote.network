package database

import (
	"database/sql"
)

// StandaloneServiceDetails represents details for a standalone service
type StandaloneServiceDetails struct {
	ID                   int64             `json:"id"`
	ServiceID            int64             `json:"service_id"`
	ExecutablePath       string            `json:"executable_path"`
	Arguments            string            `json:"arguments"`             // JSON array
	WorkingDirectory     string            `json:"working_directory"`
	EnvironmentVariables string            `json:"environment_variables"` // JSON map
	TimeoutSeconds       int               `json:"timeout_seconds"`
	RunAsUser            string            `json:"run_as_user"`
	Source               string            `json:"source"` // 'local', 'upload', 'git'
	GitRepoURL           string            `json:"git_repo_url"`
	GitCommitHash        string            `json:"git_commit_hash"`
	UploadHash           string            `json:"upload_hash"`
}

// AddStandaloneServiceDetails adds standalone service details
func (sm *SQLiteManager) AddStandaloneServiceDetails(details *StandaloneServiceDetails) error {
	query := `
		INSERT INTO standalone_service_details (
			service_id, executable_path, arguments, working_directory,
			environment_variables, timeout_seconds, run_as_user, source,
			git_repo_url, git_commit_hash, upload_hash
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := sm.db.Exec(
		query,
		details.ServiceID,
		details.ExecutablePath,
		details.Arguments,
		details.WorkingDirectory,
		details.EnvironmentVariables,
		details.TimeoutSeconds,
		details.RunAsUser,
		details.Source,
		details.GitRepoURL,
		details.GitCommitHash,
		details.UploadHash,
	)

	if err != nil {
		sm.logger.Error("Failed to add standalone service details", "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	details.ID = id
	sm.logger.Info("Standalone service details added successfully", "database")
	return nil
}

// GetStandaloneServiceDetails retrieves standalone service details by service ID
func (sm *SQLiteManager) GetStandaloneServiceDetails(serviceID int64) (*StandaloneServiceDetails, error) {
	query := `
		SELECT id, service_id, executable_path, arguments, working_directory,
		       environment_variables, timeout_seconds, run_as_user, source,
		       git_repo_url, git_commit_hash, upload_hash
		FROM standalone_service_details
		WHERE service_id = ?
	`

	return QueryRowSingle(sm.db, query,
		func(row *sql.Row) (*StandaloneServiceDetails, error) {
			var details StandaloneServiceDetails
			var arguments, workingDir, envVars, runAsUser, gitRepoURL, gitCommitHash, uploadHash sql.NullString

			err := row.Scan(
				&details.ID,
				&details.ServiceID,
				&details.ExecutablePath,
				&arguments,
				&workingDir,
				&envVars,
				&details.TimeoutSeconds,
				&runAsUser,
				&details.Source,
				&gitRepoURL,
				&gitCommitHash,
				&uploadHash,
			)

			if err != nil {
				return nil, err
			}

			// Handle nullable fields
			details.Arguments = ScanNullableString(arguments)
			details.WorkingDirectory = ScanNullableString(workingDir)
			details.EnvironmentVariables = ScanNullableString(envVars)
			details.RunAsUser = ScanNullableString(runAsUser)
			details.GitRepoURL = ScanNullableString(gitRepoURL)
			details.GitCommitHash = ScanNullableString(gitCommitHash)
			details.UploadHash = ScanNullableString(uploadHash)

			return &details, nil
		},
		sm.logger, "database", serviceID)
}

// UpdateStandaloneServiceDetails updates standalone service details
func (sm *SQLiteManager) UpdateStandaloneServiceDetails(details *StandaloneServiceDetails) error {
	query := `
		UPDATE standalone_service_details
		SET executable_path = ?, arguments = ?, working_directory = ?,
		    environment_variables = ?, timeout_seconds = ?, run_as_user = ?,
		    source = ?, git_repo_url = ?, git_commit_hash = ?, upload_hash = ?
		WHERE service_id = ?
	`

	_, err := ExecWithAffectedRowsCheck(
		sm.db,
		query,
		sm.logger,
		"database",
		details.ExecutablePath,
		details.Arguments,
		details.WorkingDirectory,
		details.EnvironmentVariables,
		details.TimeoutSeconds,
		details.RunAsUser,
		details.Source,
		details.GitRepoURL,
		details.GitCommitHash,
		details.UploadHash,
		details.ServiceID,
	)

	if err != nil {
		return err
	}

	sm.logger.Info("Standalone service details updated successfully", "database")
	return nil
}

// DeleteStandaloneServiceDetails deletes standalone service details by service ID
func (sm *SQLiteManager) DeleteStandaloneServiceDetails(serviceID int64) error {
	query := `DELETE FROM standalone_service_details WHERE service_id = ?`

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

	sm.logger.Info("Standalone service details deleted successfully", "database")
	return nil
}
