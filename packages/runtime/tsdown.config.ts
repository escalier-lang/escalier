import { defineConfig } from 'tsdown';

// Builds an ESM and a CommonJS bundle of src/index.ts, each with a bundled
// declaration file (index.d.ts and index.d.cts) that `exports` points at.
export default defineConfig({
    entry: ['src/index.ts'],
    format: ['esm', 'cjs'],
    dts: true,
    sourcemap: true,
    outDir: 'dist',
});
