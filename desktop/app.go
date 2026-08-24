package main

import (
	"context"
	"sync"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/keystore"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
	"github.com/beresta-app/beresta/core/transport"
)

// App owns desktop process lifecycle and the coarse application services
// exposed to the Wails frontend. Every bound method returns JSON-safe DTOs
// and *AppError values; none exposes a raw database handle, secret key
// material, or a Go-only io.Reader/io.Writer across the JS bridge (see
// docs/architecture.md, "trust boundaries").
type App struct {
	mu    sync.Mutex
	ctx   context.Context
	ready bool // set once startup(ctx) has wired the Wails runtime dispatcher

	account         *account.Account
	keyProtection   string // keystore.Protection.String() for the active account, "" when locked
	transport       transport.SyncTransport
	httpTransport   *transport.HTTP
	syncCoordinator *coresync.Coordinator
	syncRepository  *store.SyncRepository
	syncErrorDetail string
	settings        AppSettings

	// keyWrapperFactory builds the platform keystore.Wrapper used to
	// wrap/unwrap the local device database key. It defaults to
	// newKeyWrapper (real Windows Hello/DPAPI); tests substitute a fast,
	// non-interactive fake so they never trigger a real OS credential
	// prompt or the multi-second Windows Hello availability check.
	keyWrapperFactory func(ctx context.Context, prompt string) (keystore.Wrapper, string, error)

	// shell is the running tray/hotkey controller (desktop/main.go wires
	// it in before wails.Run, once traymenu.Start succeeds), or nil when
	// tray integration is unavailable. UpdateSettings re-registers the
	// hotkey through it; shutdown closes it.
	shell shellController

	// applyAutostart enables/disables launching this app at sign-in. It
	// defaults to applyAutostartReal (desktop/autostart_windows.go, real
	// HKCU Run key); tests substitute a no-op so they never touch the
	// developer's or CI machine's real autostart registration.
	applyAutostart func(enabled bool) error
}

func newApp() *App {
	return &App{
		transport:         transport.NewLocal(),
		settings:          defaultSettings(),
		keyWrapperFactory: newKeyWrapper,
		applyAutostart:    applyAutostartReal,
	}
}

// startup is the Wails OnStartup hook. It wires the runtime context (so
// emit and platform keystore prompts can use it) and loads persisted
// application settings; a missing or corrupt settings file falls back to
// defaults rather than blocking startup.
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.ready = true
	a.mu.Unlock()

	settings, err := loadSettings()
	if err != nil {
		settings = defaultSettings()
	}
	a.mu.Lock()
	a.settings = settings
	a.mu.Unlock()
}

// shutdown is the Wails OnShutdown hook. It locks any open account,
// wiping every live secret, so the process never exits with unwrapped key
// material resident regardless of how it is asked to close, and tears
// down the tray icon/hotkey so neither lingers after the process exits.
func (a *App) shutdown(ctx context.Context) {
	a.lockAccount()
	a.mu.Lock()
	shell := a.shell
	a.mu.Unlock()
	if shell != nil {
		shell.Close()
	}
}

// currentAccount returns the active unlocked account, or ErrLocked if none
// is open. Every bound method that needs account state goes through this
// instead of reading a.account directly, so the locked check is never
// forgotten.
func (a *App) currentAccount() (*account.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.account == nil {
		return nil, ErrLocked
	}
	return a.account, nil
}

// primaryWorkspace returns the current account's active workspace ID: every
// bound method that is implicitly workspace-scoped resolves it this way
// instead of asking the frontend to track and pass it. Once an account holds
// more than one workspace (see core/account.Account.ShareWorkspace /
// AcceptWorkspaceShare), "active" is whichever one settings.ActiveWorkspaceID
// names, chosen via SetActiveWorkspace or AcceptWorkspaceGrant; before either
// has ever run, or if that preference no longer names a workspace this
// account holds, it falls back to a deterministic pick.
func (a *App) primaryWorkspace() (*account.Account, model.ID, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return nil, model.ID{}, err
	}
	ids, err := acc.Workspaces()
	if err != nil {
		return nil, model.ID{}, err
	}
	if len(ids) == 0 {
		return nil, model.ID{}, ErrLocked
	}
	a.mu.Lock()
	preferred := a.settings.ActiveWorkspaceID
	a.mu.Unlock()
	return acc, resolveActiveWorkspace(ids, preferred), nil
}

