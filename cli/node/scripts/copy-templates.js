#!/usr/bin/env node
// Copies the shared Copilot assets from the repo's github/ folder into the
// package's templates/ folder, and refreshes reference/tools.md if a live
// catalog is reachable. Run before packing (npm run prepack).
import { cpSync, mkdirSync, rmSync, existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const SRC = join(HERE, '..', '..', 'github');   // repo-level github/ assets
const DST = join(HERE, '..', 'templates');

if (!existsSync(SRC)) { console.error(`source assets not found at ${SRC}`); process.exit(1); }
rmSync(DST, { recursive: true, force: true });
mkdirSync(DST, { recursive: true });
for (const p of ['skills', 'agents', 'copilot-instructions.fairmind.md']) {
  cpSync(join(SRC, p), join(DST, p), { recursive: true });
}
console.log('templates/ populated from', SRC);
