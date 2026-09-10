import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  test: {
    environment: "jsdom",
    setupFiles: ["./src/setupTests.ts"],
    // The default 5000ms timeout is too tight for tests that drive a real
    // keyboard/click interaction through note creation and editor mount on
    // a loaded CI runner; a few of these have flaked with "Test timed out
    // in 5000ms" under CI load even though they pass locally in well under
    // a second.
    testTimeout: 15000,
  },
});
