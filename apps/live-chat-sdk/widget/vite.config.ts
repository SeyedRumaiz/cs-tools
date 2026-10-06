import { defineConfig } from "vitest/config";

// Library build: one self-contained ES module (the SDK is bundled in), so a
// product vendors or installs a single package and imports one file.
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
