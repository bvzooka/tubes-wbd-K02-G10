package admin

import "errors"

// error aturan bisnis admin, dicek di service dan dipetakan ke pesan di handler
var (
	ErrSelfDemote     = errors.New("admin cannot remove own admin role")
	ErrSelfDeactivate = errors.New("admin cannot deactivate own account")
	ErrLastAdmin      = errors.New("cannot leave zero active platform admins")
	ErrGroupNotEmpty  = errors.New("group still has members or database access")
)
