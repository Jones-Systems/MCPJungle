package mcp

// Platform support still requires a lifetime inode guard and migrated, file-backed SQLite.
func registrationPlatformSupported(platform string) bool {
	switch platform {
	case "linux", "darwin":
		return true
	default:
		return false
	}
}
