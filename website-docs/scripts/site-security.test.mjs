import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const siteRoot = resolve(scriptsDir, '..');

test('W7-ENT-01: docker-entrypoint.sh validates WEBSITE_NGINX_PORT and blocks Nginx directive injection', t => {
  const work = mkdtempSync(join(tmpdir(), 'genyee-docs-entry-'));
  t.after(() => rmSync(work, { recursive: true, force: true }));

  const binDir = join(work, 'bin');
  const tmplDir = join(work, 'templates');
  const confDir = join(work, 'conf.d');
  mkdirSync(binDir, { recursive: true });
  mkdirSync(tmplDir, { recursive: true });
  mkdirSync(confDir, { recursive: true });

  const template = readFileSync(join(siteRoot, 'deploy/nginx.conf'), 'utf8')
    .replace('listen 80;', 'listen ${WEBSITE_NGINX_PORT};');
  writeFileSync(join(tmplDir, 'default.conf.template'), template);

  const argsLog = join(work, 'nginx.args');
  const nginxStub = join(binDir, 'nginx');
  writeFileSync(nginxStub, `#!/bin/sh\nprintf '%s\\n' "$@" > "${argsLog}"\n`);
  chmodSync(nginxStub, 0o755);

  const entrypointSrc = readFileSync(join(siteRoot, 'docker-entrypoint.sh'), 'utf8')
    .replaceAll('/etc/nginx/templates/default.conf.template', join(tmplDir, 'default.conf.template'))
    .replaceAll('/etc/nginx/conf.d/default.conf', join(confDir, 'default.conf'));
  const entrypoint = join(work, 'docker-entrypoint.sh');
  writeFileSync(entrypoint, entrypointSrc);
  chmodSync(entrypoint, 0o755);

  const runEntry = (envPort) => spawnSync('/bin/sh', [entrypoint], {
    encoding: 'utf8',
    env: {
      PATH: `${binDir}:${process.env.PATH}`,
      ...(envPort === undefined ? {} : { WEBSITE_NGINX_PORT: envPort }),
    },
  });

  // Valid default and custom ports succeed and preserve Nginx $uri variables.
  assert.equal(runEntry(undefined).status, 0);
  assert.match(readFileSync(join(confDir, 'default.conf'), 'utf8'), /listen 80;/);
  assert.match(readFileSync(join(confDir, 'default.conf'), 'utf8'), /try_files \$uri \$uri\.html \$uri\/ =404;/);

  assert.equal(runEntry('08080').status, 0);
  assert.match(readFileSync(join(confDir, 'default.conf'), 'utf8'), /listen 8080;/);

  // Malicious or out-of-range ports fail closed before invoking nginx.
  rmSync(argsLog, { force: true });
  for (const bad of ['0', '65536', '-1', 'abc', '80; autoindex on', '80\nserver {}', '80 81']) {
    const res = runEntry(bad);
    assert.notEqual(res.status, 0, `expected non-zero exit for WEBSITE_NGINX_PORT=${JSON.stringify(bad)}`);
  }
  assert.equal(spawnSync('/bin/sh', ['-c', `test ! -e "${argsLog}"`]).status, 0);
});

test('W7-LIC-01: commercial licensing alignment and zero external Unsplash remotePatterns', () => {
  const vitepressCfg = readFileSync(join(siteRoot, '.vitepress/config.mts'), 'utf8');
  const homePage = readFileSync(join(siteRoot, 'homepage/app/page.tsx'), 'utf8');
  const homeLayout = readFileSync(join(siteRoot, 'homepage/app/layout.tsx'), 'utf8');
  const leaderboard = readFileSync(join(siteRoot, 'homepage/app/model-leaderboard.tsx'), 'utf8');
  const nextCfg = readFileSync(join(siteRoot, 'homepage/next.config.ts'), 'utf8');

  assert.doesNotMatch(vitepressCfg, /MIT License/i, 'VitePress footer must not claim GENYEE is MIT licensed');
  assert.doesNotMatch(homePage, /MIT License|Open Source|OPEN SOURCE|开源的|版本说明与发布视频/, 'Homepage must not claim MIT/open-source licensing or retired release videos');
  assert.doesNotMatch(homeLayout, /开源知识框架|v0\.8\.3/, 'Homepage metadata must reflect commercial v0.9.0 framing');
  assert.doesNotMatch(leaderboard, /即将开源|陆续开源/, 'Model leaderboard must not promise open-sourcing proprietary synthetic dataset');
  assert.doesNotMatch(nextCfg, /unsplash\.com/i, 'next.config.ts must not include unused external image CDN domains');
});

