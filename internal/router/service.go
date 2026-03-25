package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"multi-tenant-bot/pkg/llm"
)

type Service struct {
	llm          llm.LLMClient
	log          *slog.Logger
	businessType string
}

func NewService(client llm.LLMClient, logger *slog.Logger, businessType string) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		llm:          client,
		log:          logger,
		businessType: businessType,
	}
}

func (s *Service) Route(ctx context.Context, req RouteRequest) RouteDecision {
	if err := req.Validate(); err != nil {
		s.log.Warn("invalid route request", "error", err)
		return FallbackDecision("invalid request: "+err.Error(), false)
	}

	prompt, err := BuildPrompt(req, s.businessType)
	if err != nil {
		s.log.Error("build prompt failed", "error", err, "user_id", req.UserID)
		return FallbackDecision("prompt build failed", req.CurrentFlow != "" || len(req.History) > 0)
	}

	raw, err := s.llm.Generate(ctx, prompt)
	if err != nil {
		s.log.Error("llm generation failed", "error", err, "user_id", req.UserID)
		return FallbackDecision("llm generation failed", req.CurrentFlow != "" || len(req.History) > 0)
	}

	decision, err := parseDecision(raw)
	if err != nil {
		s.log.Error("invalid llm response", "error", err, "user_id", req.UserID, "raw", raw)
		return FallbackDecision("invalid llm response", req.CurrentFlow != "" || len(req.History) > 0)
	}

	s.log.Info(
		"route decision created",
		"user_id", req.UserID,
		"current_flow", req.CurrentFlow,
		"intent", decision.Intent,
		"redirect_to", decision.RedirectTo,
		"flow_target", decision.FlowTarget,
		"confidence", decision.Confidence,
	)

	return decision
}

func parseDecision(raw string) (RouteDecision, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RouteDecision{}, fmt.Errorf("empty llm response")
	}

	var decision RouteDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return RouteDecision{}, fmt.Errorf("unmarshal decision: %w", err)
	}

	decision.Normalize()

	if decision.RedirectTo == "" {
		decision.RedirectTo = decision.Intent
	}
	if decision.FlowTarget == "" {
		decision.FlowTarget = FlowTargetStart
	}
	if decision.NextStep == "" {
		decision.NextStep = "FALLBACK_UNKNOWN"
	}

	if err := decision.Validate(); err != nil {
		return RouteDecision{}, err
	}

	return decision, nil
}
