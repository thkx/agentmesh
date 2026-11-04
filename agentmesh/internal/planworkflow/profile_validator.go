package planworkflow

import (
	"context"
	"errors"
)

type ProfileValidatorFuncs struct {
	ModelExists     func(string) bool
	WorkspaceExists func(string) bool
}

func (v ProfileValidatorFuncs) ValidateProfile(_ context.Context, profile string, binding AgentProfile) error {
	if profile == "" || binding.ModelID == "" || binding.WorkspaceID == "" {
		return ErrInvalidProfileBinding
	}
	if v.ModelExists != nil && !v.ModelExists(binding.ModelID) {
		return errors.New("model_id is not registered")
	}
	if v.WorkspaceExists != nil && !v.WorkspaceExists(binding.WorkspaceID) {
		return errors.New("workspace_id is not a managed workspace")
	}
	return nil
}
