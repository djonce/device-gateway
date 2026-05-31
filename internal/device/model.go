package device

import "time"

// DeviceType is the access class of a device. It decides how the device
// authenticates and which transport/channel it uses, NOT what product it is.
type DeviceType string

const (
	DeviceTypeESP       DeviceType = "esp"
	DeviceTypeAndroid   DeviceType = "android"
	DeviceTypeOrangePi  DeviceType = "orangepi"
	DeviceTypeLinuxNode DeviceType = "linux-node"
	DeviceTypeUnknown   DeviceType = "unknown"
)

// DeviceCategory is the product class of a device. It decides the capability
// profile (which commands/telemetry are valid) and how the console renders it.
// This is the v2 productization axis layered on top of DeviceType.
type DeviceCategory string

const (
	CategoryGeneric DeviceCategory = ""      // legacy / unclassified devices
	CategoryLight   DeviceCategory = "light" // 灯带 / 小夜灯
	CategoryClock   DeviceCategory = "clock" // 时钟 / 日历 / 天气屏
	CategoryGPS     DeviceCategory = "gps"   // GPS 定位
	CategoryVoice   DeviceCategory = "voice" // 小智 / 语音助手
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

// Geofence is a circular geofence stored server-side so the gateway can detect
// enter/exit transitions from incoming gps.fix telemetry.
type Geofence struct {
	CenterLat float64 `json:"centerLat"`
	CenterLng float64 `json:"centerLng"`
	RadiusM   float64 `json:"radiusM"`
}

// TrackPoint is one position sample, derived from gps.fix telemetry.
type TrackPoint struct {
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Speed     float64   `json:"speed,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// SetGeofenceRequest configures (or clears, with radiusM<=0) a device geofence.
type SetGeofenceRequest struct {
	CenterLat float64 `json:"centerLat"`
	CenterLng float64 `json:"centerLng"`
	RadiusM   float64 `json:"radiusM"`
}

// Firmware is an uploaded firmware artifact for a product category (+ optional
// model). The binary blob is stored alongside this metadata.
type Firmware struct {
	ID        string         `json:"id"`
	Category  DeviceCategory `json:"category"`
	Model     string         `json:"model,omitempty"`
	Version   string         `json:"version"`
	Size      int64          `json:"size"`
	SHA256    string         `json:"sha256"`
	Notes     string         `json:"notes,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

// AddFirmwareRequest carries firmware metadata (the binary comes in the body).
type AddFirmwareRequest struct {
	Category DeviceCategory
	Model    string
	Version  string
	Notes    string
}

// SetTargetRequest sets a device's desired firmware version.
type SetTargetRequest struct {
	Version string `json:"version"`
}

// OTAStatus is what a device polls to learn whether an update is available.
type OTAStatus struct {
	UpdateAvailable bool   `json:"updateAvailable"`
	CurrentVersion  string `json:"currentVersion"`
	TargetVersion   string `json:"targetVersion,omitempty"`
	FirmwareID      string `json:"firmwareId,omitempty"`
	Version         string `json:"version,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
	Size            int64  `json:"size,omitempty"`
	DownloadURL     string `json:"downloadUrl,omitempty"`
}

type Device struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Type            DeviceType        `json:"type"`
	Category        DeviceCategory    `json:"category,omitempty"`
	Model           string            `json:"model,omitempty"`
	Profile         string            `json:"profile,omitempty"`
	FwVersion       string            `json:"fwVersion,omitempty"`
	Geofence        *Geofence         `json:"geofence,omitempty"`
	GeofenceState   string            `json:"geofenceState,omitempty"` // inside | outside | "" (unknown)
	TargetFwVersion string            `json:"targetFwVersion,omitempty"`
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
	Category     DeviceCategory    `json:"category,omitempty"`
	Model        string            `json:"model,omitempty"`
	Profile      string            `json:"profile,omitempty"`
	FwVersion    string            `json:"fwVersion,omitempty"`
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
