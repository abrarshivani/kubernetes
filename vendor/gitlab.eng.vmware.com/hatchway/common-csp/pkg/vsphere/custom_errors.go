package vsphere

import "errors"

// Error Messages
const (
	NoVMFoundErrMsg = "No VM found"
)

// Error constants
var (
	ErrNoVMFound = errors.New(NoVMFoundErrMsg)
)
