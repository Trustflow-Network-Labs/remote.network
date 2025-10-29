package services

import (
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
)

// ServiceCounts represents the count of different service types
type ServiceCounts struct {
	FilesCount int // Count of ACTIVE DATA services
	AppsCount  int // Count of ACTIVE DOCKER + STANDALONE services
}

// CountLocalServices counts ACTIVE services from the database
// Only ACTIVE services are counted in the metadata
// DATA services = files_count
// DOCKER + STANDALONE services = apps_count
func CountLocalServices(dbManager *database.SQLiteManager) (*ServiceCounts, error) {
	services, err := dbManager.GetAllServices()
	if err != nil {
		return nil, err
	}

	counts := &ServiceCounts{
		FilesCount: 0,
		AppsCount:  0,
	}

	for _, svc := range services {
		// Only count ACTIVE services
		if svc.Status != "ACTIVE" {
			continue
		}

		switch svc.ServiceType {
		case "DATA":
			counts.FilesCount++
		case "DOCKER", "STANDALONE":
			counts.AppsCount++
		}
	}

	return counts, nil
}
