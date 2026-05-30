package device

import "time"

type DeviceType string

const (
	DeviceTypeESP       DeviceType = "esp"
	DeviceTypeAndroid   DeviceType = "android"
	DeviceTypeOrangePi  DeviceType = "orangepi"
	DeviceTypeLinuxNode DeviceType = "linux-node"
	DeviceTypeUnknown   DeviceType = "unknown"
)

type OnlineState string

const (
	OnlineStateOnline  OnlineState = "online"
	OnlineStateStale   OnlineState = "stale"
	OnlineStateOffline OnlineState = "offline"
)

type CommandStatus string

const (
	CommandStatusQueued    CommandStatus = "queued"
	CommandStatusDelivered CommandStatus = "delivered"
	CommandStatusSucceeded CommandStatus = "succeeded"
	CommandStatusFailed    CommandStatus = "failed"
	CommandStatusExpired   CommandStatus = "expired"
)

type Capability struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Schema      map[string]string `json:"schema,omitempty"`
}

type Device struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Type            DeviceType        `json:"type"`
	AgentVersion    string            `json:"agentVersion,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Capabilities    []Capability      `json:"capabilities,omitempty"`
	Metadata        map[string]any    `json:"metadata,omitempty"`
	Disabled        bool              `json:"disabled"`
	TokenIssuedAt   *time.Time        `json:"tokenIssuedAt,omitempty"`
	TokenLastUsedAt *time.Time        `json:"tokenLastUsedAt,omitempty"`
	LastSeenAt      time.Time         `json:"lastSeenAt"`
	RegisteredAt    time.Time         `json:"registeredAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	State           OnlineState       `json:"state"`
}

type DeviceToken struct {
	DeviceID   string     `json:"deviceId"`
	TokenHash  string     `json:"tokenHash"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

type DeviceRegistration struct {
	Device Device `json:"device"`
	Token  string `json:"token,omitempty"`
	Reused bool   `json:"reused"`
}

type DeviceTokenReset struct {
	DeviceID string    `json:"deviceId"`
	Token    string    `json:"token"`
	IssuedAt time.Time `json:"issuedAt"`
}

type TelemetryPoint struct {
	ID        string         `json:"id"`
	DeviceID  string         `json:"deviceId"`
	Key       string         `json:"key"`
	Value     any            `json:"value"`
	Unit      string         `json:"unit,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Command struct {
	ID          string         `json:"id"`
	DeviceID    string         `json:"deviceId"`
	Type        string         `json:"type"`
	Payload     map[string]any `json:"payload,omitempty"`
	Status      CommandStatus  `json:"status"`
	Result      map[string]any `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
	RequestedBy string         `json:"requestedBy,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	DeliveredAt *time.Time     `json:"deliveredAt,omitempty"`
	FinishedAt  *time.Time     `json:"finishedAt,omitempty"`
	ExpiresAt   *time.Time     `json:"expiresAt,omitempty"`
}

type Event struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	DeviceID string         `json:"deviceId,omitempty"`
	Message  string         `json:"message"`
	At       time.Time      `json:"at"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type RegisterDeviceRequest struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         DeviceType        `json:"type"`
	AgentVersion string            `json:"agentVersion,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Capabilities []Capability      `json:"capabilities,omitempty"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
}

type UpdateDeviceStatusRequest struct {
	Disabled bool `json:"disabled"`
}

type HeartbeatRequest struct {
	AgentVersion string            `json:"agentVersion,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
}

type TelemetryRequest struct {
	Key      string         `json:"key"`
	Value    any            `json:"value"`
	Unit     string         `json:"unit,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type CreateCommandRequest struct {
	Type        string         `json:"type"`
	Payload     map[string]any `json:"payload,omitempty"`
	RequestedBy string         `json:"requestedBy,omitempty"`
	TTLSeconds  int            `json:"ttlSeconds,omitempty"`
}

type AckCommandRequest struct {
	Status CommandStatus  `json:"status"`
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}
