import { mkdirSync, readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const check = spawnSync(process.execPath, ['scripts/check-site.mjs'], { cwd: root, stdio: 'inherit' });
if (check.status !== 0) process.exit(check.status || 1);
mkdirSync(resolve(root, 'releases'), { recursive: true });
const rawVersion = readFileSync(resolve(root, 'VERSION'), 'utf8').trim().replace(/^v/, '');
if (!/^[0-9A-Za-z._-]+$/.test(rawVersion) || rawVersion.includes('..')) {
  throw new Error(`Invalid VERSION value: ${rawVersion}`);
}
const output = resolve(root, `releases/genyee-site-v${rawVersion}.tar.gz`);
const packed = spawnSync('tar', ['-czf', output, '-C', resolve(root, 'static-site'), '.'], { stdio: 'inherit', env: { ...process.env, COPYFILE_DISABLE: '1' } });
if (packed.status !== 0) process.exit(packed.status || 1);
console.log(`Deployable archive: ${output}`);
