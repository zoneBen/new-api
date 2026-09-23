package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// recoverTxPanic converts a panic inside a self-managed transaction into an
// error for the caller, rolling the transaction back first.
//
// A bare recover that only rolls back is not enough: the function then returns
// its zero values with a nil error, so a panicked write looks like a successful
// one and a panicked read looks like an empty result. Callers that cannot
// distinguish those get wrong answers without any signal.
//
// Use it as `defer recoverTxPanic(tx, "GetAllUsers", &err)` in functions that
// began the transaction themselves, and make sure the target is the named
// return value the caller receives. When the target is nil the panic is re-raised
// instead of swallowed, because there would be no way to report it.
func recoverTxPanic(tx *gorm.DB, operation string, err *error) {
	r := recover()
	if r == nil {
		return
	}

	if tx != nil {
		tx.Rollback()
	}

	panicErr := fmt.Errorf("%s panicked: %v", operation, r)
	if err == nil {
		common.SysError(panicErr.Error())
		panic(panicErr)
	}

	*err = panicErr
	common.SysError(panicErr.Error())
}
