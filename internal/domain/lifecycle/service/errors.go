package service

import "github.com/domainry/domainry-foundation/apperror"

func invalidLifecycleRequest(err error) error {
	return apperror.New(apperror.KindBadRequest, "lifecycle.invalid_request", err, nil)
}

func conflictingLifecycleRequest(code string, err error) error {
	return apperror.New(apperror.KindConflict, code, err, nil)
}
