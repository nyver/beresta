import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock, runtimeMock } from "../setupTests";
import { mockLocaleCatalog, mockSettings } from "../testUtils";
import { AttachmentPanel, type AttachmentPanelHandle } from "./AttachmentPanel";
import { main } from "../../wailsjs/go/models";

/** media_type defaults to a non-image type so most tests never trigger
 * AttachmentPanel's automatic image-preview fetch (ReadAttachmentPreview) -
 * only the dedicated preview test below opts into that by overriding it. */
function fakeAttachment(overrides: Partial<main.AttachmentDTO> = {}): main.AttachmentDTO {
  return {
    blob_id: "blob-1",
    workspace_id: "workspace",
    display_name: "photo.png",
    media_type: "application/octet-stream",
    size_bytes: 1024,
    ...overrides,
  };
}

function renderPanel(noteId = "note-1") {
  mockLocaleCatalog();
  mockSettings();
  const ref = createRef<AttachmentPanelHandle>();
  render(
    <I18nProvider>
      <AttachmentPanel ref={ref} noteId={noteId} />
    </I18nProvider>,
  );
  return { ref };
}

describe("AttachmentPanel", () => {
  it("shows the empty state when a note has no attachments", async () => {
    appMock.ListNoteAttachments.mockResolvedValue([]);
    renderPanel();

    expect(await screen.findByText("attachments.empty")).toBeInTheDocument();
  });

  it("lists an existing attachment with its size", async () => {
    appMock.ListNoteAttachments.mockResolvedValue([fakeAttachment({ size_bytes: 2048 })]);
    renderPanel();

    expect(await screen.findByText("photo.png")).toBeInTheDocument();
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();
  });

  it("shows a localized error when the attachment list fails to load", async () => {
    appMock.ListNoteAttachments.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    renderPanel();

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
  });

  it("fetches and shows an inline thumbnail for an image attachment", async () => {
    appMock.ListNoteAttachments.mockResolvedValue([fakeAttachment({ media_type: "image/png" })]);
    appMock.ReadAttachmentPreview.mockResolvedValue({
      display_name: "photo.png",
      media_type: "image/png",
      data_base64: "Zm9v",
    });
    renderPanel();

    await waitFor(() => expect(appMock.ReadAttachmentPreview).toHaveBeenCalledWith("blob-1"));
    const thumbnail = await screen.findByAltText("photo.png");
    expect(thumbnail).toHaveAttribute("src", "data:image/png;base64,Zm9v");
  });

  it("attaches a file chosen through the native picker", async () => {
    appMock.ListNoteAttachments.mockResolvedValueOnce([]).mockResolvedValueOnce([fakeAttachment()]);
    appMock.PickAttachmentFile.mockResolvedValue("C:\\Users\\me\\photo.png");
    appMock.AddAttachmentFromFile.mockResolvedValue(fakeAttachment());
    renderPanel("note-1");
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "attachments.add_button" }));

    await waitFor(() =>
      expect(appMock.AddAttachmentFromFile).toHaveBeenCalledWith("note-1", "C:\\Users\\me\\photo.png"),
    );
    expect(await screen.findByText("photo.png")).toBeInTheDocument();
  });

  it("does nothing when the native picker is canceled", async () => {
    appMock.ListNoteAttachments.mockResolvedValue([]);
    appMock.PickAttachmentFile.mockResolvedValue("");
    renderPanel();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "attachments.add_button" }));

    expect(appMock.AddAttachmentFromFile).not.toHaveBeenCalled();
  });

  it("removes an attachment and refreshes the list", async () => {
    appMock.ListNoteAttachments.mockResolvedValueOnce([fakeAttachment()]).mockResolvedValueOnce([]);
    appMock.RemoveAttachment.mockResolvedValue(undefined);
    renderPanel("note-1");
    const user = userEvent.setup();

    await screen.findByText("photo.png");
    await user.click(screen.getByRole("button", { name: "attachments.remove_button" }));

    await waitFor(() => expect(appMock.RemoveAttachment).toHaveBeenCalledWith("note-1", "blob-1"));
    await waitFor(() => expect(screen.queryByText("photo.png")).not.toBeInTheDocument());
  });

  it("saves an attachment to a chosen destination", async () => {
    appMock.ListNoteAttachments.mockResolvedValue([fakeAttachment()]);
    appMock.PickSaveDestination.mockResolvedValue("C:\\Users\\me\\Downloads\\photo.png");
    appMock.SaveAttachmentToFile.mockResolvedValue({ display_name: "photo.png", media_type: "image/png" });
    renderPanel();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "attachments.save_button" }));

    await waitFor(() =>
      expect(appMock.SaveAttachmentToFile).toHaveBeenCalledWith(
        "blob-1",
        "C:\\Users\\me\\Downloads\\photo.png",
      ),
    );
  });

  it("does not save when the destination picker is canceled", async () => {
    appMock.ListNoteAttachments.mockResolvedValue([fakeAttachment()]);
    appMock.PickSaveDestination.mockResolvedValue("");
    renderPanel();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "attachments.save_button" }));

    expect(appMock.SaveAttachmentToFile).not.toHaveBeenCalled();
  });

  it("attaches files dropped through the native drag-and-drop bridge", async () => {
    appMock.ListNoteAttachments.mockResolvedValueOnce([]).mockResolvedValueOnce([fakeAttachment()]);
    appMock.AddAttachmentFromFile.mockResolvedValue(fakeAttachment());
    renderPanel("note-1");

    await waitFor(() => expect(runtimeMock.OnFileDrop).toHaveBeenCalled());
    const [onDrop] = runtimeMock.OnFileDrop.mock.calls[0] as [
      (x: number, y: number, paths: string[]) => void,
      boolean,
    ];
    onDrop(0, 0, ["C:\\Users\\me\\dropped.png"]);

    await waitFor(() =>
      expect(appMock.AddAttachmentFromFile).toHaveBeenCalledWith("note-1", "C:\\Users\\me\\dropped.png"),
    );
    expect(await screen.findByText("photo.png")).toBeInTheDocument();
  });

  it("attaches a pasted image via the imperative attachFiles handle", async () => {
    appMock.ListNoteAttachments.mockResolvedValueOnce([]).mockResolvedValueOnce([fakeAttachment()]);
    appMock.AddAttachmentFromBytes.mockResolvedValue(fakeAttachment());
    const { ref } = renderPanel("note-1");
    await screen.findByText("attachments.empty");

    const file = new File(["clipboard bytes"], "", { type: "image/png" });
    ref.current!.attachFiles([file]);

    await waitFor(() => expect(appMock.AddAttachmentFromBytes).toHaveBeenCalled());
    const [noteId, displayName, mediaType, dataBase64] = appMock.AddAttachmentFromBytes.mock.calls[0] as [
      string,
      string,
      string,
      string,
    ];
    expect(noteId).toBe("note-1");
    expect(displayName).toMatch(/^pasted-image-.*\.png$/);
    expect(mediaType).toBe("image/png");
    expect(dataBase64.length).toBeGreaterThan(0);
  });

  it("cancels a still-queued upload without calling the backend for it", async () => {
    appMock.ListNoteAttachments.mockResolvedValue([]);
    appMock.PickAttachmentFile.mockResolvedValueOnce("C:\\a.bin").mockResolvedValueOnce("C:\\b.bin");
    let releaseFirstUpload!: () => void;
    appMock.AddAttachmentFromFile.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          releaseFirstUpload = () => resolve(fakeAttachment({ blob_id: "blob-a" }));
        }),
    );
    renderPanel("note-1");
    const user = userEvent.setup();
    const addButton = await screen.findByRole("button", { name: "attachments.add_button" });

    await user.click(addButton);
    await user.click(addButton);

    await waitFor(() => expect(screen.getByText("attachments.uploading")).toBeInTheDocument());
    expect(screen.getByText("attachments.queued")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "attachments.cancel_pending_button" }));
    expect(screen.queryByText("attachments.queued")).not.toBeInTheDocument();

    releaseFirstUpload();
    await waitFor(() => expect(appMock.AddAttachmentFromFile).toHaveBeenCalledTimes(1));
    expect(appMock.AddAttachmentFromFile).not.toHaveBeenCalledWith("note-1", "C:\\b.bin");
  });
});
