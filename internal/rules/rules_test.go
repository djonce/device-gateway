package rules

import "testing"

func tempRule(op string, value float64) Rule {
	return Rule{
		ID: "r1", Name: "hot", Enabled: true,
		Trigger: Trigger{Type: TriggerTelemetry, Key: "env.temp", Op: op, Value: value},
		Action:  Action{Type: ActionCommand, CommandType: "light.power", Payload: map[string]any{"on": true}},
	}
}

func TestTelemetryThresholdOps(t *testing.T) {
	cases := []struct {
		op   string
		val  float64
		read float64
		fire bool
	}{
		{"gt", 30, 31, true}, {"gt", 30, 30, false},
		{"gte", 30, 30, true}, {"lt", 10, 9, true}, {"lt", 10, 10, false},
		{"lte", 10, 10, true}, {"eq", 5, 5, true}, {"eq", 5, 6, false},
		{"ne", 5, 6, true}, {"ne", 5, 5, false},
	}
	for _, c := range cases {
		out := Evaluate([]Rule{tempRule(c.op, c.val)}, Context{Kind: TriggerTelemetry, DeviceID: "d1", Key: "env.temp", Value: c.read})
		if (len(out) == 1) != c.fire {
			t.Fatalf("op %s val %v read %v: expected fire=%v, got %d outcomes", c.op, c.val, c.read, c.fire, len(out))
		}
	}
}

func TestTelemetryDefaultsTargetToTriggeringDevice(t *testing.T) {
	out := Evaluate([]Rule{tempRule("gt", 30)}, Context{Kind: TriggerTelemetry, DeviceID: "lamp-7", Key: "env.temp", Value: 35})
	if len(out) != 1 {
		t.Fatalf("expected 1 outcome")
	}
	if out[0].Action.TargetDeviceID != "lamp-7" {
		t.Fatalf("expected target defaulted to triggering device, got %q", out[0].Action.TargetDeviceID)
	}
}

func TestFiltersByDeviceAndCategory(t *testing.T) {
	r := tempRule("gt", 30)
	r.Trigger.Category = "clock"
	// Wrong category -> no fire.
	if out := Evaluate([]Rule{r}, Context{Kind: TriggerTelemetry, DeviceID: "d1", Category: "light", Key: "env.temp", Value: 35}); len(out) != 0 {
		t.Fatalf("expected category filter to exclude, got %d", len(out))
	}
	// Right category -> fire.
	if out := Evaluate([]Rule{r}, Context{Kind: TriggerTelemetry, DeviceID: "d1", Category: "clock", Key: "env.temp", Value: 35}); len(out) != 1 {
		t.Fatalf("expected category match to fire")
	}

	r2 := tempRule("gt", 30)
	r2.Trigger.DeviceID = "only-this"
	if out := Evaluate([]Rule{r2}, Context{Kind: TriggerTelemetry, DeviceID: "other", Key: "env.temp", Value: 35}); len(out) != 0 {
		t.Fatalf("expected device filter to exclude")
	}
}

func TestEventTriggerAndWebhook(t *testing.T) {
	r := Rule{
		ID: "r2", Name: "geofence alert", Enabled: true,
		Trigger: Trigger{Type: TriggerEvent, EventType: "geofence.exit"},
		Action:  Action{Type: ActionWebhook, WebhookURL: "https://example.com/hook"},
	}
	out := Evaluate([]Rule{r}, Context{Kind: TriggerEvent, DeviceID: "tracker", EventType: "geofence.exit"})
	if len(out) != 1 || out[0].Action.Type != ActionWebhook || out[0].Action.WebhookURL != "https://example.com/hook" {
		t.Fatalf("unexpected webhook outcome: %+v", out)
	}
	// Different event -> no fire.
	if out := Evaluate([]Rule{r}, Context{Kind: TriggerEvent, EventType: "geofence.enter"}); len(out) != 0 {
		t.Fatalf("expected non-matching event to be ignored")
	}
	// Webhook target is NOT defaulted to a device (only command actions are).
	if out[0].Action.TargetDeviceID != "" {
		t.Fatalf("webhook action should not get a default device target")
	}
}

func TestDisabledRuleNeverFires(t *testing.T) {
	r := tempRule("gt", 0)
	r.Enabled = false
	if out := Evaluate([]Rule{r}, Context{Kind: TriggerTelemetry, DeviceID: "d1", Key: "env.temp", Value: 100}); len(out) != 0 {
		t.Fatalf("disabled rule should not fire")
	}
}

func TestKindMismatchNeverFires(t *testing.T) {
	// A telemetry rule must not match an event context and vice versa.
	if out := Evaluate([]Rule{tempRule("gt", 0)}, Context{Kind: TriggerEvent, EventType: "anything"}); len(out) != 0 {
		t.Fatalf("telemetry rule should not match event context")
	}
}
