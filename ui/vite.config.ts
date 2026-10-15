import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { "/config.json": "http://localhost:8787" },
  },
  build: { outDir: "dist", sourcemap: false },
});
