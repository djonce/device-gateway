package device

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"light-gateway/internal/rules"
)

var webhookClient = &http.Client{Timeout: 10 * time.Second}

// Event types that must NOT trigger rule evaluation: high-frequency noise and
// rule-action side effects (to prevent feedback loops). Telemetry is evaluated
// via the dedicated telemetry hook, not the event hook.
var ruleEventDenylist = map[string]bool{
	"telemetry.received": true,
	"rule.fired":         true,
	"rule.created":       true,
	"rule.deleted":       true,
	"command.created":    true,
	"command.delivered":  true,
	"command.ack":        true,
	"device.heartbeat":   true,
}

func ruleEventAllowed(eventType string) bool { return !ruleEventDenylist[eventType] }

// AddRule stores an automation rule.
func (s *Store) AddRule(rule rules.Rule) (rules.Rule, error) {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return rules.Rule{}, fmt.Errorf("%w: rule name is required", ErrBadRequest)
	}
	if rule.Trigger.Type != rules.TriggerTelemetry && rule.Trigger.Type != rules.TriggerEvent {
		return rules.Rule{}, fmt.Errorf("%w: trigger.type must be telemetry or event", ErrBadRequest)
	}
	if rule.Action.Type != rules.ActionCommand && rule.Action.Type != rules.ActionWebhook {
		return rules.Rule{}, fmt.Errorf("%w: action.type must be command or webhook", ErrBadRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	rule.ID = newID("rule", now)
	rule.CreatedAt = now
	s.rules[rule.ID] = rule
	s.recountEnabledRulesLocked()
	s.appendEventLocked("rule.created", "", "automation rule created", map[string]any{"ruleId": rule.ID, "name": rule.Name})
	if err := s.persistRuleLocked(rule); err != nil {
		return rules.Rule{}, err
	}
	return rule, nil
}

// ListRules returns all rules, newest first.
func (s *Store) ListRules() []rules.Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]rules.Rule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// SetRuleEnabled enables or disables a rule.
func (s *Store) SetRuleEnabled(id string, enabled bool) (rules.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.rules[id]
	if !ok {
		return rules.Rule{}, ErrNotFound
	}
	rule.Enabled = enabled
	s.rules[id] = rule
	s.recountEnabledRulesLocked()
	if err := s.persistRuleLocked(rule); err != nil {
		return rules.Rule{}, err
	}
	return rule, nil
}

// DeleteRule removes a rule.
func (s *Store) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return ErrNotFound
	}
	delete(s.rules, id)
	s.recountEnabledRulesLocked()
	s.appendEventLocked("rule.deleted", "", "automation rule deleted", map[string]any{"ruleId": id})
	if s.storage == "sqlite" {
		if _, err := s.db.Exec("DELETE FROM rules WHERE id=?", id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recountEnabledRulesLocked() {
	n := 0
	for _, r := range s.rules {
		if r.Enabled {
			n++
		}
	}
	s.enabledRules = n
}

// fireRules evaluates rules against a context and executes matching outcomes.
// Runs in its own goroutine (spawned by the telemetry/event hooks), so calling
// back into the store (CreateCommand) is safe.
func (s *Store) fireRules(ctx rules.Context) {
	s.mu.RLock()
	snapshot := make([]rules.Rule, 0, len(s.rules))
	for _, r := range s.rules {
		snapshot = append(snapshot, r)
	}
	s.mu.RUnlock()

	for _, outcome := range rules.Evaluate(snapshot, ctx) {
		s.executeOutcome(outcome, ctx)
	}
}

func (s *Store) executeOutcome(outcome rules.Outcome, ctx rules.Context) {
	switch outcome.Action.Type {
	case rules.ActionCommand:
		for _, target := range s.resolveTargets(outcome.Action) {
			_, err := s.CreateCommand(target, CreateCommandRequest{
				Type:        outcome.Action.CommandType,
				Payload:     outcome.Action.Payload,
				RequestedBy: "rule:" + outcome.RuleID,
			})
			if err != nil {
				s.logger.Warn("rule command failed", "error", err, "ruleId", outcome.RuleID, "target", target)
				continue
			}
			s.RecordEvent(target, "rule.fired", "rule fired: "+outcome.RuleName, map[string]any{
				"ruleId": outcome.RuleID, "action": "command", "commandType": outcome.Action.CommandType,
			})
		}
	case rules.ActionWebhook:
		s.postWebhook(outcome, ctx)
	}
}

// resolveTargets expands a command action to one or more device ids.
func (s *Store) resolveTargets(action rules.Action) []string {
	if action.TargetCategory != "" {
		s.mu.RLock()
		defer s.mu.RUnlock()
		var ids []string
		for id, d := range s.devices {
			if string(d.Category) == action.TargetCategory {
				ids = append(ids, id)
			}
		}
		return ids
	}
	if action.TargetDeviceID != "" {
		return []string{action.TargetDeviceID}
	}
	return nil
}

func (s *Store) postWebhook(outcome rules.Outcome, ctx rules.Context) {
	body, _ := json.Marshal(map[string]any{
		"ruleId":   outcome.RuleID,
		"ruleName": outcome.RuleName,
		"trigger": map[string]any{
			"kind": string(ctx.Kind), "deviceId": ctx.DeviceID, "category": ctx.Category,
			"key": ctx.Key, "value": ctx.Value, "eventType": ctx.EventType,
		},
		"firedAt": s.now().UTC().Format(time.RFC3339),
	})
	reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, outcome.Action.WebhookURL, bytes.NewReader(body))
	if err != nil {
		s.logger.Warn("rule webhook build failed", "error", err, "ruleId", outcome.RuleID)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := webhookClient.Do(req)
	if err != nil {
		s.logger.Warn("rule webhook failed", "error", err, "ruleId", outcome.RuleID)
		return
	}
	resp.Body.Close()
	s.RecordEvent(ctx.DeviceID, "rule.fired", "rule fired: "+outcome.RuleName, map[string]any{
		"ruleId": outcome.RuleID, "action": "webhook", "status": resp.StatusCode,
	})
}

func (s *Store) persistRuleLocked(rule rules.Rule) error {
	if s.storage != "sqlite" {
		return nil
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO rules(id, data) VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`,
		rule.ID, string(raw))
	return err
}

func (s *Store) loadSQLiteRules() error {
	rows, err := s.db.Query("SELECT data FROM rules")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var rule rules.Rule
		if err := json.Unmarshal([]byte(raw), &rule); err != nil {
			return err
		}
		s.rules[rule.ID] = rule
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.recountEnabledRulesLocked()
	return nil
}
