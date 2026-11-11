const STORAGE_KEY = "weknora_settings";

function normalizeBaseUrl(baseUrl) {
  if (!baseUrl || typeof baseUrl !== "string") {
    return "";
  }

  return baseUrl.trim().replace(/\/+$/, "");
}

function isLoopbackHost(host) {
  const normalized = String(host || "").toLowerCase();
  return normalized === "localhost" || normalized === "127.0.0.1" || normalized === "[::1]" || normalized === "::1";
}

function isSecureBaseUrl(baseUrl) {
  const normalized = normalizeBaseUrl(baseUrl);
  if (!normalized) {
    return false;
  }
  const match = normalized.match(/^(https?):\/\/([^/:?#]+)(?::\d+)?(?:\/.*)?$/i);
  if (!match) {
    return false;
  }
  const scheme = match[1].toLowerCase();
  const host = match[2];
  if (scheme === "https") {
    return true;
  }
  return scheme === "http" && isLoopbackHost(host);
}

function normalizeLocale(locale) {
  return locale === "en" ? "en" : "zh";
}

function getSettings() {
  const stored = wx.getStorageSync(STORAGE_KEY) || {};
  return {
    baseUrl: normalizeBaseUrl(stored.baseUrl || ""),
    apiKey: stored.apiKey || "",
    selectedKnowledgeBaseId: stored.selectedKnowledgeBaseId || "",
    locale: normalizeLocale(stored.locale || "zh")
  };
}

function saveSettings(settings) {
  const current = getSettings();
  const nextBaseUrl =
    settings.baseUrl !== undefined && settings.baseUrl !== null
      ? settings.baseUrl
      : current.baseUrl;
  const nextLocale =
    settings.locale !== undefined && settings.locale !== null
      ? settings.locale
      : current.locale;
  const normalizedBaseUrl = normalizeBaseUrl(nextBaseUrl);
  if (normalizedBaseUrl && !isSecureBaseUrl(normalizedBaseUrl)) {
    throw new Error("Base URL must use https:// (http:// is only allowed for localhost/127.0.0.1)");
  }
  const next = {
    baseUrl: normalizedBaseUrl,
    apiKey: settings.apiKey !== undefined ? settings.apiKey : current.apiKey,
    selectedKnowledgeBaseId:
      settings.selectedKnowledgeBaseId !== undefined
        ? settings.selectedKnowledgeBaseId
        : current.selectedKnowledgeBaseId,
    locale: normalizeLocale(nextLocale)
  };
  wx.setStorageSync(STORAGE_KEY, next);
  return next;
}

module.exports = {
  STORAGE_KEY,
  getSettings,
  isSecureBaseUrl,
  normalizeBaseUrl,
  normalizeLocale,
  saveSettings
};
