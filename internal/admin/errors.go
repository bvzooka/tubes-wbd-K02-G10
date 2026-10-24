package admin

import "errors"

var (
	ErrSelfDemote     = errors.New("admin cannot remove own admin role")
	ErrSelfDeactivate = errors.New("admin cannot deactivate own account")
	ErrLastAdmin      = errors.New("cannot leave zero active platform admins")
)
