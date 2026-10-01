package model

// RegistrationLifecycle is never deleted: a fenced name cannot acquire another creation lifetime.
// Definition contains the canonical typed definition, not a client-side digest.
type RegistrationLifecycle struct {
	Name           string `gorm:"primaryKey;not null"`
	Definition     string `gorm:"not null"`
	ServerID       uint
	State          string `gorm:"not null"`
	Fenced         bool   `gorm:"not null;default:false"`
	CleanupPending bool   `gorm:"not null;default:false"`
}