// resolveActiveWorkspace picks preferred out of ids when it names one of
// them, else falls back to primaryWorkspaceID's deterministic pick. ids must
// be non-empty.
func resolveActiveWorkspace(ids []model.ID, preferred string) model.ID {
	if preferred != "" {
		if id, err := model.ParseIDString(preferred); err == nil {
			for _, candidate := range ids {
				if candidate == id {
					return candidate
				}
			}
		}
	}
	return primaryWorkspaceID(ids)
}

// primaryWorkspaceID picks one workspace ID deterministically out of ids
// (the lexicographically smallest), so every caller that needs "the"
// workspace agrees on the same one whenever no ActiveWorkspaceID preference
// applies, even though Account.Workspaces has no defined order. ids must be
// non-empty.
func primaryWorkspaceID(ids []model.ID) model.ID {
	workspaceID := ids[0]
	for _, id := range ids[1:] {
		if id.Compare(workspaceID) < 0 {
			workspaceID = id
		}
	}
	return workspaceID
}

// requestContext returns the context bound methods should pass to core
// calls: the Wails runtime context once startup has run, or a background
// context in tests that construct an App directly.
func (a *App) requestContext() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// AccountInfo is the public identity of the unlocked account: every field
// is safe to display and log (see core/account.Account's doc comment).
type AccountInfo struct {
	AccountID     string `json:"account_id"`
	DeviceID      string `json:"device_id"`
	WorkspaceID   string `json:"workspace_id"`
	KeyProtection string `json:"key_protection"`
}

func (a *App) describeAccount(acc *account.Account) (AccountInfo, error) {
	ids, err := acc.Workspaces()
	if err != nil {
		return AccountInfo{}, err
	}
	workspaceID := model.Nil
	if len(ids) > 0 {
		workspaceID = primaryWorkspaceID(ids)
	}
	a.mu.Lock()
	protection := a.keyProtection
	a.mu.Unlock()
	return AccountInfo{
		AccountID:     idString(acc.ID),
		DeviceID:      idString(acc.DeviceID),
		WorkspaceID:   idString(workspaceID),
		KeyProtection: protection,
	}, nil
}

// AccountStatus reports whether an account is currently open, and its
// public identity if so.
type AccountStatus struct {
	Unlocked bool         `json:"unlocked"`
	Account  *AccountInfo `json:"account,omitempty"`
}

// Status returns the current account/lock state for the frontend to
// render on load, before it has received any account:* event.
func (a *App) Status() (AccountStatus, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return AccountStatus{Unlocked: false}, nil
	}
	info, err := a.describeAccount(acc)
	if err != nil {
		return AccountStatus{}, mapError(err)
	}
	return AccountStatus{Unlocked: true, Account: &info}, nil
}

// CreateAccountRequest is CreateAccount's request DTO.
type CreateAccountRequest struct {
	DatabasePath string `json:"database_path"`
	Passphrase   string `json:"passphrase"`
}

// CreateAccount creates a brand-new local account entirely on-device and
// makes it the active account. It never contacts a network (see
// core/account.Create).
func (a *App) CreateAccount(req CreateAccountRequest) (AccountInfo, error) {
	ctx := a.requestContext()
	wrapper, protection, err := a.keyWrapper(ctx, "Beresta needs to protect your new account key.")
	if err != nil {
		return AccountInfo{}, mapError(err)
	}
	acc, err := account.Create(ctx, account.CreateOptions{
		DatabasePath: req.DatabasePath,
		Passphrase:   []byte(req.Passphrase),
		Wrapper:      wrapper,
	})
	if err != nil {
		return AccountInfo{}, mapError(err)
	}
	return a.activate(acc, protection, req.DatabasePath)
}

