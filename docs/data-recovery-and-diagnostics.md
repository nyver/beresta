# Data Recovery and Diagnostics

This document collects the reliability topics a user or support conversation
is most likely to need in one place: where logs live and how long they are
kept, what diagnostics actually show, what happens when storage runs low,
why backup and synchronization are not the same thing, and how migration
and update failures recover. Each section is a summary; where a fuller
treatment already exists elsewhere, it links there instead of duplicating it.

## Bounded log locations and retention

The server writes structured JSON logs to stderr and, by default, to a
bounded, rotated file at `<data_dir>/logs/beresta-server.log`: the active
file rotates once it exceeds `logging.max_size_mb` (10 MiB by default),
retaining `logging.max_backups` rotated files (5 by default) before
deleting the oldest. The Windows desktop client writes the equivalent
bounded, rotated JSON log to `%AppData%\Beresta\logs\beresta-desktop.log`
(same default size/retention, not yet operator-configurable). Every log
record - server or desktop - is limited to stable identifiers and
classification codes: never note content, titles, search queries,
passwords, keys, tokens, or invite codes. See
[Configuration](../README.md#configuration) for the full `config.yaml`
reference and [`config.example.yaml`](../config.example.yaml)'s `logging`
section.

Android release builds minimize what reaches Logcat at all rather than
writing a separate rotated file: sensitive fields are stripped before any
log call, matching the same allowlist server/desktop logging enforces.

## Diagnostics contents

Both clients expose Settings > Diagnostics: a summary layer, always
visible once expanded, and an "expand technical details" layer that loads
only when opened. Both are backed by fixed, reviewed Go structs
(`core/presentation.DiagnosticSummary` and `TechnicalDiagnostics`) that a
forbidden-word list re-validates on every call, so this document is a
description of the schema, not a promise a future field can silently
widen it.

The **summary** layer shows: app version, platform, whether sync is
configured, last successful sync time, pending change count, connection
state, backup status (health/last-verified/location), total local storage
used (database plus attachment cache), local database health, and update
status.

The **technical details** layer additionally shows: the active workspace
and device's opaque identifiers, the last synchronization error's
classification (never its raw backend text), quarantined operation IDs
from the unsafe-incoming-operation journal, the durable sync cursor's
sequence and epoch, consecutive-failure retry count and remaining
backoff, the configured transport's protocol/security mode/URL (already
visible in Synchronization settings), the local database's applied schema
migration version, and whether a workspace-key rotation this device began
is still waiting to be confirmed applied everywhere (resolves
automatically on a later sync cycle; only a value stuck at `true` across
several syncs is worth investigating).

A "Copy diagnostics" action renders the same bounded fields as a
plain-text bundle for support conversations - never a raw log dump, and
never anything the forbidden-word list would reject.

## Storage-pressure recovery

Beresta preflights capacity rather than discovering a full disk mid-write:

- **Backups**: creating a manual or automatic backup first estimates its
  size; if the destination lacks room, `CreateBackup` fails closed
  *before writing anything* with a "not enough free space" error, and the
  only offered remedy is changing the backup destination. Existing valid
  backups are never touched by this failure - a failed backup attempt
  never costs you a working one you already had.
- **Attachments**: adding an attachment preflights free space with margin
  before staging or encrypting any of it, so a full disk is rejected
  immediately with a distinct, localized message rather than after a
  partial copy.
- **Android attachment cache**: Data settings expose an attachment
  retention mode (keep all / selected notebooks / metadata only) and an
  encrypted cache size limit. The underlying eviction planner
  (`core/mobileapi.PlanCacheEviction`) never selects an unsynchronized
  original or a pinned attachment for eviction - only a redundant,
  already-synced downloaded copy is ever a candidate. As of this writing
  these settings configure the *policy*; nothing in the Android app
  currently calls the eviction planner on a running trigger (a full
  content-URI-driven background sweep), so treat "configure retention and
  cache limit" as the accurate user-facing description rather than "the
  app automatically frees space for you" until that wiring lands.
- **Garbage collection** (blob and tombstone cleanup past the 30-day
  minimum retention floor) never touches backup-set contents: a backup is
  a self-contained copy independent of live-data retention (see below).

## Backup-versus-sync behavior

Backup and synchronization are independent systems that protect against
different failure modes, and Beresta's own UI is deliberate about
distinguishing them (never presenting a backup as a sync status or vice
versa).

**Synchronization** keeps your devices converged with each other through
an optional home server, which stores only opaque, unreadable operation
and blob data. Sync protects you against **losing one device**: your
content already exists on every other device that has synced. It does not
protect against a mistake or corruption that syncs faithfully to every
device just as fast as a good edit would - that is what quarantine
(for unsafe incoming operations) and backups (for everything else) are
for. Sync also does nothing for a single-device user with no server
configured at all.

**Backup** is a separate, self-contained encrypted snapshot - your
database and every attachment it references, compressed and encrypted
under a key derived from your account's own Root Key - written to a
destination you choose, with no server involved. A backup's restorability
never depends on what happens to your live database afterward: local
corruption, a botched migration, accidental deletion, or device failure
all still leave a valid backup restorable. Backups also work with no
sync configured at all.

**Neither protects against losing your passphrase.** Both your live data
and every backup are encrypted to the same account key material; there is
no recovery path around that by design (see
[the threat model](threat-model.md)).

In short: sync is about staying current across devices, backup is about
being able to go back. Keep backups even with sync enabled - Beresta's
automatic daily backup already runs regardless of sync configuration.

## Migration safety

A pending schema migration opens through a safety backup taken
immediately beforehand - not merely a transaction rollback for a failed
SQL statement (which never reaches disk in the first place), but a
recovery path for the harder case of the live database file itself ending
up corrupted during migration. Restoring that safety backup recovers a
fully working database with every pre-migration record intact. See
[Desktop updates](desktop-updates.md#release-signing) for how this
interacts with the update flow, since a version upgrade is the most common
time a migration runs.

## Update rollback

Before replacing the installed executable, the desktop updater verifies
the manifest signature, exact version policy, artifact identity, and
Windows Authenticode trust; it retains the prior executable and restores
it automatically if installation or post-install validation fails.
Rollback *failure* itself is tested, not only rollback success: a missing
or corrupt preserved executable, and an installer failure that also fails
its own rollback, both surface as reported errors with the installed
executable left exactly as the failing installer left it - never falsely
reported as recovered. See [Desktop updates](desktop-updates.md) for the
complete signing, verification, and manual `beresta-updater rollback`
reference.
