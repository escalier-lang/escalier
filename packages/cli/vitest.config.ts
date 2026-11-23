import { defineConfig } from 'vitest/config';

// Gives the package its own config so vitest runs only these unit tests.
// Without it, vitest picks up the repo-root config and its browser projects.
export default defineConfig({
    test: {
        include: ['src/**/*.test.ts'],
    },
});
