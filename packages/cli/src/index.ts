import { readFile } from 'node:fs/promises';
import * as path from 'node:path';
import * as fs from 'node:fs';
import { fileURLToPath } from 'node:url';

import './wasm_exec.js'; // run for side-effects

import wasmUrl from '../../../bin/escalier.wasm?url';

const Go = globalThis.Go;

async function loadWasm() {
    try {
        const __dirname = path.dirname(fileURLToPath(import.meta.url));
        // NOTE: `wasmUrl` is an absolute path so we need to prefix it with a
        // '.' before resolving the full path.
        const wasmPath = path.resolve(__dirname, `.${wasmUrl}`);
        const wasmBuffer = await readFile(wasmPath);

        globalThis.fs = fs;
        globalThis.path = path;
        globalThis.TextEncoder = TextEncoder;
        globalThis.TextDecoder = TextDecoder;

        const go = new Go();

        const { instance } = await WebAssembly.instantiate(
            wasmBuffer,
            go.importObject,
        );

        const args = process.argv.slice(2);
        go.argv = ['escalier', ...args];

        go.run(instance);
    } catch (error) {
        console.error('Error loading WASM:', error);
        process.exit(1);
    }
}

loadWasm();
