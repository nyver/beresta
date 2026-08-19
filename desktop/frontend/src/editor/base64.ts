/**
 * bytesToBase64 encodes an arbitrary byte array (a Yjs update, in this
 * codebase) as base64, chunked to avoid "Maximum call stack size exceeded"
 * from spreading a large typed array into String.fromCharCode's arguments.
 */
export function bytesToBase64(bytes: Uint8Array): string {
  const chunkSize = 0x8000;
  let binary = "";
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}

/** base64ToBytes decodes what desktop/dto.go's decodeBase64 expects back. */
export function base64ToBytes(base64: string): Uint8Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}
