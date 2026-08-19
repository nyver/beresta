import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";

import {
  addAttachmentFromBytes,
  addAttachmentFromFile,
  listNoteAttachments,
  pickAttachmentFile,
  pickAttachmentSaveDestination,
  readAttachmentPreview,
  removeAttachment,
  saveAttachmentToFile,
  unwrapError,
} from "../api";
import { formatBytes } from "../format";
import { useI18n } from "../i18n";
import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime/runtime";
import { main } from "../../wailsjs/go/models";

export interface AttachmentPanelHandle {
  /** Queues one or more in-memory files (clipboard paste) for upload. See
   * NoteEditor's onAttachFiles prop, which is the only current caller. */
  attachFiles: (files: File[]) => void;
}

export interface AttachmentPanelProps {
  noteId: string;
}

/** Matches desktop/attachments.go's maxAttachmentPreviewBytes: the panel
 * never asks the backend to decrypt an attachment inline that the backend
 * would refuse anyway, so a too-large image just skips straight to
 * "unpreviewable" instead of round-tripping a doomed request. */
const MAX_PREVIEW_BYTES = 8 * 1024 * 1024;

type UploadStatus = "pending" | "uploading" | "error";

interface UploadItem {
  id: string;
  name: string;
  status: UploadStatus;
  errorText?: string;
  source: { kind: "path"; path: string } | { kind: "blob"; file: File };
}

let nextUploadId = 0;

/** resolvePastedFileName falls back to a generated name for a clipboard
 * image File whose name is empty (common for a direct screenshot paste,
 * as opposed to dragging a named file), deriving an extension from its
 * MIME type. The backend still independently rejects any path separator
 * or control character in the final name (see
 * core/account.validateAttachmentDisplayName); this only supplies a
 * sensible default. */
function resolvePastedFileName(file: File): string {
  if (file.name) return file.name;
  const extension = PASTED_IMAGE_EXTENSIONS[file.type] ?? "bin";
  const timestamp = new Date().toISOString().replace(/[:.]/g, "-");
  return `pasted-image-${timestamp}.${extension}`;
}

const PASTED_IMAGE_EXTENSIONS: Record<string, string> = {
  "image/png": "png",
  "image/jpeg": "jpg",
  "image/gif": "gif",
  "image/webp": "webp",
  "image/bmp": "bmp",
};

function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error ?? new Error("read failed"));
    reader.onload = () => {
      const result = reader.result as string;
      // "data:<mediaType>;base64,<data>" - only the payload after the
      // comma is the base64 the Go bridge expects (see decodeBase64).
      resolve(result.slice(result.indexOf(",") + 1));
    };
    reader.readAsDataURL(file);
  });
}

/**
 * AttachmentPanel lists, adds, previews, saves, and removes one note's
 * attachments: native drag-and-drop of files (via Wails' OnFileDrop) and
 * clipboard image paste (forwarded from NoteEditor) both feed the same
 * upload queue as the "Attach file..." picker button. Uploads run one at a
 * time; a still-queued (not yet started) item can be canceled, but an
 * in-flight one runs to completion since the underlying bound call has no
 * cooperative cancellation across the JS bridge.
 */
