package core

import "errors"

type ActivationStage string

const (
	ActivationFileInput           ActivationStage = "file_input"
	ActivationBinary              ActivationStage = "binary"
	ActivationResources           ActivationStage = "resources"
	ActivationConfigValidation    ActivationStage = "config_validation"
	ActivationProcessStart        ActivationStage = "process_start"
	ActivationControllerReadiness ActivationStage = "controller_readiness"
	ActivationSystemProxy         ActivationStage = "system_proxy"
	// ActivationSystemProxyUnsupported separates a desktop environment this
	// build cannot configure from an apply that failed. The cause is stripped at
	// the subscription boundary, so the distinction travels as stage identity.
	ActivationSystemProxyUnsupported ActivationStage = "system_proxy_unsupported"
	ActivationStateCommit            ActivationStage = "state_commit"
	ActivationRollback               ActivationStage = "rollback"
)

func (stage ActivationStage) Message() string {
	switch stage {
	case ActivationFileInput:
		return "local profile file is unavailable or invalid"
	case ActivationBinary:
		return "selected Mihomo binary is incompatible or unavailable"
	case ActivationResources:
		return "managed resources could not be validated"
	case ActivationConfigValidation:
		return "generated Mihomo configuration was rejected"
	case ActivationProcessStart:
		return "Mihomo process could not start"
	case ActivationControllerReadiness:
		return "Mihomo controller did not become ready"
	case ActivationSystemProxy:
		return "System Proxy could not be applied"
	case ActivationSystemProxyUnsupported:
		return "system proxy is unsupported in this desktop environment"
	case ActivationStateCommit:
		return "private activation state could not be committed"
	case ActivationRollback:
		return "activation rollback failed; prior runtime needs attention"
	default:
		return "activation failed"
	}
}

type ActivationError struct {
	Stage      ActivationStage
	ResourceID string
	cause      error
}

func validResourceID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := range len(id) {
		b := id[i]
		alpha := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
		if !alpha && !(i > 0 && (b == '-' || b == '_' || b == '.')) {
			return false
		}
	}
	return true
}

func WrapActivation(stage ActivationStage, cause error) error {
	return &ActivationError{Stage: stage, cause: cause}
}

func WrapActivationResource(stage ActivationStage, id string, cause error) error {
	if stage != ActivationBinary && stage != ActivationResources || !validResourceID(id) {
		id = ""
	}
	return &ActivationError{Stage: stage, ResourceID: id, cause: cause}
}

func (e *ActivationError) Unwrap() error { return e.cause }

func (e *ActivationError) Error() string {
	if (e.Stage == ActivationBinary || e.Stage == ActivationResources) && validResourceID(e.ResourceID) {
		return e.Stage.Message() + " (resource " + e.ResourceID + ")"
	}
	return e.Stage.Message()
}

func PublicActivation(err error) (*ActivationError, bool) {
	var failure *ActivationError
	if !errors.As(err, &failure) {
		return nil, false
	}
	id := failure.ResourceID
	if !validResourceID(id) || failure.Stage != ActivationBinary && failure.Stage != ActivationResources {
		id = ""
	}
	return &ActivationError{Stage: failure.Stage, ResourceID: id}, true
}
