package model

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// failUpdates makes every UPDATE against the test database fail, which stands in
// for a write the database rejects. Model tests do not run in parallel, so a
// callback registered here cannot leak into another test. Callers that need to
// read the result back can stop the injection early with the returned function.
func failUpdates(t *testing.T, table string) func() {
	t.Helper()
	const callbackName = "test:fail_update"
	DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if table == "" || tx.Statement.Table == table {
			tx.AddError(errors.New("injected update failure"))
		}
	})
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		DB.Callback().Update().Remove(callbackName)
	}
	t.Cleanup(stop)
	return stop
}

func newPersistTask(t *testing.T, taskID string, status TaskStatus) *Task {
	t.Helper()
	task := &Task{
		TaskID:   taskID,
		UserId:   1,
		Platform: "google",
		Status:   status,
		Progress: "10%",
		Data:     json.RawMessage(`{}`),
	}
	insertTask(t, task)
	return task
}

// TestPersistIfChangedWritesChangedState covers the refresh that succeeds: the
// new upstream values land in the database and the task is left describing them.
func TestPersistIfChangedWritesChangedState(t *testing.T) {
	truncateTables(t)

	task := newPersistTask(t, "persist_write", TaskStatusInProgress)
	snap := task.Snapshot()

	task.Status = TaskStatusSuccess
	task.Progress = "100%"
	task.PrivateData.ResultURL = "https://upstream/video.mp4"

	require.NoError(t, task.PersistIfChanged(snap))

	var reloaded Task
	require.NoError(t, DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, TaskStatusSuccess, reloaded.Status)
	assert.Equal(t, "100%", reloaded.Progress)
	assert.Equal(t, "https://upstream/video.mp4", reloaded.PrivateData.ResultURL)
}

// TestPersistIfChangedRestoresOnLostCAS covers the refresh that another writer
// beat: the guarded status was no longer current, so the update matched nothing.
// The task must roll back to its snapshot rather than report a status that was
// never written — that is what the client used to receive before this was fixed.
func TestPersistIfChangedRestoresOnLostCAS(t *testing.T) {
	truncateTables(t)

	task := newPersistTask(t, "persist_lost_cas", TaskStatusInProgress)
	snap := task.Snapshot()

	task.Status = TaskStatusSuccess
	task.Progress = "100%"
	task.PrivateData.ResultURL = "https://upstream/video.mp4"

	// A concurrent writer moves the task on before this refresh is persisted.
	require.NoError(t, DB.Model(&Task{}).Where("id = ?", task.ID).
		Update("status", TaskStatusFailure).Error)

	err := task.PersistIfChanged(snap)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTaskSnapshotNotPersisted)
	assert.EqualValues(t, TaskStatusInProgress, task.Status, "the unpersisted status must not survive")
	assert.Equal(t, "10%", task.Progress)
	assert.Empty(t, task.PrivateData.ResultURL)

	var reloaded Task
	require.NoError(t, DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, TaskStatusFailure, reloaded.Status, "the concurrent transition must survive")
}

// TestPersistIfChangedRestoresOnWriteError covers the failing write: the caller
// is told, and the in-memory task no longer claims the state that was rejected.
func TestPersistIfChangedRestoresOnWriteError(t *testing.T) {
	truncateTables(t)

	task := newPersistTask(t, "persist_write_error", TaskStatusInProgress)
	snap := task.Snapshot()
	failUpdates(t, "tasks")

	task.Status = TaskStatusSuccess
	task.Progress = "100%"

	err := task.PersistIfChanged(snap)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTaskSnapshotNotPersisted)
	assert.Contains(t, err.Error(), "injected update failure")
	assert.EqualValues(t, TaskStatusInProgress, task.Status)
	assert.Equal(t, "10%", task.Progress)

	var reloaded Task
	require.NoError(t, DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, TaskStatusInProgress, reloaded.Status)
}

// TestPersistIfChangedIsNoOpWhenUnchanged keeps the common case (nothing new
// from upstream) from touching the database at all: an update would take a row
// lock and bump updated_at on every poll of an unchanged task.
func TestPersistIfChangedIsNoOpWhenUnchanged(t *testing.T) {
	truncateTables(t)

	task := newPersistTask(t, "persist_unchanged", TaskStatusInProgress)
	failUpdates(t, "tasks")

	require.NoError(t, task.PersistIfChanged(task.Snapshot()))
}
