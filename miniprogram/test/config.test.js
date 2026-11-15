const assert = require("node:assert/strict");
const test = require("node:test");

const { isSecureBaseUrl, saveSettings, STORAGE_KEY } = require("../utils/config");

test("isSecureBaseUrl rejects query strings and URL fragments", () => {
  assert.equal(isSecureBaseUrl("https://api.example.com"), true);
  assert.equal(isSecureBaseUrl("https://api.example.com/api/v1"), true);
  assert.equal(isSecureBaseUrl("http://localhost:8080"), true);

  for (const invalid of [
    "https://api.example.com?foo=bar",
    "https://api.example.com/?foo=bar",
    "https://api.example.com?",
    "https://api.example.com#frag",
    "https://api.example.com/#frag",
    "https://api.example.com#",
    "https://api.example.com/api/v1?x=1",
    "https://api.example.com/api/v1#section",
    "http://localhost:8080?debug=1",
    "http://127.0.0.1:8080#hash",
  ]) {
    assert.equal(isSecureBaseUrl(invalid), false, `expected ${invalid} to be rejected`);
  }
});

test("saveSettings rejects baseUrl containing query or fragment", () => {
  const store = new Map();
  const prevWx = global.wx;
  global.wx = {
    getStorageSync(key) {
      return store.get(key);
    },
    setStorageSync(key, val) {
      store.set(key, val);
    },
  };

  try {
    assert.throws(() => saveSettings({ baseUrl: "https://api.example.com?foo=bar" }));
    assert.throws(() => saveSettings({ baseUrl: "https://api.example.com#frag" }));

    const saved = saveSettings({ baseUrl: "https://api.example.com/" });
    assert.equal(saved.baseUrl, "https://api.example.com");
    assert.equal(store.get(STORAGE_KEY).baseUrl, "https://api.example.com");
  } finally {
    global.wx = prevWx;
  }
});