export const AttachmentPanel = forwardRef<AttachmentPanelHandle, AttachmentPanelProps>(
  function AttachmentPanel({ noteId }, ref) {
    const { t, errorMessage, ready } = useI18n();
    const [attachments, setAttachments] = useState<main.AttachmentDTO[]>([]);
    const [loadError, setLoadError] = useState<string | null>(null);
    const [previews, setPreviews] = useState<Record<string, string>>({});
    const [itemError, setItemError] = useState<string | null>(null);
    // The upload queue's processing order and item lifecycle live in a ref,
    // not React state: processQueue's loop needs to read-modify-write the
    // queue synchronously between awaits, and two enqueue() calls racing
    // against React's batched setState timing (e.g. two "Attach file..."
    // clicks in quick succession) previously let a second item slip through
    // as "uploading" before the first had actually finished. `queue` (state)
    // is kept in sync purely for rendering, via syncQueue after every
    // mutation of queueRef.
    const queueRef = useRef<UploadItem[]>([]);
    const [queue, setQueue] = useState<UploadItem[]>([]);
    const processingRef = useRef(false);
    // Tracks blob IDs already requested (or loaded) so the preview effect
    // only depends on `attachments`, not on `previews` itself: depending on
    // `previews` would re-run the effect (and re-request every other
    // still-pending preview) each time one preview resolves.
    const requestedPreviewsRef = useRef<Set<string>>(new Set());
    const mountedRef = useRef(true);
    useEffect(() => {
      mountedRef.current = true;
      return () => {
        mountedRef.current = false;
      };
    }, []);

    function syncQueue() {
      setQueue([...queueRef.current]);
    }

    const refresh = useCallback(() => {
      listNoteAttachments(noteId)
        .then(setAttachments)
        .catch((thrown: unknown) => setLoadError(errorMessage(unwrapError(thrown))));
    }, [noteId, errorMessage]);

    useEffect(() => {
      // Gated on ready (as Shell.tsx's own load does) so a failed fetch is
      // reported through the already-loaded locale catalog; firing before
      // then would localize through an empty catalog and fall back to raw
      // backend error text.
      if (!ready) return;
      setLoadError(null);
      setPreviews({});
      requestedPreviewsRef.current = new Set();
      queueRef.current = [];
      setQueue([]);
      refresh();
      // Deliberately keyed on noteId/ready, not on refresh: refresh's own
      // identity also shifts whenever errorMessage does (see useI18n),
      // which happens once more as soon as the locale catalog finishes its
      // first async load. Depending on refresh here would re-run this
      // reset-and-refetch a second time right after mount for that reason
      // alone, even though the note being shown never changed.
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [noteId, ready]);

    const processQueue = useCallback(async () => {
      if (processingRef.current) return;
      processingRef.current = true;
      try {
        for (;;) {
          const index = queueRef.current.findIndex((entry) => entry.status === "pending");
          if (index === -1) break;
          const item = queueRef.current[index];
          queueRef.current = queueRef.current.map((entry, i) =>
            i === index ? { ...entry, status: "uploading" } : entry,
          );
          syncQueue();
          try {
            if (item.source.kind === "path") {
              await addAttachmentFromFile(noteId, item.source.path);
            } else {
              const dataBase64 = await readFileAsBase64(item.source.file);
              await addAttachmentFromBytes(
                noteId,
                // Reuse the name resolved once in attachFiles (and already
                // shown in the queue row) rather than recomputing it here:
                // resolvePastedFileName timestamps unnamed clipboard images,
                // so calling it again after the readFileAsBase64 await could
                // mint a different name than what the user watched upload.
                item.name,
                item.source.file.type || "application/octet-stream",
                dataBase64,
              );
            }
            queueRef.current = queueRef.current.filter((entry) => entry.id !== item.id);
            syncQueue();
            refresh();
          } catch (thrown: unknown) {
            const message = errorMessage(unwrapError(thrown));
            queueRef.current = queueRef.current.map((entry) =>
              entry.id === item.id ? { ...entry, status: "error", errorText: message } : entry,
            );
            syncQueue();
          }
        }
      } finally {
        processingRef.current = false;
      }
    }, [noteId, refresh, errorMessage]);

    function enqueue(items: UploadItem[]) {
      if (items.length === 0) return;
      queueRef.current = [...queueRef.current, ...items];
      syncQueue();
      void processQueue();
    }

    const attachFiles = useCallback((files: File[]) => {
      enqueue(
        files.map((file) => ({
          id: `paste-${(nextUploadId += 1)}`,
          name: resolvePastedFileName(file),
          status: "pending",
          source: { kind: "blob", file },
        })),
      );
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    useImperativeHandle(ref, () => ({ attachFiles }), [attachFiles]);

    useEffect(() => {
      OnFileDrop((_x: number, _y: number, paths: string[]) => {
        enqueue(
          paths.map((path) => ({
            id: `drop-${(nextUploadId += 1)}`,
            name: path.split(/[/\\]/).pop() ?? path,
            status: "pending",
            source: { kind: "path", path },
          })),
        );
      }, true);
      return () => OnFileDropOff();
      // Re-subscribed per noteId so the drop callback's closure always
      // enqueues onto the currently open note, not a stale one from before
      // the user switched notes.
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [noteId]);

    useEffect(() => {
      const previewable = attachments.filter(
        (attachment) =>
          attachment.media_type.startsWith("image/") &&
          attachment.size_bytes <= MAX_PREVIEW_BYTES &&
          !requestedPreviewsRef.current.has(attachment.blob_id),
      );
      for (const attachment of previewable) {
        requestedPreviewsRef.current.add(attachment.blob_id);
        readAttachmentPreview(attachment.blob_id)
          .then((preview) => {
            if (!mountedRef.current) return;
            setPreviews((current) => ({
              ...current,
              [attachment.blob_id]: `data:${preview.media_type};base64,${preview.data_base64}`,
            }));
          })
          .catch(() => {
            // A failed preview just leaves the generic file row shown
            // instead of a thumbnail; save-as still authenticates and
            // decrypts independently, so this is not the user's only path
            // to the content.
          });
      }
    }, [attachments]);

    function cancelPending() {
      queueRef.current = queueRef.current.filter((item) => item.status !== "pending");
      syncQueue();
    }

    async function handlePickFile() {
      const path = await pickAttachmentFile().catch((thrown: unknown) => {
        setItemError(errorMessage(unwrapError(thrown)));
        return "";
      });
      if (!path) return;
      enqueue([
        {
          id: `pick-${(nextUploadId += 1)}`,
          name: path.split(/[/\\]/).pop() ?? path,
          status: "pending",
          source: { kind: "path", path },
        },
      ]);
    }

    async function handleRemove(blobId: string) {
      setItemError(null);
      try {
        await removeAttachment(noteId, blobId);
        refresh();
      } catch (thrown: unknown) {
        setItemError(errorMessage(unwrapError(thrown)));
      }
    }

    async function handleSave(attachment: main.AttachmentDTO) {
      setItemError(null);
      try {
        const destination = await pickAttachmentSaveDestination(attachment.display_name);
        if (!destination) return;
        await saveAttachmentToFile(attachment.blob_id, destination);
      } catch (thrown: unknown) {
        setItemError(errorMessage(unwrapError(thrown)));
      }
    }

    const pendingCount = queue.filter((item) => item.status === "pending").length;

    return (
      <section className="attachment-panel" aria-label={t("attachments.section_title")}>
        <div className="attachment-panel-header">
          <h2>{t("attachments.section_title")}</h2>
          <button type="button" onClick={() => void handlePickFile()}>
            {t("attachments.add_button")}
          </button>
        </div>
        <p className="hint attachment-dropzone">{t("attachments.dropzone_hint")}</p>

        {loadError ? (
          <p className="error" role="alert">
            {loadError}
          </p>
        ) : null}
        {itemError ? (
          <p className="error" role="alert">
            {itemError}
          </p>
        ) : null}

        {queue.length > 0 ? (
          <ul className="attachment-upload-queue">
            {queue.map((item) => (
              <li key={item.id} className={`attachment-upload-item status-${item.status}`}>
                <span className="attachment-upload-name">{item.name}</span>
                <span className="attachment-upload-status">
                  {item.status === "error"
                    ? item.errorText
                    : item.status === "uploading"
                      ? t("attachments.uploading")
                      : t("attachments.queued")}
                </span>
              </li>
            ))}
          </ul>
        ) : null}
        {pendingCount > 0 ? (
          <button type="button" className="link-button" onClick={cancelPending}>
            {t("attachments.cancel_pending_button")}
          </button>
        ) : null}

        {attachments.length === 0 && queue.length === 0 ? (
          <p className="hint">{t("attachments.empty")}</p>
        ) : (
          <ul className="attachment-list">
            {attachments.map((attachment) => (
              <li key={attachment.blob_id} className="attachment-row">
                {previews[attachment.blob_id] ? (
                  <img
                    className="attachment-thumbnail"
                    src={previews[attachment.blob_id]}
                    alt={attachment.display_name}
                  />
                ) : (
                  <span className="attachment-thumbnail attachment-thumbnail-placeholder" aria-hidden="true" />
                )}
                <span className="attachment-name">{attachment.display_name}</span>
                <span className="attachment-size">{formatBytes(attachment.size_bytes)}</span>
                <button type="button" onClick={() => void handleSave(attachment)}>
                  {t("attachments.save_button")}
                </button>
                <button
                  type="button"
                  className="link-button"
                  onClick={() => void handleRemove(attachment.blob_id)}
                >
                  {t("attachments.remove_button")}
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    );
  },
);
