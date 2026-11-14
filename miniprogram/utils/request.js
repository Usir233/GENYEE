const { getSettings, isSecureBaseUrl } = require("./config");
const { t } = require("./i18n");

function request(path, options = {}) {
  const settings = getSettings();
  if (!settings.baseUrl) {
    return Promise.reject(new Error(t("missingBaseUrl")));
  }
  if (!isSecureBaseUrl(settings.baseUrl)) {
    return Promise.reject(new Error("Base URL must use https:// for non-loopback hosts."));
  }
  if (!settings.apiKey) {
    return Promise.reject(new Error(t("missingApiKey")));
  }

  return new Promise((resolve, reject) => {
    wx.request({
      url: `${settings.baseUrl}${path}`,
      method: options.method || "GET",
      data: options.data,
      header: {
        "Content-Type": "application/json",
        "X-API-Key": settings.apiKey,
        "X-Request-ID": `mp-${Date.now()}-${Math.random().toString(16).slice(2)}`
      },
      success(response) {
        if (response.statusCode >= 200 && response.statusCode < 300) {
          resolve(response.data);
          return;
        }
        const data = response.data || {};
        const errorObj = data.error || {};
        const message = errorObj.message || data.message || `HTTP ${response.statusCode}`;
        reject(new Error(message));
      },
      fail(error) {
        reject(new Error(error.errMsg || "Network request failed."));
      }
    });
  });
}

function listKnowledgeBases() {
  return request("/api/v1/knowledge-bases");
}

function encodeSafeSegment(value, name = "id") {
  if (typeof value !== "string") {
    throw new Error(`Invalid ${name}: expected non-empty string`);
  }
  const trimmed = value.trim();
  if (!trimmed || trimmed !== value) {
    throw new Error(`Invalid ${name}: must not be empty or contain surrounding whitespace`);
  }
  if (trimmed === "." || trimmed === ".." || trimmed.includes("..")) {
    throw new Error(`Invalid ${name}: path traversal is forbidden`);
  }
  if (/[\/\\?#%\x00-\x1f\x7f]/.test(trimmed)) {
    throw new Error(`Invalid ${name}: contains disallowed URL character`);
  }
  return encodeURIComponent(trimmed);
}

function createKnowledgeFromURL(knowledgeBaseId, url, enableMultimodel = false) {
  let safeId;
  try {
    safeId = encodeSafeSegment(knowledgeBaseId, "knowledgeBaseId");
  } catch (err) {
    return Promise.reject(err);
  }
  return request(`/api/v1/knowledge-bases/${safeId}/knowledge/url`, {
    method: "POST",
    data: {
      url,
      enable_multimodel: enableMultimodel
    }
  });
}

function createSession(knowledgeBaseId) {
  return request("/api/v1/sessions", {
    method: "POST",
    data: knowledgeBaseId ? { knowledge_base_id: knowledgeBaseId } : {}
  });
}

function knowledgeChat(sessionId, query, knowledgeBaseId) {
  let safeSessionId;
  try {
    safeSessionId = encodeSafeSegment(sessionId, "sessionId");
  } catch (err) {
    return Promise.reject(err);
  }
  const data = { query };
  if (knowledgeBaseId) {
    data.knowledge_base_ids = [knowledgeBaseId];
  }

  return request(`/api/v1/knowledge-chat/${safeSessionId}`, {
    method: "POST",
    data
  });
}

module.exports = {
  createKnowledgeFromURL,
  createSession,
  encodeSafeSegment,
  knowledgeChat,
  listKnowledgeBases,
  request
};
