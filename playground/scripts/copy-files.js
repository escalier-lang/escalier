import fs from 'node:fs';

fs.mkdirSync('public/types', { recursive: true });

fs.copyFileSync(
    'node_modules/typescript/lib/lib.es5.d.ts',
    'public/types/lib.es5.d.ts',
);
