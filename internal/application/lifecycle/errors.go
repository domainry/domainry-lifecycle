package lifecycle

import (
	"github.com/domainry/domainry-foundation/apperror"
)

func lifecycleBadRequest(err error) error {
	return apperror.New(apperror.KindBadRequest, "lifecycle.invalid_request", err, nil)
}

func lifecycleForbidden(err error) error {
	return apperror.New(apperror.KindForbidden, "auth.permission_denied", err, nil)
}

func lifecycleNotFound(err error) error {
	return apperror.New(apperror.KindNotFound, "lifecycle.not_found", err, nil)
}

func lifecycleConflict(err error) error {
	return apperror.New(apperror.KindConflict, "lifecycle.conflict", err, nil)
}

func lifecycleLegalHoldActive(err error) error {
	return apperror.New(apperror.KindConflict, "lifecycle.legal_hold.active", err, nil)
}

func lifecycleUnavailable(err error) error {
	return apperror.New(apperror.KindUnavailable, "lifecycle.unavailable", err, nil)
}

func lifecycleInternal(err error) error {
	return apperror.New(apperror.KindInternal, "lifecycle.internal", err, nil)
}
