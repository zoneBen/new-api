package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// failQuotaDataLookups makes every read of the quota_data table fail until the
// returned function is called, standing in for a database that cannot answer the
// lookup. Model tests do not run in parallel, so the callback cannot leak into
// another test, but the caller still stops it explicitly to read the result back.
func failQuotaDataLookups(t *testing.T) func() {
	t.Helper()
	const callbackName = "test:fail_quota_data_lookup"
	DB.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "quota_data" {
			tx.AddError(errors.New("injected lookup failure"))
		}
	})
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		DB.Callback().Query().Remove(callbackName)
	}
	t.Cleanup(stop)
	return stop
}

// TestSaveQuotaDataCacheDoesNotDuplicateOnLookupFailure pins the choice between
// UPDATE and INSERT. A failed lookup is not the same as "no row exists yet":
// treating it that way inserts a second row for the same dimension tuple, and
// every dashboard query then counts that usage twice.
func TestSaveQuotaDataCacheDoesNotDuplicateOnLookupFailure(t *testing.T) {
	truncateTables(t)
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3600,
		UseGroup:  "default",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Count:     2,
		Quota:     100,
		TokenUsed: 40,
	})
	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3661,
		UseGroup:  "default",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     50,
		TokenUsed: 20,
	})

	stopFailingLookups := failQuotaDataLookups(t)
	SaveQuotaDataCache()
	stopFailingLookups()

	var rows []QuotaData
	require.NoError(t, DB.Find(&rows).Error)
	require.Len(t, rows, 1, "a failed lookup must not be mistaken for a missing row")
	assert.Equal(t, 2, rows[0].Count)
	assert.Equal(t, 100, rows[0].Quota)
}
