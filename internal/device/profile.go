package device

// Capability profiles ("能力档案") turn the v1 free-form capability strings into
// a per-product contract: which command types are valid, what payload they take,
// and which telemetry keys the device reports. A device opts in by registering a
// Category (or an explicit Profile id). Legacy devices with no profile keep the
// old "anything goes" behavior, so this is fully backward compatible.

// CommandSpec describes one command a profile accepts. PayloadKeys is advisory
// metadata the console uses to render a control panel; the gateway only enforces
// the command Type today (payload schema enforcement is a later phase).
type CommandSpec struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	PayloadKeys []string `json:"payloadKeys,omitempty"`
}

// TelemetrySpec documents an expected telemetry key for a profile.
type TelemetrySpec struct {
	Key         string `json:"key"`
	Unit        string `json:"unit,omitempty"`
	Description string `json:"description,omitempty"`
}

// Profile is a capability档案 bound to a product category.
type Profile struct {
	ID        string          `json:"id"`
	Category  DeviceCategory  `json:"category"`
	Title     string          `json:"title"`
	RealTime  bool            `json:"realTime,omitempty"` // needs a real-time channel (e.g. voice)
	Commands  []CommandSpec   `json:"commands"`
	Telemetry []TelemetrySpec `json:"telemetry"`
}

// AllowsCommand reports whether type t is part of this profile's contract.
func (p Profile) AllowsCommand(t string) bool {
	for _, c := range p.Commands {
		if c.Type == t {
			return true
		}
	}
	return false
}

var builtinProfiles = []Profile{
	{
		ID:       "light.v1",
		Category: CategoryLight,
		Title:    "灯带 / 小夜灯",
		Commands: []CommandSpec{
			{Type: "light.power", Description: "开关", PayloadKeys: []string{"on"}},
			{Type: "light.brightness", Description: "亮度 0-100", PayloadKeys: []string{"value"}},
			{Type: "light.color", Description: "颜色", PayloadKeys: []string{"r", "g", "b", "hex"}},
			{Type: "light.effect", Description: "灯效", PayloadKeys: []string{"name", "speed"}},
			{Type: "light.schedule", Description: "定时小夜灯", PayloadKeys: []string{"on", "off"}},
		},
		Telemetry: []TelemetrySpec{
			{Key: "light.state", Description: "开关状态"},
			{Key: "light.brightness", Description: "当前亮度"},
			{Key: "power.mw", Unit: "mW", Description: "功耗(可选)"},
		},
	},
	{
		ID:       "clock.v1",
		Category: CategoryClock,
		Title:    "时钟 / 日历 / 天气屏",
		Commands: []CommandSpec{
			{Type: "display.mode", Description: "显示模式", PayloadKeys: []string{"mode"}},
			{Type: "display.brightness", Description: "屏幕亮度", PayloadKeys: []string{"value"}},
			{Type: "time.sync", Description: "校时", PayloadKeys: []string{"epoch", "tz"}},
			{Type: "weather.push", Description: "下发天气", PayloadKeys: []string{"city", "temp", "cond", "forecast"}},
		},
		Telemetry: []TelemetrySpec{
			{Key: "env.temp", Unit: "celsius", Description: "环境温度(可选)"},
			{Key: "env.humidity", Unit: "percent", Description: "环境湿度(可选)"},
			{Key: "display.mode", Description: "当前显示模式"},
		},
	},
	{
		ID:       "gps.v1",
		Category: CategoryGPS,
		Title:    "GPS 定位",
		Commands: []CommandSpec{
			{Type: "gps.interval", Description: "上报频率(秒)", PayloadKeys: []string{"seconds"}},
			{Type: "geofence.set", Description: "设置地理围栏", PayloadKeys: []string{"center", "radius_m"}},
		},
		Telemetry: []TelemetrySpec{
			{Key: "gps.fix", Description: "定位点 {lat,lng,alt,speed,sats}"},
		},
	},
	{
		ID:       "voice.v1",
		Category: CategoryVoice,
		Title:    "小智 / 语音助手",
		RealTime: true,
		Commands: []CommandSpec{
			{Type: "voice.say", Description: "TTS 播报", PayloadKeys: []string{"text"}},
			{Type: "voice.wake", Description: "唤醒开关", PayloadKeys: []string{"enabled"}},
		},
		Telemetry: []TelemetrySpec{
			{Key: "voice.session", Description: "会话事件(占位，实时音频走独立通道)"},
		},
	},
}

var (
	profilesByID       = map[string]Profile{}
	defaultProfileByID = map[DeviceCategory]Profile{}
)

func init() {
	for _, p := range builtinProfiles {
		profilesByID[p.ID] = p
		// First profile registered for a category is its default.
		if _, ok := defaultProfileByID[p.Category]; !ok {
			defaultProfileByID[p.Category] = p
		}
	}
}

// Profiles returns all built-in profiles (for the management console).
func Profiles() []Profile {
	out := make([]Profile, len(builtinProfiles))
	copy(out, builtinProfiles)
	return out
}

// ProfileByID looks up a profile by its id (e.g. "light.v1").
func ProfileByID(id string) (Profile, bool) {
	p, ok := profilesByID[id]
	return p, ok
}

// DefaultProfileForCategory returns the default profile bound to a category.
func DefaultProfileForCategory(c DeviceCategory) (Profile, bool) {
	p, ok := defaultProfileByID[c]
	return p, ok
}

// resolveProfileID picks the effective profile id for a device: an explicit
// profile id wins; otherwise the category's default; otherwise "" (legacy).
func resolveProfileID(category DeviceCategory, profileID string) string {
	if profileID != "" {
		if _, ok := profilesByID[profileID]; ok {
			return profileID
		}
		return profileID // unknown id is kept as-is but won't enforce a contract
	}
	if p, ok := defaultProfileByID[category]; ok {
		return p.ID
	}
	return ""
}

// profileForDevice resolves the enforceable profile for a device, if any.
func profileForDevice(d Device) (Profile, bool) {
	if d.Profile != "" {
		if p, ok := profilesByID[d.Profile]; ok {
			return p, true
		}
	}
	if p, ok := defaultProfileByID[d.Category]; ok {
		return p, true
	}
	return Profile{}, false
}
