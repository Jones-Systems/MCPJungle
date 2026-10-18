package mcp

import (
	"context"
	"time"

	"github.com/mcpjungle/mcpjungle/internal/model"
)

const registrationStatusLimit = 128

// RegistrationStatus omits stored definitions, credentials and process arguments.
type RegistrationStatus struct {
	Version             int      `json:"registration_status"`
	Available           bool     `json:"available"`
	Complete            bool     `json:"complete"`
	CleanupPendingNames []string `json:"cleanup_pending_names"`
}

// RegistrationCleanupStatus reads lifecycle blockers without changing or renewing them.
func (m *MCPService) RegistrationCleanupStatus(ctx context.Context) (*RegistrationStatus, error) {
	result := &RegistrationStatus{Version: 1, CleanupPendingNames: []string{}}
	if m.registration == nil || !m.registration.storage {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := m.db.WithContext(ctx).Model(&model.RegistrationLifecycle{}).
		Where("cleanup_pending = ?", true).Order("name").Limit(registrationStatusLimit+1).
		Pluck("name", &result.CleanupPendingNames).Error; err != nil {
		return nil, err
	}
	result.Available = true
	result.Complete = len(result.CleanupPendingNames) <= registrationStatusLimit
	if !result.Complete {
		result.CleanupPendingNames = result.CleanupPendingNames[:registrationStatusLimit]
	}
	return result, nil
}
