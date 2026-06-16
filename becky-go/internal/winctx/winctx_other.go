//go:build !windows

package winctx

// openExplorerFolders is a stub for non-Windows platforms. Windows File Explorer
// is not present on Linux/macOS; callers should check for ErrUnsupportedOS and
// degrade gracefully (e.g. show the Browse button instead of auto-detecting a folder).
func openExplorerFolders() ([]ExplorerWindow, error) {
	return nil, ErrUnsupportedOS
}

// foregroundExplorerFolder is a stub for non-Windows platforms.
func foregroundExplorerFolder() (string, error) {
	return "", ErrUnsupportedOS
}
