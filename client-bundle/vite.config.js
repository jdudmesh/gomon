// vite.config.js
import { defineConfig } from "vite"
import { resolve } from "path"

export default defineConfig({
  build: {
    sourcemap: "inline",
    outDir: "dist",
    rollupOptions: {
      input: "main.ts",
      output: {
        entryFileNames: "main.js",
        assetFileNames: "[name].[ext]",
      }
    }
  },})