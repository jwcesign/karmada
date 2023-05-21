package options

import (
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func (o *Options) Validate() error {
	var errs []error
	errs = append(errs, o.SecureServing.Validate()...)
	errs = append(errs, o.Authentication.Validate()...)
	errs = append(errs, o.Authorization.Validate()...)
	errs = append(errs, o.Audit.Validate()...)
	errs = append(errs, o.Features.Validate()...)

	return utilerrors.NewAggregate(errs)
}
