// Kept byte-identical with website-docs/scripts/generate-npm-notices.mjs so each
// independently buildable Docker context can generate notices without network.
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync, mkdirSync, writeFileSync } from 'node:fs';
import { resolve, relative, dirname, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const roots = [];
let output;
let distribution;
for (let i = 2; i < process.argv.length; i += 2) {
  const option = process.argv[i], value = process.argv[i + 1];
  if (!value) throw new Error(`Missing value for ${option}`);
  if (option === '--root') roots.push(resolve(value));
  else if (option === '--output') output = resolve(value);
  else if (option === '--distribution') distribution = resolve(value);
  else throw new Error(`Unknown argument: ${option}`);
}
if (!roots.length || !output) throw new Error('Usage: --root <package> [--root <package>] --output <directory>');

const hash = value => createHash('sha256').update(value).digest('hex');
const order = (a, b) => a < b ? -1 : a > b ? 1 : 0;
const policy = JSON.parse(readFileSync(resolve(scriptDir, 'npm-license-policy.json'), 'utf8'));
const documents = [];
const packages = [];
const excluded = [];

function child(root, path) {
  const file = resolve(root, path);
  if (!file.startsWith(root + sep)) throw new Error(`Path escapes package: ${path}`);
  return file;
}
function walk(root, visit, directory = root, packageTree = true) {
  for (const item of readdirSync(directory, { withFileTypes: true }).sort((a, b) => order(a.name, b.name))) {
    if (packageTree && (item.name === 'node_modules' || item.name === '.git')) continue;
    const file = resolve(directory, item.name);
    if (item.isSymbolicLink()) {
      if (!packageTree) throw new Error(`Symlink in static distribution cannot be audited: ${relative(root, file)}`);
      continue;
    }
    if (item.isDirectory()) walk(root, visit, file, packageTree);
    else if (item.isFile()) visit(file, relative(root, file).split(sep).join('/'));
  }
}
function document(file, label) {
  const bytes = readFileSync(file);
  const text = bytes.toString('utf8');
  if (!text.trim() || !Buffer.from(text).equals(bytes)) throw new Error(`Empty or non-UTF8 license: ${label}`);
  return { path: label, sha256: hash(bytes), text };
}

for (const root of roots.sort(order)) {
  const lockBytes = readFileSync(resolve(root, 'package-lock.json'));
  const lock = JSON.parse(lockBytes);
  const project = lock.packages?.['']?.name ?? lock.name;
  if (!project || !lock.packages) throw new Error(`Missing lockfile package metadata: ${root}`);
  documents.push({ project, lockfileSha256: hash(lockBytes) });
  for (const [installedPath, pinned] of Object.entries(lock.packages).sort(([a], [b]) => order(a, b))) {
    if (!installedPath) continue;
    const directory = child(root, installedPath);
    const metadataPath = resolve(directory, 'package.json');
    if (!existsSync(metadataPath)) {
      if (!pinned.optional) throw new Error(`Required dependency missing: ${project}/${installedPath}; run npm ci`);
      excluded.push({ project, path: installedPath, version: pinned.version, reason: 'Optional dependency not installed for this build platform' });
      continue;
    }
    const metadata = JSON.parse(readFileSync(metadataPath, 'utf8'));
    const { name, version } = metadata;
    if (!name || version !== pinned.version) throw new Error(`Installed dependency differs from lockfile: ${installedPath}`);
    const identity = `${name}@${version}`;
    const supplemental = policy.packages?.[identity];
    const license = typeof metadata.license === 'string' ? metadata.license
      : metadata.license?.type ?? metadata.licenses?.map(item => item.type).join(' OR ')
        ?? pinned.license ?? supplemental?.license;
    if (!license || !pinned.resolved || !pinned.integrity) throw new Error(`Missing license/source/integrity metadata: ${identity}`);
    const entry = { project, path: installedPath, name, version, license, source: pinned.resolved, integrity: pinned.integrity };
    const native = policy.nativeBuildOnlyPrefixes.find(prefix => name.startsWith(prefix));
    if (native) {
      excluded.push({ ...entry, reason: 'Native build tool; no native binaries are distributed in the static output' });
      continue;
    }
    const texts = [];
    walk(directory, (file, path) => {
      const base = path.split('/').at(-1);
      const licenseDirectory = path.split('/').slice(0, -1).some(part => /^licen[cs]es?$/i.test(part));
      if ((/(?:licen[cs]e|copying|copyright|notice)/i.test(base) || licenseDirectory)
          && !/\.(?:[cm]?js|ts|json|map|wasm|node)$/i.test(base)) texts.push(document(file, path));
    });
    if (supplemental?.readme) {
      const readme = document(child(directory, supplemental.readme), supplemental.readme);
      if (!/Permission is hereby granted|Redistribution and use in source and binary forms/.test(readme.text)) {
        throw new Error(`Reviewed README no longer contains complete license terms: ${identity}`);
      }
      texts.push(readme);
    }
    for (const attribution of supplemental?.attributions ?? []) {
      const original = document(child(directory, attribution.path), attribution.path);
      if (original.sha256 !== attribution.sha256) throw new Error(`Reviewed attribution changed: ${identity}`);
      texts.push(original);
    }
    for (const extra of supplemental?.files ?? []) {
      const original = document(child(scriptDir, extra.path), extra.path);
      if (original.sha256 !== extra.sha256 || !extra.source) throw new Error(`Unverified supplemental license: ${identity}`);
      texts.push({ ...original, source: extra.source });
    }
    if (!texts.length) throw new Error(`No license text for ${identity}; add a version-specific reviewed policy entry`);
    packages.push({ ...entry, ...(supplemental?.reason ? { licenseResolution: supplemental.reason } : {}), notices: texts.sort((a, b) => order(a.path, b.path)) });
  }
}

// This policy is for static websites only. A native binary copied by a future
// build invalidates the build-only exclusions and requires its own legal bundle.
distribution ??= resolve(output, '..');
if (existsSync(distribution)) walk(distribution, (_file, path) => {
  if (/\.(?:node|dylib|dll|a|so(?:\.\d+)*)$/i.test(path)) throw new Error(`Native binary in static distribution: ${path}`);
}, distribution, false);

const text = [
  'NPM THIRD-PARTY LICENSES AND NOTICES',
  'Generated from the installed, lockfile-verified dependency trees. Includes JavaScript build dependencies as a conservative superset.',
  'Original license and copyright text follows without editing. See npm-dependencies.json for versions, sources, integrity and explicit build-only exclusions.',
  '',
  ...packages.flatMap(pkg => [
    `${'='.repeat(80)}\n${pkg.name}@${pkg.version}\nProject: ${pkg.project}\nLicense: ${pkg.license}\nSource: ${pkg.source}\n`,
    ...pkg.notices.map(notice => `--- ${notice.path}${notice.source ? ` (${notice.source})` : ''} ---\n${notice.text}\n`),
  ]),
].join('\n');
const manifest = { schemaVersion: 1, scope: 'static-web-assets', projects: documents,
  noticesSha256: hash(text),
  packages: packages.map(pkg => ({ ...pkg, notices: pkg.notices.map(({ text: _text, ...notice }) => notice) })), excluded };
mkdirSync(output, { recursive: true });
writeFileSync(resolve(output, 'npm-third-party-notices.txt'), text);
writeFileSync(resolve(output, 'npm-dependencies.json'), JSON.stringify(manifest, null, 2) + '\n');
console.log(`NPM notices: ${packages.length} installed packages, ${excluded.length} explicit platform/build-only exclusions -> ${output}`);
