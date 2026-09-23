package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestUpdateAbilitiesPanicIsReportedAndRolledBack covers the self-managed
// transaction path: the deferred recover used to roll back and return nil, so a
// panicked rebuild of the routing table looked like a successful one, with the
// abilities that had already been deleted in that transaction gone for good.
func TestUpdateAbilitiesPanicIsReportedAndRolledBack(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Ability{}, &Channel{}))

	existing := Ability{Group: "default", Model: "gpt-4o", ChannelId: 4242, Enabled: true}
	require.NoError(t, DB.Create(&existing).Error)
	t.Cleanup(func() {
		DB.Where("channel_id = ?", 4242).Delete(&Ability{})
	})

	// Registering on the shared DB is enough: GORM sessions cloned from it,
	// including the transaction under test begins itself, share one callback
	// registry. The panic fires after the insert statement, so the transaction
	// holds uncommitted work for the deferred recover to undo.
	const callbackName = "test:panic_on_create"
	DB.Callback().Create().After("gorm:create").Register(callbackName, func(*gorm.DB) {
		panic("injected panic")
	})
	t.Cleanup(func() { DB.Callback().Create().Remove(callbackName) })

	channel := &Channel{Id: 4242, Group: "default", Models: "gpt-4o", Status: common.ChannelStatusEnabled}
	err := channel.UpdateAbilities(nil)

	require.Error(t, err, "a panicked ability rebuild must not report success")
	assert.Contains(t, err.Error(), "injected panic")

	// The delete that ran before the panic has to be rolled back with it.
	var survivors []Ability
	require.NoError(t, DB.Where("channel_id = ?", 4242).Find(&survivors).Error)
	require.Len(t, survivors, 1)
	assert.Equal(t, "gpt-4o", survivors[0].Model)
}

// TestGetAllUsersPanicIsReported guards the read path: the same bare recover made
// a panicked query indistinguishable from an empty result.
func TestGetAllUsersPanicIsReported(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&User{}))

	const callbackName = "test:panic_on_query"
	DB.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		panic("injected panic")
	})
	t.Cleanup(func() { DB.Callback().Query().Remove(callbackName) })

	users, total, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 10})

	require.Error(t, err, "a panicked query must not look like an empty page")
	assert.Contains(t, err.Error(), "injected panic")
	assert.Empty(t, users)
	assert.Zero(t, total)
}

// TestRecoverTxPanicReraisesWithoutErrorTarget pins the one case the helper
// cannot report: with nowhere to put the error it must not swallow the panic.
func TestRecoverTxPanicReraisesWithoutErrorTarget(t *testing.T) {
	assert.PanicsWithError(t, "no target panicked: injected panic", func() {
		func() {
			defer recoverTxPanic(nil, "no target", nil)
			panic("injected panic")
		}()
	})
}

// TestRecoverTxPanicIgnoresNormalReturn keeps the helper a no-op on the happy
// path, so a named error that was already set is not overwritten.
func TestRecoverTxPanicIgnoresNormalReturn(t *testing.T) {
	expected := errors.New("already set")
	var err error = expected
	func() {
		defer recoverTxPanic(nil, "no panic", &err)
	}()
	assert.ErrorIs(t, err, expected)
}
