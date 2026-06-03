// Package rules is the gateway's automation engine: telemetry/event triggers ->
// command/webhook actions. The matching logic here is pure (no I/O, no store
// access) so it is trivially testable; the device package wires it to live
// telemetry/events and executes the resulting outcomes.
package rules

import "time"

type TriggerType string

const (
	TriggerTelemetry TriggerType = "telemetry"
	TriggerEvent     TriggerType = "event"
)

type ActionType string

const (
	ActionCommand ActionType = "command"
	ActionWebhook ActionType = "webhook"
)

// Trigger decides when a rule fires.
type Trigger struct {
	Type TriggerType `json:"type"`

	// Telemetry trigger: <key> <op> <value> (op: gt|gte|lt|lte|eq|ne).
	Key   string  `json:"key,omitempty"`
	Op    string  `json:"op,omitempty"`
	Value float64 `json:"value,omitempty"`

	// Event trigger: match this event type (empty = any event).
	EventType string `json:"eventType,omitempty"`

	// Optional filters (apply to both kinds).
	DeviceID string `json:"deviceId,omitempty"`
	Category string `json:"category,omitempty"`
}

// Action is what a rule does when it fires.
type Action struct {
	Type ActionType `json:"type"`

	// Command action: target the triggering device by default, or a specific
	// device, or broadcast to a whole category.
	TargetDeviceID string         `json:"targetDeviceId,omitempty"`
	TargetCategory string         `json:"targetCategory,omitempty"`
	CommandType    string         `json:"commandType,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`

	// Webhook action.
	WebhookURL string `json:"webhookUrl,omitempty"`
}

// Rule is a stored automation rule.
type Rule struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Trigger   Trigger   `json:"trigger"`
	Action    Action    `json:"action"`
	CreatedAt time.Time `json:"createdAt"`
}

// Context describes a telemetry point or event being evaluated.
type Context struct {
	Kind      TriggerType
	DeviceID  string
	Category  string
	Key       string  // telemetry
	Value     float64 // telemetry
	EventType string  // event
}

// Outcome is a resolved action to execute (target device defaulted when empty).
type Outcome struct {
	RuleID   string
	RuleName string
	Action   Action
}

// Evaluate returns the outcomes for all enabled rules matching ctx.
func Evaluate(rs []Rule, ctx Context) []Outcome {
	var out []Outcome
	for _, r := range rs {
		if !r.Enabled || !matches(r.Trigger, ctx) {
			continue
		}
		act := r.Action
		if act.Type == ActionCommand && act.TargetDeviceID == "" && act.TargetCategory == "" {
			act.TargetDeviceID = ctx.DeviceID // default to the triggering device
		}
		out = append(out, Outcome{RuleID: r.ID, RuleName: r.Name, Action: act})
	}
	return out
}

func matches(t Trigger, ctx Context) bool {
	if t.Type != ctx.Kind {
		return false
	}
	if t.DeviceID != "" && t.DeviceID != ctx.DeviceID {
		return false
	}
	if t.Category != "" && t.Category != ctx.Category {
		return false
	}
	switch t.Type {
	case TriggerTelemetry:
		return t.Key == ctx.Key && compare(ctx.Value, t.Op, t.Value)
	case TriggerEvent:
		return t.EventType == "" || t.EventType == ctx.EventType
	}
	return false
}

func compare(a float64, op string, b float64) bool {
	switch op {
	case "gt":
		return a > b
	case "gte":
		return a >= b
	case "lt":
		return a < b
	case "lte":
		return a <= b
	case "eq":
		return a == b
	case "ne":
		return a != b
	}
	return false
}
