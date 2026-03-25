package router

import (
	"errors"
	"strings"
)

const (
	IntentDelivery    = "DELIVERY"
	IntentPickup      = "PICKUP"
	IntentOrderStatus = "ORDER_STATUS"
	IntentPQR         = "PQR"
	IntentGreeting    = "GREETING"
	IntentMenu        = "MENU"
	IntentUnknown     = "UNKNOWN"

	FlowTargetStart = "START"
	FlowTargetMid   = "MID"
)

var validIntents = map[string]struct{}{
	IntentDelivery:    {},
	IntentPickup:      {},
	IntentOrderStatus: {},
	IntentPQR:         {},
	IntentGreeting:    {},
	IntentMenu:        {},
	IntentUnknown:     {},
}

var validFlowTargets = map[string]struct{}{
	FlowTargetStart: {},
	FlowTargetMid:   {},
	"":              {},
}

type HistoryItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type RouteRequest struct {
	Message     string        `json:"message"`
	CurrentFlow string        `json:"current_flow"`
	UserID      string        `json:"user_id"`
	History     []HistoryItem `json:"history,omitempty"`
}

type RouteDecision struct {
	Intent      string  `json:"intent"`
	Confidence  float64 `json:"confidence"`
	RedirectTo  string  `json:"redirect_to"`
	FlowTarget  string  `json:"flow_target"`
	NextStep    string  `json:"next_step"`
	ContextUsed bool    `json:"context_used"`
	Reasoning   string  `json:"reasoning"`
}

func (r RouteRequest) Validate() error {
	if strings.TrimSpace(r.Message) == "" {
		return errors.New("message is required")
	}
	if strings.TrimSpace(r.UserID) == "" {
		return errors.New("user_id is required")
	}
	return nil
}

func (d *RouteDecision) Normalize() {
	d.Intent = strings.ToUpper(strings.TrimSpace(d.Intent))
	d.RedirectTo = strings.ToUpper(strings.TrimSpace(d.RedirectTo))
	d.FlowTarget = strings.ToUpper(strings.TrimSpace(d.FlowTarget))
	d.NextStep = strings.ToUpper(strings.TrimSpace(d.NextStep))
	d.Reasoning = strings.TrimSpace(d.Reasoning)
	if d.Confidence < 0 {
		d.Confidence = 0
	}
	if d.Confidence > 1 {
		d.Confidence = 1
	}
}

func (d RouteDecision) Validate() error {
	if _, ok := validIntents[d.Intent]; !ok {
		return errors.New("invalid intent")
	}
	if d.RedirectTo != "" {
		if _, ok := validIntents[d.RedirectTo]; !ok {
			return errors.New("invalid redirect_to")
		}
	}
	if _, ok := validFlowTargets[d.FlowTarget]; !ok {
		return errors.New("invalid flow_target")
	}
	return nil
}

func FallbackDecision(reason string, contextUsed bool) RouteDecision {
	return RouteDecision{
		Intent:      IntentUnknown,
		Confidence:  0.10,
		RedirectTo:  IntentUnknown,
		FlowTarget:  FlowTargetStart,
		NextStep:    "FALLBACK_UNKNOWN",
		ContextUsed: contextUsed,
		Reasoning:   reason,
	}
}
