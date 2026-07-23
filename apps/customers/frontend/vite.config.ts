import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
    plugins: [react()],
    server: {
        port: 10011,
        proxy: {
            '/api': {
                target: process.env.services__customers_api__http__0 || 'http://localhost:10010',
                changeOrigin: true,
                secure: false,
            },
        },
    },
    build: {
        outDir: 'dist',
        emptyOutDir: true,
    },
});