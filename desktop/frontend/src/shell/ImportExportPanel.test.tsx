import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { mockLocaleCatalog, mockSettings } from "../testUtils";
import { ImportExportPanel } from "./ImportExportPanel";

function renderPanel() {
  mockLocaleCatalog();
  mockSettings();
  const onImported = vi.fn();
  render(
    <I18nProvider>
      <ImportExportPanel onImported={onImported} />
    </I18nProvider>,
  );
  return { onImported };
}

describe("ImportExportPanel", () => {
  it("shows the plaintext warning only after starting export, and exports to the chosen destination plus folder name", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.PickExportDestination.mockResolvedValue("D:\\exports");
    appMock.ExportNotes.mockResolvedValue({ version: 1, exported_unix_ms: 0, note_count: 3 });

    expect(screen.queryByText("export.warning")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "export.start_button" }));
    expect(screen.getByText("export.warning")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "export.choose_destination_button" }));
    await screen.findByText("D:\\exports");

    const folderInput = screen.getByDisplayValue("beresta-export");
    await user.clear(folderInput);
    await user.type(folderInput, "my-export");
    await user.click(screen.getByRole("button", { name: "export.confirm_button" }));

    await waitFor(() =>
      expect(appMock.ExportNotes).toHaveBeenCalledWith("D:\\exports\\my-export", []),
    );
    expect(await screen.findByText("export.success")).toBeInTheDocument();
  });

  it("cancels the export confirmation without exporting", async () => {
    renderPanel();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "export.start_button" }));
    await user.click(screen.getByRole("button", { name: "common.cancel" }));

    expect(screen.queryByText("export.warning")).not.toBeInTheDocument();
    expect(appMock.ExportNotes).not.toHaveBeenCalled();
  });

  it("imports a Beresta archive from the picked directory and reports it", async () => {
    const { onImported } = renderPanel();
    const user = userEvent.setup();
    appMock.PickImportSource.mockResolvedValue("D:\\old-export");
    appMock.ImportBerestaArchive.mockResolvedValue({
      new_note_ids: ["n1", "n2"],
      simplified_count: 0,
      skipped_count: 0,
      warnings: [],
    });

    await user.click(screen.getByRole("button", { name: "import.beresta_button" }));

    expect(appMock.PickImportSource).toHaveBeenCalledWith("beresta");
    await waitFor(() => expect(appMock.ImportBerestaArchive).toHaveBeenCalledWith("D:\\old-export"));
    expect(await screen.findByText("import.success")).toBeInTheDocument();
    expect(onImported).toHaveBeenCalled();
  });

  it("imports an Evernote export and surfaces per-note warnings", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.PickImportSource.mockResolvedValue("D:\\export.enex");
    appMock.ImportEvernoteArchive.mockResolvedValue({
      new_note_ids: ["n1"],
      simplified_count: 1,
      skipped_count: 0,
      warnings: [
        {
          note_title: "Recipe",
          kind: "simplified_formatting",
          message: "attached checklist was flattened to plain text",
        },
      ],
    });

    await user.click(screen.getByRole("button", { name: "import.evernote_button" }));

    expect(appMock.PickImportSource).toHaveBeenCalledWith("evernote");
    expect(await screen.findByText("import.warnings_title")).toBeInTheDocument();
    expect(screen.getByText("Recipe")).toBeInTheDocument();
    expect(screen.getByText(/attached checklist was flattened/)).toBeInTheDocument();
  });

  it("shows a count summary of notes imported, formatting simplified, and files skipped (task 6.7)", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.PickImportSource.mockResolvedValue("D:\\old-export");
    appMock.ImportBerestaArchive.mockResolvedValue({
      new_note_ids: ["n1", "n2", "n3"],
      simplified_count: 3,
      skipped_count: 1,
      warnings: [
        { note_title: "A", kind: "simplified_formatting", message: "formatting simplified" },
        { note_title: "A", kind: "skipped_file", message: "attachment could not be imported" },
        { note_title: "B", kind: "simplified_formatting", message: "formatting simplified" },
        { note_title: "C", kind: "simplified_formatting", message: "formatting simplified" },
      ],
    });

    await user.click(screen.getByRole("button", { name: "import.beresta_button" }));

    expect(await screen.findByText("import.summary_notes_imported: 3")).toBeInTheDocument();
    expect(screen.getByText("import.summary_formatting_simplified: 3")).toBeInTheDocument();
    expect(screen.getByText("import.summary_files_skipped: 1")).toBeInTheDocument();
  });

  it("does nothing when the import source picker is canceled", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.PickImportSource.mockResolvedValue("");

    await user.click(screen.getByRole("button", { name: "import.beresta_button" }));

    expect(appMock.ImportBerestaArchive).not.toHaveBeenCalled();
  });

  it("shows a localized error when import fails", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.PickImportSource.mockResolvedValue("D:\\old-export");
    appMock.ImportBerestaArchive.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );

    await user.click(screen.getByRole("button", { name: "import.beresta_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
  });
});
