package p2p

import (
	"fmt"
	"strings"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// ServiceQueryHandler handles incoming service search queries via QUIC
type ServiceQueryHandler struct {
	dbManager *database.SQLiteManager
	logger    *utils.LogsManager
}

// NewServiceQueryHandler creates a new service query handler
func NewServiceQueryHandler(
	dbManager *database.SQLiteManager,
	logger *utils.LogsManager,
) *ServiceQueryHandler {
	return &ServiceQueryHandler{
		dbManager: dbManager,
		logger:    logger,
	}
}

// ServiceSearchRequest represents a service search query
type ServiceSearchRequest struct {
	Phrases      string `json:"phrases"`        // Comma-separated search terms
	ServiceType  string `json:"service_type"`   // Comma-separated types: "DATA", "DOCKER", "STANDALONE"
	ActiveOnly   bool   `json:"active_only"`    // Only return ACTIVE services
	SourcePeerID string `json:"source_peer_id"` // Peer ID of the requester (for relay response routing)
}

// ServiceSearchResult represents a single service in search results
type ServiceSearchResult struct {
	ID             int64                  `json:"id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	ServiceType    string                 `json:"service_type"`
	Type           string                 `json:"type"`
	Status         string                 `json:"status"`
	PricingAmount  float64                `json:"pricing_amount"`
	PricingType    string                 `json:"pricing_type"`
	PricingInterval int                   `json:"pricing_interval"`
	PricingUnit    string                 `json:"pricing_unit"`
	Capabilities   map[string]interface{} `json:"capabilities,omitempty"`
	Hash           string                 `json:"hash,omitempty"`      // For DATA services
	SizeBytes      int64                  `json:"size_bytes,omitempty"` // For DATA services
	Interfaces     []ServiceInterface     `json:"interfaces,omitempty"` // Service interfaces (STDIN, STDOUT, MOUNT)
}

// ServiceInterface represents a service interface for remote service info
type ServiceInterface struct {
	InterfaceType string `json:"interface_type"` // STDIN, STDOUT, MOUNT
	Path          string `json:"path,omitempty"`
}

// ServiceSearchResponse represents the response to a service search query
type ServiceSearchResponse struct {
	Services []*ServiceSearchResult `json:"services"`
	Count    int                    `json:"count"`
	Error    string                 `json:"error,omitempty"`
}

// HandleServiceSearchRequest processes a service search request and returns matching services
func (sqh *ServiceQueryHandler) HandleServiceSearchRequest(msg *QUICMessage, remoteAddr string) *QUICMessage {
	sqh.logger.Debug(fmt.Sprintf("Handling service search request from %s", remoteAddr), "service-query")

	// Parse request data
	var request ServiceSearchRequest
	if err := msg.GetDataAs(&request); err != nil {
		sqh.logger.Error(fmt.Sprintf("Failed to parse service search request from %s: %v", remoteAddr, err), "service-query")
		return CreateServiceSearchResponse(nil, fmt.Sprintf("invalid request: %v", err))
	}

	sqh.logger.Info(fmt.Sprintf("Service search request from %s: phrases=%q, type=%q, activeOnly=%v",
		remoteAddr, request.Phrases, request.ServiceType, request.ActiveOnly), "service-query")

	// Get all services from database
	allServices, err := sqh.dbManager.GetAllServices()
	if err != nil {
		sqh.logger.Error(fmt.Sprintf("Failed to retrieve services: %v", err), "service-query")
		return CreateServiceSearchResponse(nil, fmt.Sprintf("database error: %v", err))
	}

	// Filter services based on search criteria
	matchingServices := sqh.filterServices(allServices, &request)

	// Get additional details for DATA services (hash, size)
	results := make([]*ServiceSearchResult, 0, len(matchingServices))
	for _, service := range matchingServices {
		result := &ServiceSearchResult{
			ID:              service.ID,
			Name:            service.Name,
			Description:     service.Description,
			ServiceType:     service.ServiceType,
			Type:            service.Type,
			Status:          service.Status,
			PricingAmount:   service.PricingAmount,
			PricingType:     service.PricingType,
			PricingInterval: service.PricingInterval,
			PricingUnit:     service.PricingUnit,
			Capabilities:    service.Capabilities,
		}

		// For DATA services, include hash and size
		if service.ServiceType == "DATA" {
			details, err := sqh.dbManager.GetDataServiceDetails(service.ID)
			if err == nil && details != nil {
				result.Hash = details.Hash
				result.SizeBytes = details.SizeBytes
			}
		}

		// Get service interfaces (STDIN, STDOUT, MOUNT)
		interfaces, err := sqh.dbManager.GetServiceInterfaces(service.ID)
		if err == nil && len(interfaces) > 0 {
			result.Interfaces = make([]ServiceInterface, 0, len(interfaces))
			for _, iface := range interfaces {
				result.Interfaces = append(result.Interfaces, ServiceInterface{
					InterfaceType: iface.InterfaceType,
					Path:          iface.Path,
				})
			}
		}

		results = append(results, result)
	}

	sqh.logger.Info(fmt.Sprintf("Service search from %s: found %d matching services", remoteAddr, len(results)), "service-query")

	return CreateServiceSearchResponse(results, "")
}

// filterServices filters services based on search criteria
func (sqh *ServiceQueryHandler) filterServices(services []*database.OfferedService, request *ServiceSearchRequest) []*database.OfferedService {
	var filtered []*database.OfferedService

	// Parse search phrases
	var searchTerms []string
	if request.Phrases != "" {
		searchTerms = strings.Split(strings.ToLower(request.Phrases), ",")
		for i := range searchTerms {
			searchTerms[i] = strings.TrimSpace(searchTerms[i])
		}
	}

	// Parse service types
	var serviceTypes []string
	if request.ServiceType != "" {
		serviceTypes = strings.Split(strings.ToUpper(request.ServiceType), ",")
		for i := range serviceTypes {
			serviceTypes[i] = strings.TrimSpace(serviceTypes[i])
		}
	}

	for _, service := range services {
		// Filter by active status
		if request.ActiveOnly && service.Status != "ACTIVE" {
			continue
		}

		// Filter by service type
		if len(serviceTypes) > 0 {
			typeMatch := false
			for _, sType := range serviceTypes {
				if service.ServiceType == sType {
					typeMatch = true
					break
				}
			}
			if !typeMatch {
				continue
			}
		}

		// Filter by search phrases (match in name or description)
		if len(searchTerms) > 0 {
			nameDesc := strings.ToLower(service.Name + " " + service.Description)
			phraseMatch := false
			for _, term := range searchTerms {
				if term != "" && strings.Contains(nameDesc, term) {
					phraseMatch = true
					break
				}
			}
			if !phraseMatch {
				continue
			}
		}

		// Service passed all filters
		filtered = append(filtered, service)
	}

	return filtered
}

// CreateServiceSearchRequest creates a service search request message
func CreateServiceSearchRequest(phrases string, serviceType string, activeOnly bool) *QUICMessage {
	return &QUICMessage{
		Type:      MessageTypeServiceRequest,
		Version:   1,
		Timestamp: time.Now(),
		Data: ServiceSearchRequest{
			Phrases:     phrases,
			ServiceType: serviceType,
			ActiveOnly:  activeOnly,
		},
	}
}

// CreateServiceSearchResponse creates a service search response message
func CreateServiceSearchResponse(services []*ServiceSearchResult, errorMsg string) *QUICMessage {
	response := ServiceSearchResponse{
		Services: services,
		Count:    len(services),
		Error:    errorMsg,
	}

	if services == nil {
		response.Services = []*ServiceSearchResult{}
		response.Count = 0
	}

	return &QUICMessage{
		Type:      MessageTypeServiceResponse,
		Version:   1,
		Timestamp: time.Now(),
		Data:      response,
	}
}
