import { describe, expect, it } from "vitest";

import {
  createAccount,
  createManualBackup,
  createNote,
  lockAccount,
  restoreWhole,
  search,
  syncSummary,
  unwrapError,
} from "./api";
import { appMock } from "./setupTests";
import { fakeAccountInfo, fakeNote } from "./testUtils";

describe("Wails API adapter", () => {
  it("forwards local lifecycle calls through the generated App bindings", async () => {
    const account = fakeAccountInfo();
    const note = fakeNote({ title: "Offline note" });
    appMock.CreateAccount.mockResolvedValue(account);
    appMock.CreateNote.mockResolvedValue(note);
    appMock.Search.mockResolvedValue([{ note, rank: -1 }]);
    appMock.CreateManualBackup.mockResolvedValue({
      id: "backup-1",
      kind: "manual",
      location: "C:\\backups\\backup-1",
      created_unix_ms: 1,
      corrupt: false,
    });
    appMock.RestoreWhole.mockResolvedValue({
      safety_backup: {
        id: "safety-1",
        kind: "pre_restore",
        location: "C:\\backups\\safety-1",
        created_unix_ms: 2,
        corrupt: false,
      },
      new_note_ids: [],
    });
    appMock.LockAccount.mockResolvedValue(undefined);

    const request = { database_path: "C:\\data\\beresta.db", passphrase: "test passphrase" };
    await expect(createAccount(request)).resolves.toEqual(account);
    await expect(createNote("", note.title)).resolves.toEqual(note);
    await expect(search("offline")).resolves.toEqual([{ note, rank: -1 }]);
    await expect(createManualBackup("C:\\backups")).resolves.toMatchObject({ id: "backup-1" });
    await expect(restoreWhole("backup-1", "C:\\backups")).resolves.toMatchObject({
      safety_backup: { id: "safety-1" },
    });
    await expect(lockAccount()).resolves.toBeUndefined();

    expect(appMock.CreateAccount).toHaveBeenCalledWith(request);
    expect(appMock.CreateNote).toHaveBeenCalledWith("", note.title);
    expect(appMock.Search).toHaveBeenCalledWith("offline");
    expect(appMock.CreateManualBackup).toHaveBeenCalledWith("C:\\backups");
    expect(appMock.RestoreWhole).toHaveBeenCalledWith("backup-1", "C:\\backups");
    expect(appMock.LockAccount).toHaveBeenCalledOnce();
  });

  it("rejects an unknown synchronization state instead of misreporting it", async () => {
    appMock.SyncSummary.mockResolvedValue({
      state: "future-state",
      pending_count: 0,
      last_success_unix_ms: 0,
      retry_in_ms: 0,
      unsafe_count: 0,
      action_required: "none",
    });

    await expect(syncSummary()).rejects.toThrow("unknown synchronization state");
  });

  it("preserves structured AppError codes and safely normalizes bridge failures", () => {
    expect(unwrapError(new Error('{"code":"locked","message":"account is locked"}'))).toEqual({
      code: "locked",
      message: "account is locked",
    });
    expect(unwrapError("runtime disconnected")).toEqual({
      code: "internal",
      message: "runtime disconnected",
    });
  });
});
