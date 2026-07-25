import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tanstackRouter from "@tanstack/router-plugin/vite";

export default defineConfig({
    plugins: [
        tanstackRouter({
            target: 'react',
            autoCodeSplitting: true,
            routesDirectory: "src/routes",
            generatedRouteTree: "src/routeTree.gen.ts",
            quoteStyle: "single",
            routeFileIgnorePrefix: "-",
        }),
        react(),
    ],
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