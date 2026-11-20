/// <reference types="vite/client" />

interface ImportMetaEnv {
    /** The commit the playground was built from. The Vite config sets it at build time. */
    readonly VITE_PLAYGROUND_COMMIT?: string;
}