// UnlockAccountRequest is UnlockAccount's request DTO.
type UnlockAccountRequest struct {
	DatabasePath string `json:"database_path"`
	Passphrase   string `json:"passphrase"`
}

// UnlockAccount opens an existing local account and makes it the active
// account.
func (a *App) UnlockAccount(req UnlockAccountRequest) (AccountInfo, error) {
	ctx := a.requestContext()
	wrapper, protection, err := a.keyWrapper(ctx, "Unlock your Beresta account.")
	if err != nil {
		return AccountInfo{}, mapError(err)
	}
	acc, err := account.Unlock(ctx, account.UnlockOptions{
		DatabasePath: req.DatabasePath,
		Passphrase:   []byte(req.Passphrase),
		Wrapper:      wrapper,
	})
	if err != nil {
		return AccountInfo{}, mapError(err)
	}
	return a.activate(acc, protection, req.DatabasePath)
}

func (a *App) activate(acc *account.Account, protection, databasePath string) (AccountInfo, error) {
	a.mu.Lock()
	a.account = acc
	a.keyProtection = protection
	settings := a.settings
	settings.LastDatabasePath = databasePath
	a.settings = settings
	a.mu.Unlock()
	// Persisting the settings update is best-effort: losing "last database
	// path" only costs a re-typed path next launch, never account data.
	_ = saveSettings(settings)

	info, err := a.describeAccount(acc)
	if err != nil {
		return AccountInfo{}, mapError(err)
	}
	a.emit(EventAccountUnlocked, info)
	// Reattach a previously configured server without making unlock depend on
	// network availability. ConnectServer performs authentication and leaves
	// the complete local collection usable if the attempt fails.
	if settings.SyncEnabled {
		go func(config AppSettings) {
			_, _ = a.ConnectServer(ConnectServerRequest{
				URL: config.SyncServerURL, SecurityMode: config.SyncSecurityMode,
				Fingerprint: config.SyncFingerprint, DeviceName: "Windows desktop",
			})
		}(settings)
	}
	return info, nil
}

// LockAccount wipes every live secret and closes the active account, if
// any. It is idempotent.
func (a *App) LockAccount() error {
	return mapError(a.lockAccount())
}

func (a *App) lockAccount() error {
	a.mu.Lock()
	acc := a.account
	coordinator := a.syncCoordinator
	a.account = nil
	a.keyProtection = ""
	a.transport = transport.NewLocal()
	a.httpTransport = nil
	a.syncCoordinator = nil
	a.syncRepository = nil
	a.syncErrorDetail = ""
	a.mu.Unlock()
	if coordinator != nil {
		coordinator.Detach()
	}
	if acc == nil {
		return nil
	}
	err := acc.Lock()
	a.emit(EventAccountLocked)
	return err
}

// SyncStatus returns the current synchronization transport's status
// string (see core/transport.Status). Phase 4 only wires the local
// no-op transport, so this always currently reports "disabled"; the field
// exists now so the desktop shell's sync indicator (task 5.10) has a
// stable contract to bind against before a real transport lands.
func (a *App) SyncStatus() string {
	a.mu.Lock()
	t := a.transport
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	status := string(t.Status(ctx))
	a.emit(EventSyncStatus, status)
	return status
}

// SyncError returns the bounded diagnostic detail from the most recent
// failed synchronization cycle. It is cleared only after a full successful
// cycle, disconnect, or lock; individual successful HTTP requests do not
// hide an unapplied sync error.
func (a *App) SyncError() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.syncErrorDetail
}

// keyWrapper builds the platform keystore.Wrapper used to wrap/unwrap the
// local device database key, and reports which protection mode it
// selected (see desktop/keystore_windows.go).
func (a *App) keyWrapper(ctx context.Context, prompt string) (keystore.Wrapper, string, error) {
	a.mu.Lock()
	factory := a.keyWrapperFactory
	a.mu.Unlock()
	return factory(ctx, prompt)
}
