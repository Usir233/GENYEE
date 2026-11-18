import assert from 'node:assert/strict';
import { copyFileSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath, pathToFileURL } from 'node:url';
import vm from 'node:vm';

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const homepageRoot = resolve(scriptsDir, '..');
const siteRoot = resolve(homepageRoot, '..');

async function loadThemeModule() {
  const dir = mkdtempSync(join(tmpdir(), 'genyee-theme-mod-'));
  try {
    writeFileSync(join(dir, 'package.json'), '{"type":"module"}\n');
    copyFileSync(join(homepageRoot, 'app/theme.ts'), join(dir, 'theme.ts'));
    return await import(pathToFileURL(join(dir, 'theme.ts')).href);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

const {
  getThemeSnapshot,
  subscribeToTheme,
  themeInitializationScript,
  themeStorageKey,
  toggleTheme,
} = await loadThemeModule();

function installBrowserEnv({ initialSystemDark = false, initialStored = null, throwOnStorage = false } = {}) {
  let darkClass = false;
  let systemDark = initialSystemDark;
  const storage = new Map();
  if (initialStored !== null) storage.set(themeStorageKey, initialStored);

  const mediaListeners = new Set();
  const windowListeners = new Map();
  const mutationObservers = new Set();
  const metaColorScheme = { content: 'light dark' };

  const classList = {
    contains(token) {
      return token === 'dark' ? darkClass : false;
    },
    toggle(token, force) {
      if (token !== 'dark') return false;
      const next = force === undefined ? !darkClass : Boolean(force);
      const changed = next !== darkClass;
      darkClass = next;
      if (changed) {
        for (const observer of mutationObservers) observer.notify();
      }
      return darkClass;
    },
  };

  const documentElement = { classList };

  const localStorageMock = {
    getItem(key) {
      if (throwOnStorage) throw new Error('Storage disabled');
      return storage.has(key) ? storage.get(key) : null;
    },
    setItem(key, value) {
      if (throwOnStorage) throw new Error('Storage disabled');
      storage.set(key, String(value));
    },
    removeItem(key) {
      if (throwOnStorage) throw new Error('Storage disabled');
      storage.delete(key);
    },
  };

  const mediaQueryList = {
    get matches() {
      return systemDark;
    },
    addEventListener(type, listener) {
      if (type === 'change') mediaListeners.add(listener);
    },
    removeEventListener(type, listener) {
      if (type === 'change') mediaListeners.delete(listener);
    },
  };

  const matchMediaMock = (query) => {
    assert.equal(query, '(prefers-color-scheme: dark)');
    return mediaQueryList;
  };

  class MockMutationObserver {
    constructor(callback) {
      this.callback = callback;
    }
    observe(_target, _options) {
      mutationObservers.add(this);
    }
    disconnect() {
      mutationObservers.delete(this);
    }
    notify() {
      this.callback([]);
    }
  }

  const windowMock = {
    matchMedia: matchMediaMock,
    addEventListener(type, listener) {
      if (!windowListeners.has(type)) windowListeners.set(type, new Set());
      windowListeners.get(type).add(listener);
    },
    removeEventListener(type, listener) {
      windowListeners.get(type)?.delete(listener);
    },
  };

  const documentMock = {
    documentElement,
    querySelector(selector) {
      if (selector === 'meta[name="color-scheme"]') return metaColorScheme;
      return null;
    },
  };

  const previous = {
    window: globalThis.window,
    document: globalThis.document,
    localStorage: globalThis.localStorage,
    matchMedia: globalThis.matchMedia,
    MutationObserver: globalThis.MutationObserver,
  };

  Object.assign(globalThis, {
    window: windowMock,
    document: documentMock,
    localStorage: localStorageMock,
    matchMedia: matchMediaMock,
    MutationObserver: MockMutationObserver,
  });

  return {
    metaColorScheme,
    storage,
    setSystemDark(next) {
      systemDark = next;
      for (const listener of mediaListeners) listener({ matches: next });
    },
    dispatchStorage(key, newValue) {
      if (newValue === null) storage.delete(key);
      else if (newValue !== undefined) storage.set(key, newValue);
      for (const listener of windowListeners.get('storage') ?? []) {
        listener({ key, newValue });
      }
    },
    runInitScript() {
      vm.runInNewContext(themeInitializationScript, {
        localStorage: localStorageMock,
        document: documentMock,
        matchMedia: matchMediaMock,
      });
    },
    restore() {
      for (const [k, v] of Object.entries(previous)) {
        if (v === undefined) delete globalThis[k];
        else globalThis[k] = v;
      }
    },
  };
}

test('W8-THEME-01: toggling back to match system preference clears stored override and restores auto mode', t => {
  const env = installBrowserEnv({ initialSystemDark: false });
  let notifications = 0;
  const unsubscribe = subscribeToTheme(() => {
    notifications++;
  });
  t.after(() => {
    unsubscribe();
    env.restore();
  });

  assert.equal(getThemeSnapshot(), false);
  assert.equal(env.metaColorScheme.content, 'light dark');
  assert.equal(env.storage.has(themeStorageKey), false);

  // User toggles to dark while OS is light -> stores explicit "dark" override.
  toggleTheme();
  assert.equal(getThemeSnapshot(), true);
  assert.equal(env.storage.get(themeStorageKey), 'dark');
  assert.equal(env.metaColorScheme.content, 'dark');

  // OS changes to dark, then back to light -> stored "dark" override MUST remain pinned (no retroactive removal).
  env.setSystemDark(true);
  assert.equal(env.storage.get(themeStorageKey), 'dark');
  assert.equal(getThemeSnapshot(), true);
  env.setSystemDark(false);
  assert.equal(env.storage.get(themeStorageKey), 'dark');
  assert.equal(getThemeSnapshot(), true);

  // User toggles back to light while OS is light -> matches system preference, so stored key is removed ("auto").
  toggleTheme();
  assert.equal(getThemeSnapshot(), false);
  assert.equal(env.storage.has(themeStorageKey), false);
  assert.equal(env.metaColorScheme.content, 'light dark');

  // Because override was cleared, subsequent OS dark-mode changes are followed automatically!
  env.setSystemDark(true);
  assert.equal(getThemeSnapshot(), true);
  assert.equal(env.metaColorScheme.content, 'light dark');
  assert.ok(notifications > 0);
});

test('W8-THEME-02: inline FOUC script, cross-tab storage sync, and disabled-storage fallback keep color-scheme meta aligned', t => {
  const env = installBrowserEnv({ initialSystemDark: true, initialStored: 'light' });

  env.runInitScript();
  assert.equal(getThemeSnapshot(), false);
  assert.equal(env.metaColorScheme.content, 'light');

  const unsubscribe = subscribeToTheme(() => {});
  t.after(() => {
    unsubscribe();
    env.restore();
  });

  // Toggling from pinned light -> dark while OS is dark clears stored key back to system default.
  toggleTheme();
  assert.equal(getThemeSnapshot(), true);
  assert.equal(env.storage.has(themeStorageKey), false);
  assert.equal(env.metaColorScheme.content, 'light dark');

  // Cross-tab storage updates ("light", "auto", and key removal) propagate cleanly.
  env.dispatchStorage(themeStorageKey, 'light');
  assert.equal(getThemeSnapshot(), false);
  assert.equal(env.metaColorScheme.content, 'light');

  env.dispatchStorage(themeStorageKey, 'auto');
  assert.equal(getThemeSnapshot(), true);
  assert.equal(env.metaColorScheme.content, 'light dark');

  env.dispatchStorage(themeStorageKey, null);
  assert.equal(getThemeSnapshot(), true);
  assert.equal(env.metaColorScheme.content, 'light dark');
});

test('W8-DOCS-A11Y-01: package naming, color-scheme CSS/meta, --gy-* token purity, and semantic controls', () => {
  const pkg = JSON.parse(readFileSync(join(homepageRoot, 'package.json'), 'utf8'));
  const lock = JSON.parse(readFileSync(join(homepageRoot, 'package-lock.json'), 'utf8'));
  assert.equal(pkg.name, '@genyee/homepage');
  assert.equal(lock.name, '@genyee/homepage');
  assert.equal(lock.packages?.['']?.name, '@genyee/homepage');

  const brandCss = readFileSync(join(siteRoot, 'shared/brand.css'), 'utf8');
  assert.doesNotMatch(brandCss, /:root\s*\{[^}]*color-scheme:\s*light\s*;/);
  assert.match(brandCss, /color-scheme:\s*light dark\s*;/);
  assert.match(brandCss, /meta\[name="color-scheme"\]\[content="dark"\]/);
  assert.match(brandCss, /meta\[name="color-scheme"\]\[content="light"\]/);

  const homeLayout = readFileSync(join(homepageRoot, 'app/layout.tsx'), 'utf8');
  const vitepressCfg = readFileSync(join(siteRoot, '.vitepress/config.mts'), 'utf8');
  assert.match(homeLayout, /name="color-scheme"\s+content="light dark"/);
  assert.match(vitepressCfg, /name:\s*'color-scheme',\s*content:\s*'light dark'/);

  // Zero stray #hex or rgb()/rgba() color literals outside shared/brand.css.
  const cssTargets = [
    join(siteRoot, 'shared/header.css'),
    join(siteRoot, '.vitepress/theme/style.css'),
    join(siteRoot, '.vitepress/theme/MermaidZoom.vue'),
    ...readdirSync(join(homepageRoot, 'app'))
      .filter((name) => name.endsWith('.css'))
      .map((name) => join(homepageRoot, 'app', name)),
  ];
  for (const file of cssTargets) {
    const content = readFileSync(file, 'utf8');
    assert.doesNotMatch(
      content,
      /#[0-9a-fA-F]{3,8}\b|rgba?\(/,
      `Found hardcoded color literal outside shared/brand.css in ${file}`,
    );
  }

  // Every <button> across homepage TSX and VitePress theme Vue files must specify an explicit type attribute.
  const uiFiles = [
    ...readdirSync(join(homepageRoot, 'app'))
      .filter((name) => name.endsWith('.tsx'))
      .map((name) => join(homepageRoot, 'app', name)),
    ...readdirSync(join(siteRoot, '.vitepress/theme'))
      .filter((name) => name.endsWith('.vue'))
      .map((name) => join(siteRoot, '.vitepress/theme', name)),
  ];
  for (const file of uiFiles) {
    const content = readFileSync(file, 'utf8');
    for (const match of content.matchAll(/<button\b[^>]*>/g)) {
      assert.match(match[0], /\btype="(?:button|submit|reset)"/, `Missing explicit button type in ${file}: ${match[0]}`);
    }
  }

  const gallery = readFileSync(join(homepageRoot, 'app/product-gallery.tsx'), 'utf8');
  assert.doesNotMatch(gallery, /<div[^>]*\bonClick=/, 'product-gallery.tsx must not attach click handlers to non-semantic <div> elements');
  for (const match of gallery.matchAll(/<div\b[^>]*\baria-label=/g)) {
    assert.match(match[0], /\brole="(?:group|img|region)"/, `Roleless <div> with aria-label in product-gallery.tsx: ${match[0]}`);
  }

  const mermaidZoom = readFileSync(join(siteRoot, '.vitepress/theme/MermaidZoom.vue'), 'utf8');
  assert.match(mermaidZoom, /<dialog\b/, 'MermaidZoom.vue should use native <dialog>');
  assert.match(mermaidZoom, /\.showModal\(\)/, 'MermaidZoom.vue should open modal via showModal()');
  assert.match(mermaidZoom, /aria-label="缩小"/);
  assert.match(mermaidZoom, /aria-label="放大"/);
});
