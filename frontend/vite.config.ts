import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: { outDir: '../web/dist', emptyOutDir: true },
  server: {
    proxy: {
      // changeOrigin harus false: backend mencocokkan header Origin dengan Host (cek CSRF M1).
      // Bentuk singkat '/api': '...' memakai changeOrigin true, sehingga Host menjadi :8080
      // sementara Origin tetap :5173, dan login ditolak 403.
      '/api': { target: 'http://localhost:8080', changeOrigin: false },
    },
  },
})
