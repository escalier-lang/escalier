import { builtinModules } from 'node:module';
import { defineConfig } from 'vite';

export default defineConfig({
    build: {
        target: 'node20', // target Node version
        outDir: 'dist',
        emptyOutDir: true,
        minify: false, // easier debugging for CLIs
        sourcemap: true,
        rollupOptions: {
            input: 'src/index.ts',
            output: {
                entryFileNames: '[name].js',
                format: 'esm', // or "cjs" if you prefer
                banner: '#!/usr/bin/env node',
            },
            external: [
                // Don’t bundle Node built-ins
                ...builtinModules,
                ...builtinModules.map((m) => `node:${m}`),
            ],
        },
    },
});
