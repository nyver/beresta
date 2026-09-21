import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { mockLocaleCatalog, mockSettings } from "../testUtils";
import { BackupsPanel } from "./BackupsPanel";
import { main } from "../../wailsjs/go/models";

function fakeBackup(overrides: Partial<main.BackupDTO> = {}): main.BackupDTO {
  return {
    id: "backup-1",
    kind: "daily",
    location: "C:\\backups\\daily\\backup-1",
    created_unix_ms: Date.UTC(2026, 0, 1),
    corrupt: false,
    ...overrides,
  };
}

function fakeBackupStatus(overrides: Partial<main.BackupStatusDTO> = {}): main.BackupStatusDTO {
  return {
    health: "unknown",
    last_verified_unix_ms: 0,
    location: "",
    ...overrides,
  };
}

function renderPanel(initialBackups: main.BackupDTO[] = []) {
  mockLocaleCatalog();
  mockSettings({ backup_directory: "C:\\backups" });
  appMock.ListBackups.mockResolvedValue(initialBackups);
  appMock.BackupStatus.mockResolvedValue(fakeBackupStatus());
  appMock.EstimateBackupSize.mockResolvedValue(0);
  const onRestored = vi.fn();
  render(
    <I18nProvider>
      <BackupsPanel onRestored={onRestored} />
    </I18nProvider>,
  );
  return { onRestored };
}

