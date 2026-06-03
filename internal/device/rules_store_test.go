package device

import (
	"testing"

	"light-gateway/internal/rules"
)

func TestRuleFiresCommandToCategory(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := store.Register(RegisterDeviceRequest{ID: "lamp", Name: "Lamp", Type: DeviceTypeESP, Category: CategoryLight}); err != nil {
		t.Fatalf("register: %v", err)
	}

	rule, err := store.AddRule(rules.Rule{
		Name:    "hot->light",
		Enabled: true,
		Trigger: rules.Trigger{Type: rules.TriggerTelemetry, Key: "env.temp", Op: "gt", Value: 30},
		Action:  rules.Action{Type: rules.ActionCommand, TargetCategory: "light", CommandType: "light.power", Payload: map[string]any{"on": true}},
	})
	if err != nil {
		t.Fatalf("add rule: %v", err)
	}
	if store.enabledRules != 1 {
		t.Fatalf("expected 1 enabled rule, got %d", store.enabledRules)
	}

	// Below threshold: no command.
	store.fireRules(rules.Context{Kind: rules.TriggerTelemetry, DeviceID: "lamp", Category: "light", Key: "env.temp", Value: 20})
	if cmds, _ := store.ListCommands("lamp", 10); len(cmds) != 0 {
		t.Fatalf("expected no command below threshold, got %+v", cmds)
	}

	// Above threshold: a light.power command is created on the light device.
	store.fireRules(rules.Context{Kind: rules.TriggerTelemetry, DeviceID: "lamp", Category: "light", Key: "env.temp", Value: 35})
	cmds, _ := store.ListCommands("lamp", 10)
	found := false
	for _, c := range cmds {
		if c.Type == "light.power" && c.RequestedBy == "rule:"+rule.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected light.power command from rule, got %+v", cmds)
	}
}

func TestRuleEnableDisableAndDelete(t *testing.T) {
	store, _ := NewStore("")
	r, err := store.AddRule(rules.Rule{
		Name:    "evt",
		Enabled: true,
		Trigger: rules.Trigger{Type: rules.TriggerEvent, EventType: "geofence.exit"},
		Action:  rules.Action{Type: rules.ActionWebhook, WebhookURL: "https://example.com/hook"},
	})
	if err != nil {
		t.Fatalf("add rule: %v", err)
	}
	if store.enabledRules != 1 {
		t.Fatalf("expected 1 enabled")
	}
	if _, err := store.SetRuleEnabled(r.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if store.enabledRules != 0 {
		t.Fatalf("expected 0 enabled after disable")
	}
	if err := store.DeleteRule(r.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(store.ListRules()) != 0 {
		t.Fatalf("expected no rules after delete")
	}
}

func TestRuleEventDenylist(t *testing.T) {
	for _, blocked := range []string{"telemetry.received", "rule.fired", "command.created", "device.heartbeat"} {
		if ruleEventAllowed(blocked) {
			t.Fatalf("expected %q to be denied for rule evaluation", blocked)
		}
	}
	if !ruleEventAllowed("geofence.exit") {
		t.Fatalf("expected geofence.exit to be allowed")
	}
}
