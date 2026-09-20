import { useEffect, useState } from "react";
import QRCode from "qrcode";

export interface QrCodeProps {
  /** The opaque text to encode - an identity or grant code, never a raw
   * key/secret rendered as a separate visible string next to this image
   * (see the "no primary-flow key details" requirement in
   * specs/identity-and-sharing's guided pairing scenario). */
  value: string;
  /** Accessible name for the rendered image, since the QR pattern itself
   * conveys no information to someone who cannot scan it. */
  alt: string;
}

/**
 * QrCode renders `value` as a scannable QR code image, entirely offline
 * (the `qrcode` package has no network dependency) - the primary transfer
 * method task 7.7's guided pairing wizard prefers over reading and typing
 * a long opaque code by hand.
 */
export function QrCode({ value, alt }: QrCodeProps) {
  const [dataUrl, setDataUrl] = useState<string | null>(null);

  useEffect(() => {
    let canceled = false;
    setDataUrl(null);
    if (!value) return;
    QRCode.toDataURL(value, { margin: 1, width: 220 })
      .then((url) => {
        if (!canceled) setDataUrl(url);
      })
      .catch(() => {
        // No code image is a graceful degradation, not a hard failure -
        // the caller's copy-and-paste fallback remains available.
        if (!canceled) setDataUrl(null);
      });
    return () => {
      canceled = true;
    };
  }, [value]);

  if (!dataUrl) return null;
  return <img className="qr-code" src={dataUrl} alt={alt} width={220} height={220} />;
}
