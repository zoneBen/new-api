package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedInviteUser(t *testing.T, username string) *User {
	t.Helper()
	user := &User{
		Username: username,
		AffCode:  "aff-" + username,
		Password: "hashed",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

// enableInviteRewards turns on the compliance gate and the two invite bonuses for
// the duration of the test.
func enableInviteRewards(t *testing.T, inviteeQuota int, inviterQuota int) {
	t.Helper()
	setting := operation_setting.GetPaymentSetting()
	previousConfirmed, previousVersion := setting.ComplianceConfirmed, setting.ComplianceTermsVersion
	previousInvitee, previousInviter := common.QuotaForInvitee, common.QuotaForInviter
	setting.ComplianceConfirmed = true
	setting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	common.QuotaForInvitee, common.QuotaForInviter = inviteeQuota, inviterQuota
	t.Cleanup(func() {
		setting.ComplianceConfirmed, setting.ComplianceTermsVersion = previousConfirmed, previousVersion
		common.QuotaForInvitee, common.QuotaForInviter = previousInvitee, previousInviter
	})
}

// TestGrantInviteRewardsLogsOnlyWhatWasGranted covers the invite bonuses whose
// write results used to be discarded with `_ =`. The log table is the audit trail
// users and admins read to explain a balance, so an entry claiming a bonus that
// never landed misleads every later reconciliation.
func TestGrantInviteRewardsLogsOnlyWhatWasGranted(t *testing.T) {
	truncateTables(t)

	inviter := seedInviteUser(t, "inviter")
	invitee := seedInviteUser(t, "invitee")
	enableInviteRewards(t, 100, 50)

	stopFailingWrites := failUpdates(t, "users")
	grantInviteRewards(invitee.Id, inviter.Id)
	stopFailingWrites()

	var logCount int64
	require.NoError(t, LOG_DB.Model(&Log{}).
		Where("user_id IN ?", []int{invitee.Id, inviter.Id}).Count(&logCount).Error)
	assert.Zero(t, logCount, "no audit entry may claim a bonus that was not granted")

	var failedInvitee, failedInviter User
	require.NoError(t, DB.First(&failedInvitee, invitee.Id).Error)
	require.NoError(t, DB.First(&failedInviter, inviter.Id).Error)
	assert.Zero(t, failedInvitee.Quota)
	assert.Zero(t, failedInviter.AffQuota)
	assert.Zero(t, failedInviter.AffCount)

	// With the writes working again both rewards land and both are recorded.
	grantInviteRewards(invitee.Id, inviter.Id)

	require.NoError(t, LOG_DB.Model(&Log{}).
		Where("user_id IN ?", []int{invitee.Id, inviter.Id}).Count(&logCount).Error)
	assert.EqualValues(t, 2, logCount)

	var grantedInvitee, grantedInviter User
	require.NoError(t, DB.First(&grantedInvitee, invitee.Id).Error)
	require.NoError(t, DB.First(&grantedInviter, inviter.Id).Error)
	assert.Equal(t, 100, grantedInvitee.Quota)
	assert.Equal(t, 50, grantedInviter.AffQuota)
	assert.Equal(t, 50, grantedInviter.AffHistoryQuota)
	assert.Equal(t, 1, grantedInviter.AffCount)
}

// TestGrantInviteRewardsSkipsWithoutCompliance pins that the bonuses stay off
// until the payment compliance terms have been accepted.
func TestGrantInviteRewardsSkipsWithoutCompliance(t *testing.T) {
	truncateTables(t)

	inviter := seedInviteUser(t, "inviter")
	invitee := seedInviteUser(t, "invitee")
	enableInviteRewards(t, 100, 50)
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false

	grantInviteRewards(invitee.Id, inviter.Id)

	var inviteeAfter, inviterAfter User
	require.NoError(t, DB.First(&inviteeAfter, invitee.Id).Error)
	require.NoError(t, DB.First(&inviterAfter, inviter.Id).Error)
	assert.Zero(t, inviteeAfter.Quota)
	assert.Zero(t, inviterAfter.AffQuota)
}
