import { resolve } from 'node:path';
import { defineConfig } from 'vite';

// https://vite.dev/config/
export default defineConfig({
    build: {
        lib: {
            entry: resolve(__dirname, 'src/index.ts'),
            name: 'EscalierRuntime',
            formats: ['es', 'cjs'],
            fileName: (format) => `index.${format === 'es' ? 'js' : 'cjs'}`,
        },
        outDir: 'dist',
        sourcemap: true,
        rollupOptions: {
            // Externalize deps that shouldn't be bundled
            external: [],
        },
    },
});