test('W7-SEC-01: external URL sanitization, conditional repo links, noopener, and MermaidZoom SVG hardening', () => {
  const headerTs = readFileSync(join(siteRoot, 'shared/header.ts'), 'utf8');
  const siteHeaderVue = readFileSync(join(siteRoot, '.vitepress/theme/SiteHeader.vue'), 'utf8');
  const interactiveTsx = readFileSync(join(siteRoot, 'homepage/app/interactive.tsx'), 'utf8');
  const adopterTsx = readFileSync(join(siteRoot, 'homepage/app/adopter-showcase.tsx'), 'utf8');
  const pageTsx = readFileSync(join(siteRoot, 'homepage/app/page.tsx'), 'utf8');
  const galleryTsx = readFileSync(join(siteRoot, 'homepage/app/product-gallery.tsx'), 'utf8');
  const mermaidZoomVue = readFileSync(join(siteRoot, '.vitepress/theme/MermaidZoom.vue'), 'utf8');
  const screenshotVue = readFileSync(join(siteRoot, '.vitepress/theme/Screenshot.vue'), 'utf8');

  assert.match(headerTs, /export function sanitizeExternalHttpUrl/);
  assert.doesNotMatch(siteHeaderVue, /\/docs\/07-releases\/v0\.8\.3/, 'SiteHeader.vue must not hardcode stale v0.8.3 release link');
  assert.match(siteHeaderVue, /v-if="safeRepoUrl"/, 'SiteHeader.vue must omit GitHub anchors when repository URL is empty');
  assert.match(interactiveTsx, /safeRepoUrl\s*&&/, 'Homepage Header must omit GitHub anchors when repository URL is empty');
  assert.match(adopterTsx, /safeAdopterIssue\s*&&/, 'AdopterShowcase must omit empty adopterIssue anchor');

  for (const [name, src] of [
    ['SiteHeader.vue', siteHeaderVue],
    ['interactive.tsx', interactiveTsx],
    ['adopter-showcase.tsx', adopterTsx],
    ['page.tsx', pageTsx],
    ['product-gallery.tsx', galleryTsx],
  ]) {
    for (const match of src.matchAll(/target="_blank"[^>]*>/g)) {
      assert.match(match[0], /rel="noopener noreferrer"/, `${name} target="_blank" link missing noopener noreferrer: ${match[0]}`);
    }
  }

  assert.match(mermaidZoomVue, /dragging\s*=\s*false[\s\S]*open\.value\s*=\s*false|open\.value\s*=\s*false[\s\S]*dragging\s*=\s*false/);
  assert.match(mermaidZoomVue, /querySelectorAll\(['"]script['"]\)/, 'MermaidZoom.vue should strip <script> elements from cloned SVGs');
  assert.match(screenshotVue, /Object\.hasOwn/, 'Screenshot.vue must use Object.hasOwn on genyeeScreenshotSizes');
});

test('W7-SCR-01: check-site.mjs rejects empty href and javascript: URLs, and package-site.mjs rejects VERSION traversal', t => {
  const work = mkdtempSync(join(tmpdir(), 'genyee-check-site-'));
  t.after(() => rmSync(work, { recursive: true, force: true }));

  mkdirSync(join(work, 'docs/licenses'), { recursive: true });
  writeFileSync(join(work, '404.html'), '<!doctype html><title>404</title>');
  writeFileSync(join(work, 'docs/404.html'), '<!doctype html><title>404</title>');
  writeFileSync(join(work, 'docs/licenses/npm-third-party-notices.txt'), 'ok\n');
  writeFileSync(join(work, 'docs/licenses/npm-dependencies.json'), '{}\n');
  writeFileSync(join(work, 'index.html'), '<!doctype html><a href="" target="_blank">Empty</a>');

  const emptyHrefRes = spawnSync(process.execPath, [join(scriptsDir, 'check-site.mjs'), work], { encoding: 'utf8' });
  assert.notEqual(emptyHrefRes.status, 0, 'check-site.mjs must reject empty href="" attributes');

  writeFileSync(join(work, 'index.html'), '<!doctype html><a href="javascript:alert(1)">XSS</a>');
  const jsHrefRes = spawnSync(process.execPath, [join(scriptsDir, 'check-site.mjs'), work], { encoding: 'utf8' });
  assert.notEqual(jsHrefRes.status, 0, 'check-site.mjs must reject javascript: URLs');

  const packageSiteSrc = readFileSync(join(scriptsDir, 'package-site.mjs'), 'utf8');
  assert.match(packageSiteSrc, /\^\[0-9A-Za-z\._-\]\+\$/, 'package-site.mjs must validate VERSION characters');

  const testDockerSrc = readFileSync(join(scriptsDir, 'test-docker.mjs'), 'utf8');
  assert.doesNotMatch(testDockerSrc, /0\.8\.3/, 'test-docker.mjs must not default to stale 0.8.3 tag');
});

test('UP-DOCS-WEB-001: sanitizeExternalHttpUrl rejects embedded userinfo credentials and dangerous schemes', async () => {
  const { sanitizeExternalHttpUrl } = await import('../shared/header.ts');
  assert.equal(sanitizeExternalHttpUrl('https://docs.example.com/guide?tab=1#sec'), 'https://docs.example.com/guide?tab=1#sec');
  assert.equal(sanitizeExternalHttpUrl('http://example.com/path'), 'http://example.com/path');
  for (const unsafe of [
    '',
    '   ',
    'javascript:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'vbscript:msgbox(1)',
    'https://user:secret@example.com/path',
    'https://genyee.com@evil.example/phish',
    'http://admin@127.0.0.1:8080/',
    'https://example.com/\u0000bad',
  ]) {
    assert.equal(sanitizeExternalHttpUrl(unsafe), '', `expected empty string for unsafe URL: ${unsafe}`);
  }
});
