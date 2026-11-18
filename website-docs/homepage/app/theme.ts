// VitePress uses this same key and the values light / dark / auto.
export const themeStorageKey = "vitepress-theme-appearance";

type ExplicitTheme = "light" | "dark";
type ThemePreference = ExplicitTheme | "auto";

export const themeInitializationScript = `(function(){var t='auto';try{var v=localStorage.getItem('${themeStorageKey}');if(v==='dark'||v==='light')t=v}catch(e){}var m=document.querySelector('meta[name="color-scheme"]');if(m)m.content=t==='auto'?'light dark':t;document.documentElement.classList.toggle('dark',t==='dark'||(t==='auto'&&matchMedia('(prefers-color-scheme: dark)').matches))})()`;

function readStoredPreference(): ThemePreference {
  try {
    const raw = localStorage.getItem(themeStorageKey);
    if (raw === "dark" || raw === "light") return raw;
  } catch {
    /* Storage can be disabled. */
  }
  return "auto";
}

function syncColorSchemeMeta(preference: ThemePreference) {
  const meta = document.querySelector<HTMLMetaElement>('meta[name="color-scheme"]');
  if (meta) {
    meta.content = preference === "auto" ? "light dark" : preference;
  }
}

export function getThemeSnapshot() {
  return document.documentElement.classList.contains("dark");
}

export function subscribeToTheme(notify: () => void) {
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  const applyPreference = () => {
    const preference = readStoredPreference();
    syncColorSchemeMeta(preference);
    document.documentElement.classList.toggle(
      "dark",
      preference === "dark" || (preference === "auto" && media.matches),
    );
    notify();
  };
  const onStorage = (event: StorageEvent) => {
    if (event.key === themeStorageKey || event.key === null) applyPreference();
  };
  const observer = new MutationObserver(notify);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  media.addEventListener("change", applyPreference);
  window.addEventListener("storage", onStorage);
  window.addEventListener("pageshow", applyPreference);
  applyPreference();
  return () => {
    observer.disconnect();
    media.removeEventListener("change", applyPreference);
    window.removeEventListener("storage", onStorage);
    window.removeEventListener("pageshow", applyPreference);
  };
}

export function toggleTheme() {
  const dark = !getThemeSnapshot();
  const systemDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  const matchesSystem = dark === systemDark;
  document.documentElement.classList.toggle("dark", dark);
  syncColorSchemeMeta(matchesSystem ? "auto" : dark ? "dark" : "light");
  try {
    if (matchesSystem) {
      localStorage.removeItem(themeStorageKey);
    } else {
      localStorage.setItem(themeStorageKey, dark ? "dark" : "light");
    }
  } catch {
    /* The current page still switches. */
  }
}
