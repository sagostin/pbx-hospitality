package pbx

import (
	"fmt"
)

// ProviderConfig holds the configuration needed to create a PBX provider.
// The specific fields used depend on the provider type.
type ProviderConfig struct {
	// Provider type: "bicom" (default), "zultys", "freeswitch", etc.
	Type string

	// Bicom-specific settings
	BicomAPIURL   string
	BicomAPIKey   string
	BicomTenantID string

	// ARI settings (for Asterisk-based PBXs)
	ARIURL     string
	ARIWSUrl   string
	ARIUser    string
	ARIPass    string
	ARIAppName string

	// Zultys-specific settings (session-based auth)
	APIURL        string // Base API URL
	AuthURL       string // Login endpoint for session token
	Username      string
	Password      string
	WebhookSecret string // Secret for validating inbound webhooks
}

// providerFactory is a function that creates a Provider from config
type providerFactory func(cfg ProviderConfig) (Provider, error)

// registry holds the registered provider factories
var registry = make(map[string]providerFactory)

// Register adds a provider factory to the registry.
// Called by provider packages in their init() functions.
func Register(providerType string, factory providerFactory) {
	registry[providerType] = factory
}

// NewProvider creates a Provider based on the configuration type.
// Returns an error if the provider type is unknown.
func NewProvider(cfg ProviderConfig) (Provider, error) {
	providerType := cfg.Type
	if providerType == "" {
		providerType = "bicom" // Default to Bicom for backward compatibility
	}

	factory, ok := registry[providerType]
	if !ok {
		return nil, fmt.Errorf("unknown PBX provider type: %s", providerType)
	}

	return factory(cfg)
}

// ListProviders returns a list of registered provider types.
func ListProviders() []string {
	types := make([]string, 0, len(registry))
	for t := range registry {
		types = append(types, t)
	}
	return types
}
