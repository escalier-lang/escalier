import fs from 'node:fs';

fs.mkdirSync('public/types', { recursive: true });

fs.copyFileSync(
    'node_modules/typescript/lib/lib.es5.d.ts',
    'public/types/lib.es5.d.ts',
);

fs.copyFileSync(
    'node_modules/typescript/lib/lib.dom.d.ts',
    'public/types/lib.dom.d.ts',
);

const libDir = 'node_modules/typescript/lib';
const es2015Files = fs
    .readdirSync(libDir)
    .filter((file) => file.startsWith('lib.es2015.'));
for (const file of es2015Files) {
    fs.copyFileSync(`${libDir}/${file}`, `public/types/${file}`);
}

const manifest = ['lib.es5.d.ts', 'lib.dom.d.ts', ...es2015Files];
fs.writeFileSync(
    'public/types/manifest.json',
    JSON.stringify(manifest, null, 2),
);