describe("BackupsPanel", () => {
  it("shows the backup catalog's health, last verified time, and location", async () => {
    renderPanel();
    appMock.BackupStatus.mockResolvedValue(
      fakeBackupStatus({ health: "healthy", last_verified_unix_ms: Date.UTC(2026, 0, 1), location: "C:\\backups\\daily\\backup-1" }),
    );

    expect(await screen.findByText("backups.health_healthy")).toBeInTheDocument();
    expect(screen.getByText("C:\\backups\\daily\\backup-1")).toBeInTheDocument();
  });

  it("explains a corrupt backup catalog status in plain language", async () => {
    renderPanel();
    appMock.BackupStatus.mockResolvedValue(fakeBackupStatus({ health: "corrupt" }));

    expect(await screen.findByText("backups.health_corrupt")).toBeInTheDocument();
    expect(screen.getByText("errors.backup_corrupt")).toBeInTheDocument();
  });

  it("refreshes the backup status after creating a manual backup", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.CreateManualBackup.mockResolvedValue(fakeBackup({ kind: "manual" }));

    await screen.findByText("C:\\backups");
    appMock.BackupStatus.mockClear();
    await user.click(screen.getByRole("button", { name: "backups.create_manual_button" }));

    await waitFor(() => expect(appMock.BackupStatus).toHaveBeenCalled());
  });

  it("shows the configured backup directory and the empty state for the default kind", async () => {
    renderPanel();

    expect(await screen.findByText("C:\\backups")).toBeInTheDocument();
    expect(await screen.findByText("backups.empty")).toBeInTheDocument();
    expect(appMock.ListBackups).toHaveBeenCalledWith("daily");
  });

  it("changes the backup directory through the picker and persists it", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.PickBackupDirectory.mockResolvedValue("D:\\external\\backups");
    appMock.UpdateSettings.mockResolvedValue({
      language: "en",
      last_database_path: "",
      auto_lock_minutes: 15,
      backup_directory: "D:\\external\\backups",
    });

    await screen.findByText("C:\\backups");
    await user.click(screen.getByRole("button", { name: "backups.change_directory_button" }));

    await waitFor(() => expect(screen.getByText("D:\\external\\backups")).toBeInTheDocument());
    expect(appMock.UpdateSettings).toHaveBeenCalledWith(
      expect.objectContaining({ backup_directory: "D:\\external\\backups" }),
    );
  });

  it("switches kind tabs and refetches the catalog", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.ListBackups.mockResolvedValue([fakeBackup({ id: "manual-1", kind: "manual" })]);

    await screen.findByText("backups.empty");
    await user.click(screen.getByRole("tab", { name: "backups.kind_manual" }));

    await waitFor(() => expect(appMock.ListBackups).toHaveBeenCalledWith("manual"));
  });

  it("creates a manual backup under the configured directory", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.CreateManualBackup.mockResolvedValue(fakeBackup({ kind: "manual" }));

    await screen.findByText("C:\\backups");
    await user.click(screen.getByRole("button", { name: "backups.create_manual_button" }));

    await waitFor(() => expect(appMock.CreateManualBackup).toHaveBeenCalledWith("C:\\backups"));
  });

  it("offers a retry action when the backup status fetch fails", async () => {
    mockLocaleCatalog();
    mockSettings({ backup_directory: "C:\\backups" });
    appMock.ListBackups.mockResolvedValue([]);
    appMock.EstimateBackupSize.mockResolvedValue(0);
    appMock.BackupStatus.mockRejectedValueOnce(new Error("status unavailable"));
    render(
      <I18nProvider>
        <BackupsPanel onRestored={vi.fn()} />
      </I18nProvider>,
    );
    const user = userEvent.setup();
    await screen.findByRole("alert");
    appMock.BackupStatus.mockResolvedValue(fakeBackupStatus({ health: "healthy" }));

    await user.click(screen.getByRole("button", { name: "common.retry" }));

    expect(await screen.findByText("backups.health_healthy")).toBeInTheDocument();
  });

  it("offers a retry action when the backup catalog fetch fails", async () => {
    mockLocaleCatalog();
    mockSettings({ backup_directory: "C:\\backups" });
    appMock.BackupStatus.mockResolvedValue(fakeBackupStatus());
    appMock.EstimateBackupSize.mockResolvedValue(0);
    appMock.ListBackups.mockRejectedValueOnce(new Error("catalog unavailable"));
    render(
      <I18nProvider>
        <BackupsPanel onRestored={vi.fn()} />
      </I18nProvider>,
    );
    const user = userEvent.setup();
    await screen.findByRole("alert");
    appMock.ListBackups.mockResolvedValue([fakeBackup()]);

    await user.click(screen.getByRole("button", { name: "common.retry" }));

    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    expect(appMock.ListBackups).toHaveBeenCalledTimes(2);
  });

  it("shows a storage-pressure estimate before creating a backup", async () => {
    renderPanel();
    appMock.EstimateBackupSize.mockResolvedValue(1_500_000);

    expect(await screen.findByText(/backups.estimated_size_label/)).toBeInTheDocument();
  });

  it("offers a change-destination remedy when a manual backup fails from insufficient space", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.CreateManualBackup.mockRejectedValue(
      new Error(JSON.stringify({ code: "insufficient_space", message: "Not enough free space." })),
    );
    appMock.PickBackupDirectory.mockResolvedValue("D:\\external\\backups");
    appMock.UpdateSettings.mockResolvedValue({
      language: "en",
      last_database_path: "",
      auto_lock_minutes: 15,
      backup_directory: "D:\\external\\backups",
    });

    await screen.findByText("C:\\backups");
    await user.click(screen.getByRole("button", { name: "backups.create_manual_button" }));

    const remedyButton = await screen.findAllByRole("button", { name: "backups.change_directory_button" });
    expect(remedyButton.length).toBeGreaterThan(1);

    await user.click(remedyButton[remedyButton.length - 1]);

    await waitFor(() => expect(appMock.PickBackupDirectory).toHaveBeenCalled());
  });

  it("previews a backup's note titles", async () => {
    renderPanel([fakeBackup()]);
    const user = userEvent.setup();
    appMock.PreviewBackup.mockResolvedValue({
      backup: fakeBackup(),
      note_titles: ["Grocery list", "Trip notes"],
    });

    await user.click(await screen.findByRole("button", { name: "backups.preview_button" }));

    expect(await screen.findByText("Grocery list")).toBeInTheDocument();
    expect(screen.getByText("Trip notes")).toBeInTheDocument();
  });

  it("runs a dry-run plan and restores only the selected notes as new", async () => {
    renderPanel([fakeBackup()]);
    const user = userEvent.setup();
    appMock.PreviewBackup.mockResolvedValue({ backup: fakeBackup(), note_titles: ["Note A", "Note B"] });
    appMock.PlanRestore.mockResolvedValue({
      entries: [
        { note_id: "note-a", title: "Note A", kind: "addition" },
        { note_id: "note-b", title: "Note B", kind: "unchanged" },
      ],
      required_storage_bytes: 2048,
    });
    appMock.RestoreSelective.mockResolvedValue({ safety_backup: fakeBackup({ kind: "pre_restore" }), new_note_ids: ["note-a"] });

    await user.click(await screen.findByRole("button", { name: "backups.preview_button" }));
    await user.click(await screen.findByRole("button", { name: "backups.start_restore_button" }));

    // "unchanged" is not pre-selected, only "addition" is.
    const restoreSelectedButton = await screen.findByRole("button", {
      name: "backups.restore_selected_button",
    });
    await user.click(restoreSelectedButton);

    await waitFor(() =>
      expect(appMock.RestoreSelective).toHaveBeenCalledWith("backup-1", ["note-a"], "C:\\backups"),
    );
  });

  it("requires an extra confirmation before replacing everything with RestoreWhole", async () => {
    const { onRestored } = renderPanel([fakeBackup()]);
    const user = userEvent.setup();
    appMock.PreviewBackup.mockResolvedValue({ backup: fakeBackup(), note_titles: [] });
    appMock.PlanRestore.mockResolvedValue({ entries: [], required_storage_bytes: 0 });
    appMock.RestoreWhole.mockResolvedValue({ safety_backup: fakeBackup({ kind: "pre_restore" }), new_note_ids: [] });

    await user.click(await screen.findByRole("button", { name: "backups.preview_button" }));
    await user.click(await screen.findByRole("button", { name: "backups.start_restore_button" }));
    await user.click(await screen.findByRole("button", { name: "backups.restore_whole_button" }));

    expect(appMock.RestoreWhole).not.toHaveBeenCalled();
    expect(screen.getByText("backups.restore_whole_confirm")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "backups.restore_whole_confirm_button" }));

    await waitFor(() => expect(appMock.RestoreWhole).toHaveBeenCalledWith("backup-1", "C:\\backups"));
    await waitFor(() => expect(onRestored).toHaveBeenCalled());
  });
});
