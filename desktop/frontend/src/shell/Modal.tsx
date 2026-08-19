import type { ReactNode } from "react";

import { useI18n } from "../i18n";

export interface ModalProps {
  title: string;
  onClose: () => void;
  children: ReactNode;
}

/**
 * Modal is a simple full-screen overlay dialog: clicking the backdrop or
 * the close button calls onClose, clicking inside the dialog itself does
 * not (stopPropagation on the dialog body).
 */
export function Modal({ title, onClose, children }: ModalProps) {
  const { t } = useI18n();
  return (
    <div className="modal-overlay" role="presentation" onClick={onClose}>
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="modal-header">
          <h2>{title}</h2>
          <button type="button" className="link-button modal-close" onClick={onClose} aria-label={t("common.close")}>
            ×
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  );
}
