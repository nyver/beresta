package main

import (
	"github.com/beresta-app/beresta/core/account"
)

// WipeLocalAccount permanently erases every local file this device holds
// for the account at databasePath: the database, its key envelope, and the
// attachment blob store (see account.EraseLocalAccount). It is the desktop
// client's local-wipe primitive for windows-desktop-client's
// revocation-response requirement ("erase local decrypted and encrypted
// account data") and for a user-triggered reset alike; the frontend is
// responsible for the spec's required irreversible-confirmation step
// before calling this - it is not re-confirmed here, and it does not
// require the account to be unlocked first (a device that already lost
// its passphrase, or was revoked, must still be able to wipe itself).
//
// Any currently open account is locked first, releasing its database file
// handle so Windows does not refuse the deletion; this is safe to do even
// when the open account is not the one at databasePath.
func (a *App) WipeLocalAccount(databasePath string) error {
	_ = a.lockAccount()
	if err := account.EraseLocalAccount(databasePath); err != nil {
		return mapError(err)
	}

	a.mu.Lock()
	settings := a.settings
	if settings.LastDatabasePath == databasePath {
		settings.LastDatabasePath = ""
		a.settings = settings
	}
	a.mu.Unlock()
	// Best-effort, same as activate()'s settings persistence: losing this
	// update only costs re-offering a now-deleted path on next launch,
	// never account data (which is already gone).
	if settings.LastDatabasePath == "" {
		_ = saveSettings(settings)
	}
	return nil
}
