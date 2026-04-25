import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:3456',
      '/data': 'http://localhost:3456',
    }
  },
  build: {
    outDir: '../backend/fs/dist',
    emptyOutDir: true,
  }
})