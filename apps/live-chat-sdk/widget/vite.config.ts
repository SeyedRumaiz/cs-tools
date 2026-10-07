import { defineConfig } from "vitest/config";

// Builds a single ES module with the SDK bundled in, so a product installs
// one package and imports one file.
export default defineConfig({
  build: {
    lib: {
      entry: "src/index.ts",
      formats: ["es"],
      fileName: () => "live-chat-widget.js",
    },
    sourcemap: true,
    target: "es2022",
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.ts"],
  },
});
