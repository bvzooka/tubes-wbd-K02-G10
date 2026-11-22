package connmgr

import "dbplatform/internal/contract"

var _ contract.DriverProvider = (*Provider)(nil)
