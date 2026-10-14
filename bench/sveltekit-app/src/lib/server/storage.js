import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';

// Relative to the application directory, as in the Go version.
export const STORAGE_DIR = resolve(process.env.STORAGE_DIR || '../storage');
mkdirSync(STORAGE_DIR, { recursive: true });
